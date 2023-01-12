package tusgo

import "github.com/bdragon300/tusgo/checksum"

type ServerCapabilities struct {
	Extensions         []string
	MaxSize            int64
	ProtocolVersions   []string
	ChecksumAlgorithms []checksum.Algorithm
}
