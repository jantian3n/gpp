package core

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/danbai225/gpp/backend/config"
	"github.com/sagernet/sing-box/option"
)

// fakeTunnel 记录启动/关闭，避免测试真的创建 TUN（那需要管理员权限）。
type fakeTunnel struct {
	mu      sync.Mutex
	started int
	closed  int
	failErr error
}

func newFakeTunnel() *fakeTunnel { return &fakeTunnel{} }

func (f *fakeTunnel) Start() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failErr != nil {
		return f.failErr
	}
	f.started++
	return nil
}

func (f *fakeTunnel) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
	return nil
}

func (f *fakeTunnel) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.started, f.closed
}

// newTestEngine 用临时配置文件与假隧道构造核心。
func newTestEngine(t *testing.T, tunnel *fakeTunnel) *Engine {
	t.Helper()
	config.SetPath(filepath.Join(t.TempDir(), "config.json"))
	t.Cleanup(func() { config.SetPath("") })
	if err := config.InitConfig(); err != nil {
		t.Fatal(err)
	}
	engine := New()
	engine.SetKind("test")
	if tunnel != nil {
		engine.SetNewTunnel(func(_, _ *config.Peer, _, _ string, _ []option.Rule) (Tunnel, error) {
			return tunnel, nil
		})
	}
	if err := engine.Load(); err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	return engine
}

func addTestPeer(t *testing.T, engine *Engine, host string) {
	t.Helper()
	token := base64.StdEncoding.EncodeToString([]byte("gpp://vless@" + host + ":443/uuid"))
	if err := engine.Import(token); err != nil {
		t.Fatalf("导入节点失败: %v", err)
	}
}

func TestEngineStartStop(t *testing.T) {
	tunnel := newFakeTunnel()
	engine := newTestEngine(t, tunnel)
	addTestPeer(t, engine, "1.2.3.4")

	if engine.Running() {
		t.Fatal("初始状态不应在加速")
	}
	if err := engine.Start(); err != nil {
		t.Fatal(err)
	}
	if !engine.Running() {
		t.Fatal("启动后应处于加速中")
	}
	// 幂等：重复启动不应重复建立隧道
	if err := engine.Start(); err != nil {
		t.Fatal(err)
	}
	if started, _ := tunnel.counts(); started != 1 {
		t.Fatalf("Start 应当幂等，实际启动 %d 次", started)
	}
	if err := engine.Stop(); err != nil {
		t.Fatal(err)
	}
	if engine.Running() {
		t.Fatal("停止后不应在加速")
	}
	if _, closed := tunnel.counts(); closed != 1 {
		t.Fatalf("停止应当关闭隧道，实际关闭 %d 次", closed)
	}
	if err := engine.Stop(); err != nil {
		t.Fatalf("未加速时停止不应报错: %v", err)
	}
}

func TestEngineRestartUsesCurrentPeer(t *testing.T) {
	tunnel := newFakeTunnel()
	engine := newTestEngine(t, tunnel)
	addTestPeer(t, engine, "1.1.1.1")
	addTestPeer(t, engine, "2.2.2.2")

	if err := engine.Start(); err != nil {
		t.Fatal(err)
	}
	if err := engine.Select("2.2.2.2:443", "2.2.2.2:443"); err != nil {
		t.Fatal(err)
	}
	if engine.Status().Warning == "" {
		t.Fatal("换节点后应当提示「需要重启才生效」")
	}
	if err := engine.Restart(); err != nil {
		t.Fatal(err)
	}
	if !engine.Running() {
		t.Fatal("重启后应处于加速中")
	}
	if started, closed := tunnel.counts(); started != 2 || closed != 1 {
		t.Fatalf("重启应当先关后开，实际 start=%d close=%d", started, closed)
	}
	if peer := engine.Status().GamePeer; peer == nil || peer.Name != "2.2.2.2:443" {
		t.Fatalf("重启后应使用新节点，实际 %+v", peer)
	}
}

func TestEngineStartFailureKeepsState(t *testing.T) {
	tunnel := newFakeTunnel()
	tunnel.failErr = errors.New("模拟启动失败")
	engine := newTestEngine(t, tunnel)
	addTestPeer(t, engine, "1.2.3.4")

	if err := engine.Start(); err == nil {
		t.Fatal("启动失败应当返回错误")
	}
	if engine.Running() {
		t.Fatal("启动失败后不应标记为加速中")
	}
	if _, closed := tunnel.counts(); closed != 1 {
		t.Fatal("启动失败应当关闭半成品隧道，避免残留")
	}
	// 失败后仍可重试
	tunnel.mu.Lock()
	tunnel.failErr = nil
	tunnel.mu.Unlock()
	if err := engine.Start(); err != nil {
		t.Fatal(err)
	}
	if !engine.Running() {
		t.Fatal("重试成功后应处于加速中")
	}
}

