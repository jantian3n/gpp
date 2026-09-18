package control

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/danbai225/gpp/backend/config"
	"github.com/danbai225/gpp/backend/core"
	"github.com/danbai225/gpp/backend/data"
)

// Client 是前端访问核心的客户端（回环 HTTP，开销可忽略）。
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// NewClient 根据发现信息创建客户端。
func NewClient(info *Info) *Client {
	return &Client{
		baseURL: fmt.Sprintf("http://127.0.0.1:%d", info.Port),
		token:   info.Token,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) do(method, path string, body any, target any) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	req.Header.Set(tokenHeader, c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode == http.StatusUnauthorized {
		return errors.New("控制面鉴权失败（token 不匹配），请退出所有 gpp 进程后重试")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("控制面返回状态码 %d", resp.StatusCode)
	}
	if target == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(target)
}

// Ping 做一次健康检查（同时校验 token）。
func (c *Client) Ping() error {
	var status data.Status
	return c.do(http.MethodGet, "/api/status", nil, &status)
}

// Status 查询核心状态。
func (c *Client) Status() (*data.Status, error) {
	status := &data.Status{}
	if err := c.do(http.MethodGet, "/api/status", nil, status); err != nil {
		return nil, err
	}
	return status, nil
}

// Peers 查询节点列表（顺序与配置一致，供需要稳定序号的界面使用）。
func (c *Client) Peers() ([]*config.Peer, error) {
	return c.peers("")
}

// PeersByPing 查询按延迟排序的节点列表（未测速排最后）。
func (c *Client) PeersByPing() ([]*config.Peer, error) {
	return c.peers("ping")
}

func (c *Client) peers(sort string) ([]*config.Peer, error) {
	path := "/api/peers"
	if sort != "" {
		path += "?sort=" + sort
	}
	var peers []*config.Peer
	if err := c.do(http.MethodGet, path, nil, &peers); err != nil {
		return nil, err
	}
	return peers, nil
}

// Info 查询核心信息（不含 token）。
func (c *Client) Info() (*Info, error) {
	info := &Info{}
	if err := c.do(http.MethodGet, "/api/info", nil, info); err != nil {
		return nil, err
	}
	return info, nil
}

// 写操作统一走 /api/*，失败原因由核心原样返回。
func (c *Client) call(path string, body any) error {
	var res result
	if err := c.do(http.MethodPost, path, body, &res); err != nil {
		return err
	}
	if strings.TrimSpace(res.Error) != "" {
		return errors.New(res.Error)
	}
	return nil
}

func (c *Client) Start() error   { return c.call("/api/start", nil) }
func (c *Client) Stop() error    { return c.call("/api/stop", nil) }
func (c *Client) Restart() error { return c.call("/api/restart", nil) }
func (c *Client) PingAll() error { return c.call("/api/ping", nil) }

func (c *Client) Select(game, httpPeer string) error {
	return c.call("/api/select", map[string]string{"game": game, "http": httpPeer})
}

func (c *Client) Import(token string) error {
	return c.call("/api/import", map[string]string{"token": token})
}

func (c *Client) Delete(name string) error {
	return c.call("/api/delete", map[string]string{"name": name})
}

// Events 订阅核心事件（SSE）；返回的通道在 ctx 取消或连接断开后关闭。
func (c *Client) Events(ctx context.Context) (<-chan core.Event, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/events", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set(tokenHeader, c.token)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("订阅事件失败：状态码 %d", resp.StatusCode)
	}
	events := make(chan core.Event, 16)
	go func() {
		defer close(events)
		defer func() { _ = resp.Body.Close() }()
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			var event core.Event
			if err := json.Unmarshal([]byte(strings.TrimSpace(strings.TrimPrefix(line, "data:"))), &event); err != nil {
				continue
			}
			select {
			case events <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return events, nil
}

// Handle 是前端持有的句柄：无论本进程是不是核心，调用方式完全一致。
type Handle struct {
	client           *Client
	info             Info
	owner            bool
	lease            *Lease
	server           *Server
	engine           *core.Engine
	kind             string
	version          string
	cancelBackground context.CancelFunc
}

// Attach 让本进程接入控制面：
//
//  1. 已有健康的核心 → 作为前端连接上去（不创建隧道、不读写别人的配置）
//  2. 否则抢占所有权锁，自己成为核心；抢不到就明确报错（附处置办法）
//
// 注意：这里刻意**不用 PID 判断"核心是否在运行"**——系统会复用 PID，
// 陈旧发现文件 + 被复用的 PID 会让新实例永远起不来。真正的互斥是所有权锁，
// PID 只在判断"锁是否陈旧"时作为参考。
//
// kind 取值 gui / tui，用于界面显示核心来源；version 可选。
func Attach(kind, version string, logOutput string) (*Handle, error) {
	// 1) 尝试接入已有核心
	if info, err := LoadInfo(); err == nil {
		client := NewClient(info)
		if perr := client.Ping(); perr == nil {
			return &Handle{client: client, info: *info, owner: false, kind: kind, version: version}, nil
		}
		// 连不上：可能是核心崩溃留下的陈旧信息，也可能只是控制面暂时无响应。
		// 交给所有权锁裁决：能抢到锁就说明确实没有活着的核心在持有它。
	}

	// 2) 成为核心
	lease, err := AcquireLease(LockPath())
	if err != nil {
		return nil, fmt.Errorf("无法成为 gpp 核心：%v；确认没有其他 gpp 在运行后可删除 %s 后重试", err, LockPath())
	}
	engine := core.New()
	engine.SetKind(kind)
	if logOutput != "" {
		engine.SetLogOutput(logOutput)
	}
	server, err := StartServer(engine, kind, version)
	if err != nil {
		lease.Release()
		return nil, err
	}
	// 成为核心后由自己周期刷新节点延迟（延迟是所有前端共享的状态）
	backgroundCtx, cancelBackground := context.WithCancel(context.Background())
	go engine.RunBackground(backgroundCtx)
	info := server.Info()
	return &Handle{
		client:           NewClient(&info),
		info:             info,
		owner:            true,
		lease:            lease,
		server:           server,
		engine:           engine,
		kind:             kind,
		version:          version,
		cancelBackground: cancelBackground,
	}, nil
}

// IsOwner 返回本进程是否持有隧道（核心）。
func (h *Handle) IsOwner() bool { return h != nil && h.owner }

// Engine 返回核心引擎（仅当本进程是核心时非 nil，供核心自行初始化配置）。
func (h *Handle) Engine() *core.Engine { return h.engine }

// Info 返回连接信息（含核心 PID/来源，供界面显示"当前核心是谁"）。
func (h *Handle) Info() Info { return h.info }

func (h *Handle) Status() (*data.Status, error)  { return h.client.Status() }
func (h *Handle) Peers() ([]*config.Peer, error) { return h.client.Peers() }

// PeersByPing 返回按延迟排序的节点（用于"挑最快节点"）。
func (h *Handle) PeersByPing() ([]*config.Peer, error) { return h.client.PeersByPing() }

func (h *Handle) Start() error              { return h.client.Start() }
func (h *Handle) Stop() error               { return h.client.Stop() }
func (h *Handle) Restart() error            { return h.client.Restart() }
func (h *Handle) PingAll() error            { return h.client.PingAll() }
func (h *Handle) Import(token string) error { return h.client.Import(token) }
func (h *Handle) Delete(name string) error  { return h.client.Delete(name) }

func (h *Handle) Select(game, httpPeer string) error {
	return h.client.Select(game, httpPeer)
}

// Events 订阅核心事件。
func (h *Handle) Events(ctx context.Context) (<-chan core.Event, error) {
	return h.client.Events(ctx)
}

// Close 释放本进程占用的资源：
//   - 作为核心：关闭控制服务并释放所有权锁（调用方应先把隧道停掉）
//   - 作为前端：什么都不做，隧道继续由核心持有
func (h *Handle) Close() {
	if h == nil || !h.owner {
		return
	}
	if h.cancelBackground != nil {
		h.cancelBackground()
	}
	if h.server != nil {
		_ = h.server.Close()
	}
	if h.lease != nil {
		h.lease.Release()
	}
}
