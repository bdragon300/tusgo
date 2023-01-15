package tusgo

import "time"

const FileSizeUnknown = -1

type File struct {
	Metadata      map[string]string
	RemoteSize    int64
	Location      string
	UploadExpired *time.Time
	RemoteOffset  int64
	Partial       bool
}
