package go_readers_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	Readers "github.com/lubyshev/go-readers"
	"github.com/stretchr/testify/assert"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	body1 = "MultipartRequestBody number 1.\n"
	body2 = "MultipartRequestBody number 2. MultipartRequestBody number 3. MultipartRequestBody number 4. " +
		"MultipartRequestBody number 5. MultipartRequestBody number 6. MultipartRequestBody number 7. " +
		"MultipartRequestBody number 8. MultipartRequestBody number 9. MultipartRequestBody number 10.\n"
)

func Test_MultipartRequestBody_Size(t *testing.T) {
	mp := genMultipartRequest()
	defer func() { err := mp.Close(); assert.NoError(t, err) }()

	var b bytes.Buffer
	_, err := io.Copy(&b, mp)
	assert.NoError(t, err)
	assert.Equal(t, int64(b.Len()), mp.Size())
}

func Test_MultipartRequestBody_Request(t *testing.T) {
	var hello = []byte("HELLO")
	svr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		form, err := r.MultipartReader()
		assert.NoError(t, err)
		if err == nil {
			for {
				part, err := form.NextPart()
				if err == io.EOF {
					break
				}
				assert.NoError(t, err)
				if err != nil {
					_ = part.Close()
					return
				}
				bt, err := ioutil.ReadAll(part)
				assert.NoError(t, err)
				switch part.FormName() {
				case "meta-1":
					assert.Equal(t, body1, string(bt))
				case "meta-2":
					dec := base64.NewDecoder(base64.StdEncoding, bytes.NewReader(bt))
					bt, err = ioutil.ReadAll(dec)
					assert.NoError(t, err)
					assert.Equal(t, body2, string(bt))
				}
				_ = part.Close()
			}
		}
		_, err = w.Write(hello)
		assert.NoError(t, err)
	}))
	defer svr.Close()

	mp := genMultipartRequest()
	defer func() { err := mp.Close(); assert.NoError(t, err) }()

	req, err := http.NewRequest(http.MethodPost, svr.URL, nil)
	assert.NoError(t, err)

	req.Body = mp
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Content-Type", fmt.Sprintf("multipart/form-data; boundary=%s", mp.Boundary()))
	req.ContentLength = mp.Size()

	cl := &http.Client{}
	resp, err := cl.Do(req)
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	var b bytes.Buffer
	_, err = io.Copy(&b, resp.Body)
	assert.NoError(t, err)
	assert.Equal(t, hello, b.Bytes())
}

func Test_Base64EncodeReader_MultiReadCloser(t *testing.T) {
	var r = make([]io.ReadCloser, 0)
	var rr io.Reader
	var sz int64
	var str = []string{
		"11111\n",
		base64.StdEncoding.EncodeToString([]byte("22222\n")),
		"33333",
	}

	for i, s := range str {
		rr = io.Reader(strings.NewReader(s))
		if i == 1 {
			rr = base64.NewDecoder(base64.StdEncoding, rr)
		}
		r = append(r, ioutil.NopCloser(rr))
		sz += int64(len(s))
	}
	mp := Readers.NewBase64EncodeReader(sz, Readers.NewMultiReadCloser(r))
	defer func() {
		err := mp.Close()
		assert.NoError(t, err)
	}()
	bts, err := ioutil.ReadAll(mp)
	assert.NoError(t, err)
	assert.Equal(t, "MTExMTEKMjIyMjIKMzMzMzM=    ", string(bts))
}

func genMultipartRequest() Readers.MultipartRequestBody {
	mp := Readers.NewMultipartRequestBody()

	h := textproto.MIMEHeader{}
	h.Set("Content-Type", "text/plain; charset=utf8")

	h.Set("Content-Disposition", `form-data; name="meta-1"`)
	mp.NewPart(h, int64(len(body1)), ioutil.NopCloser(strings.NewReader(body1)))

	h.Set("Content-Disposition", `form-data; name="meta-2"`)
	h.Set("Content-Transfer-Encoding", "base64")
	r := Readers.NewBase64EncodeReader(
		int64(len(body2)),
		ioutil.NopCloser(strings.NewReader(body2)),
	)
	mp.NewPart(h, r.Size(), r)

	return mp
}

