# tusgo

[![codecov](https://codecov.io/gh/bdragon300/tusgo/branch/master/graph/badge.svg?token=ZLI69A7FHO)](https://codecov.io/gh/bdragon300/tusgo)
[![Go Report Card](https://goreportcard.com/badge/github.com/bdragon300/tusgo)](https://goreportcard.com/report/github.com/bdragon300/tusgo)
![GitHub Workflow Status (with branch)](https://img.shields.io/github/actions/workflow/status/bdragon300/tusgo/run-tests.yml?branch=master)
[![Go reference](https://pkg.go.dev/badge/github.com/bdragon300/tusgo)](https://pkg.go.dev/github.com/bdragon300/tusgo)
![GitHub go.mod Go version (subdirectory of monorepo)](https://img.shields.io/github/go-mod/go-version/bdragon300/tusgo)

Full-featured Go client for [TUS](https://tus.io), a protocol for resumable uploads built on HTTP.

Documentation is available at [pkg.go.dev](https://pkg.go.dev/github.com/bdragon300/tusgo)

## Features

* Resumable Upload writer with chunked and streamed mode support. Conforms the `io.Writer`/`io.ReaderFrom`, which allows 
  to use the standard utils such as `io.Copy`
* Client for Upload manipulation such as creation, deletion, concatenation, etc.
* Intermediate data store (for chunked Uploads) now is only in-memory
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
	"time"
)
import "github.com/bdragon300/tusgo"

func UploadWithRetry(dst *tusgo.UploadStream, src *os.File) error {
	// Adjust stream and file pointer to be equal to the remote pointer
	// (if we resume the upload that was interrupted earlier)
	if _, err := dst.Sync(); err != nil {
		return err
	}
	if _, err := src.Seek(dst.Tell(), io.SeekStart); err != nil {
		return err
	}

	_, err := io.Copy(dst, src)
	attempts := 10
	for err != nil && attempts > 0 {
		if _, ok := err.(net.Error); !ok && !errors.Is(err, tusgo.ErrChecksumMismatch) {
			return err // Permanent error, no luck
		}
		time.Sleep(5 * time.Second)
		attempts--
		_, err = io.Copy(dst, src) // Try to resume transfer after error
	}
	if attempts == 0 {
		return errors.New("too many attempts to upload the data")
	}
	return nil
}

func CreateUploadFromFile(f *os.File, cl *tusgo.Client) *tusgo.Upload {
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
	u := CreateUploadFromFile(f, cl)

	stream := tusgo.NewUploadStream(cl, u)
	if err = UploadWithRetry(stream, f); err != nil {
		panic(err)
	}
}
```
