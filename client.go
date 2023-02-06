package tusgo

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
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

// Client contains methods to manipulate server uploads except for uploading data. This includes creating, deleting,
// getting the information, making concatenated uploads from partial ones. For uploading the data please see UploadStream
//
// The following errors the methods may return:
//
//   - ErrProtocol -- unexpected condition detected in a successful server response
//
//   - ErrUnsupportedFeature -- to do requested action we need the extension, that server was not advertised in capabilities
//
//   - ErrUploadTooLarge -- size of the requested upload more than server ready to accept. See ServerCapabilities.MaxSize
//
//   - ErrUploadDoesNotExist -- requested upload does not exist or access denied
//
//   - ErrUnexpectedResponse -- unexpected server response code
type Client struct {
	// BaseURL is base url the client making queries to. For example, "http://example.com/files"
	BaseURL *url.URL

	// ProtocolVersion is TUS protocol version will be used in requests. Default is "1.0.0"
	ProtocolVersion string

	// Server capabilities and settings. Use UpdateCapabilities to query the capabilities from a server
	Capabilities *ServerCapabilities

	// GetRequest is a callback function that are called by the library to get a new request object
	// By default it returns a new empty http.Request
	GetRequest GetRequestFunc

	client *http.Client
	ctx    context.Context
}

type GetRequestFunc func(method, url string, body io.Reader, tusClient *Client, httpClient *http.Client) (*http.Request, error)

// WithContext returns a client copy with given context object assigned to it
func (c *Client) WithContext(ctx context.Context) *Client {
	res := *c
	res.ctx = ctx
	return &res
}

// GetUpload obtains an upload by location. Fills `u` variable with upload info.
// Returns http response from server (with closed body) and error (if any).
//
// For regular upload we fill in just a remote offset and set Partial flag. For final concatenated uploads we also
// may set upload size (if server provided). Also, we may set remote offset to OffsetUnknown for concatenated final
// uploads, if concatenation still in progress on server side.
//
// This method may return ErrUploadDoesNotExist error if upload with such location has not found on the server. If other
// unexpected response has received from the server, method returns ErrUnexpectedResponse
func (c *Client) GetUpload(u *Upload, location string) (response *http.Response, err error) {
	if u == nil {
		panic("u is nil")
	}

	var loc *url.URL
	if loc, err = url.Parse(location); err != nil {
		return
	}
	ref := c.BaseURL.ResolveReference(loc).String()

	var req *http.Request
	if req, err = c.GetRequest(http.MethodHead, ref, nil, c, c.client); err != nil {
		return
	}
	if response, err = c.tusRequest(c.ctx, req); err != nil {
		return
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusOK:
		// TODO: can metadata, Expired be returned?
		u2 := Upload{}
		u2.Location = location
		u2.Partial = response.Header.Get("Upload-Concat") == "partial"

		uploadOffset := response.Header.Get("Upload-Offset")
		// Upload-Offset may not be present if final upload concatenation still in progress on server side
		if uploadOffset == "" {
			if response.Header.Get("Upload-Concat") != "final" {
				err = fmt.Errorf("lack of Upload-Offset required header in response: %w", ErrProtocol)
				return
			}
			u2.RemoteOffset = OffsetUnknown
		} else if uploadOffset != "" {
			if u2.RemoteOffset, err = strconv.ParseInt(uploadOffset, 10, 64); err != nil {
				err = fmt.Errorf("cannot parse Upload-Offset header %q: %w", uploadOffset, ErrProtocol)
				return
			}
		}
		// Responses for final concatenated upload may contain Upload-Length header
		if v := response.Header.Get("Upload-Length"); v != "" {
			if u2.RemoteSize, err = strconv.ParseInt(v, 10, 64); err != nil {
				err = fmt.Errorf("cannot parse Upload-Length header %q: %w", v, ErrProtocol)
				return
			}
		}
		if v := response.Header.Get("Upload-Metadata"); v != "" {
			if u2.Metadata, err = DecodeMetadata(v); err != nil {
				err = fmt.Errorf("cannot parse Upload-Metadata header %q: %w", v, ErrProtocol) // TODO: keep err
			}
		}
		*u = u2
	case http.StatusNotFound, http.StatusGone, http.StatusForbidden:
		err = ErrUploadDoesNotExist
	default:
		err = ErrUnexpectedResponse
	}
	return
}