func TestEngineSelectDeleteAndNormalize(t *testing.T) {
	engine := newTestEngine(t, newFakeTunnel())
	addTestPeer(t, engine, "1.2.3.4")

	if err := engine.Select("不存在的节点", "也不存在"); err == nil {
		t.Fatal("选择不存在的节点应当报错")
	}
	if err := engine.Delete("1.2.3.4:443"); err != nil {
		t.Fatal(err)
	}
	status := engine.Status()
	if status.GamePeer == nil {
		t.Fatal("删除当前节点后应自动回退到可用节点，而不是留下 nil")
	}
	if status.PeerCount != 1 { // 只剩「直连」
		t.Fatalf("删除后节点数不对: %d", status.PeerCount)
	}
}

func TestEngineDeleteActivePeerWhileRunningWarnsRestart(t *testing.T) {
	engine := newTestEngine(t, newFakeTunnel())
	addTestPeer(t, engine, "1.2.3.4")
	if err := engine.Start(); err != nil {
		t.Fatal(err)
	}
	if err := engine.Delete("1.2.3.4:443"); err != nil {
		t.Fatal(err)
	}
	if warning := engine.Status().Warning; !strings.Contains(warning, "重启") {
		t.Fatalf("加速中删除当前节点必须提示重启，实际 %q", warning)
	}
}

func TestEngineEventsAndWarnings(t *testing.T) {
	engine := newTestEngine(t, newFakeTunnel())
	events, unsubscribe := engine.Subscribe()
	defer unsubscribe()

	engine.AddWarning("测试告警")
	select {
	case event := <-events:
		if event.Type != "warning" || event.Message != "测试告警" {
			t.Fatalf("事件内容不对: %+v", event)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("未收到告警事件")
	}

	// 告警只应被某个前端取走一次，避免两端反复弹窗
	if first := engine.Status().Warning; first != "测试告警" {
		t.Fatalf("第一个前端应当拿到告警，实际 %q", first)
	}
	if second := engine.Status().Warning; second != "" {
		t.Fatalf("告警取走后应当清空，实际 %q", second)
	}
}

func TestEngineStatusFields(t *testing.T) {
	engine := newTestEngine(t, newFakeTunnel())
	engine.SetLogOutput(filepath.Join(t.TempDir(), "gpp.log"))
	status := engine.Status()
	if status.CoreKind != "test" || status.CorePID != os.Getpid() {
		t.Fatalf("核心信息不对: kind=%s pid=%d", status.CoreKind, status.CorePID)
	}
	if status.ConfigPath != config.Path() {
		t.Fatalf("配置路径不对: %s", status.ConfigPath)
	}
	if status.LogPath == "" {
		t.Fatal("日志路径应当透出给界面")
	}
	if status.Running {
		t.Fatal("未启动时不应标记为加速中")
	}
}

func TestEnginePeersOrdering(t *testing.T) {
	engine := newTestEngine(t, newFakeTunnel())
	addTestPeer(t, engine, "1.1.1.1")
	addTestPeer(t, engine, "2.2.2.2")

	engine.mu.Lock()
	for _, peer := range engine.conf.PeerList {
		switch peer.Name {
		case "2.2.2.2:443":
			peer.Ping = 20
		case "1.1.1.1:443":
			peer.Ping = 0 // 未测速
		}
	}
	engine.mu.Unlock()

	// Peers 保持配置顺序：界面上的序号必须稳定，不能因为测速结果而跳动
	stable := engine.Peers()
	if got := strings.Join(names(stable), ","); got != "直连,1.1.1.1:443,2.2.2.2:443" {
		t.Fatalf("Peers 应保持配置顺序，实际 %s", got)
	}
	// PeersByPing 把测速成功的排前面，未测速（含直连）的排最后
	byPing := engine.PeersByPing()
	if len(byPing) < 2 || byPing[0].Name != "2.2.2.2:443" {
		t.Fatalf("PeersByPing 应把测速成功的排前面: %v", names(byPing))
	}
	for _, peer := range byPing[1:] {
		if peer.Ping != 0 {
			t.Fatalf("未测速节点不应排在测速成功的节点之前: %v", names(byPing))
		}
	}
}

func names(peers []*config.Peer) []string {
	list := make([]string, 0, len(peers))
	for _, peer := range peers {
		if peer != nil {
			list = append(list, peer.Name)
		}
	}
	return list
}
