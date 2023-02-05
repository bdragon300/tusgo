package tusgo

import "io"

// counterReader is reader that counts bytes read from underlying reader
type counterReader struct {
	Rd        io.Reader
	BytesRead int64
}

func (c *counterReader) Read(p []byte) (n int, err error) {
	n, err = c.Rd.Read(p)
	c.BytesRead += int64(n)
	return n, err
}
