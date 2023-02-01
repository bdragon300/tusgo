package tusgo

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"hash"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/bdragon300/tusgo/checksum"
)

func NewUploadStream(client *Client, upload *Upload) *UploadStream {
	if upload == nil {
		panic("upload is nil")
	}
	const chunkSize = 2 * 1024 * 1024
	return &UploadStream{
		ChunkSize:    chunkSize,
		upload:       upload,
		client:       client,
		uploadMethod: http.MethodPatch,
		ctx:          client.ctx,
	}
}

const NoChunked = 0

type UploadStream struct {
	ChunkSize     int64
	LastResponse  *http.Response
	SetUploadSize bool

	checksumHash     hash.Hash
	checksumHashName checksum.Algorithm
	upload           *Upload
	client           *Client
	dirtyBuffer      []byte
	uploadMethod     string
	ctx              context.Context
}

func (us *UploadStream) WithContext(ctx context.Context) *UploadStream {
	res := *us
	res.LastResponse = nil
	res.dirtyBuffer = nil
	res.ctx = ctx
	return &res
}

func (us *UploadStream) WithChecksumAlgorithm(name checksum.Algorithm) *UploadStream {
	res := *us
	res.LastResponse = nil
	res.dirtyBuffer = nil

	f := checksum.Algorithms[name] // Get algorithm by name from list of known algorithms
	res.checksumHash = f()
	res.checksumHashName = name
	return &res
}

func (us *UploadStream) ReadFrom(r io.Reader) (n int64, err error) {
	if err = us.validate(); err != nil {
		return
	}

	if us.dirtyBuffer != nil {
		if _, err = us.uploadWithDirtyBuffer(bytes.NewReader(us.dirtyBuffer)); err != nil {
			return
		}
		if us.ChunkSize == NoChunked || int64(len(us.dirtyBuffer)) != us.ChunkSize {
			us.dirtyBuffer = nil
		}
	}
	if us.ChunkSize != NoChunked && us.dirtyBuffer == nil {
		us.dirtyBuffer = make([]byte, us.ChunkSize)
	}

	uploaded := us.ChunkSize
	for uploaded == us.ChunkSize {
		if uploaded, err = us.uploadWithDirtyBuffer(r); err != nil {
			return
		}
		n += uploaded
	}
	us.dirtyBuffer = nil // Mark stream as clean if the whole data has been uploaded successfully
	return
}

func (us *UploadStream) Write(p []byte) (n int, err error) {
	if err = us.validate(); err != nil {
		return
	}
	if us.ChunkSize > 0 {
		us.dirtyBuffer = make([]byte, us.ChunkSize)
		defer func() { us.dirtyBuffer = nil }() // Always mark stream as clean, since p is seekable
	}
	var rd io.Reader = bytes.NewReader(p)

	uploaded := us.ChunkSize
	for uploaded == us.ChunkSize {
		if uploaded, err = us.uploadWithDirtyBuffer(rd); err != nil {
			return
		}
		n += int(uploaded)
	}
	if n != len(p) && err == nil {
		err = io.ErrShortWrite
	}
	return
}

func (us *UploadStream) Sync() (response *http.Response, err error) {
	f := Upload{}
	if response, err = us.client.GetUpload(&f, us.upload.Location); err != nil {
		return
	}
	us.upload.RemoteOffset = f.RemoteOffset
	return
}

