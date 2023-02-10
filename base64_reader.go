package go_readers

import (
	"bytes"
	"encoding/base64"
	"io"
	"strings"
)

type Base64EncodeReader interface {
	io.ReadCloser
	Size() int64
}

func NewBase64EncodeReader(size int64, reader io.ReadCloser) Base64EncodeReader {
	res := &base64EncodeReader{
		reader: reader,
		size:   size,
	}
	res.enc = base64.NewEncoder(base64.StdEncoding, &res.b64)

	return res
}

type base64EncodeReader struct {
	b64     bytes.Buffer
	buf     []byte
	enc     io.WriteCloser
	reader  io.ReadCloser
	size    int64
	szRead  int64
	success bool
}

func (inst *base64EncodeReader) Read(p []byte) (n int, err error) {
	// some ready data in buffer
	if lenBuf := len(inst.buf); lenBuf > 0 {
		n = len(p)
		if lenBuf < n {
			n = lenBuf
		}
		copy(p, inst.buf[:n])
		inst.buf = inst.buf[n:]
		inst.szRead += int64(n)

		return
	}
	// all reads are done
	if inst.success {
		// append space if length is less
		left := int(inst.Size() - inst.szRead)
		if left > 0 {
			inst.buf = []byte(strings.Repeat(" ", left))
			return inst.Read(p)
		}
		return 0, io.EOF
	}
	// append next part to buffer
	if err = inst.refreshBuffer(p); err != nil {
		return 0, err
	}

	return inst.Read(p)
}

func (inst *base64EncodeReader) Close() error {
	_ = inst.enc.Close()
	return inst.reader.Close()
}

func (inst *base64EncodeReader) Size() int64 {
	var l = float32((inst.size + 2) * 4 / 3)
	var res = int64(l)
	if l-float32(res) > 0 {
		res++
	}

	return res
}

func (inst *base64EncodeReader) refreshBuffer(p []byte) (err error) {
	var n int
	// read original data
	n, err = inst.reader.Read(p)
	if err == io.EOF && n > 0 {
		err = nil
	}
	if err == io.EOF {
		inst.success = true
		// some data may stay in the encryptor
		err = inst.enc.Close()
		if err != nil {
			return err
		}
	} else {
		// data encryption to base64 buffer
		if _, err = inst.enc.Write(p[:n]); err != nil {
			return err
		}
	}
	// copy data to buffer
	if b64Len := inst.b64.Len(); b64Len > 0 {
		inst.buf = inst.b64.Bytes()[:b64Len]
		inst.b64 = bytes.Buffer{}
	}

	return nil
}
