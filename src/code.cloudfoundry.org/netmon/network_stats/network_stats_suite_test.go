package network_stats_test

import (
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
)

func TestNetworkStats(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "NetworkStats Suite")
}
