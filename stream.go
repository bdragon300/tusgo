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
	return &UploadStream{
		ChunkSize:    2 * 1024 * 1024,
		file:         file,
		client:       client,
		readBuffer:   bytes.NewBuffer(make([]byte, 0)),
		uploadMethod: http.MethodPatch,
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
	readBuffer       *bytes.Buffer
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
	var copyErr error
	var bytesRead int64
	if err = us.validate(); err != nil {
		return
	}
	remoteOffset := us.file.RemoteOffset

	for copyErr != io.EOF {
		copySize := us.ChunkSize
		if remoteOffset+copySize > us.file.RemoteSize {
			copySize = us.file.RemoteSize - remoteOffset
		}
		if copySize == 0 {
			return
		}

		if us.client.ctx != nil {
			select {
			case <-us.client.ctx.Done():
				err = context.Canceled
				return
			default:
			}
		}

		if !us.Dirty() { // TODO: flag to read directly from r (unsafe). (Because this is too much memory may be consumed on much data and Chunksize == len of data)
			bytesRead, copyErr = io.CopyN(us.readBuffer, r, copySize)
			n += bytesRead
			if copyErr != nil && copyErr != io.EOF {
				us.readBuffer.Truncate(0)
				err = copyErr
				return
			}
			if bytesRead == 0 {
				return
			}
		}
		if _, remoteOffset, us.LastResponse, err = us.UploadChunk(us.readBuffer); err != nil {
			return
		}
		if remoteOffset <= us.file.RemoteOffset {
			err = fmt.Errorf("server offset %d did not move forward, new offset is %d: %w", us.file.RemoteOffset, remoteOffset, ErrProtocol)
			return
		}
		us.file.RemoteOffset = remoteOffset
		us.readBuffer.Truncate(0)
	}
	err = copyErr
	return
}

func (us *UploadStream) Write(p []byte) (n int, err error) {
	if err = us.validate(); err != nil {
		return
	}
	us.readBuffer.Truncate(0)
	bytesUploaded := 1
	buf := bytes.NewBuffer(p)
	var remoteOffset int64

	for buf.Len() > 0 && bytesUploaded > 0 {
		bytesUploaded, remoteOffset, us.LastResponse, err = us.UploadChunk(buf)
		n += bytesUploaded
		if err != nil {
			return
		} else if us.LastResponse.StatusCode >= 300 {
			err = fmt.Errorf("server returned HTTP %d code", us.LastResponse.StatusCode)
			return
		}
		us.file.RemoteOffset = remoteOffset
	}
	return
}

func (us *UploadStream) Sync() error {
	us.readBuffer.Truncate(0)
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

func (us *UploadStream) UploadChunk(buf *bytes.Buffer) (bytesUploaded int, remoteOffset int64, response *http.Response, err error) {
	var copyErr error
	remoteOffset = us.file.RemoteOffset
	copySize := us.ChunkSize
	remoteBytesLeft := us.file.RemoteSize - remoteOffset

	if err = us.validate(); err != nil {
		return
	}
	if copySize > remoteBytesLeft {
		copySize = remoteBytesLeft
	}
	if copySize > int64(buf.Len()) {
		copySize = int64(buf.Len())
	}
	if remoteBytesLeft <= us.ChunkSize && remoteBytesLeft < int64(buf.Len()) {
		copyErr = io.ErrShortWrite // We've reached the file end, but buffer contains more data
	}
	if copySize == 0 {
		return
	}
	data := io.LimitReader(buf, copySize)
	if us.checksumHash != nil {
		if err = us.client.ensureExtension("checksum"); err != nil {
			return
		}
		us.checksumHash.Reset()
		data = io.TeeReader(data, us.checksumHash)
	}

	var loc *url.URL
	if loc, err = url.Parse(us.file.Location); err != nil {
		return
	}
	u := us.client.BaseURL.ResolveReference(loc).String()

	var req *http.Request
	if req, err = us.client.GetRequest(us.uploadMethod, u, data, us.client, us.client.client, us.client.capabilities); err != nil {
		return
	}
	if us.client.ctx != nil {
		req = req.WithContext(us.client.ctx)
	}

	req.Header.Set("Content-Type", "application/offset+octet-stream")
	req.Header.Set("Content-Length", strconv.FormatInt(copySize, 10))
	req.Header.Set("Upload-Offset", strconv.FormatInt(remoteOffset, 10))
	if us.SetFileSize && remoteOffset == 0 {
		if err = us.client.ensureExtension("creation-defer-length"); err != nil {
			return
		}
		req.Header.Set("Upload-Length", strconv.FormatInt(us.file.RemoteSize, 10))
	}
	if us.checksumHash != nil {
		sum := make([]byte, 0)
		us.checksumHash.Sum(sum)
		req.Header.Set("Upload-Checksum", fmt.Sprintf("%s %s", us.checksumHashName, base64.StdEncoding.EncodeToString(sum)))
	}

	if response, err = us.client.tusRequest(req); err != nil {
		return
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusNoContent:
		bytesUploaded = int(copySize)
		if remoteOffset, err = strconv.ParseInt(response.Header.Get("Upload-Offset"), 10, 64); err != nil {
			return
		}
		if v := response.Header.Get("Upload-Expires"); v != "" {
			var t time.Time
			if t, err = time.Parse(time.RFC1123, v); err != nil {
				return
			}
			us.file.UploadExpired = &t
		}
		err = copyErr
	case http.StatusConflict:
		err = ErrOffsetsNotSynced
	case http.StatusNotFound, http.StatusGone, http.StatusForbidden:
		err = ErrFileDoesNotExist
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
	us.readBuffer.Truncate(0)
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
	return us.readBuffer.Len() > 0
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
	return nil
}
