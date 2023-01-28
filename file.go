package tusgo

import "time"

const FileSizeUnknown = -1

type File struct {
	Metadata      map[string]string
	RemoteSize    int64
	Location      string
	UploadExpired *time.Time
	RemoteOffset  int64 // TODO: -1 means no offset for final upload
	Partial       bool
}

func (f *File) Reset() {
	f.Metadata = nil
	f.RemoteSize = 0
	f.Location = ""
	f.UploadExpired = nil
	f.RemoteOffset = 0
	f.Partial = false
}
