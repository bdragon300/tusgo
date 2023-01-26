package tusgo

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/bdragon300/tusgo/checksum"
)

// NewClient returns a new Client instance with given underlying http client and base url where the requests will be
// headed to
func NewClient(client *http.Client, baseURL *url.URL) *Client {
	c := &Client{
		ProtocolVersion: "1.0.0",
		GetRequest:      newRequest,
		client:          client,
		BaseURL:         baseURL,
	}
	if client == nil {
		c.client = http.DefaultClient
	}
	if baseURL == nil {
		c.BaseURL, _ = url.Parse("http://example.com/files")
	}
	return c
}

type GetRequestFunc func(method, url string, body io.Reader, tusClient *Client, httpClient *http.Client, capabilities *ServerCapabilities) (*http.Request, error)

// Client implements method that manage uploads and retrieve server information. To send data to server use UploadStream.
type Client struct {
	BaseURL *url.URL

	// TUS protocol version we're using. This value is sent in Tus-Resumable HTTP header. Default is "1.0.0"
	ProtocolVersion string

	// Server capabilities and settings. The method UpdateCapabilities queries actual capabilities from a server
	// and fills this variable
	Capabilities *ServerCapabilities

	// GetRequest is a callback function that are called by the library to get a new request object
	// By default it returns a new empty http.Request
	GetRequest GetRequestFunc

	client *http.Client
	ctx    context.Context
}

// WithContext returns a client copy with given context object assigned to it. If context assigned, it will be
// used in every HTTP request further made by this client.
func (c *Client) WithContext(ctx context.Context) *Client {
	res := *c
	res.ctx = ctx
	return &res
}

// GetFile obtains info about upload by location. Receives a pointer to File variable, that fills with this info.
// Returns http response from server (with closed body) and error (if any).
//
// For regular upload we fill in just a remote offset and set Partial flag. For final concatenated uploads we also
// may set upload size (if server provided). Also, we may set remote offset to -1 for concatenated final uploads, if
// concatenation still in progress on server side, and offset is unknown for the moment.
//
// This method may return ErrFileDoesNotExist error if upload with such location has not found on the server. If other
// unexpected response has received from the server, method returns ErrUnexpectedResponse
func (c *Client) GetFile(location string, f *File) (response *http.Response, err error) {
	if f == nil {
		panic("f is nil")
	}
	*f = File{}

	var loc *url.URL
	if loc, err = url.Parse(location); err != nil {
		return
	}
	u := c.BaseURL.ResolveReference(loc).String()

	var req *http.Request
	if req, err = c.GetRequest(http.MethodHead, u, nil, c, c.client, c.Capabilities); err != nil {
		return
	}
	if response, err = c.tusRequest(c.ctx, req); err != nil {
		return
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
		// TODO: can metadata, Expired be returned?
		f.Location = location
		f.Partial = response.Header.Get("Upload-Concat") == "partial"
		uploadOffset := response.Header.Get("Upload-Offset")
		// Upload-Offset may not be present if final upload concatenation still in progress on server side
		if uploadOffset == "" {
			if response.Header.Get("Upload-Concat") != "final" {
				err = fmt.Errorf("lack of Upload-Offset required header in response: %w", ErrProtocol)
				return
			}
			f.RemoteOffset = -1
		} else if uploadOffset != "" {
			if f.RemoteOffset, err = strconv.ParseInt(uploadOffset, 10, 64); err != nil {
				err = fmt.Errorf("cannot parse Upload-Offset header %q: %w", uploadOffset, ErrProtocol)
				return
			}
		}
		// Responses for final concatenated upload may contain Upload-Length header
		if v := response.Header.Get("Upload-Length"); v != "" {
			if f.RemoteSize, err = strconv.ParseInt(v, 10, 64); err != nil {
				err = fmt.Errorf("cannot parse Upload-Length header %q: %w", v, ErrProtocol)
				return
			}
		}
	case http.StatusNotFound, http.StatusGone, http.StatusForbidden:
		err = ErrFileDoesNotExist
	default:
		err = ErrUnexpectedResponse
	}
	return
}

