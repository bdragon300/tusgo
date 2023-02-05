package checksum

import (
	"encoding/base64"
	"hash"
	"io"
	"strings"
)

// HashBase64ReadWriter an io.Reader that wraps a hash.Hash, it feeds the prefix + hash in base64 format
type HashBase64ReadWriter struct {
	hash.Hash
	rd     io.Reader
	prefix string
}

// NewHashBase64ReadWriter constructs a new HashBase64ReadWriter. Receives a hash object to wrap and a prefix
// to prepend hash result string
func NewHashBase64ReadWriter(h hash.Hash, prefix string) *HashBase64ReadWriter {
	return &HashBase64ReadWriter{Hash: h, prefix: prefix}
}

// Read reads up to len(p) bytes of base64 hash sum into p. It invokes Sum calculation on the first call.
// The function returns the number of bytes read (0 <= n <= len(p)) and any error
// encountered. Returns io.EOF error if all result has read and no more data available.
func (h *HashBase64ReadWriter) Read(p []byte) (n int, err error) {
	if h.rd == nil {
		sum := h.Hash.Sum(make([]byte, 0))
		s := h.prefix + base64.StdEncoding.EncodeToString(sum)
		h.rd = strings.NewReader(s)
	}
	return h.rd.Read(p)
}