func Test_ReadDispatcher_ClientClosed_ClientNotExist_ReaderNotClosed(t *testing.T) {
	var p = make([]byte, 10)
	r := ioutil.NopCloser(strings.NewReader("01234567890123456789"))

	dsp := Readers.NewReadDispatcher(context.Background(), r, nil)
	cl, err := dsp.NewClient(nil)
	assert.NoError(t, err)
	err = cl.Close()
	assert.Equal(t, nil, err)
	_, err = cl.Read(p)
	assert.Equal(t, Readers.ErrReadDispatcherClientClosed, err)
	err = cl.Close()
	assert.Equal(t, Readers.ErrReadDispatcherClientClosed, err)
	assert.Equal(t, Readers.ErrReadDispatcherReaderNotClosed, dsp.CloseError())
}

func Test_ReadDispatcher_InvalidBufferSize(t *testing.T) {
	var p = make([]byte, 10)
	r := ioutil.NopCloser(strings.NewReader("01234567890123456789"))

	dsp := Readers.NewReadDispatcher(context.Background(), r, &Readers.ReadDispatcherOptions{
		BuffersCount:         0,
		RefreshBuffersPeriod: 0,
		WaitBufferPeriod:     0,
	})
	cl, err := dsp.NewClient(nil)
	assert.NoError(t, err)
	time.Sleep(60 * time.Millisecond)
	_, err = cl.Read(p)
	assert.NoError(t, err)
	p = make([]byte, 5)
	_, err = cl.Read(p)
	assert.Equal(t, Readers.ErrReadDispatcherInvalidBufferSize, err)
}

func Test_ReadDispatcher_OutOfBuffer(t *testing.T) {
	var p = make([]byte, 5)
	r := ioutil.NopCloser(strings.NewReader("01234567890123456789"))

	dsp := Readers.NewReadDispatcher(context.Background(), r, nil)
	cl, err := dsp.NewClient(nil)
	assert.NoError(t, err)
	time.Sleep(60 * time.Millisecond)
	_, err = cl.Read(p)
	time.Sleep(60 * time.Millisecond)
	assert.NoError(t, err)
	_, err = cl.Read(p)
	assert.NoError(t, err)
	_, err = cl.Read(p)
	assert.NoError(t, err)
	_, err = cl.Read(p)
	assert.NoError(t, err)
	_, err = cl.Read(p)
	assert.Equal(t, io.EOF, err)
	_, err = cl.Read(p)
	assert.Equal(t, Readers.ErrReadDispatcherBuffersOutOfRange, err)
}

func Test_ReadDispatcher_ReadContext(t *testing.T) {
	var p = make([]byte, 5)
	r := ioutil.NopCloser(strings.NewReader("01234567890123456789"))
	ctx, cf := context.WithTimeout(context.Background(), time.Second)
	defer cf()

	dsp := Readers.NewReadDispatcher(ctx, r, nil)
	cl, err := dsp.NewClient(nil)
	assert.NoError(t, err)
	_, err = dsp.NewClient(nil)
	assert.NoError(t, err)
	time.Sleep(60 * time.Millisecond)
	_, err = cl.Read(p)
	assert.NoError(t, err)
	_, err = cl.Read(p)
	assert.NoError(t, err)
	_, err = cl.Read(p)
	assert.Equal(t, Readers.ErrReadDispatcherDestroyOrContextDone, err)
	_, err = cl.Read(p)
	assert.Equal(t, Readers.ErrReadDispatcherBuffersOutOfRange, err)
}

