package checksum_test

import (
	"crypto"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/bdragon300/tusgo/checksum"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("DeferTrailerReader", func() {
	var testSrv *httptest.Server
	var srvBody []byte
	var srvTrailers http.Header
	const bodyValue = "body value"

	BeforeEach(func() {
		testSrv = httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			srvBody, _ = io.ReadAll(r.Body)
			srvTrailers = r.Trailer
		}))
	})
	Context("set trailer in http.Request", func() {
		When("read body and trailers", func() {
			It("should form correct request", func() {
				readers := map[string]io.Reader{
					"test-trailer1": strings.NewReader("trailer value1"),
					"test-trailer2": strings.NewReader("trailer value2"),
				}
				req, err := http.NewRequest(http.MethodPost, testSrv.URL, nil)
				Ω(err).Should(Succeed())

				data := checksum.NewDeferTrailerReader(strings.NewReader(bodyValue), readers, req)
				req.Body = io.NopCloser(data)
				resp, err := testSrv.Client().Do(req)
				Ω(err).Should(Succeed())
				defer resp.Body.Close()

				Ω(srvBody).Should(Equal([]byte(bodyValue)))
				Ω(srvTrailers.Get("Test-Trailer1")).Should(Equal("trailer value1"))
				Ω(srvTrailers.Get("Test-Trailer2")).Should(Equal("trailer value2"))
			})
		})
		When("no trailers given", func() {
			It("should send only body", func() {
				req, err := http.NewRequest(http.MethodPost, testSrv.URL, nil)
				Ω(err).Should(Succeed())

				data := checksum.NewDeferTrailerReader(strings.NewReader(bodyValue), nil, req)
				req.Body = io.NopCloser(data)
				resp, err := testSrv.Client().Do(req)
				Ω(err).Should(Succeed())
				defer resp.Body.Close()

				Ω(srvBody).Should(Equal([]byte(bodyValue)))
				Ω(srvTrailers).Should(BeEmpty())
			})
		})
	})
})

func ExampleDeferTrailerReader() {
	req, err := http.NewRequest(http.MethodPost, "http://example.com", nil)
	if err != nil {
		panic(err)
	}

	b64hash := checksum.NewHashBase64ReadWriter(crypto.SHA1.New(), "sha1 ")
	body := io.TeeReader(strings.NewReader("Hello world!"), b64hash)
	trailers := map[string]io.Reader{"Checksum": body}
	req.Body = io.NopCloser(checksum.NewDeferTrailerReader(body, trailers, req))

	// Request will contain header "Trailer: sha1 Checksum"
	// and an HTTP trailer "Checksum: sha1 00hq6RNueFa8QiEjhep5cJRHWAI=" after request body
	response, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer response.Body.Close()
}