// CreateUpload creates upload on the server. Fills `u` with upload that was created.
// Returns http response from server (with closed body) and error (if any).
//
// Server must support "creation" extension. We create an upload with given size and metadata.
// If Partial flag is true, we create a partial upload. Metadata map keys must not contain spaces.
//
// If `remoteSize` is equal to SizeUnknown, we create an upload with deferred size, i.e. upload with size that is
// unknown for a moment, but must be known once the upload will be started. Server must also support
// "creation-defer-length" extension for this feature.
//
// This method may return ErrUploadTooLarge if upload size exceeds maximum MaxSize that server is capable to accept.
// If other unexpected response has received from the server, method returns ErrUnexpectedResponse
func (c *Client) CreateUpload(u *Upload, remoteSize int64, partial bool, meta map[string]string) (response *http.Response, err error) {
	if u == nil {
		panic("u is nil")
	}
	if err = c.ensureExtension("creation"); err != nil {
		return
	}

	var req *http.Request
	if req, err = c.GetRequest(http.MethodPost, c.BaseURL.String(), nil, c, c.client); err != nil {
		return
	}

	req.Header.Set("Content-Length", strconv.FormatInt(0, 10))
	if partial {
		req.Header.Set("Upload-Concat", "partial")
	}
	switch {
	case remoteSize == SizeUnknown:
		if err = c.ensureExtension("creation-defer-length"); err != nil {
			return
		}
		req.Header.Set("Upload-Defer-Length", "1")
	case remoteSize > 0:
		req.Header.Set("Upload-Length", strconv.FormatInt(remoteSize, 10))
	default:
		panic(fmt.Sprintf("upload size is negative: %d", remoteSize))
	}

	if len(meta) > 0 {
		var m string
		if m, err = EncodeMetadata(meta); err != nil {
			return
		}
		req.Header.Set("Upload-Metadata", m)
	}

	if response, err = c.tusRequest(c.ctx, req); err != nil {
		return
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusCreated:
		u2 := Upload{}
		u2.Location = response.Header.Get("Location")
		u2.Metadata = meta
		u2.Partial = partial
		u2.RemoteSize = remoteSize
		if v := response.Header.Get("Upload-Expires"); v != "" {
			var t time.Time
			if t, err = time.Parse(time.RFC1123, v); err != nil {
				err = fmt.Errorf("cannot parse Upload-Expires RFC1123 header %q: %w", v, ErrProtocol)
				return
			}
			u2.UploadExpired = &t
		}
		*u = u2
	case http.StatusRequestEntityTooLarge:
		err = ErrUploadTooLarge
	default:
		err = ErrUnexpectedResponse
	}

	return
}

// CreateUploadWithData creates an upload on the server and sends its data in the same HTTP request. Receives a stream
// and data to upload. Returns count of bytes uploaded and error (if any).
//
// Server must support "creation-with-upload" extension for this feature.
//
// This method may return ErrUnsupportedFeature if server doesn't support an extension. Also, it may return all errors
// the UploadStream methods may return.
func (c *Client) CreateUploadWithData(u *Upload, data []byte, remoteSize int64, partial bool, meta map[string]string) (uploadedBytes int64, response *http.Response, err error) {
	if err = c.ensureExtension("creation-with-upload"); err != nil {
		return
	}
	u2 := Upload{}
	s := NewUploadStream(c, &u2)
	s.ChunkSize = int64(len(data)) // Data must be uploaded in one request
	s.uploadMethod = http.MethodPost
	headers := map[string]string{"Upload-Length": strconv.Itoa(int(remoteSize)), "Upload-Offset": ""}
	if partial {
		headers["Upload-Concat"] = "partial"
	}
	if len(meta) > 0 {
		var m string
		if m, err = EncodeMetadata(meta); err != nil {
			return
		}
		headers["Upload-Metadata"] = m
	}
	u2.RemoteSize = remoteSize
	u2.Partial = partial
	u2.Metadata = meta

	rd := bytes.NewReader(data)
	s.setupDirtyBuffer()
	uploadedBytes, _, response, err = s.uploadChunkImpl(c.BaseURL.String(), rd, headers) // Upload in one request
	if err == nil {
		u2.Location = response.Header.Get("Location")
		u2.RemoteOffset = uploadedBytes
		*u = u2
	}

	return
}

// DeleteUpload deletes an upload. Receives `u` with upload to be deleted. Returns http response from server
// (with closed body) and error (if any).
//
// Server must support "termination" extension to be able to delete uploads.
//
// This method may return ErrUploadDoesNotExist error if such upload has not found on the server, ErrUnsupportedFeature if
// the server doesn't support "termination" extension. If unexpected response has received from the
// server, the method returns ErrUnexpectedResponse
func (c *Client) DeleteUpload(u Upload) (response *http.Response, err error) {
	if err = c.ensureExtension("termination"); err != nil {
		return
	}

	var req *http.Request
	var loc *url.URL
	if loc, err = url.Parse(u.Location); err != nil {
		return
	}
	ref := c.BaseURL.ResolveReference(loc).String()
	if req, err = c.GetRequest(http.MethodDelete, ref, nil, c, c.client); err != nil {
		return
	}
	if response, err = c.tusRequest(c.ctx, req); err != nil {
		return
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusNoContent:
	case http.StatusNotFound, http.StatusGone, http.StatusForbidden:
		err = ErrUploadDoesNotExist
	default:
		err = ErrUnexpectedResponse
	}

	return
}

