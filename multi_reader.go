package go_readers

import "io"

type multiReadCloser struct {
	rIdx int
	r    []io.ReadCloser
}

func NewMultiReadCloser(readers []io.ReadCloser) io.ReadCloser {
	return &multiReadCloser{0, readers}
}

func (mrc *multiReadCloser) Close() (err error) {
	for _, r := range mrc.r {
		if lErr := r.Close(); lErr != nil {
			err = lErr
		}
	}

	return
}

func (mrc *multiReadCloser) Read(p []byte) (n int, err error) {
	if mrc.rIdx < len(mrc.r) {
		n, err = mrc.r[mrc.rIdx].Read(p)
		if err == io.EOF {
			if n == 0 {
				mrc.rIdx++
				return mrc.Read(p)
			}
			err = nil
		}
		return
	}

	return 0, io.EOF
}
