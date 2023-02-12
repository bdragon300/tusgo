package tusgo_test

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sync"
	"time"

	"github.com/bdragon300/tusgo"
)

// DoUpload does the transfer of the data until we will finish the upload, or until too many errors will occur
func DoUpload(dst *tusgo.UploadStream, src *os.File) error {
	attempts := 10

	// Reset offsets -- they will be further adjusted to the remote upload offset
	if _, err := dst.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := src.Seek(0, io.SeekStart); err != nil {
		return err
	}

	for dst.Upload.RemoteOffset < dst.Upload.RemoteSize && attempts > 0 {
		_, err := io.Copy(dst, src)
		if err == nil {
			break // Transfer has finished
		}

		attempts--
		if errors.Is(err, tusgo.ErrOffsetsNotSynced) {
			// Sync local and remote offsets
			if _, err = dst.Sync(); err != nil {
				return err
			}
			// Also adjust the file pointer
			if _, err = src.Seek(dst.Tell(), io.SeekStart); err != nil {
				return err
			}
		} else if errors.Is(err, tusgo.ErrChecksumMismatch) {
			continue // Checksum mismatch, try to make a transfer again
		} else if _, ok := err.(net.Error); !ok {
			return err // Permanent error
		}
	}
	if attempts == 0 {
		return errors.New("too many attempts to upload the data")
	}
	return nil
}

func CreateUploadFromFile(file *os.File, cl *tusgo.Client, partial bool) *tusgo.Upload {
	// Open a file to be transferred
	finfo, err := file.Stat()
	if err != nil {
		panic(err)
	}

	u := tusgo.Upload{}
	if _, err := cl.CreateUpload(&u, finfo.Size(), partial, nil); err != nil {
		panic(err)
	}
	fmt.Printf("Location: %s\n", u.Location)
	return &u
}

func ExampleClient_CreateUpload() {
	baseURL, err := url.Parse("http://example.com/files")
	if err != nil {
		panic(err)
	}
	cl := tusgo.NewClient(http.DefaultClient, baseURL)
	if _, err = cl.UpdateCapabilities(); err != nil {
		panic(err)
	}

	u := tusgo.Upload{}
	// Create an upload with 2 MiB size
	if _, err = cl.CreateUpload(&u, 1024*1024*2, false, nil); err != nil {
		panic(err)
	}
	fmt.Printf("Location: %s\n", u.Location)
}

func ExampleClient_ConcatenateUploads_withCreation() {
	baseURL, err := url.Parse("http://example.com/files")
	if err != nil {
		panic(err)
	}
	cl := tusgo.NewClient(http.DefaultClient, baseURL)
	if _, err = cl.UpdateCapabilities(); err != nil {
		panic(err)
	}

	wg := &sync.WaitGroup{}
	fileNames := []string{"/tmp/file1.txt", "/tmp/file2.txt"}
	// Assume that uploads were already been created
	uploads := make([]*tusgo.Upload, 2)
	wg.Add(len(fileNames))

	// Transfer partial uploads in parallel
	for ind, fn := range fileNames {
		fn := fn
		ind := ind
		go func() {
			defer wg.Done()

			f, err := os.Open(fn)
			if err != nil {
				panic(err)
			}
			defer f.Close()
			uploads[ind] = CreateUploadFromFile(f, cl, true)
			fmt.Printf("Upload #%d: Location: %s", ind, uploads[ind].Location)

			fmt.Printf("Upload #%d: transferring file %s to %s\n", ind, fn, uploads[ind].Location)
			stream := tusgo.NewUploadStream(cl, uploads[ind])
			if err = DoUpload(stream, f); err != nil {
				panic(err)
			}
		}()
	}

	wg.Wait()
	fmt.Println("Uploading complete, starting concatenation...")

	// Concatenate partial uploads into a final upload
	final := tusgo.Upload{}
	if _, err = cl.ConcatenateUploads(&final, []tusgo.Upload{*uploads[0], *uploads[1]}, nil); err != nil {
		panic(err)
	}

	fmt.Printf("Final upload location: %s\n", final.Location)

	// Get file info
	u := tusgo.Upload{RemoteOffset: tusgo.OffsetUnknown}
	for {
		if _, err = cl.GetUpload(&u, final.Location); err != nil {
			panic(err)
		}
		// When concatenation still in progress the offset can be either OffsetUnknown or a value less than size
		// depending on server implementation
		if u.RemoteOffset != tusgo.OffsetUnknown && u.RemoteOffset == u.RemoteSize {
			break
		}
		fmt.Println("Waiting concatenation to be finished")
		time.Sleep(2 * time.Second)
	}

	fmt.Printf("Concatenation finished. Offset: %d, Size: %d\n", u.RemoteOffset, u.RemoteSize)
}