// ConcatenateUploads makes a request to concatenate the partial uploads created before into one final upload. Fills
// `final` with upload that was created. Returns http response from server
// (with closed body) and error (if any).
//
// Server must support "concatenation" extension for this feature. Typically, partial uploads must be fully uploaded
// to the server, but if server supports "concatenation-unfinished" extension, it may accept unfinished uploads.
//
// This method may return ErrUnsupportedFeature if server doesn't support extension, or ErrUnexpectedResponse if
// unexpected response has been received from server.
func (c *Client) ConcatenateUploads(final *Upload, partials []Upload, meta map[string]string) (response *http.Response, err error) {
	if final == nil {
		panic("final is nil")
	}
	if len(partials) == 0 {
		panic("must be at least one partial upload to concatenate")
	}
	if err = c.ensureExtension("concatenation"); err != nil {
		return
	}

	var req *http.Request
	if req, err = c.GetRequest(http.MethodPost, c.BaseURL.String(), nil, c, c.client); err != nil {
		return
	}

	locations := make([]string, 0)
	for _, f := range partials {
		if !f.Partial {
			return nil, fmt.Errorf("upload %q is not partial", f.Location)
		}
		locations = append(locations, f.Location)
	}
	req.Header.Set("Upload-Concat", "final;"+strings.Join(locations, " "))

	if len(meta) > 0 {
		var m string
		if m, err = EncodeMetadata(meta); err != nil {
			return
		}
		req.Header.Set("Upload-Metadata", m)
	}

	if response, err = c.tusRequest(c.ctx, req); err != nil {
		return
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusCreated:
		u2 := Upload{}
		u2.Location = response.Header.Get("Location")
		u2.Metadata = meta
		*final = u2
	case http.StatusNotFound, http.StatusGone: // TODO: check on server
		err = fmt.Errorf("unable to concatenate uploads: %w", ErrUploadDoesNotExist)
	default:
		err = ErrUnexpectedResponse
	}
	return
}

// ConcatenateStreams makes a request to concatenate partial uploads from given streams into one final upload. Final
// Upload object will be filled with location of a created final upload. Returns http response from server
// (with closed body) and error (if any).
//
// Server must support "concatenation" extension for this feature. Streams with pointers that not point to an end of
// streams are treated as unfinished -- server must support "concatenation-unfinished" in this case.
//
// This method may return ErrUnsupportedFeature if server doesn't support extension, or ErrUnexpectedResponse if
// unexpected response has been received from server.
func (c *Client) ConcatenateStreams(final *Upload, streams []*UploadStream, meta map[string]string) (response *http.Response, err error) {
	if len(streams) == 0 {
		panic("must be at least one stream to concatenate")
	}

	uploads := make([]Upload, 0)
	for i, s := range streams {
		if s.Tell() < s.Len() {
			if err = c.ensureExtension("concatenation-unfinished"); err != nil {
				return nil, fmt.Errorf("stream #%d is not finished: %w", i, err)
			}
		}
		uploads = append(uploads, *s.Upload)
	}

	return c.ConcatenateUploads(final, uploads, meta)
}

// UpdateCapabilities gathers server capabilities and updates Capabilities client variable. Returns http response
// from server (with closed body) and error (if any).
func (c *Client) UpdateCapabilities() (response *http.Response, err error) {
	var req *http.Request
	if req, err = c.GetRequest(http.MethodOptions, c.BaseURL.String(), nil, c, c.client); err != nil {
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
			c.Capabilities.ChecksumAlgorithms = strings.Split(v, ",")
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
	if err == nil && response.StatusCode == http.StatusPreconditionFailed {
		versions := response.Header.Get("Tus-Version")
		err = fmt.Errorf("request protocol version %q, server supported versions: %q: %w", c.ProtocolVersion, versions, ErrProtocol)
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

// EncodeMetadata converts map of values to the Tus Upload-Metadata header format
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

// DecodeMetadata decodes metadata in Tus Upload-Metadata header format
func DecodeMetadata(raw string) (map[string]string, error) {
	res := make(map[string]string)
	for _, item := range strings.Split(raw, ",") {
		kv := strings.SplitN(item, " ", 2)
		if len(kv) <= 1 {
			return res, fmt.Errorf("metadata item %q has bad format", item)
		}
		val, err := base64.StdEncoding.DecodeString(kv[1])
		if err != nil {
			return res, err
		}
		res[kv[0]] = string(val)
	}

	return res, nil
}

func newRequest(method, url string, body io.Reader, tusClient *Client, _ *http.Client) (*http.Request, error) {
	return http.NewRequest(method, url, body)
}
