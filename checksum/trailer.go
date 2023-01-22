package checksum

import (
	"bytes"
	"io"
	"net/http"
)

// DeferTrailerReader is io.Reader that concatenates body and trailer readers and substitutes trailer values to
// request just after body data was drawn out. This is suitable when trailer values are unknown before the whole
// body was fully read. For example -- get the checksum of huge body without copying it to an intermediate buffer.
type DeferTrailerReader struct {
	body    io.Reader
	readers map[string]io.Reader
	request *http.Request
}

// NewDeferTrailerReader constructs a new DeferTrailerReader object. Receives a body data reader,
// trailers and their readers to be sent, and a request object
func NewDeferTrailerReader(body io.Reader, trailers map[string]io.Reader, request *http.Request) *DeferTrailerReader {
	if request.Trailer == nil {
		request.Trailer = make(http.Header)
	}
	// Fill out trailers with nils in order to make http.Request add a Trailer: header to a request
	for k := range trailers {
		request.Trailer[k] = nil
	}

	return &DeferTrailerReader{
		body:    body,
		readers: trailers,
		request: request,
	}
}

// Read reads up to len(p) bytes of request body into p. After the body reader has fully drawn out, it sequentially
// gets given trailers data from their readers and assigns it to the request.
// The function returns the number of bytes read (0 <= n <= len(p)) and any error
// encountered. Returns io.EOF error if all result has read and no more data available.
func (h DeferTrailerReader) Read(p []byte) (n int, err error) {
	n, err = h.body.Read(p)
	if err == io.EOF {
		buf := bytes.NewBuffer(make([]byte, 0))
		for k, r := range h.readers {
			buf.Reset()
			if _, e := buf.ReadFrom(r); e != nil && e != io.EOF {
				return n, e
			}
			h.request.Trailer.Set(k, buf.String())
		}
	}
	return
}
