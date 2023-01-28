package tusgo

import "time"

const (
	// SizeUnknown value passed to `remoteSize` parameter in Client.CreateUpload means, that an upload size will be
	// set later during data uploading. UploadStream.SetUploadSize must be set to true before starting data uploading.
	// Server must support "creation-defer-length" extension for this feature.
	SizeUnknown = -1

	// OffsetUnknown is a special value for Upload.RemoteOffset and means that concatenation is still in progress
	// on the server. It sets by Client.GetUpload method when we get an upload created by Client.Concatenate* methods
	// before. After server will finish concatenation, the Client.GetUpload will set the offset to a particular value.
	OffsetUnknown = -1
)

type Upload struct {
	Metadata      map[string]string
	RemoteSize    int64
	Location      string
	UploadExpired *time.Time
	RemoteOffset  int64
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