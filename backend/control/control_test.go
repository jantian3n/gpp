package control

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/danbai225/gpp/backend/config"
	"github.com/danbai225/gpp/backend/core"
	"github.com/sagernet/sing-box/option"
)

type fakeTunnel struct{ started, closed int }

func (f *fakeTunnel) Start() error { f.started++; return nil }
func (f *fakeTunnel) Close() error { f.closed++; return nil }

// useTempPaths 把配置/发现文件/锁文件都放到临时目录，保证测试互不干扰。
func useTempPaths(t *testing.T) {
	t.Helper()
	config.SetPath(filepath.Join(t.TempDir(), "config.json"))
	t.Cleanup(func() { config.SetPath("") })
	if err := config.InitConfig(); err != nil {
		t.Fatal(err)
	}
}

func newTestEngine(t *testing.T) *core.Engine {
	t.Helper()
	engine := core.New()
	engine.SetKind("test")
	engine.SetNewTunnel(func(_, _ *config.Peer, _, _ string, _ []option.Rule) (core.Tunnel, error) {
		return &fakeTunnel{}, nil
	})
	if err := engine.Load(); err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	token := base64.StdEncoding.EncodeToString([]byte("gpp://vless@1.2.3.4:443/uuid"))
	if err := engine.Import(token); err != nil {
		t.Fatalf("导入节点失败: %v", err)
	}
	return engine
}

// TestProcessAliveAfterExit 回归测试：进程被终止后必须判定为"已退出"。
//
// 之前只看 OpenProcess 是否成功：进程刚被杀掉时它依然会成功（内核对象还在），
// 于是一个崩溃遗留的锁会被永久判定为"核心在运行"，新实例再也接管不了。
func TestProcessAliveAfterExit(t *testing.T) {
	if !processAlive(os.Getpid()) {
		t.Fatal("当前进程应当判定为存活")
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("cmd", "/c", "ping -n 20 127.0.0.1")
	} else {
		cmd = exec.Command("sh", "-c", "sleep 20")
	}
	if err := cmd.Start(); err != nil {
		t.Skipf("无法启动子进程: %v", err)
	}
	pid := cmd.Process.Pid
	if !processAlive(pid) {
		t.Fatalf("刚启动的子进程 %d 应当判定为存活", pid)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatalf("杀掉子进程失败: %v", err)
	}
	_ = cmd.Wait()
	if processAliveWithRetry(pid) {
		t.Fatalf("已退出的子进程 %d 仍被判定为存活，崩溃遗留的锁将无法接管", pid)
	}
}

func TestLeaseExclusive(t *testing.T) {
	useTempPaths(t)
	path := LockPath()

	first, err := AcquireLease(path)
	if err != nil {
		t.Fatalf("第一次应当抢到所有权: %v", err)
	}
	if _, err = AcquireLease(path); err == nil {
		t.Fatal("第二次抢所有权应当失败（同一进程也不允许两个核心）")
	}
	first.Release()
	second, err := AcquireLease(path)
	if err != nil {
		t.Fatalf("释放后应当可以重新抢到: %v", err)
	}
	second.Release()
}

func TestLeaseTakesOverStaleLock(t *testing.T) {
	useTempPaths(t)
	// 写入一个绝对不存在的 PID，模拟上次崩溃遗留的陈旧锁
	if err := os.WriteFile(LockPath(), []byte("2147483646"), 0o600); err != nil {
		t.Fatal(err)
	}
	lease, err := AcquireLease(LockPath())
	if err != nil {
		t.Fatalf("陈旧锁应当可以被接管: %v", err)
	}
	lease.Release()
	if _, err = os.Stat(LockPath()); !os.IsNotExist(err) {
		t.Fatal("释放后锁文件应当被清理")
	}
}

