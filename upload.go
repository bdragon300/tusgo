package tusgo

import "time"

const SizeUnknown = -1

type Upload struct {
	Metadata      map[string]string
	RemoteSize    int64
	Location      string
	UploadExpired *time.Time
	RemoteOffset  int64 // TODO: -1 means no offset for final upload
	Partial       bool
}

func (f *Upload) Reset() {
	f.Metadata = nil
	f.RemoteSize = 0
	f.Location = ""
	f.UploadExpired = nil
	f.RemoteOffset = 0
	f.Partial = false
}
