package checksum

import (
	"bytes"
	"encoding/base64"
	"hash"
	"io"
)

// HashBase64ReadWriter an io.Reader that wraps a hash.Hash object to be able to read hash sum in base64 format
type HashBase64ReadWriter struct {
	hash.Hash
	rd io.Reader
}

// NewHashBase64ReadWriter constructs a new HashBase64ReadWriter. Receives a hash object to wrap
func NewHashBase64ReadWriter(h hash.Hash) *HashBase64ReadWriter {
	return &HashBase64ReadWriter{Hash: h}
}

// Read reads up to len(p) bytes of base64 hash sum into p. It invokes Sum calculation on the first call.
// The function returns the number of bytes read (0 <= n <= len(p)) and any error
// encountered. Returns io.EOF error if all result has read and no more data available.
func (h *HashBase64ReadWriter) Read(p []byte) (n int, err error) {
	if h.rd == nil {
		s := h.Hash.Sum(make([]byte, 0))
		buf := make([]byte, base64.StdEncoding.EncodedLen(len(s)))
		base64.StdEncoding.Encode(buf, s)
		h.rd = bytes.NewReader(buf)
	}
	return h.rd.Read(p)
}