func TestServerClientRoundTrip(t *testing.T) {
	useTempPaths(t)
	engine := newTestEngine(t)
	server, err := StartServer(engine, "test", "v-test")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()

	info, err := LoadInfo()
	if err != nil {
		t.Fatalf("发现文件应当写入: %v", err)
	}
	if info.PID != os.Getpid() || info.Kind != "test" {
		t.Fatalf("发现文件内容不对: %+v", info)
	}

	client := NewClient(info)
	if err = client.Ping(); err != nil {
		t.Fatalf("健康检查失败: %v", err)
	}

	// 鉴权：错误 token 必须被拒绝
	bad := NewClient(&Info{Port: info.Port, Token: "wrong"})
	if err = bad.Ping(); err == nil {
		t.Fatal("错误 token 应当被拒绝")
	}

	peers, err := client.Peers()
	if err != nil || len(peers) == 0 {
		t.Fatalf("读取节点失败: %v (%d 个)", err, len(peers))
	}

	if err = client.Select("1.2.3.4:443", "1.2.3.4:443"); err != nil {
		t.Fatalf("选择节点失败: %v", err)
	}
	if err = client.Start(); err != nil {
		t.Fatalf("启动失败: %v", err)
	}
	status, err := client.Status()
	if err != nil {
		t.Fatal(err)
	}
	if !status.Running {
		t.Fatal("启动后状态应为加速中")
	}
	if status.CorePID != os.Getpid() {
		t.Fatalf("状态里应当带核心 PID，实际 %d", status.CorePID)
	}
	if err = client.Stop(); err != nil {
		t.Fatalf("停止失败: %v", err)
	}
	status, _ = client.Status()
	if status.Running {
		t.Fatal("停止后状态应为未加速")
	}

	// 业务错误要原样返回给前端展示
	if err = client.Select("不存在", "不存在"); err == nil {
		t.Fatal("选择不存在的节点应当返回错误")
	}
	if err = client.Delete("1.2.3.4:443"); err != nil {
		t.Fatalf("删除失败: %v", err)
	}
	if err = client.Delete("1.2.3.4:443"); err == nil {
		t.Fatal("重复删除应当返回错误")
	}
}

func TestEventsStream(t *testing.T) {
	useTempPaths(t)
	engine := newTestEngine(t)
	server, err := StartServer(engine, "test", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = server.Close() }()

	info, _ := LoadInfo()
	client := NewClient(info)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := client.Events(ctx)
	if err != nil {
		t.Fatalf("订阅事件失败: %v", err)
	}
	time.Sleep(100 * time.Millisecond) // 等 SSE 连接建立
	engine.AddWarning("事件测试")

	select {
	case event := <-events:
		if event.Message != "事件测试" {
			t.Fatalf("事件内容不对: %+v", event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("未通过 SSE 收到事件")
	}
}

// TestAttachSecondFrontend 后启动的前端应当自动接入已有核心，而不是自己再起一条隧道。
func TestAttachSecondFrontend(t *testing.T) {
	useTempPaths(t)
	first, err := Attach("gui", "v-test", "")
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	if !first.IsOwner() {
		t.Fatal("第一个 Attach 应当成为核心")
	}
	if err := first.Engine().Import(base64.StdEncoding.EncodeToString([]byte("gpp://vless@9.9.9.9:443/uuid"))); err != nil {
		t.Fatal(err)
	}

	second, err := Attach("tui", "v-test", "")
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if second.IsOwner() {
		t.Fatal("第二个 Attach 应当作为前端接入，而不是抢占核心")
	}
	status, err := second.Status()
	if err != nil {
		t.Fatal(err)
	}
	if status.CorePID != os.Getpid() {
		t.Fatalf("前端应当看到核心 PID，实际 %d", status.CorePID)
	}
	peers, err := second.Peers()
	if err != nil {
		t.Fatal(err)
	}
	if len(peers) < 2 {
		t.Fatalf("前端应当看到核心的节点列表，实际 %d 个", len(peers))
	}
}

// TestAttachAfterCoreExit 核心退出后，下一个启动的进程应当接管（而不是被陈旧文件挡住）。
func TestAttachAfterCoreExit(t *testing.T) {
	useTempPaths(t)
	first, err := Attach("gui", "", "")
	if err != nil {
		t.Fatal(err)
	}
	// 模拟核心异常退出：控制服务已停、锁未释放、发现文件里的 PID 指向一个不存在的进程
	info, err := LoadInfo()
	if err != nil {
		t.Fatal(err)
	}
	info.PID = 2147483646
	if err = writeInfo(info); err != nil {
		t.Fatal(err)
	}
	server := first.server
	first.server = nil
	first.lease.Release()
	_ = server.Close() // 关闭服务；发现文件属于"别的 PID"，不应被这里的清理逻辑删掉
	if _, err = os.Stat(InfoPath()); err != nil {
		t.Fatalf("陈旧发现文件应当保留下来，用于验证接管逻辑: %v", err)
	}

	second, err := Attach("tui", "", "")
	if err != nil {
		t.Fatalf("核心退出后应当可以接管: %v", err)
	}
	defer second.Close()
	if !second.IsOwner() {
		t.Fatal("接管失败：应当是核心")
	}
	// 接管后旧句柄再次关闭不应误删新核心的锁
	first.Close()
	if third, err := Attach("gui", "", ""); err != nil {
		t.Fatalf("新核心仍在运行时应当可接入: %v", err)
	} else {
		if third.IsOwner() {
			t.Fatal("锁被误删：第三个进程不应抢到所有权")
		}
		third.Close()
	}
}
