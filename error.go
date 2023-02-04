package tusgo

import (
	"errors"
	"fmt"
)

var (
	ErrUnsupportedFeature = errors.New("unsupported feature")
	ErrUploadTooLarge     = errors.New("upload is too large")
	ErrUploadDoesNotExist = errors.New("upload does not exist")
	ErrOffsetsNotSynced   = errors.New("client stream and server offsets are not synced")
	ErrChecksumMismatch   = errors.New("checksum mismatch")
	ErrProtocol           = errors.New("tus protocol error")
	ErrCannotUpload       = errors.New("can not upload")
	ErrUnexpectedResponse = fmt.Errorf("unexpected HTTP response code: %w", ErrProtocol)
)
