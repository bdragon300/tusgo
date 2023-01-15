package tusgo

import "errors"

// TODO: mark some errors below as temporary, some as persistent
var (
	ErrProtocol             = errors.New("tus protocol error")
	ErrUnsupportedOperation = errors.New("unsupported operation")
	ErrFileDoesNotExist     = errors.New("file does not exist")
	ErrFileTooLarge         = errors.New("file is too large")
	ErrOffsetsNotSynced     = errors.New("client and server offsets are not synced")
	ErrChecksumMismatch     = errors.New("checksum mismatch")
	ErrCannotUpload         = errors.New("can not upload")
	ErrUnknown              = errors.New("unknown error")
)
