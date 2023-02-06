package tusgo

import (
	"errors"
	"fmt"
	"io"
	"net/http"
)

type TusError struct {
	inner error

	msg string
}

func (te TusError) Error() string {
	return fmt.Sprintf("%s: %s", te.msg, te.inner)
}

func (te TusError) Unwrap() error {
	return te.inner
}

func (te TusError) Is(e error) bool {
	v, ok := e.(TusError)
	return ok && v.msg == te.msg || errors.Is(te.inner, e)
}

func (te TusError) WithErr(err error) TusError {
	te.inner = err
	return te
}

func (te TusError) WithText(s string) TusError {
	te.inner = errors.New(s)
	return te
}

func (te TusError) WithResponse(r *http.Response) TusError {
	if r == nil {
		te.inner = fmt.Errorf("response is nil")
		return te
	}

	b := make([]byte, 256)
	if l, err := io.ReadFull(r.Body, b); err == nil || err == io.EOF {
		if l > 0 {
			te.inner = fmt.Errorf("HTTP %d: <no body>", r.StatusCode)
		} else {
			te.inner = fmt.Errorf("HTTP %d: %s", r.StatusCode, b[:l])
		}
	} else {
		panic(err)
	}
	return te
}

var (
	ErrUnsupportedFeature = TusError{msg: "unsupported feature"}
	ErrUploadTooLarge     = TusError{msg: "upload is too large"}
	ErrUploadDoesNotExist = TusError{msg: "upload does not exist"}
	ErrOffsetsNotSynced   = TusError{msg: "client stream and server offsets are not synced"}
	ErrChecksumMismatch   = TusError{msg: "checksum mismatch"}
	ErrProtocol           = TusError{msg: "protocol error"}
	ErrCannotUpload       = TusError{msg: "can not upload"}
	ErrUnexpectedResponse = TusError{msg: "unexpected HTTP response code"}
)
