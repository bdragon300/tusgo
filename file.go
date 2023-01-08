package tusgo

type File struct {
	Metadata   map[string]string
	RemoteSize int64 // TODO: may be deferred
	Location   string
}