func (us *UploadStream) Upload(data io.Reader, buf []byte) (bytesUploaded int64, offset int64, response *http.Response, err error) {
	const unknownSize int64 = -1
	if err = us.validate(); err != nil {
		return
	}
	offset = us.upload.RemoteOffset
	bytesToUpload := unknownSize
	if buf != nil {
		bytesToUpload = int64(len(buf))
		remoteBytesLeft := us.upload.RemoteSize - offset
		if bytesToUpload > remoteBytesLeft {
			bytesToUpload = remoteBytesLeft
		}
		if bytesToUpload == 0 {
			return // Return early before creating a request
		}
	}

	var loc *url.URL
	if loc, err = url.Parse(us.upload.Location); err != nil {
		return
	}
	u := us.client.BaseURL.ResolveReference(loc).String()

	var req *http.Request
	if req, err = us.client.GetRequest(us.uploadMethod, u, nil, us.client, us.client.client, us.client.Capabilities); err != nil {
		return
	}

	if buf != nil {
		t, e := io.ReadAtLeast(data, buf, int(bytesToUpload))
		switch e {
		case io.ErrUnexpectedEOF:
			bytesToUpload = int64(t) // Reader has ended early
		case io.EOF:
			return // Reader is empty
		default:
			if e != nil {
				err = e
				return
			}
		}
		data = bytes.NewReader(buf[:bytesToUpload])
	}

	if us.checksumHash != nil {
		us.checksumHash.Reset()

		if buf != nil {
			sumBuf := make([]byte, 0)
			us.checksumHash.Sum(sumBuf)
			req.Header.Set("Upload-Checksum", fmt.Sprintf("%s %s", us.checksumHashName, base64.StdEncoding.EncodeToString(sumBuf)))
		} else {
			if err = us.client.ensureExtension("checksum-trailer"); err != nil {
				return
			}
			trailers := map[string]io.Reader{"Upload-Checksum": checksum.NewHashBase64ReadWriter(us.checksumHash)}
			data = checksum.NewDeferTrailerReader(io.TeeReader(data, us.checksumHash), trailers, req)
		}
	}
	req.Body = io.NopCloser(data)
	if bytesToUpload != unknownSize {
		req.Header.Set("Content-Length", strconv.FormatInt(bytesToUpload, 10))
	}
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	req.Header.Set("Upload-Offset", strconv.FormatInt(offset, 10))

	if us.SetUploadSize && offset == 0 {
		req.Header.Set("Upload-Length", strconv.FormatInt(us.upload.RemoteSize, 10))
	}

	if us.ctx != nil {
		req = req.WithContext(us.ctx)
	}
	if response, err = us.client.tusRequest(us.ctx, req); err != nil {
		return
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusNoContent:
		bytesUploaded = req.ContentLength
		if offset, err = strconv.ParseInt(response.Header.Get("Upload-Offset"), 10, 64); err != nil {
			err = fmt.Errorf("cannot parse Upload-Offset header %q: %w", response.Header.Get("Upload-Offset"), ErrProtocol)
			return
		}
		if v := response.Header.Get("Upload-Expires"); v != "" {
			var t time.Time
			if t, err = time.Parse(time.RFC1123, v); err != nil {
				err = fmt.Errorf("cannot parse Upload-Expires RFC1123 header %q: %w", v, ErrProtocol)
				return
			}
			us.upload.UploadExpired = &t
		}
	case http.StatusConflict:
		err = ErrOffsetsNotSynced
	case http.StatusForbidden:
		err = ErrCannotUpload
	case http.StatusNotFound, http.StatusGone:
		err = ErrUploadDoesNotExist
	case http.StatusRequestEntityTooLarge:
		err = ErrUploadTooLarge
	case 460: // Non-standard HTTP code '460 Checksum Mismatch'
		if us.checksumHash != nil {
			err = ErrChecksumMismatch
			return
		}
		fallthrough
	default:
		err = ErrUnexpectedResponse
	}
	return
}

func (us *UploadStream) Seek(offset int64, whence int) (int64, error) {
	var newOffset int64
	switch whence {
	case io.SeekStart:
		newOffset = offset
	case io.SeekCurrent:
		newOffset = us.upload.RemoteOffset + offset
	case io.SeekEnd:
		newOffset = us.upload.RemoteSize - 1 + offset
	default:
		panic("unknown whence value")
	}
	if offset >= us.upload.RemoteSize {
		return newOffset, fmt.Errorf("offset %d exceeds the upload size %d bytes", newOffset, us.upload.RemoteSize)
	}
	if offset < 0 {
		return newOffset, fmt.Errorf("offset %d is negative", newOffset)
	}
	us.upload.RemoteOffset = newOffset
	return newOffset, nil
}

func (us *UploadStream) Tell() int64 {
	return us.upload.RemoteOffset
}

func (us *UploadStream) Len() int64 {
	return us.upload.RemoteSize
}

func (us *UploadStream) Dirty() bool {
	return us.dirtyBuffer != nil
}

func (us *UploadStream) Reset() {
	us.dirtyBuffer = nil
}

func (us *UploadStream) validate() error {
	if us.upload.RemoteSize == SizeUnknown {
		panic("upload must have size before start the uploading")
	}
	if us.upload.RemoteSize < 0 {
		panic(fmt.Sprintf("upload size is negative %d", us.upload.RemoteSize))
	}
	if us.SetUploadSize {
		if err := us.client.ensureExtension("creation-defer-length"); err != nil {
			return err
		}
	}
	if us.checksumHash != nil {
		if err := us.client.ensureExtension("checksum"); err != nil {
			return err
		}
	}
	if us.ChunkSize < 0 && us.ChunkSize != NoChunked {
		panic("ChunkSize must be either a positive number or NoChunked")
	}
	return nil
}

func (us *UploadStream) uploadWithDirtyBuffer(r io.Reader) (uploaded int64, err error) {
	var offset int64
	if uploaded, offset, us.LastResponse, err = us.Upload(r, us.dirtyBuffer); err != nil {
		return
	}
	if offset <= us.upload.RemoteOffset {
		err = fmt.Errorf("server offset %d did not move forward, new offset is %d: %w", us.upload.RemoteOffset, offset, ErrProtocol)
	}
	us.upload.RemoteOffset = offset
	return
}
