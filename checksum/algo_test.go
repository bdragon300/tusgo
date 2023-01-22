package checksum_test

import (
	"github.com/bdragon300/tusgo/checksum"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("GetAlgorithm", func() {
	When("pass a correct name", func() {
		DescribeTable("should return correct algorithm",
			func(name string, expect checksum.Algorithm) {
				alg, ok := checksum.GetAlgorithm(name)
				Ω(ok).Should(BeTrue())
				Ω(alg).Should(Equal(expect))
				Ω(alg).Should(BeKeyOf(checksum.Algorithms))
			},
			Entry("sha1", "sha1", checksum.SHA1),
			Entry("SHA-1", "SHA-1", checksum.SHA1),
			Entry("md_5", "md_5", checksum.MD5),
			Entry("Blake-2B-256", "Blake-2B-256", checksum.BLAKE2B_256),
		)
	})
	When("pass an unknown name", func() {
		DescribeTable("should return not ok",
			func(name string) {
				_, ok := checksum.GetAlgorithm(name)
				Ω(ok).Should(BeFalse())
			},
			Entry("unknown", "unknown"),
			Entry("sha11", "sha11"),
		)
	})
})
