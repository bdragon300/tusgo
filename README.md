# tusgo

[![codecov](https://codecov.io/gh/bdragon300/tusgo/branch/master/graph/badge.svg?token=ZLI69A7FHO)](https://codecov.io/gh/bdragon300/tusgo)

Full-featured Go client for [TUS](https://tus.io) protocol.

## Features

* Resumable chunked and streamed uploading, both using standard Go's `io.Writer` and `io.ReaderFrom` interfaces.
* Client for uploads manipulation such as creation, deletion, concatenation, etc.
* Intermediate data store (for chunked uploads) now is only in-memory. But the transfer can be resumed if your data
  source is seekable.
* Server extensions are supported:
	* `creation` extension -- upload creation
	* `creation-defer-length` -- upload creation without size. Its size is set on the first data transfer
	* `creation-with-upload` -- upload creation and data transferring in one HTTP request
	* `expiration` -- parsing the upload expiration info
	* `checksum` -- data integrity verification for chunked uploads. Many checksum algorithms from Go stdlib are
	  supported
	* `checksum-trailer` -- data integrity verification for streamed uploads. Checksum hash is calculated for all data
	  in stream and is put to HTTP trailer
	* `termination` -- deleting uploads from server
	* `concatenation` -- merge finished uploads into one
	* `concatenation-unfinished` -- merge unfinished uploads (data streams) into one upload

## Installation

```shell
go get github.com/bdragon300/tusgo
```

Go v1.18 or newer is required

## Example

```go
package main

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
)
import "github.com/bdragon300/tusgo"

func doUploadFile(dst *tusgo.UploadStream, f *os.File) error {
	attempts := 10

	// Sync stream and file offsets
	if _, err := dst.Seek(0, io.SeekStart); err != nil {
		return err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return err
	}

	for dst.Upload.RemoteOffset < dst.Upload.RemoteSize && attempts > 0 {
		_, err := io.Copy(dst, f)
		if err == nil {
			break // Transfer has finished
		}

		// Error handling
		attempts--
		if errors.Is(err, tusgo.ErrOffsetsNotSynced) { // Offset is differ from server offset, sync
			if _, err = dst.Sync(); err != nil {
				return err
			}
			if _, err = f.Seek(dst.Tell(), io.SeekStart); err != nil {
				return err
			}
		} else if _, ok := err.(net.Error); !ok {
			return err // Permanent error, no luck
		}
	}
	if attempts == 0 {
		return errors.New("too many attempts to upload the data")
	}
	return nil
}

func createUploadFromFile(f *os.File, cl *tusgo.Client) *tusgo.Upload {
	// Open a file to be transferred
	finfo, err := f.Stat()
	if err != nil {
		panic(err)
	}

	u := tusgo.Upload{}
	if _, err = cl.CreateUpload(&u, finfo.Size(), false, nil); err != nil {
		panic(err)
	}
	return &u
}

func main() {
	baseURL, err := url.Parse("http://example.com/files")
	if err != nil {
		panic(err)
	}
	cl := tusgo.NewClient(http.DefaultClient, baseURL)

	f, err := os.Open("/tmp/file.txt")
	if err != nil {
		panic(err)
	}
	defer f.Close()
	u := createUploadFromFile(f, cl)

	stream := tusgo.NewUploadStream(cl, u)
	if err = doUploadFile(stream, f); err != nil {
		panic(err)
	}
}
```
