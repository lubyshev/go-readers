package go_readers

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"
)

// ReadDispatcher

// Default options values
const (
	ReadDispatcherBuffersCount         = 2
	ReadDispatcherRefreshBuffersPeriod = 50 * time.Millisecond
	ReadDispatcherWaitBufferPeriod     = 10 * time.Millisecond
)

// Errors
var (
	ErrReadDispatcherReaderNotClosed      = fmt.Errorf("reader was not closed")
	ErrReadDispatcherClientNotExist       = fmt.Errorf("client does not exist")
	ErrReadDispatcherClientClosed         = fmt.Errorf("client has been closed")
	ErrReadDispatcherInvalidBufferSize    = fmt.Errorf("invalid read buffer size")
	ErrReadDispatcherDestroyOrContextDone = fmt.Errorf("exit with destroy func or after context is done")
	ErrReadDispatcherBuffersOutOfRange    = fmt.Errorf("read buffers: out of range")
	ErrReadDispatcherClientAfterRead      = fmt.Errorf("could not create a new client after read from another client")
)

type ReadDispatcher interface {
	NewClient(wg *sync.WaitGroup) (io.ReadCloser, error)
	BufferLength() int
	CloseError() error
	ScanError() error
	Destroy()
}

type ReadDispatcherOptions struct {
	BuffersCount         int
	RefreshBuffersPeriod time.Duration
	WaitBufferPeriod     time.Duration
}

func NewReadDispatcher(ctx context.Context, r io.ReadCloser, opts *ReadDispatcherOptions) ReadDispatcher {
	return (&readDispatcher{
		ctx:     ctx,
		reader:  r,
		options: opts,
	}).init()
}

type dispatcherBuffer struct {
	buf []byte
	n   int
	err error
}

type readDispatcher struct {
	ctx     context.Context
	reader  io.ReadCloser
	options *ReadDispatcherOptions

	cntClients int
	clients    map[clientReadCloser]int

	bufLen        int
	buffers       map[int]*dispatcherBuffer
	scanComplete  bool
	idxBufferNext int
	mxBuffers     sync.RWMutex

	chQuit chan struct{}

	closeErr error
	scanErr  error
}

func (dsp *readDispatcher) NewClient(wg *sync.WaitGroup) (io.ReadCloser, error) {
	defer dsp.mxBuffers.Unlock()
	dsp.mxBuffers.Lock()

	if dsp.bufLen > 0 { // read() has been called
		return nil, ErrReadDispatcherClientAfterRead
	}
	client := &clReadCloser{dispatcher: dsp, wg: wg}
	dsp.clients[client] = 0
	dsp.cntClients++

	return client, nil
}

func (dsp *readDispatcher) CloseError() error {
	return dsp.closeErr
}

func (dsp *readDispatcher) BufferLength() int {
	return dsp.bufLen
}

func (dsp *readDispatcher) ScanError() error {
	return dsp.scanErr
}

func (dsp *readDispatcher) Destroy() {
	defer dsp.mxBuffers.Unlock()
	dsp.mxBuffers.Lock()

	if !dsp.scanComplete {
		dsp.chQuit <- struct{}{}
		dsp.mxBuffers.Unlock()
		<-dsp.chQuit
		dsp.mxBuffers.Lock()
	}

	for idx := range dsp.clients {
		delete(dsp.clients, idx)
	}

	for idx := range dsp.buffers {
		dsp.buffers[idx].buf = nil
		delete(dsp.buffers, idx)
	}
}

func (dsp *readDispatcher) init() ReadDispatcher {
	if dsp.options == nil {
		dsp.options = &ReadDispatcherOptions{
			BuffersCount:         ReadDispatcherBuffersCount,
			RefreshBuffersPeriod: ReadDispatcherRefreshBuffersPeriod,
			WaitBufferPeriod:     ReadDispatcherWaitBufferPeriod,
		}
	} else {
		if dsp.options.BuffersCount <= 0 {
			dsp.options.BuffersCount = ReadDispatcherBuffersCount
		}
		if dsp.options.RefreshBuffersPeriod <= 0 {
			dsp.options.RefreshBuffersPeriod = ReadDispatcherRefreshBuffersPeriod
		}
		if dsp.options.WaitBufferPeriod <= 0 {
			dsp.options.WaitBufferPeriod = ReadDispatcherWaitBufferPeriod
		}
	}

	dsp.chQuit = make(chan struct{})
	dsp.clients = map[clientReadCloser]int{}
	dsp.buffers = map[int]*dispatcherBuffer{}
	dsp.closeErr = ErrReadDispatcherReaderNotClosed

	go dsp.scanReader()

	return dsp
}

