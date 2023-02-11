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

// Upload represents an upload on the server.
type Upload struct {
	// Location is the upload location. Can be either a path or URL
	Location string

	// RemoteSize is the remote upload size in bytes. Value SizeUnknown here means that the upload was created with
	// deferred size and must be determined before the first data transfer.
	RemoteSize int64

	// RemoteOffset reflects the offset of remote upload. This field is continuously updated by UploadStream
	// while transferring the data.
	RemoteOffset int64

	// Metadata is additional data assigned to the upload, when it was created on the server.
	Metadata map[string]string

	// UploadExpired represents the time when an upload expire on the server and won't be available since then. Nil
	// value means that upload will not be expired.
	UploadExpired *time.Time

	// Partial true value denotes that the upload is "partial" and meant to be concatenated into a "final" upload further.
	Partial bool
}
