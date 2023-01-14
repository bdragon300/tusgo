package checksum

import (
	"io"
	"net/http"
)

func NewTrailerReader(body io.Reader, readers map[string]io.Reader, request *http.Request) *TrailerReader {
	return &TrailerReader{
		body:    body,
		readers: readers,
		request: request,
	}
}

type TrailerReader struct {
	body    io.Reader
	readers map[string]io.Reader
	request *http.Request
}

func (h TrailerReader) Read(p []byte) (n int, err error) {
	n, err = h.body.Read(p)
	if err == io.EOF {
		buf := make([]byte, 0)
		for k, r := range h.readers {
			buf = buf[:0]
			if _, e := r.Read(buf); e != nil && e != io.EOF {
				return n, e
			}
			h.request.Trailer.Set(k, string(buf))
		}
	}
	return
}
