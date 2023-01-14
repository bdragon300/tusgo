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

func NewUploadStream(client *Client, file *File) *UploadStream {
	if file == nil {
		panic("file is nil")
	}
	const chunkSize = 2 * 1024 * 1024
	return &UploadStream{
		ChunkSize:    chunkSize,
		file:         file,
		client:       client,
		uploadMethod: http.MethodPatch, // TODO: method override header
	}
}

type UploadStream struct {
	ChunkSize    int64
	LastResponse *http.Response
	SetFileSize  bool

	checksumHash     hash.Hash
	checksumHashName checksum.Algorithm
	file             *File
	client           *Client
	dirtyBuffer      []byte
	uploadMethod     string
}

func (us *UploadStream) WithContext(ctx context.Context) *UploadStream {
	us.client.ctx = ctx // FIXME: copy object
	return us
}

func (us *UploadStream) WithChecksumAlgorithm(name checksum.Algorithm) *UploadStream {
	f := checksum.Algorithms[name] // Get algorithm by name from list of known algorithms
	us.checksumHash = f()          // FIXME: copy object
	us.checksumHashName = name
	return us
}

func (us *UploadStream) ReadFrom(r io.Reader) (n int64, err error) {
	if err = us.validate(); err != nil {
		return
	}
	uploaded := us.ChunkSize

	if us.dirtyBuffer != nil {
		if _, err = us.uploadWithDirtyBuffer(bytes.NewReader(us.dirtyBuffer)); err != nil {
			return
		}
		if us.ChunkSize == -1 || int64(len(us.dirtyBuffer)) != us.ChunkSize {
			us.dirtyBuffer = nil
		}
	}
	if us.ChunkSize != -1 && us.dirtyBuffer == nil {
		us.dirtyBuffer = make([]byte, us.ChunkSize)
	}

	for uploaded == us.ChunkSize {
		if uploaded, err = us.uploadWithDirtyBuffer(r); err != nil {
			return
		}
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
	uploaded := us.ChunkSize
	var rd io.Reader = bytes.NewReader(p)

	for uploaded == us.ChunkSize {
		if us.ChunkSize != -1 {
			rd = io.LimitReader(rd, us.ChunkSize)
		}
		if uploaded, err = us.uploadWithDirtyBuffer(rd); err != nil {
			return
		}
		n += int(uploaded)
	}
	return
}

func (us *UploadStream) Sync() error {
	loc, err := url.Parse(us.file.Location)
	if err != nil {
		return err
	}
	u := us.client.BaseURL.ResolveReference(loc).String()
	req, err := us.client.GetRequest(http.MethodHead, u, nil, us.client, us.client.client, us.client.capabilities)
	if err != nil {
		return err
	}
	if us.client.ctx != nil {
		req = req.WithContext(us.client.ctx)
	}

	if us.LastResponse, err = us.client.client.Do(req); err != nil {
		return err
	}
	if us.LastResponse.StatusCode >= 300 {
		switch us.LastResponse.StatusCode {
		case http.StatusNotFound, http.StatusGone, http.StatusForbidden:
			return ErrFileDoesNotExist
		default:
			return ErrUnknown
		}
	}
	if us.file.RemoteOffset, err = strconv.ParseInt(us.LastResponse.Header.Get("Upload-Offset"), 10, 64); err != nil {
		return err
	}
	return nil
}

func (us *UploadStream) Upload(data io.Reader, buf []byte) (bytesUploaded int64, offset int64, response *http.Response, err error) {
	if err = us.validate(); err != nil {
		return
	}
	offset = us.file.RemoteOffset
	bytesToUpload := int64(-1)
	if buf != nil {
		bytesToUpload = int64(len(buf))
	}

	remoteBytesLeft := us.file.RemoteSize - offset
	if bytesToUpload > remoteBytesLeft {
		bytesToUpload = remoteBytesLeft
	}
	if bytesToUpload == 0 {
		return
	}

	var loc *url.URL
	if loc, err = url.Parse(us.file.Location); err != nil {
		return
	}
	u := us.client.BaseURL.ResolveReference(loc).String()

	var req *http.Request
	if req, err = us.client.GetRequest(us.uploadMethod, u, nil, us.client, us.client.client, us.client.capabilities); err != nil {
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
		if err = us.client.ensureExtension("checksum"); err != nil {
			return
		}
		us.checksumHash.Reset()

		if buf != nil {
			sumBuf := make([]byte, 0)
			us.checksumHash.Sum(sumBuf)
			req.Header.Set("Upload-Checksum", fmt.Sprintf("%s %s", us.checksumHashName, base64.StdEncoding.EncodeToString(sumBuf)))
		} else {
			if err = us.client.ensureExtension("checksum-trailer"); err != nil {
				return
			}
			trailers := map[string]io.Reader{"Upload-Checksum": checksum.HashReader{us.checksumHash}}
			data = checksum.NewTrailerReader(io.TeeReader(data, us.checksumHash), trailers, req)
		}
	}
	req.Body = io.NopCloser(data)
	req.Header.Set("Content-Length", strconv.FormatInt(bytesToUpload, 10))
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	req.Header.Set("Upload-Offset", strconv.FormatInt(offset, 10))

	if us.SetFileSize && offset == 0 {
		if err = us.client.ensureExtension("creation-defer-length"); err != nil {
			return
		}
		req.Header.Set("Upload-Length", strconv.FormatInt(us.file.RemoteSize, 10))
	}

	if us.client.ctx != nil {
		req = req.WithContext(us.client.ctx)
	}
	if response, err = us.client.tusRequest(req); err != nil {
		return
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusNoContent:
		bytesUploaded = bytesToUpload
		if offset, err = strconv.ParseInt(response.Header.Get("Upload-Offset"), 10, 64); err != nil {
			return
		}
		if v := response.Header.Get("Upload-Expires"); v != "" {
			var t time.Time
			if t, err = time.Parse(time.RFC1123, v); err != nil {
				return
			}
			us.file.UploadExpired = &t
		}
	case http.StatusConflict:
		err = ErrOffsetsNotSynced
	case http.StatusNotFound, http.StatusGone, http.StatusForbidden:
		err = ErrFileDoesNotExist
	case http.StatusRequestEntityTooLarge:
		err = ErrFileTooLarge
	case 460: // Non-standard HTTP code '460 Checksum Mismatch'
		if us.checksumHash != nil {
			err = ErrChecksumMismatch
			return
		}
		fallthrough
	default:
		err = ErrUnknown
		if response.StatusCode < 300 {
			err = fmt.Errorf("server returned unexpected %d HTTP code: %w", response.StatusCode, ErrProtocol)
		}
	}
	return
}

func (us *UploadStream) Seek(offset int64, whence int) (int64, error) {
	var newOffset int64
	switch whence {
	case io.SeekStart:
		newOffset = offset
	case io.SeekCurrent:
		newOffset = us.file.RemoteOffset + offset
	case io.SeekEnd:
		newOffset = us.file.RemoteSize - 1 + offset
	default:
		panic("unknown whence value")
	}
	if offset >= us.file.RemoteSize {
		return newOffset, fmt.Errorf("offset %d exceeds the file size %d bytes", newOffset, us.file.RemoteSize)
	}
	if offset < 0 {
		return newOffset, fmt.Errorf("offset %d is negative", newOffset)
	}
	us.file.RemoteOffset = newOffset
	return newOffset, nil
}

func (us *UploadStream) Tell() int64 {
	return us.file.RemoteOffset
}

func (us *UploadStream) Len() int64 {
	return us.file.RemoteSize
}

func (us *UploadStream) Dirty() bool {
	return us.dirtyBuffer != nil
}

func (us *UploadStream) Reset() {
	us.dirtyBuffer = nil
}

func (us *UploadStream) validate() error {
	if us.file.RemoteSize == FileSizeUnknown {
		panic("file with unknown size")
	}
	if us.file.RemoteSize < 0 {
		panic(fmt.Sprintf("file size is negative %d", us.file.RemoteSize))
	}
	if us.SetFileSize {
		if err := us.client.ensureExtension("creation-defer-length"); err != nil {
			return err
		}
	}
	if us.ChunkSize <= 0 && us.ChunkSize != -1 {
		panic("ChunkSize must be either a positive number or -1")
	}
	return nil
}

func (us *UploadStream) uploadWithDirtyBuffer(r io.Reader) (uploaded int64, err error) {
	var offset int64
	if _, offset, us.LastResponse, err = us.Upload(r, us.dirtyBuffer); err != nil {
		return
	}
	if offset <= us.file.RemoteOffset {
		err = fmt.Errorf("server offset %d did not move forward, new offset is %d: %w", us.file.RemoteOffset, offset, ErrProtocol)
		return
	}
	us.file.RemoteOffset = offset
	return
}
