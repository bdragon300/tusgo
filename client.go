package tusgo

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

func NewClient(client *http.Client, baseURL *url.URL) *Client {
	c := &Client{BaseURL: baseURL}
	if client == nil {
		c.client = http.DefaultClient
	}
	return c
}

type Client struct {
	BaseURL         *url.URL
	ProtocolVersion string
	client          *http.Client
	capabilities    *ServerCapabilities
	ctx             context.Context
}

func (c *Client) WithContext(ctx context.Context) *Client {
	c.ctx = ctx
	return c
}

func (c *Client) CreateFile(f *File) (response *http.Response, err error) {
	if err = c.ensureExtension("creation"); err != nil {
		return
	}

	var req *http.Request
	var loc *url.URL
	if loc, err = url.Parse(f.Location); err != nil {
		return
	}
	u := c.BaseURL.ResolveReference(loc).String()
	if req, err = http.NewRequest(http.MethodPost, u, nil); err != nil {
		return
	}

	req.Header.Set("Content-Length", strconv.FormatInt(0, 10))
	req.Header.Set("Upload-Length", strconv.FormatInt(f.RemoteSize, 10))
	var meta string
	if meta, err = EncodeMetadata(f.Metadata); err != nil {
		return
	}
	req.Header.Set("Upload-Metadata", meta)

	if response, err = c.tusRequest(req); err != nil {
		return
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusCreated {
		f.Location = response.Header.Get("Location")
	}

	return
}

func (c *Client) DeleteFile(f *File) (response *http.Response, err error) {
	if err = c.ensureExtension("termination"); err != nil {
		return
	}

	var req *http.Request
	var loc *url.URL
	if loc, err = url.Parse(f.Location); err != nil {
		return
	}
	u := c.BaseURL.ResolveReference(loc).String()
	if req, err = http.NewRequest(http.MethodDelete, u, nil); err != nil {
		return
	}
	if response, err = c.tusRequest(req); err != nil {
		return
	}
	defer response.Body.Close()
	return
}

func (c *Client) GetCapabilities() (caps *ServerCapabilities, response *http.Response, err error) {
	var req *http.Request
	if req, err = http.NewRequest(http.MethodOptions, c.BaseURL.String(), nil); err != nil {
		return
	}
	if response, err = c.tusRequest(req); err != nil {
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent && response.StatusCode != http.StatusOK {
		return
	}

	caps = &ServerCapabilities{}
	if v := response.Header.Get("Tus-Max-Size"); v != "" {
		if caps.MaxSize, err = strconv.ParseInt(v, 10, 64); err != nil {
			err = fmt.Errorf("cannot parse Tus-Max-Size integer value: %w", err)
			return
		}
	}
	if v := response.Header.Get("Tus-Extension"); v != "" {
		caps.Extensions = strings.Split(v, ",")
	}
	if v := response.Header.Get("Tus-Version"); v != "" {
		caps.ProtocolVersions = strings.Split(v, ",")
	}

	return
}

func (c *Client) tusRequest(req *http.Request) (response *http.Response, err error) {
	if req.Method != http.MethodOptions {
		req.Header.Set("Tus-Resumable", c.ProtocolVersion)
	}
	if c.ctx != nil {
		req = req.WithContext(c.ctx)
	}
	response, err = c.client.Do(req)
	if response.StatusCode == http.StatusPreconditionFailed {
		versions := response.Header.Get("Tus-Version")
		err = fmt.Errorf("server does not support version %s, supported versions: %s", c.ProtocolVersion, versions)
	} else if v := response.Header.Get("Tus-Resumable"); v != c.ProtocolVersion {
		err = fmt.Errorf("server unexpectedly responded Tus protocol version %s, but we requested version %s", v, c.ProtocolVersion)
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
	return fmt.Errorf("server does not support %q extension", extension)
}

func (c *Client) maybeUpdateCapabilities() (err error) {
	var response *http.Response
	if c.capabilities == nil {
		if c.capabilities, response, err = c.GetCapabilities(); err != nil {
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
