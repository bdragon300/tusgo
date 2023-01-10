package tusgo

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
)

func NewUploadStream(client *Client, file File) *UploadStream {
	return &UploadStream{
		ChunkSize:    2 * 1024 * 1024,
		LastResponse: nil,
		client:       client,
		file:         file,
		remoteOffset: 0,
		readBuffer:   bytes.NewBuffer(make([]byte, 0)),
	}
}

type UploadStream struct {
	ChunkSize    int64
	LastResponse *http.Response

	client       *Client
	file         File
	remoteOffset int64
	readBuffer   *bytes.Buffer
}

func (us *UploadStream) WithContext(ctx context.Context) *UploadStream {
	us.client.ctx = ctx
	return us
}

func (us *UploadStream) ReadFrom(r io.Reader) (n int64, err error) {
	var copyErr error
	var bytesRead int64
	remoteOffset := us.remoteOffset

	for copyErr != io.EOF {
		copySize := us.ChunkSize
		if remoteOffset+copySize > us.file.RemoteSize {
			copySize = us.file.RemoteSize - remoteOffset
		}
		if copySize == 0 {
			return
		}

		select {
		case <-us.client.ctx.Done():
			return us.remoteOffset, context.Canceled
		default:
		}

		// Finish reading the intermediate buffer if the previous read has failed
		if us.readBuffer.Len() == 0 {
			bytesRead, copyErr = io.CopyN(us.readBuffer, r, copySize)
			n += bytesRead
			if copyErr != nil && copyErr != io.EOF {
				us.readBuffer.Truncate(0)
				err = copyErr
				return
			}
		}
		if _, remoteOffset, us.LastResponse, err = us.UploadChunk(us.readBuffer); err != nil {
			return
		} else if us.LastResponse.StatusCode >= 300 {
			err = fmt.Errorf("server returned HTTP %d code, %d bytes were not uploaded", us.LastResponse.StatusCode, bytesRead)
			return
		}
		us.readBuffer.Truncate(0)
		if remoteOffset <= us.remoteOffset {
			err = fmt.Errorf("server returned bad next offset: %d, previous offset was %d", remoteOffset, us.remoteOffset)
			return
		}
		us.remoteOffset = remoteOffset
	}
	err = copyErr
	return
}

func (us *UploadStream) Write(p []byte) (n int, err error) {
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
		us.remoteOffset = remoteOffset
	}
	return
}

func (us *UploadStream) GetRemoteOffset() (remoteOffset int64, response *http.Response, err error) {
	us.readBuffer.Truncate(0)
	remoteOffset = -1
	var req *http.Request
	var loc *url.URL
	if loc, err = url.Parse(us.file.Location); err != nil {
		return
	}
	u := us.client.BaseURL.ResolveReference(loc).String()
	if req, err = us.client.GetRequest(http.MethodHead, u, nil, us.client, us.client.client, us.client.capabilities); err != nil {
		return
	}

	if response, err = us.client.client.Do(req.WithContext(us.client.ctx)); err != nil {
		return
	}
	if response.StatusCode < 300 {
		if remoteOffset, err = strconv.ParseInt(response.Header.Get("Upload-Offset"), 10, 64); err != nil {
			return
		}
	}
	return
}

func (us *UploadStream) UploadChunk(buf *bytes.Buffer) (bytesUploaded int, remoteOffset int64, response *http.Response, err error) {
	var copyErr error
	remoteOffset = us.remoteOffset
	copySize := us.ChunkSize
	remoteBytesLeft := us.file.RemoteSize - remoteOffset

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

	var loc *url.URL
	if loc, err = url.Parse(us.file.Location); err != nil {
		return
	}
	u := us.client.BaseURL.ResolveReference(loc).String()

	var req *http.Request
	if req, err = us.client.GetRequest(http.MethodPatch, u, data, us.client, us.client.client, us.client.capabilities); err != nil {
		return
	}

	req.Header.Set("Content-Type", "application/offset+octet-stream")
	req.Header.Set("Content-Length", strconv.FormatInt(copySize, 10))
	req.Header.Set("Upload-Offset", strconv.FormatInt(remoteOffset, 10))

	if response, err = us.client.tusRequest(req); err != nil {
		return
	}
	defer response.Body.Close()
	if response.StatusCode < 300 {
		bytesUploaded = int(copySize)
	}

	if response.StatusCode == http.StatusNoContent {
		if remoteOffset, err = strconv.ParseInt(response.Header.Get("Upload-Offset"), 10, 64); err != nil {
			return
		}
		if remoteOffset <= us.remoteOffset {
			err = fmt.Errorf("server returned bad next offset: %d, previous offset was %d", remoteOffset, us.remoteOffset)
			return
		}
	}
	err = copyErr
	return
}

func (us *UploadStream) Seek(offset int64, whence int) (int64, error) {
	us.readBuffer.Truncate(0)
	var newOffset int64
	switch whence {
	case io.SeekStart:
		newOffset = offset
	case io.SeekCurrent:
		newOffset = us.remoteOffset + offset
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
	us.remoteOffset = newOffset
	return newOffset, nil
}

func (us *UploadStream) Tell() int64 {
	return us.remoteOffset
}

func (us *UploadStream) Len() int64 {
	return us.file.RemoteSize
}
