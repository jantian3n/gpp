// Package control 是 gpp 的本地控制面：
//
//	唯一核心进程持有 TUN 隧道，GUI / TUI 都通过 127.0.0.1 上的本地 HTTP API
//	读写同一份状态；谁先启动谁成为核心，后启动的一方自动作为前端连上去。
//
// 发现方式：核心把 {pid, port, token} 写到 ~/.gpp/control.json（仅本机可读），
// 其他进程读它并做一次健康检查；token 防止本机其他用户/程序随意控制。
package control

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/danbai225/gpp/backend/config"
) // Info 是核心写进发现文件的连接信息。
type Info struct {
	PID       int       `json:"pid"`
	Port      int       `json:"port"`
	Token     string    `json:"token"`
	Kind      string    `json:"kind"` // gui / tui
	Version   string    `json:"version,omitempty"`
	StartedAt time.Time `json:"started_at"`
}

// InfoPath 返回发现文件路径（与配置文件同目录，便于整体迁移与测试隔离）。
func InfoPath() string { return filepath.Join(filepath.Dir(config.Path()), "control.json") }

// LockPath 返回所有权锁文件路径。
func LockPath() string { return filepath.Join(filepath.Dir(config.Path()), "control.lock") }

// LoadInfo 读取发现文件。
func LoadInfo() (*Info, error) {
	data, err := os.ReadFile(InfoPath())
	if err != nil {
		return nil, err
	}
	info := &Info{}
	if err = json.Unmarshal(data, info); err != nil {
		return nil, err
	}
	if info.Port <= 0 || strings.TrimSpace(info.Token) == "" {
		return nil, errors.New("发现文件内容不完整")
	}
	return info, nil
}

// writeInfo 原子写入发现文件，权限 0600（只允许本用户读取）。
func writeInfo(info *Info) error {
	path := InfoPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(info, "", " ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "control.json.tmp-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	_ = os.Chmod(name, 0o600)
	return os.Rename(name, path)
}

// removeInfo 删除发现文件；只删除"属于自己"的那份，避免误删接任者的文件。
func removeInfo(expectPID int) {
	info, err := LoadInfo()
	if err != nil {
		return
	}
	if expectPID != 0 && info.PID != expectPID {
		return
	}
	_ = os.Remove(InfoPath())
}

func newToken() string {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

// pid 返回当前进程号。
func pid() int { return os.Getpid() }

// Lease 代表"本进程是核心"的所有权。
type Lease struct {
	path string
	pid  int
}

// AcquireLease 抢占核心所有权。
// 已存在锁文件时检查持有者是否还活着：进程已退出则接管（清理陈旧锁）。
func AcquireLease(path string) (*Lease, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 3; attempt++ {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			pid := os.Getpid()
			if _, werr := fmt.Fprintf(file, "%d", pid); werr != nil {
				_ = file.Close()
				_ = os.Remove(path)
				return nil, werr
			}
			_ = file.Close()
			return &Lease{path: path, pid: pid}, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		// 锁已存在：判断持有者是否存活
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			// 刚好被释放，重试
			continue
		}
		var holder int
		_, _ = fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &holder)
		if holder > 0 && processAliveWithRetry(holder) {
			return nil, fmt.Errorf("另一个 gpp 核心正在运行（PID %d）", holder)
		}
		// 陈旧锁：删除后重试
		_ = os.Remove(path)
	}
	return nil, errors.New("无法获取核心所有权锁（重试多次仍失败）")
}

// processAliveWithRetry 对"看起来还活着"的持有者再确认几次：
// 进程刚被终止时可能短暂处于"正在退出"状态，直接判定存活会把新实例挡在门外。
func processAliveWithRetry(holder int) bool {
	if !processAlive(holder) {
		return false
	}
	for i := 0; i < 3; i++ {
		time.Sleep(300 * time.Millisecond)
		if !processAlive(holder) {
			return false
		}
	}
	return true
}

// Release 释放所有权。
func (l *Lease) Release() {
	if l == nil {
		return
	}
	data, err := os.ReadFile(l.path)
	if err == nil {
		var holder int
		_, _ = fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &holder)
		if holder != 0 && holder != l.pid {
			return // 锁已被别人接管，别误删
		}
	}
	_ = os.Remove(l.path)
}

// listenLoopback 在回环地址上随机取端口，避免端口冲突。
func listenLoopback() (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:0")
}
