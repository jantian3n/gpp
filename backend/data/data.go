package data

import (
	"github.com/danbai225/gpp/backend/config"
	netutils "github.com/shirou/gopsutil/v3/net"
)

type Status struct {
	Running  bool         `json:"running"`
	GamePeer *config.Peer `json:"game_peer"`
	HttpPeer *config.Peer `json:"http_peer"`
	Up       uint64       `json:"up"`
	Down     uint64       `json:"down"`
	// UpRate/DownRate 是估算的实时速率（字节/秒），由核心采样计算，两端界面共享。
	UpRate   uint64 `json:"up_rate"`
	DownRate uint64 `json:"down_rate"`
	// Warning 是一次性提示（订阅失败、选中节点被自动切换等），提示一次后即清空。
	Warning string `json:"warning"`
	// CoreKind/CorePID 说明"隧道由谁持有"，便于用户理解 GUI 与 TUI 的关系。
	CoreKind   string `json:"core_kind"`
	CorePID    int    `json:"core_pid"`
	LogPath    string `json:"log_path"`
	ConfigPath string `json:"config_path"`
	PeerCount  int    `json:"peer_count"`
}

// Traffic 返回 gpp TUN 网卡的累计上行/下行字节数。
// 多个同名网卡时累加，避免像以前那样"后者覆盖前者"导致统计不准。
func Traffic() (up, down uint64) {
	counters, err := netutils.IOCounters(true)
	if err != nil {
		return 0, 0
	}
	for _, counter := range counters {
		if counter.Name == config.TunInterfaceName || counter.Name == "gpp" {
			up += counter.BytesSent
			down += counter.BytesRecv
		}
	}
	return up, down
}
