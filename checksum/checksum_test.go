package checksum_test

import (
	"crypto"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/bdragon300/tusgo/checksum"
)

func ExampleNewHashBase64ReadWriter() {
	data := []byte("Hello world!")
	rw := checksum.NewHashBase64ReadWriter(crypto.SHA1.New(), "sha1 ")
	if _, err := rw.Write(data); err != nil {
		panic(err)
	}

	sum, err := io.ReadAll(rw)
	if err != nil {
		panic(err)
	}
	fmt.Printf("%s\n", sum)
	// Output: sha1 00hq6RNueFa8QiEjhep5cJRHWAI=
}

func ExampleDeferTrailerReader() {
	req, err := http.NewRequest(http.MethodPost, "http://example.com", nil)
	if err != nil {
		panic(err)
	}

	b64hash := checksum.NewHashBase64ReadWriter(crypto.SHA1.New(), "sha1 ")
	body := io.TeeReader(strings.NewReader("Hello world!"), b64hash)
	trailers := map[string]io.Reader{"Checksum": body}
	req.Body = io.NopCloser(checksum.NewDeferTrailerReader(body, trailers, req))

	// Request will contain header "Trailer: Checksum"
	// and an HTTP trailer "Checksum: sha1 00hq6RNueFa8QiEjhep5cJRHWAI="
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer response.Body.Close()
}