func Test_ReadDispatcher_ClientAfterRead(t *testing.T) {
	var p = make([]byte, 5)
	r := ioutil.NopCloser(strings.NewReader("01234567890123456789"))

	dsp := Readers.NewReadDispatcher(context.Background(), r, nil)
	cl, err := dsp.NewClient(nil)
	assert.NoError(t, err)
	time.Sleep(60 * time.Millisecond)
	_, err = cl.Read(p)
	assert.NoError(t, err)
	_, err = dsp.NewClient(nil)
	assert.Equal(t, Readers.ErrReadDispatcherClientAfterRead, err)
}

func Test_ReadDispatcher_ScanContext(t *testing.T) {
	//var p = make([]byte, 5)
	r := ioutil.NopCloser(strings.NewReader("01234567890123456789"))
	ctx, cf := context.WithTimeout(context.Background(), 6*time.Millisecond)
	defer cf()

	dsp := Readers.NewReadDispatcher(ctx, r, nil)
	time.Sleep(60 * time.Millisecond)
	assert.Equal(t, Readers.ErrReadDispatcherDestroyOrContextDone, dsp.ScanError())
}

func Test_ReadDispatcher_ScanDestroy(t *testing.T) {
	var p = make([]byte, 5)
	r := ioutil.NopCloser(strings.NewReader("01234567890123456789"))
	dsp := Readers.NewReadDispatcher(context.Background(), r, nil)
	cl, err := dsp.NewClient(nil)
	assert.NoError(t, err)
	time.Sleep(6 * time.Millisecond)
	_, err = cl.Read(p)
	assert.NoError(t, err)
	dsp.Destroy()
	time.Sleep(60 * time.Millisecond)
	assert.Equal(t, Readers.ErrReadDispatcherDestroyOrContextDone, dsp.ScanError())
	err = cl.Close()
	assert.Equal(t, Readers.ErrReadDispatcherClientNotExist, err)
}

func Test_ReadDispatcher_Async_OK(t *testing.T) {
	r := ioutil.NopCloser(strings.NewReader("01234567890123456789"))
	dsp := Readers.NewReadDispatcher(context.Background(), r, nil)
	wg := &sync.WaitGroup{}
	wg.Add(2)
	go func() {
		var n int
		var p = make([]byte, 5)
		cl, err := dsp.NewClient(wg)
		assert.NoError(t, err)
		time.Sleep(60 * time.Millisecond)
		_, err = cl.Read(p)
		assert.NoError(t, err)
		assert.Equal(t, "01234", string(p))
		_, err = cl.Read(p)
		assert.NoError(t, err)
		assert.Equal(t, "56789", string(p))
		n, err = cl.Read(p)
		assert.NoError(t, err)
		assert.Equal(t, 5, n)
		assert.Equal(t, "01234", string(p))
		n, err = cl.Read(p)
		assert.NoError(t, err)
		assert.Equal(t, 5, n)
		assert.Equal(t, "56789", string(p))
		n, err = cl.Read(p)
		assert.Equal(t, io.EOF, err)
		assert.Equal(t, 0, n)
		assert.Equal(t, "\x00\x00\x00\x00\x00", string(p))
		err = cl.Close()
		assert.NoError(t, err)
	}()
	go func() {
		var p = make([]byte, 10) // invalid size
		cl, err := dsp.NewClient(wg)
		assert.NoError(t, err)
		time.Sleep(60 * time.Millisecond)
		_, err = cl.Read(p)
		assert.Equal(t, Readers.ErrReadDispatcherInvalidBufferSize, err)
		if err == Readers.ErrReadDispatcherInvalidBufferSize {
			p = make([]byte, dsp.BufferLength())
		} else {
			assert.Fail(t, "do not receive error: ErrReadDispatcherInvalidBufferSize")
			return
		}
		_, err = cl.Read(p)
		assert.NoError(t, err)
		assert.Equal(t, "01234", string(p))
		err = cl.Close()
		assert.NoError(t, err)
	}()
	wg.Wait()
	assert.NoError(t, dsp.ScanError())
	assert.NoError(t, dsp.CloseError())
}
