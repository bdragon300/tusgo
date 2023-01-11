package tusgo

const FileSizeUnknown = -1

type File struct {
	Metadata   map[string]string
	RemoteSize int64
	Location   string
}
