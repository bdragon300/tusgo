package tusgo

import "errors"

var (
	ErrProtocol             = errors.New("tus protocol error")
	ErrUnsupportedOperation = errors.New("unsupported operation")
	ErrFileDoesNotExist     = errors.New("file does not exist")
	ErrFileTooLarge         = errors.New("file is too large")
	ErrOffsetsNotSynced     = errors.New("client and server offsets are not synced")
	ErrChecksumMismatch     = errors.New("checksum mismatch")
	ErrUnknown              = errors.New("unknown error")
)
