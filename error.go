package tusgo

import (
	"errors"
	"fmt"
)

var (
	ErrProtocol           = errors.New("tus protocol error")
	ErrUnsupportedFeature = errors.New("unsupported feature")
	ErrUploadTooLarge     = errors.New("upload is too large")
	ErrCannotUpload       = errors.New("can not upload")
	ErrUploadDoesNotExist = errors.New("upload does not exist")
	ErrOffsetsNotSynced   = errors.New("client stream and server offsets are not synced")
	ErrChecksumMismatch   = errors.New("checksum mismatch")
	ErrUnexpectedResponse = fmt.Errorf("unexpected HTTP response code: %w", ErrProtocol)
)
