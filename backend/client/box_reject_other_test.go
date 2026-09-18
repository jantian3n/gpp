//go:build !windows

package client

import "testing"

func TestRejectUDP443(t *testing.T) {
	t.Skip("该回归测试仅针对 Windows TUN 环境")
}
