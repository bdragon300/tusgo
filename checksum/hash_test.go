package checksum_test

import (
	"crypto"
	"fmt"
	"io"

	"github.com/bdragon300/tusgo/checksum"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

type hashStub struct {
	s string
}

func (h hashStub) Write(p []byte) (n int, err error) {
	panic("implement me")
}

func (h hashStub) Sum(b []byte) []byte {
	b = append(b, []byte(h.s)...)
	return b
}

func (h hashStub) Reset() {
	panic("implement me")
}

func (h hashStub) Size() int {
	return len(h.s)
}

func (h hashStub) BlockSize() int {
	panic("implement me")
}

var _ = Describe("HashBase64ReadWriter", func() {
	var rd *checksum.HashBase64ReadWriter
	var buf []byte
	var expect []byte
	expectBase64 := []byte("YXNkZg==")

	BeforeEach(func() {
		rd = checksum.NewHashBase64ReadWriter(hashStub{"asdf"})
		expect = make([]byte, 5)
	})
	Context("Read()", func() {
		When("read some bytes", func() {
			It("should fill buf without err", func() {
				buf = make([]byte, 5)
				copy(expect, expectBase64[:5])
				Ω(rd.Read(buf)).Should(Equal(5))
				Ω(buf).Should(Equal(expect))
			})
		})
		When("skip and read the rest", func() {
			BeforeEach(func() {
				buf = make([]byte, 5)
				_, _ = io.CopyN(io.Discard, rd, 5)
			})
			It("should fill buf", func() {
				copy(expect, expectBase64[5:])
				Ω(rd.Read(buf)).Should(Equal(3))
				Ω(buf).Should(Equal(expect))
			})
			When("nothing left to read", func() {
				It("should read nothing and io.EOF", func() {
					_, _ = io.CopyN(io.Discard, rd, 3)
					l, err := rd.Read(buf)
					Ω(l).Should(Equal(0))
					Ω(err).Should(MatchError(io.EOF))
					Ω(buf).Should(Equal(make([]byte, 5)))
				})
			})
		})
	})
})

func ExampleNewHashBase64ReadWriter() {
	data := []byte("Hello world!")
	rw := checksum.NewHashBase64ReadWriter(crypto.SHA1.New())
	if _, err := rw.Write(data); err != nil {
		panic(err)
	}

	sum, err := io.ReadAll(rw)
	if err != nil {
		panic(err)
	}
	fmt.Printf("%s\n", sum)
	// Output: 00hq6RNueFa8QiEjhep5cJRHWAI=
}