func (dsp *readDispatcher) read(p []byte, client clientReadCloser) (n int, err error) {
	var hasLock bool
	defer func() {
		if hasLock {
			dsp.mxBuffers.RUnlock()
		}
	}()

	l := len(p)
	if dsp.bufLen == 0 {
		dsp.bufLen = l
	}
	if l != dsp.bufLen || l == 0 {
		return 0, ErrReadDispatcherInvalidBufferSize
	}

	idx := dsp.clients[client]
	dsp.mxBuffers.RLock()
	hasLock = true

	if dsp.scanComplete && idx >= dsp.idxBufferNext {
		return 0, ErrReadDispatcherBuffersOutOfRange
	}

	if buf, ok := dsp.buffers[idx]; ok {
		dsp.clients[client]++
		copy(p, buf.buf)
		return buf.n, buf.err
	}

	dsp.mxBuffers.RUnlock()
	hasLock = false

	var tmr = time.NewTicker(dsp.options.WaitBufferPeriod)
	for {
		<-tmr.C
		tmr.Stop()

		dsp.mxBuffers.RLock()
		hasLock = true
		if buf, ok := dsp.buffers[idx]; ok {
			dsp.clients[client]++
			copy(p, buf.buf)
			return buf.n, buf.err
		}
		// if Destroy() or context done occurred
		if dsp.scanComplete {
			return 0, ErrReadDispatcherDestroyOrContextDone
		}
		dsp.mxBuffers.RUnlock()
		hasLock = false

		tmr.Reset(dsp.options.WaitBufferPeriod)
	}
}

func (dsp *readDispatcher) close(client clientReadCloser) error {
	defer dsp.mxBuffers.Unlock()
	dsp.mxBuffers.Lock()

	if _, ok := dsp.clients[client]; !ok {
		return ErrReadDispatcherClientNotExist
	}

	delete(dsp.clients, client)
	dsp.cntClients--

	return nil
}

func (dsp *readDispatcher) scanReader() {
	defer func() {
		dsp.closeErr = dsp.reader.Close()
	}()

	var maxInt = int(^uint(0) >> 1)
	var tmr = time.NewTicker(dsp.options.WaitBufferPeriod)
	for {
		select {
		case <-dsp.chQuit: // Destroy() functionality
			dsp.mxBuffers.Lock()
			dsp.scanComplete = true
			dsp.scanErr = ErrReadDispatcherDestroyOrContextDone
			dsp.chQuit <- struct{}{}
			dsp.mxBuffers.Unlock()

		case <-dsp.ctx.Done():
			dsp.mxBuffers.Lock()
			dsp.scanComplete = true
			dsp.scanErr = ErrReadDispatcherDestroyOrContextDone
			dsp.mxBuffers.Unlock()

		case <-tmr.C:
			tmr.Stop()
			if dsp.bufLen > 0 { // read() has been called
				dsp.mxBuffers.Lock()
				minIdx := maxInt
				// find min not read buffer
				for _, idx := range dsp.clients {
					if idx < minIdx {
						minIdx = idx
					}
				}
				// delete all buffers that have been read
				for idx := range dsp.buffers {
					if idx < minIdx {
						delete(dsp.buffers, idx)
					}
				}
				// some space to read is available
				if dsp.options.BuffersCount-len(dsp.buffers) > 0 {
					buf := &dispatcherBuffer{
						buf: make([]byte, dsp.bufLen),
					}
					buf.n, buf.err = dsp.reader.Read(buf.buf)
					dsp.buffers[dsp.idxBufferNext] = buf
					dsp.idxBufferNext++
					if buf.err != nil {
						dsp.scanComplete = true
					}
				}
				dsp.mxBuffers.Unlock()
				if dsp.scanComplete {
					break
				}
			}
			tmr.Reset(dsp.options.RefreshBuffersPeriod)
		}
		if dsp.scanComplete {
			break
		}
	}
}

// clientReadCloser

type clientReadCloser io.ReadCloser

type readDispatcherReadCloser interface {
	read(p []byte, client clientReadCloser) (n int, err error)
	close(client clientReadCloser) error
}

type clReadCloser struct {
	dispatcher readDispatcherReadCloser
	wg         *sync.WaitGroup
	mx         sync.RWMutex
	isClosed   bool
}

func (cl *clReadCloser) Read(p []byte) (n int, err error) {
	defer cl.mx.RUnlock()
	cl.mx.RLock()
	if cl.isClosed {
		return 0, ErrReadDispatcherClientClosed
	}

	return cl.dispatcher.read(p, cl)
}

func (cl *clReadCloser) Close() error {
	defer func() {
		cl.isClosed = true
		cl.mx.Unlock()
		if cl.wg != nil {
			cl.wg.Done()
		}
	}()
	cl.mx.Lock()
	if cl.isClosed {
		return ErrReadDispatcherClientClosed
	}

	return cl.dispatcher.close(cl)
}