// CreateFile creates upload on the server. Receives a pointer to File, that also is filled with location of a created
// upload. Returns http response from server (with closed body) and error (if any).
//
// Server must support "creation" extension. We create an upload with given size and metadata.
// If Partial flag is true, we create a partial upload. Metadata map keys must not contain spaces.
//
// If upload size is equal to FileSizeUnknown, we create an upload with deferred size, i.e. upload with size that is
// unknown for a moment, but must be known once the upload will be started. Server must also support
// "creation-defer-length" extension for this feature.
//
// This method may return ErrFileTooLarge if upload size exceeds maximum MaxSize that server is capable to accept.
// If other unexpected response has received from the server, method returns ErrUnexpectedResponse
func (c *Client) CreateFile(f *File) (response *http.Response, err error) {
	if f == nil {
		panic("f is nil")
	}
	if err = c.ensureExtension("creation"); err != nil {
		return
	}

	var req *http.Request
	if req, err = c.GetRequest(http.MethodPost, c.BaseURL.String(), nil, c, c.client, c.Capabilities); err != nil {
		return
	}

	req.Header.Set("Content-Length", strconv.FormatInt(0, 10))
	if f.Partial {
		req.Header.Set("Upload-Concat", "partial")
	}
	switch {
	case f.RemoteSize == FileSizeUnknown:
		if err = c.ensureExtension("creation-defer-length"); err != nil {
			return
		}
		req.Header.Set("Upload-Defer-Length", "1")
	case f.RemoteSize > 0:
		req.Header.Set("Upload-Length", strconv.FormatInt(f.RemoteSize, 10))
	default:
		panic(fmt.Sprintf("file size is negative: %d", f.RemoteSize))
	}

	if len(f.Metadata) > 0 {
		var meta string
		if meta, err = EncodeMetadata(f.Metadata); err != nil {
			return
		}
		req.Header.Set("Upload-Metadata", meta)
	}

	if response, err = c.tusRequest(c.ctx, req); err != nil {
		return
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusCreated:
		f.Location = response.Header.Get("Location")
	case http.StatusRequestEntityTooLarge:
		err = ErrFileTooLarge
	default:
		err = ErrUnexpectedResponse
	}

	return
}

// CreateFileWithData creates an upload on the server and sends its data in the same HTTP request. Receives a stream
// and data to upload. Returns count of bytes uploaded and error (if any).
//
// Server must support "creation-with-upload" extension for this feature.
//
// This method may return ErrUnsupportedFeature if server doesn't support an extension. Also, it may return all errors
// the UploadStream methods may return.
func (c *Client) CreateFileWithData(stream *UploadStream, data []byte) (uploadedBytes int, err error) {
	if stream == nil {
		panic("stream is nil")
	}
	if err = c.ensureExtension("creation-with-upload"); err != nil {
		return
	}
	prevStream := *stream
	stream.ChunkSize = int64(len(data)) // Data must be uploaded in one request
	stream.uploadMethod = http.MethodPost

	uploadedBytes, err = stream.Write(data)

	stream.ChunkSize = prevStream.ChunkSize
	stream.uploadMethod = prevStream.uploadMethod
	return
}

// DeleteFile deletes an upload. Receives a File with upload info. Returns http response from server
// (with closed body) and error (if any).
//
// Server must support "termination" extension to be able to delete uploads.
//
// This method may return ErrFileDoesNotExist error if such upload has not found on the server, ErrUnsupportedFeature if
// the server doesn't support "termination" extension. If unexpected response has received from the
// server, the method returns ErrUnexpectedResponse
func (c *Client) DeleteFile(f *File) (response *http.Response, err error) {
	if f == nil {
		panic("f is nil")
	}
	if err = c.ensureExtension("termination"); err != nil {
		return
	}

	var req *http.Request
	var loc *url.URL
	if loc, err = url.Parse(f.Location); err != nil {
		return
	}
	u := c.BaseURL.ResolveReference(loc).String()
	if req, err = c.GetRequest(http.MethodDelete, u, nil, c, c.client, c.Capabilities); err != nil {
		return
	}
	if response, err = c.tusRequest(c.ctx, req); err != nil {
		return
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusNoContent:
	case http.StatusNotFound, http.StatusGone, http.StatusForbidden:
		err = ErrFileDoesNotExist
	default:
		err = ErrUnexpectedResponse
	}

	return
}

// ConcatenateFiles makes a request to concatenate the partial uploads created before into one final upload. Final
// File object will be filled with location of a created final upload. Returns http response from server
// (with closed body) and error (if any).
//
// Server must support "concatenation" extension for this feature. Typically, partial uploads must be fully uploaded
// to the server, but if server supports "concatenation-unfinished" extension, it may accept unfinished uploads.
//
// This method may return ErrUnsupportedFeature if server doesn't support extension, or ErrUnexpectedResponse if
// unexpected response has been received from server.
func (c *Client) ConcatenateFiles(final *File, files []File) (response *http.Response, err error) {
	if len(files) == 0 {
		panic("must be at least one file to concatenate")
	}
	if final == nil {
		panic("final is nil")
	}
	if err = c.ensureExtension("concatenation"); err != nil {
		return
	}

	var req *http.Request
	if req, err = c.GetRequest(http.MethodPost, c.BaseURL.String(), nil, c, c.client, c.Capabilities); err != nil {
		return
	}

	locations := make([]string, 0)
	for _, f := range files {
		if !f.Partial {
			return nil, fmt.Errorf("file %q is not partial", f.Location)
		}
		locations = append(locations, f.Location)
	}
	req.Header.Set("Upload-Concat", "final;"+strings.Join(locations, " "))

	if len(final.Metadata) > 0 {
		var meta string
		if meta, err = EncodeMetadata(final.Metadata); err != nil {
			return
		}
		req.Header.Set("Upload-Metadata", meta)
	}

	if response, err = c.tusRequest(c.ctx, req); err != nil {
		return
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusCreated:
		final.Partial = false
		final.Location = response.Header.Get("Location")
	case http.StatusNotFound, http.StatusGone: // TODO: check on server
		err = fmt.Errorf("unable to concatenate files: %w", ErrFileDoesNotExist)
	default:
		err = ErrUnexpectedResponse
	}
	return
}

