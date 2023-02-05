package tusgo

// ServerCapabilities contains features and limits of a Tus server. These features are exposed by a server itself
// in OPTIONS endpoint and may be fetched by Client.UpdateCapabilities method.
type ServerCapabilities struct {
	// Tus protocol extensions the server supports. For full extensions list see Tus protocol description.
	// Some of them are: creation, creation-defer-length, creation-with-upload, termination, concatenation,
	// concatenation-unfinished, checksum, checksum-trailer, creation-defer-length, expiration
	Extensions []string

	// Size of upload the server is capable to accept. 0 means that server does not set such limit.
	MaxSize int64

	// Tus protocol version a server supports. A client must select one of these versions by setting
	// Client.ProtocolVersion
	ProtocolVersions []string

	// Algorithms which server supports. For this feature a server must expose at least the "checksum" extension.
	// See also checksum.Algorithms for list of hashes the tusgo can use.
	ChecksumAlgorithms []string
}
