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

// NewUploadStream constructs a new upload stream. Receives a http client that will be used to make requests, and
// an upload object. During the upload process the given upload is modified, the RemoteOffset field in the first place.
func NewUploadStream(client *Client, upload *Upload) *UploadStream {
	if upload == nil {
		panic("upload is nil")
	}
	const chunkSize = 2 * 1024 * 1024
	return &UploadStream{
		ChunkSize:    chunkSize,
		Upload:       upload,
		client:       client,
		uploadMethod: http.MethodPatch,
		ctx:          client.ctx,
	}
}

// NoChunked assigned to UploadStream.ChunkSize makes the uploading process not to use chunking
const NoChunked = 0 // TODO: example
// TODO: remote ginkgo/gomega from go.mod
// TODO: mention Upload ownership in docs below

// UploadStream is write-only stream with TUS requests as underlying implementation. During creation, the UploadStream
// receives a pointer to Upload object, where it holds the current server offset to write data to. This offset is
// continuously updated during uploading data to the server.
//
// By default, we upload data in chunks, which size is defined in ChunkSize field. To disable chunking, set it to
// NoChunked -- dirty buffer will not be used, and the data will be written to the request body directly.
//
// The approach to work with this stream is described in appropriate methods, but in general it's the following:
//
//  1. Create a stream with an Upload with the offset we want to start writing from
//
//  2. Write the data to stream
//
//  3. If some error has interrupted uploading, call the same method again to continue from the last successful offset
//
// The TUS server generally expects that we write the data on the concrete offset it manages. We use Upload.RemoteOffset
// field to construct a request. If UploadStream local and server remote offsets are not equal, than this stream
// considered "not synced". To sync it with remote offset, use the Sync method.
//
// To use checksum data verification feature, use the WithChecksumAlgorithm method. Note, that the server must support at
// least the 'checksum' extension and the hash algorithm you're using. If ChunkSize is set to NoChunked, the server must
// also support 'checksum-trailer', since we calculate the hash once the whole data will be read, and put the hash to HTTP
// trailer.
//
// To use "Deferred length" feature, before the first write, set the Upload.RemoteSize to the particular size and
// set SetUploadSize field to true. Generally, when using "Deferred length" feature, we create an upload with
// unknown size, and the server expects that we will tell it the size on the first upload request.
// So the very first write to UploadStream for a concrete upload (i.e. when RemoteOffset == 0) generates a request
// with the upload size included.
//
// Errors, which the stream methods may return, along with the Client methods, are:
//
//   - ErrOffsetsNotSynced -- local offset and server offset are not equal. Call Sync method to adjust local offset.
//
//   - ErrChecksumMismatch -- server detects data corruption, if checksum verification feature is used
//
//   - ErrCannotUpload -- unable to write the data to the existing upload. Generally, it means that the upload is full,
//     or this upload is concatenated upload, or it does not accept the data by some reason
type UploadStream struct {
	// ChunkSize determines the chunk size and dirty buffer size for chunking uploading. You can set
	// this value to NoChunked to disable chunking which prevents using dirty buffer. Default is 2MiB
	ChunkSize int64

	// LastResponse is read-only field that contains the last response from server was received by this UploadStream.
	// This is useful, for example, if it's needed to get the response that caused an error.
	LastResponse *http.Response

	// SetUploadSize relates to the "Deferred length" TUS protocol feature. When using this feature, we create an upload
	// with unknown size, and the server expects that we will tell it the size on the first upload request.
	//
	// If SetUploadSize is true, then the very first request for an upload (i.e. when RemoteOffset == 0) will also
	// contain the upload size, which is taken from Upload.RemoteSize field.
	SetUploadSize bool // TODO: example
	// TODO: example when RemoteOffset > 0

	checksumHash        hash.Hash
	rawChecksumHashName string
	Upload              *Upload
	client              *Client
	dirtyBuffer         []byte
	uploadMethod        string
	ctx                 context.Context
}

// WithContext assigns a given context to the copy of stream and returns it
func (us *UploadStream) WithContext(ctx context.Context) *UploadStream {
	res := *us
	res.LastResponse = nil
	res.dirtyBuffer = nil
	res.ctx = ctx
	return &res
}

// WithChecksumAlgorithm sets the checksum algorithm to the copy of stream and returns it
func (us *UploadStream) WithChecksumAlgorithm(name string) *UploadStream {
	// TODO: example with checksum
	res := *us
	res.LastResponse = nil
	res.dirtyBuffer = nil
	// TODO: check if server support this algo?
	if alg, ok := checksum.GetAlgorithm(name); !ok {
		panic(fmt.Sprintf("checksum algorithm %q does not supported", name))
	} else {
		f := checksum.Algorithms[alg]
		res.checksumHash = f()
	}
	res.rawChecksumHashName = name

	return &res
}

