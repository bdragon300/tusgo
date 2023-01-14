package checksum

import "hash"

type HashReader struct {
	hash.Hash
}

func (h HashReader) Read(p []byte) (n int, err error) {
	h.Hash.Sum(p)
	return h.Hash.Size(), nil
}