func Example_creationAndTransfer() {
	baseURL, err := url.Parse("http://example.com/files")
	if err != nil {
		panic(err)
	}
	cl := tusgo.NewClient(http.DefaultClient, baseURL)
	if _, err = cl.UpdateCapabilities(); err != nil {
		panic(err)
	}

	f, err := os.Open("/tmp/file.txt")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	u := CreateUploadFromFile(f, cl, false)

	stream := tusgo.NewUploadStream(cl, u)
	if err = DoUpload(stream, f); err != nil {
		panic(err)
	}
	fmt.Printf("Uploading complete. Offset: %d, Size: %d\n", u.RemoteOffset, u.RemoteSize)
}

func Example_creationAndTransferWithDeferredSize() {
	baseURL, err := url.Parse("http://example.com/files")
	if err != nil {
		panic(err)
	}
	cl := tusgo.NewClient(http.DefaultClient, baseURL)
	if _, err = cl.UpdateCapabilities(); err != nil {
		panic(err)
	}

	u := tusgo.Upload{}
	if _, err = cl.CreateUpload(&u, tusgo.SizeUnknown, false, nil); err != nil {
		panic(err)
	}
	fmt.Printf("Location: %s\n", u.Location)

	// Open a file to be transferred
	f, err := os.Open("/tmp/file.txt")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	finfo, err := f.Stat()
	if err != nil {
		panic(err)
	}
	u.RemoteSize = finfo.Size() // Set size after the upload has been created on server

	stream := tusgo.NewUploadStream(cl, &u)
	stream.SetUploadSize = true
	if err = DoUpload(stream, f); err != nil {
		panic(err)
	}
	fmt.Printf("Uploading complete. Offset: %d, Size: %d\n", u.RemoteOffset, u.RemoteSize)
}

func Example_checksum() {
	baseURL, err := url.Parse("http://example.com/files")
	if err != nil {
		panic(err)
	}
	cl := tusgo.NewClient(http.DefaultClient, baseURL)
	if _, err = cl.UpdateCapabilities(); err != nil {
		panic(err)
	}

	// Open a file to be transferred
	f, err := os.Open("/tmp/file.txt")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	finfo, err := f.Stat()
	if err != nil {
		panic(err)
	}
	u := tusgo.Upload{Location: "http://example.com/files/foo/bar", RemoteSize: finfo.Size()}

	// We want to use sha1
	stream := tusgo.NewUploadStream(cl, &u).WithChecksumAlgorithm("sha1")
	if err = DoUpload(stream, f); err != nil {
		panic(err)
	}
	fmt.Println("Uploading complete")
}

func Example_transferWithProgressWatch() {
	baseURL, err := url.Parse("http://example.com/files")
	if err != nil {
		panic(err)
	}
	cl := tusgo.NewClient(http.DefaultClient, baseURL)
	if _, err = cl.UpdateCapabilities(); err != nil {
		panic(err)
	}

	// Open a file to be transferred
	f, err := os.Open("/tmp/file1.txt")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	u := CreateUploadFromFile(f, cl, false)

	go func() {
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			fmt.Printf("Progress: %d/%d (%.1f%%)\n", u.RemoteOffset, u.RemoteSize, float64(u.RemoteOffset)/float64(u.RemoteSize)*100)
			if u.RemoteOffset == u.RemoteSize {
				return
			}
		}
	}()

	stream := tusgo.NewUploadStream(cl, u)
	if err = DoUpload(stream, f); err != nil {
		panic(err)
	}
	fmt.Printf("Uploading complete. Offset: %d, Size: %d\n", u.RemoteOffset, u.RemoteSize)
}
