package checksum

import (
	"crypto"
	"hash"
	"hash/adler32"
	"hash/crc32"
	"hash/crc64"
	"hash/fnv"
	"strings"
	"unicode"
)

type Algorithm string

//revive:disable
const (
	MD4         Algorithm = "md4"
	MD5         Algorithm = "md5"
	SHA1        Algorithm = "sha1"
	SHA224      Algorithm = "sha224"
	SHA256      Algorithm = "sha256"
	SHA384      Algorithm = "sha384"
	SHA512      Algorithm = "sha512"
	SHA512_224  Algorithm = "sha512224"
	SHA512_256  Algorithm = "sha512256"
	SHA3_224    Algorithm = "sha3224"
	SHA3_256    Algorithm = "sha3256"
	SHA3_384    Algorithm = "sha3384"
	SHA3_512    Algorithm = "sha3512"
	RIPEMD160   Algorithm = "ripemd160"
	BLAKE2S_256 Algorithm = "blake2s256"
	BLAKE2B_256 Algorithm = "blake2b256"
	BLAKE2B_384 Algorithm = "blake2b384"
	BLAKE2B_512 Algorithm = "blake2b512"
	ADLER32     Algorithm = "adler32"
	CRC32       Algorithm = "crc32"
	CRC64       Algorithm = "crc64"
	FNV         Algorithm = "fnv"
	FNV1        Algorithm = "fnv1"
	FNV1A       Algorithm = "fnv1a"
)

//revive:enable

var Algorithms = map[Algorithm]func() hash.Hash{
	MD4:         crypto.MD4.New,
	MD5:         crypto.MD5.New,
	SHA1:        crypto.SHA1.New,
	SHA224:      crypto.SHA224.New,
	SHA256:      crypto.SHA256.New,
	SHA384:      crypto.SHA384.New,
	SHA512:      crypto.SHA512.New,
	SHA512_224:  crypto.SHA512_224.New,
	SHA512_256:  crypto.SHA512_256.New,
	SHA3_224:    crypto.SHA3_224.New,
	SHA3_256:    crypto.SHA3_256.New,
	SHA3_384:    crypto.SHA3_384.New,
	SHA3_512:    crypto.SHA3_512.New,
	RIPEMD160:   crypto.RIPEMD160.New,
	BLAKE2S_256: crypto.BLAKE2s_256.New,
	BLAKE2B_256: crypto.BLAKE2b_256.New,
	BLAKE2B_384: crypto.BLAKE2b_384.New,
	BLAKE2B_512: crypto.BLAKE2b_512.New,
	ADLER32:     func() hash.Hash { return adler32.New() },
	CRC32:       func() hash.Hash { return crc32.New(crc32.IEEETable) },
	CRC64:       func() hash.Hash { return crc64.New(crc64.MakeTable(crc64.ISO)) },
	FNV:         func() hash.Hash { return fnv.New32() },
	FNV1:        func() hash.Hash { return fnv.New32() },
	FNV1A:       func() hash.Hash { return fnv.New32a() },
}

func GetAlgorithm(name string) (algo Algorithm, ok bool) {
	res := strings.Builder{}
	for _, r := range name {
		// Keep only letters and digits in the result, uppercase converting to lowercase
		if unicode.IsUpper(r) {
			res.WriteRune(unicode.ToLower(r))
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) {
			res.WriteRune(r)
		}
	}
	algo = Algorithm(res.String())
	_, ok = Algorithms[algo]
	return
}