// ReadFrom uploads the data read from r, starting from offset Upload.RemoteOffset. Uploading stops when r
// will be fully drawn out or the upload becomes full, whichever comes first. The Upload.RemoteOffset is continuously
// updated with current offset during the process.
// The return value n is the number of bytes read from r.
//
// Here we read r to the dirty buffer by chunks. When the reading has been started, the stream becomes "dirty".
// If the error has occurred in the middle, we keep the failed chunk in the dirty buffer and return an error.
// The stream remains "dirty". On the repeated ReadFrom calls, we try to upload the dirty buffer first before further reading r.
// If error has occurred again, the dirty buffer is kept as it was.
//
// After the uploading has finished successfully, we clear the dirty buffer, and the stream becomes "clean".
//
// If ChunkSize is set to NoChunked, we copy data from r directly to the request body. We don't use the dirty buffer
// in this case, so the stream never becomes "dirty". Also, if checksum feature is used in this case, we put the hash
// to the HTTP trailer, so the "checksum-trailer" server extension is required.
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
	counterRd := &counterReader{Rd: r}
	for uploaded == us.ChunkSize {
		if uploaded, err = us.uploadWithDirtyBuffer(counterRd); err != nil {
			return counterRd.BytesRead, err
		}
	}
	us.dirtyBuffer = nil // Mark stream as clean if the whole data has been uploaded successfully
	return counterRd.BytesRead, err
}

