package go_readers

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"net/textproto"
	"sort"
	"strings"
)

const (
	partitionStateNew = iota
	partitionStateHeaderSent
	partitionStateSent
)

var (
	ErrPartitionReaderIsNil  = fmt.Errorf("partition header or body is nil")
	ErrPartitionInvalidState = fmt.Errorf("invalid partition state")
	ErrInvalidReadSize       = fmt.Errorf("invalid read size")
)

type MultipartRequestBody interface {
	io.ReadCloser
	Boundary() string
	NewPart(header textproto.MIMEHeader, sz int64, reader io.ReadCloser)
	Size() int64
}

func NewMultipartRequestBody() MultipartRequestBody {
	var buf [30]byte
	_, _ = io.ReadFull(rand.Reader, buf[:])
	boundary := fmt.Sprintf("%x", buf[:])

	return &multipartRequestBody{
		boundary:    boundary,
		parts:       readInfo{},
		partList:    make([]*partition, 0),
		closeReader: strings.NewReader(fmt.Sprintf("\r\n--%s--\r\n", boundary)),
	}
}

type multipartRequestBody struct {
	boundary    string
	parts       readInfo
	partList    []*partition
	closeReader io.Reader

	cntRead int
}

func (inst *multipartRequestBody) NewPart(header textproto.MIMEHeader, sz int64, reader io.ReadCloser) {
	var b bytes.Buffer
	keys := make([]string, 0, len(header))
	for k := range header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		for _, v := range header[k] {
			_, _ = fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	_, _ = fmt.Fprintf(&b, "\r\n")

	inst.partList = append(inst.partList, &partition{
		&b,
		reader,
		int64(b.Len()),
		sz,
		0,
		partitionStateNew,
	})
}

func (inst *multipartRequestBody) Read(p []byte) (n int, err error) {
	return inst.read(p)
}

func (inst *multipartRequestBody) read(p []byte) (n int, err error) {
	if inst.parts.partIdx < len(inst.partList) { // has unread partitions
		if inst.parts.boundaryIdx == inst.parts.partIdx { // boundary is not read
			if inst.parts.boundary == nil {
				inst.parts.boundary = strings.NewReader(fmt.Sprintf("\r\n--%s\r\n", inst.boundary))
			}
			n, err = inst.parts.boundary.Read(p)
			if err == io.EOF { // boundary is read
				inst.parts.boundaryIdx++
				inst.parts.boundary = nil
				return inst.read(p)
			}
		} else { // partition is not read
			n, err = inst.partList[inst.parts.partIdx].Read(p)
			if err == io.EOF { // partition is read
				inst.parts.partIdx++
				return inst.read(p)
			}
		}
		inst.cntRead += n

		return
	}
	n, err = inst.closeReader.Read(p)
	if err == nil && int64(n+inst.cntRead) != inst.Size() {
		return 0, fmt.Errorf("read %d != size %d: %w", n+inst.cntRead, inst.Size(), ErrInvalidReadSize)
	}

	return
}

func (inst *multipartRequestBody) Close() (err error) {
	for _, r := range inst.partList {
		if lErr := r.Close(); lErr != nil {
			err = lErr
		}
	}

	return
}

func (inst *multipartRequestBody) Size() (sz int64) {
	// boundary size
	szBoundary := int64(len(inst.boundary) + 6)
	for _, r := range inst.partList {
		sz += r.Size() + szBoundary
	}
	// last boundary size
	szBoundary += 2

	return sz + szBoundary
}

func (inst *multipartRequestBody) Boundary() string {
	return inst.boundary
}

type partition struct {
	header   io.Reader
	reader   io.ReadCloser
	szHeader int64
	szReader int64

	cntRead int
	state   int
}

func (inst *partition) Read(p []byte) (n int, err error) {
	if inst.header == nil || inst.reader == nil {
		return 0, ErrPartitionReaderIsNil
	}
	switch inst.state {
	case partitionStateNew:
		n, err = inst.header.Read(p)
		if err == io.EOF {
			if n > 0 {
				err = nil
			} else {
				inst.state = partitionStateHeaderSent
				return inst.Read(p)
			}
		}
		inst.cntRead += n
		return
	case partitionStateHeaderSent:
		n, err = inst.reader.Read(p)
		if err == io.EOF {
			if n > 0 {
				err = nil
			} else {
				inst.state = partitionStateSent
				return inst.Read(p)
			}
		}
		inst.cntRead += n
		return
	case partitionStateSent:
		if int64(inst.cntRead) != inst.Size() {
			return 0, fmt.Errorf("read %d != size %d: %w", inst.cntRead, inst.Size(), ErrInvalidReadSize)
		}
		return 0, io.EOF
	}

	return 0, ErrPartitionInvalidState
}

func (inst *partition) Close() error {
	return inst.reader.Close()
}

func (inst *partition) Size() int64 {
	return inst.szHeader + inst.szReader
}

type readInfo struct {
	partIdx     int
	boundaryIdx int
	boundary    io.Reader
}
