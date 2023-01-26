package tusgo_test

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestTusgo(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Tusgo Suite")
}
