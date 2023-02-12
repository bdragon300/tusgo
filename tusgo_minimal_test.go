package tusgo_test

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"

	"github.com/bdragon300/tusgo"
)

func Example_minimal() {
	baseURL, _ := url.Parse("http://example.com/files")
	cl := tusgo.NewClient(http.DefaultClient, baseURL)

	// Assume that the Upload has been created on server earlier with size 1KiB
	u := tusgo.Upload{Location: "http://example.com/files/foo/bar", RemoteSize: 1024 * 1024}
	// Open a file we want to upload
	f, err := os.Open("/tmp/file.txt")
	if err != nil {
		panic(err)
	}
	defer f.Close()

	s := tusgo.NewUploadStream(cl, &u)
	// Set stream and file pointers to be equal to the remote pointer
	if _, err := s.Sync(); err != nil {
		panic(err)
	}
	if _, err := f.Seek(s.Tell(), io.SeekStart); err != nil {
		panic(err)
	}

	written, err := io.Copy(s, f)
	if err != nil {
		panic(fmt.Sprintf("Written %d bytes, error: %s, last response: %v", written, err, s.LastResponse))
	}
	fmt.Printf("Written %d bytes\n", written)
}
