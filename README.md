# Пакет go-readers

[![Coverage Status](https://coveralls.io/repos/github/lubyshev/go-readers/badge.svg?branch=main)](https://coveralls.io/github/lubyshev/go-readers?branch=main)

Набор утилит для использования `io.Reader` вместо `[]byte` в вашем коде.

## Base64EncodeReader

Ридер предназначен для кодирования ридера-источника в base64 "на ходу", при чтении.

```go
package main

import (
	"bytes"
	"fmt"
	readers "github.com/lubyshev/go-readers"
	"io"
	"strings"
)

func main() {
	var str = "12123"

	b64 := readers.NewBase64EncodeReader(int64(len(str)), io.NopCloser(strings.NewReader(str)))
	fmt.Println(b64.Size())
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(b64)
	fmt.Printf("'%s'", buf.String())
	// Output:
	// 9
	// 'MTIxMjM= '
}
```

**Внимание!**

Размер кодированных данных определяется с погрешностью. 

В случае, если кодированные данные короче, они дополняются пробелами.

## MultiReadCloser

Ридер предназначен для последовательного чтения из нескольких ридеров как из одного.

```go
package main

import (
	"bytes"
	"fmt"
	readers "github.com/lubyshev/go-readers"
	"io"
	"strings"
)

func main() {
	var rs = []io.ReadCloser{
		io.NopCloser(strings.NewReader("11111_")),
		io.NopCloser(strings.NewReader("22222_")),
		io.NopCloser(strings.NewReader("33333")),
	}

	mrc := readers.NewMultiReadCloser(rs)
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(mrc)
	fmt.Printf("'%s'", buf.String())
	// Output:
	// '11111_22222_33333'
}
```
