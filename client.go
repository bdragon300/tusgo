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
)

func NewClient(client *http.Client, baseURL *url.URL) *Client {
	c := &Client{}
	if client == nil {
		c.client = http.DefaultClient
	}
	if baseURL == nil {
		c.BaseURL, _ = url.Parse("http://example.com/files")
	}
	c.ProtocolVersion = "1.0.0"
	c.GetRequest = newRequest
	return c
}

type GetRequestFunc func(method, url string, body io.Reader, tusClient *Client, httpClient *http.Client, capabilities *ServerCapabilities) (*http.Request, error)

type Client struct {
	BaseURL         *url.URL
	ProtocolVersion string

	GetRequest GetRequestFunc

	client       *http.Client
	capabilities *ServerCapabilities
	ctx          context.Context
}

func (c *Client) WithContext(ctx context.Context) *Client {
	c.ctx = ctx
	return c
}

func (c *Client) CreateFile(f *File) (response *http.Response, err error) {
	if f == nil {
		panic("f is nil")
	}
	if err = c.ensureExtension("creation"); err != nil {
		return
	}

	var req *http.Request
	var loc *url.URL
	if loc, err = url.Parse(f.Location); err != nil {
		return
	}
	u := c.BaseURL.ResolveReference(loc).String()
	if req, err = c.GetRequest(http.MethodPost, u, nil, c, c.client, c.capabilities); err != nil {
		return
	}

	req.Header.Set("Content-Length", strconv.FormatInt(0, 10))
	switch {
	case f.RemoteSize == FileSizeUnknown:
		req.Header.Set("Upload-Defer-Length", "1")
	case f.RemoteSize > 0:
		req.Header.Set("Upload-Length", strconv.FormatInt(f.RemoteSize, 10))
	default:
		panic(fmt.Sprintf("file size is negative: %d", f.RemoteSize))
	}

	var meta string
	if meta, err = EncodeMetadata(f.Metadata); err != nil {
		return
	}
	req.Header.Set("Upload-Metadata", meta)

	if response, err = c.tusRequest(req); err != nil {
		return
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusCreated:
		f.Location = response.Header.Get("Location")
	case http.StatusRequestEntityTooLarge:
		err = ErrFileTooLarge
	default:
		err = ErrUnknown
		if response.StatusCode < 300 {
			err = fmt.Errorf("server returned unexpected %d HTTP code: %w", response.StatusCode, ErrProtocol)
		}
	}

	return
}

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
	if req, err = c.GetRequest(http.MethodDelete, u, nil, c, c.client, c.capabilities); err != nil {
		return
	}
	if response, err = c.tusRequest(req); err != nil {
		return
	}
	defer response.Body.Close()
	return
}

func (c *Client) UpdateCapabilities() (response *http.Response, err error) {
	var req *http.Request
	if req, err = c.GetRequest(http.MethodOptions, c.BaseURL.String(), nil, c, c.client, c.capabilities); err != nil {
		return
	}
	if response, err = c.tusRequest(req); err != nil {
		return
	}
	defer response.Body.Close()

	switch response.StatusCode {
	case http.StatusNoContent, http.StatusOK:
		c.capabilities = &ServerCapabilities{}
		if v := response.Header.Get("Tus-Max-Size"); v != "" {
			if c.capabilities.MaxSize, err = strconv.ParseInt(v, 10, 64); err != nil {
				err = fmt.Errorf("cannot parse Tus-Max-Size integer value %q: %w", v, ErrProtocol)
				return
			}
		}
		if v := response.Header.Get("Tus-Extension"); v != "" {
			c.capabilities.Extensions = strings.Split(v, ",")
		}
		if v := response.Header.Get("Tus-Version"); v != "" {
			c.capabilities.ProtocolVersions = strings.Split(v, ",")
		}
	case http.StatusNotFound, http.StatusGone, http.StatusForbidden:
		err = ErrFileDoesNotExist
	default:
		err = ErrUnknown
		if response.StatusCode < 300 {
			err = fmt.Errorf("server returned unexpected %d HTTP code: %w", response.StatusCode, ErrProtocol)
		}
	}
	return
}

func (c *Client) Capabilities() *ServerCapabilities {
	return c.capabilities
}

func (c *Client) tusRequest(req *http.Request) (response *http.Response, err error) {
	if req.Method != http.MethodOptions && req.Header.Get("Tus-Resumable") == "" {
		req.Header.Set("Tus-Resumable", c.ProtocolVersion)
	}
	if c.ctx != nil {
		req = req.WithContext(c.ctx)
	}
	response, err = c.client.Do(req)
	if response.StatusCode == http.StatusPreconditionFailed {
		versions := response.Header.Get("Tus-Version")
		err = fmt.Errorf("request protocol version %s, server supported versions: %s: %w", c.ProtocolVersion, versions, ErrProtocol)
	} else if v := response.Header.Get("Tus-Resumable"); v != c.ProtocolVersion {
		err = fmt.Errorf(
			"server response protocol version %s, requested version %s: %w",
			v, c.ProtocolVersion, ErrProtocol,
		)
	}
	return
}

func (c *Client) ensureExtension(extension string) error {
	if err := c.maybeUpdateCapabilities(); err != nil {
		return fmt.Errorf("cannot obtain server capabilities: %w", err)
	}
	for _, e := range c.capabilities.Extensions {
		if extension == e {
			return nil
		}
	}
	return fmt.Errorf("server extension %q is required: %w", extension, ErrUnsupportedOperation)
}

func (c *Client) maybeUpdateCapabilities() (err error) {
	var response *http.Response
	if c.capabilities == nil {
		if response, err = c.UpdateCapabilities(); err != nil {
			return
		}
		if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusOK {
			return fmt.Errorf("server returned HTTP code %d", response.StatusCode)
		}
	}
	return
}

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
