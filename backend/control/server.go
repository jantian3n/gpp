package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/danbai225/gpp/backend/config"
	"github.com/danbai225/gpp/backend/core"
)

// tokenHeader 是客户端携带 token 的请求头。
const tokenHeader = "X-GPP-Token"

// Server 是核心进程上的本地控制服务。
type Server struct {
	engine   *core.Engine
	info     Info
	listener net.Listener
	server   *http.Server
}

// StartServer 在回环地址上启动控制服务（随机端口），并写出发现文件。
func StartServer(engine *core.Engine, kind, version string) (*Server, error) {
	listener, err := listenLoopback()
	if err != nil {
		return nil, err
	}
	if kind == "" {
		kind = "core"
	}
	info := Info{
		PID:       pid(),
		Port:      listener.Addr().(*net.TCPAddr).Port,
		Token:     newToken(),
		Kind:      kind,
		Version:   version,
		StartedAt: time.Now(),
	}
	server := &Server{
		engine:   engine,
		info:     info,
		listener: listener,
	}
	server.server = &http.Server{
		Handler:           server.routes(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err = writeInfo(&info); err != nil {
		_ = listener.Close()
		return nil, fmt.Errorf("写入发现文件失败: %w", err)
	}
	go func() {
		// 控制面只服务本机前端，出错（含正常关闭）时直接退出即可
		_ = server.server.Serve(listener)
	}()
	return server, nil
}

// Info 返回连接信息。
func (s *Server) Info() Info { return s.info }

// Close 关闭控制服务并清理发现文件。
func (s *Server) Close() error {
	removeInfo(s.info.PID)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return s.server.Shutdown(ctx)
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/info", s.handleInfo)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/peers", s.handlePeers)
	mux.HandleFunc("/api/select", s.handleSelect)
	mux.HandleFunc("/api/import", s.handleImport)
	mux.HandleFunc("/api/delete", s.handleDelete)
	mux.HandleFunc("/api/ping", s.handlePing)
	mux.HandleFunc("/api/start", s.handleStart)
	mux.HandleFunc("/api/stop", s.handleStop)
	mux.HandleFunc("/api/restart", s.handleRestart)
	mux.HandleFunc("/api/events", s.handleEvents)
	return s.withAuth(mux)
}

// withAuth 校验 token：控制面能启停隧道、读写节点，必须挡住本机其他程序。
func (s *Server) withAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(tokenHeader) != s.info.Token {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "token 无效"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

// result 是写操作的统一返回体：失败原因原样带给前端展示。
type result struct {
	Error string `json:"error,omitempty"`
}

func writeResult(w http.ResponseWriter, err error) {
	if err != nil {
		writeJSON(w, http.StatusOK, result{Error: err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result{})
}

func readJSON(r *http.Request, target any) error {
	if r.Body == nil {
		return nil
	}
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	if err := decoder.Decode(target); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func (s *Server) handleInfo(w http.ResponseWriter, _ *http.Request) {
	info := s.info
	info.Token = "" // 不回传 token
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.engine.Status())
}

func (s *Server) handlePeers(w http.ResponseWriter, r *http.Request) {
	// 默认返回配置顺序（界面序号必须稳定）；sort=ping 时按延迟排序
	var peers []*config.Peer
	if r.URL.Query().Get("sort") == "ping" {
		peers = s.engine.PeersByPing()
	} else {
		peers = s.engine.Peers()
	}
	if peers == nil {
		peers = []*config.Peer{}
	}
	writeJSON(w, http.StatusOK, peers)
}

func (s *Server) handleSelect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Game string `json:"game"`
		Http string `json:"http"`
	}
	if err := readJSON(r, &req); err != nil {
		writeResult(w, err)
		return
	}
	writeResult(w, s.engine.Select(req.Game, req.Http))
}

func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := readJSON(r, &req); err != nil {
		writeResult(w, err)
		return
	}
	writeResult(w, s.engine.Import(req.Token))
}

func (s *Server) handleDelete(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := readJSON(r, &req); err != nil {
		writeResult(w, err)
		return
	}
	writeResult(w, s.engine.Delete(req.Name))
}

func (s *Server) handlePing(w http.ResponseWriter, _ *http.Request) {
	go s.engine.PingAll()
	writeResult(w, nil)
}

func (s *Server) handleStart(w http.ResponseWriter, _ *http.Request) {
	writeResult(w, s.engine.Start())
}

func (s *Server) handleStop(w http.ResponseWriter, _ *http.Request) {
	writeResult(w, s.engine.Stop())
}

func (s *Server) handleRestart(w http.ResponseWriter, _ *http.Request) {
	writeResult(w, s.engine.Restart())
}

// handleEvents 通过 SSE 把核心事件推给所有前端（GUI 再转发给网页，TUI 直接渲染）。
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "不支持流式响应"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	events, unsubscribe := s.engine.Subscribe()
	defer unsubscribe()
	keepAlive := time.NewTicker(20 * time.Second)
	defer keepAlive.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-events:
			if !open {
				return
			}
			payload, err := json.Marshal(event)
			if err != nil {
				continue
			}
			if _, err = fmt.Fprintf(w, "data: %s\n\n", payload); err != nil {
				return
			}
			flusher.Flush()
		case <-keepAlive.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}