// ConcatenateStreams makes a request to concatenate partial uploads from given streams into one final upload. Final
// File object will be filled with location of a created final upload. Returns http response from server
// (with closed body) and error (if any).
//
// Server must support "concatenation" extension for this feature. Streams with pointers that not point to an end of
// streams are treated as unfinished -- server must support "concatenation-unfinished" in this case.
//
// This method may return ErrUnsupportedFeature if server doesn't support extension, or ErrUnexpectedResponse if
// unexpected response has been received from server.
func (c *Client) ConcatenateStreams(final *File, streams []*UploadStream) (response *http.Response, err error) {
	if len(streams) == 0 {
		panic("must be at least one stream to concatenate")
	}

	files := make([]File, 0)
	for i, s := range streams {
		if s.Tell() < s.Len() {
			if err = c.ensureExtension("concatenation-unfinished"); err != nil {
				return nil, fmt.Errorf("stream #%d is not finished: %w", i, err)
			}
		}
		files = append(files, *s.file)
	}

	return c.ConcatenateFiles(final, files)
}

// UpdateCapabilities gathers server capabilities and updates Capabilities variable. Returns http response from server
// (with closed body) and error (if any).
func (c *Client) UpdateCapabilities() (response *http.Response, err error) {
	var req *http.Request
	if req, err = c.GetRequest(http.MethodOptions, c.BaseURL.String(), nil, c, c.client, c.Capabilities); err != nil {
		return
	}
	if response, err = c.tusRequest(c.ctx, req); err != nil {
		return
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusNoContent, http.StatusOK:
		c.Capabilities = &ServerCapabilities{}
		if v := response.Header.Get("Tus-Max-Size"); v != "" {
			if c.Capabilities.MaxSize, err = strconv.ParseInt(v, 10, 64); err != nil {
				err = fmt.Errorf("cannot parse Tus-Max-Size integer value %q: %w", v, ErrProtocol)
				return
			}
		}
		if v := response.Header.Get("Tus-Extension"); v != "" {
			c.Capabilities.Extensions = strings.Split(v, ",")
		}
		if v := response.Header.Get("Tus-Version"); v != "" {
			c.Capabilities.ProtocolVersions = strings.Split(v, ",")
		}
		if v := response.Header.Get("Tus-Checksum-Algorithm"); v != "" {
			names := strings.Split(v, ",")
			for _, n := range names {
				if algo, ok := checksum.GetAlgorithm(n); ok {
					c.Capabilities.ChecksumAlgorithms = append(c.Capabilities.ChecksumAlgorithms, algo)
				}
			}
		}
	default:
		err = ErrUnexpectedResponse
	}
	return
}

func (c *Client) tusRequest(ctx context.Context, req *http.Request) (response *http.Response, err error) {
	if req.Method != http.MethodOptions && req.Header.Get("Tus-Resumable") == "" {
		req.Header.Set("Tus-Resumable", c.ProtocolVersion)
	}
	if ctx != nil {
		req = req.WithContext(ctx)
	}
	response, err = c.client.Do(req)
	if response.StatusCode == http.StatusPreconditionFailed {
		versions := response.Header.Get("Tus-Version")
		err = fmt.Errorf("request protocol version %q, server supported versions: %q: %w", c.ProtocolVersion, versions, ErrProtocol)
	} else if v := response.Header.Get("Tus-Resumable"); v != c.ProtocolVersion {
		err = fmt.Errorf(
			"server response protocol version %q, requested version %q: %w",
			v, c.ProtocolVersion, ErrProtocol,
		)
	}
	return
}

func (c *Client) ensureExtension(extension string) error {
	if c.Capabilities == nil {
		if _, err := c.UpdateCapabilities(); err != nil {
			return fmt.Errorf("cannot obtain server capabilities: %w", err)
		}
	}
	for _, e := range c.Capabilities.Extensions {
		if extension == e {
			return nil
		}
	}
	return fmt.Errorf("server extension %q is required: %w", extension, ErrUnsupportedFeature)
}

// EncodeMetadata converts a map of values to Tus Upload-Metadata header format
func EncodeMetadata(metadata map[string]string) (string, error) {
	var encoded []string

	for k, v := range metadata {
		if strings.Contains(k, " ") {
			return "", fmt.Errorf("key %q contains spaces", k)
		}
		encoded = append(encoded, fmt.Sprintf("%s %s", k, base64.StdEncoding.EncodeToString([]byte(v))))
	}

	return strings.Join(encoded, ","), nil
}

func newRequest(method, url string, body io.Reader, tusClient *Client, _ *http.Client, _ *ServerCapabilities) (*http.Request, error) {
	return http.NewRequest(method, url, body)
}