// Write uploads a bytes starting from offset Upload.RemoteOffset. The Upload.RemoteOffset is continuously
// updated with current offset during the process. The return value n is the number of bytes successfully uploaded
// to the server.
//
// Here we read r to the dirty buffer by chunks. When the reading has been started, the stream becomes "dirty".
// Whether an error occurred in the middle or not, the stream will become "clean" after the call. If stream is already
// "dirty" before the call, we ignore this and clear the dirty buffer.
//
// If ChunkSize is set to NoChunked, we copy the whole given bytes to the request body. We don't use the dirty buffer
// in this case, so the stream never becomes "dirty". Also, if checksum feature is used in this case, we put the hash
// to the HTTP trailer, so the "checksum-trailer" server extension is required.
//
// If the bytes to be uploaded doesn't fit to space left in the upload, we upload the data we can and return io.ErrShortWrite.
func (us *UploadStream) Write(p []byte) (n int, err error) {
	// TODO: example
	// TODO: upload progress bar example
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

// Sync method adjusts the Upload.RemoteOffset value if it does not sync with server offset. Usually this method
// need to be called, when an ErrOffsetsNotSynced error was returned by another UploadStream method.
func (us *UploadStream) Sync() (response *http.Response, err error) {
	f := Upload{}
	if response, err = us.client.GetUpload(&f, us.Upload.Location); err == nil {
		us.Upload.RemoteOffset = f.RemoteOffset
	}
	us.LastResponse = response
	return
}

// Seek moves Upload.RemoteOffset to the requested position. Returns new offset
func (us *UploadStream) Seek(offset int64, whence int) (int64, error) {
	var newOffset int64
	switch whence {
	case io.SeekStart:
		newOffset = offset
	case io.SeekCurrent:
		newOffset = us.Upload.RemoteOffset + offset
	case io.SeekEnd:
		newOffset = us.Upload.RemoteSize - 1 + offset
	default:
		panic("unknown whence value")
	}
	if offset >= us.Upload.RemoteSize {
		return newOffset, fmt.Errorf("offset %d exceeds the upload size %d bytes", newOffset, us.Upload.RemoteSize)
	}
	if offset < 0 {
		return newOffset, fmt.Errorf("offset %d is negative", newOffset)
	}
	us.Upload.RemoteOffset = newOffset
	return newOffset, nil
}

// Tell returns the current offset
func (us *UploadStream) Tell() int64 {
	return us.Upload.RemoteOffset
}

// Len returns the upload size
func (us *UploadStream) Len() int64 {
	return us.Upload.RemoteSize
}

// Dirty returns true if stream has been marked "dirty". This means it contains the data chunk, which was failed
// to upload to the server.
func (us *UploadStream) Dirty() bool {
	return us.dirtyBuffer != nil
}

// ForceClean marks the stream as "clean". It erases the data from the dirty buffer.
func (us *UploadStream) ForceClean() {
	us.dirtyBuffer = nil
}

func (us *UploadStream) uploadWithDirtyBuffer(r io.Reader) (uploaded int64, err error) {
	var loc *url.URL
	var offset int64
	var lastResponse *http.Response

	if loc, err = url.Parse(us.Upload.Location); err != nil { // TODO: called for every chunk, optimize
		return
	}
	u := us.client.BaseURL.ResolveReference(loc).String()

	if uploaded, offset, lastResponse, err = us.doUpload(u, r, us.dirtyBuffer, nil); err == nil {
		us.Upload.RemoteOffset = offset
	}
	if lastResponse != nil {
		us.LastResponse = lastResponse
	}
	return
}

func (us *UploadStream) doUpload(requestURL string, data io.Reader, buf []byte, extraHeaders map[string]string) (bytesUploaded int64, offset int64, response *http.Response, err error) {
	const unknownSize int64 = -1
	chunking := buf != nil // Chunking enabled
	offset = us.Upload.RemoteOffset
	if err = us.validate(); err != nil {
		return
	}

	bytesToUpload := unknownSize
	if chunking {
		bytesToUpload = int64(len(buf))
		remoteBytesLeft := us.Upload.RemoteSize - offset
		if bytesToUpload > remoteBytesLeft {
			bytesToUpload = remoteBytesLeft
		}
		if bytesToUpload == 0 {
			return // Return early before creating a request
		}
	}

	// Prevent reading from `data` if error occurred in the following calls
	if us.checksumHash != nil && !chunking {
		if err = us.client.ensureExtension("checksum-trailer"); err != nil {
			return
		}
	}
	var req *http.Request
	if req, err = us.client.GetRequest(us.uploadMethod, requestURL, nil, us.client, us.client.client); err != nil {
		return
	}

	if chunking {
		t, e := io.ReadAtLeast(data, buf, int(bytesToUpload))
		switch e {
		case io.ErrUnexpectedEOF: // Reader has ended early
			bytesToUpload = int64(t)
			buf = buf[:bytesToUpload]
		case io.EOF: // Reader is empty
			return
		default:
			if e != nil {
				err = e
				return
			}
		}
		data = bytes.NewReader(buf)
	}

	if us.checksumHash != nil {
		us.checksumHash.Reset()
		if chunking {
			us.checksumHash.Write(buf)
			sum := us.checksumHash.Sum(make([]byte, 0))
			req.Header.Set("Upload-Checksum", fmt.Sprintf("%s %s", us.rawChecksumHashName, base64.StdEncoding.EncodeToString(sum)))
		} else {
			trailers := map[string]io.Reader{"Upload-Checksum": checksum.NewHashBase64ReadWriter(us.checksumHash, us.rawChecksumHashName+" ")}
			data = checksum.NewDeferTrailerReader(io.TeeReader(data, us.checksumHash), trailers, req)
		}
	}

	req.Body = io.NopCloser(data)
	if bytesToUpload != unknownSize {
		req.ContentLength = bytesToUpload
	}
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	req.Header.Set("Upload-Offset", strconv.FormatInt(offset, 10))

	if us.SetUploadSize && offset == 0 {
		req.Header.Set("Upload-Length", strconv.FormatInt(us.Upload.RemoteSize, 10))
	}

	if len(extraHeaders) > 0 {
		for k, v := range extraHeaders {
			if v == "" {
				req.Header.Del(k)
			} else {
				req.Header.Set(k, v)
			}
		}
	}

	if us.ctx != nil {
		req = req.WithContext(us.ctx)
	}
	if response, err = us.client.tusRequest(us.ctx, req); err != nil {
		return
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusCreated: // For "Creation With Upload" feature
		if us.uploadMethod != http.MethodPost {
			err = ErrUnexpectedResponse
			return
		}
		fallthrough
	case http.StatusNoContent:
		if offset, err = strconv.ParseInt(response.Header.Get("Upload-Offset"), 10, 64); err != nil {
			err = fmt.Errorf("cannot parse Upload-Offset header %q: %w", response.Header.Get("Upload-Offset"), ErrProtocol)
			return
		}
		bytesUploaded = offset - us.Upload.RemoteOffset
		if bytesUploaded < 0 {
			bytesUploaded = 0
		}
		if v := response.Header.Get("Upload-Expires"); v != "" {
			var t time.Time
			if t, err = time.Parse(time.RFC1123, v); err != nil {
				err = fmt.Errorf("cannot parse Upload-Expires RFC1123 header %q: %w", v, ErrProtocol)
				return
			}
			us.Upload.UploadExpired = &t
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

func (us *UploadStream) validate() error {
	if us.Upload.RemoteSize == SizeUnknown {
		panic("upload must have size before start the uploading")
	}
	if us.Upload.RemoteSize < 0 {
		panic(fmt.Sprintf("upload size is negative %d", us.Upload.RemoteSize))
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
