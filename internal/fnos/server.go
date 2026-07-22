package fnos

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/guchengod/compose-updater/internal/config"
	"github.com/guchengod/compose-updater/internal/cronexpr"
)

//go:embed web/*
var webFiles embed.FS

type Server struct {
	configPath      string
	defaultPath     string
	version         string
	authorizedPaths []string
	supervisor      *Supervisor
	logger          *slog.Logger
	assets          http.Handler
	saveMu          sync.Mutex
}

type ServerOptions struct {
	ConfigPath      string
	DefaultPath     string
	Version         string
	AuthorizedPaths []string
	Supervisor      *Supervisor
	Logger          *slog.Logger
}

func NewServer(options ServerOptions) *Server {
	assets, _ := fs.Sub(webFiles, "web")
	if options.AuthorizedPaths == nil {
		options.AuthorizedPaths = []string{}
	}
	return &Server{
		configPath: options.ConfigPath, defaultPath: options.DefaultPath, version: options.Version,
		authorizedPaths: options.AuthorizedPaths, supervisor: options.Supervisor,
		logger: options.Logger, assets: http.FileServer(http.FS(assets)),
	}
}

func (s *Server) ServeUnix(ctx context.Context, socketPath string) error {
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("监听 Unix Socket: %w", err)
	}
	defer func() {
		_ = listener.Close()
		_ = os.Remove(socketPath)
	}()
	if err := os.Chmod(socketPath, 0o660); err != nil {
		return fmt.Errorf("设置 Unix Socket 权限: %w", err)
	}
	httpServer := &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	err = httpServer.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) ServeTCP(ctx context.Context, address string, devAdmin bool) error {
	handler := http.Handler(s)
	if devAdmin {
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			r.Header.Set("X-Trim-Isadmin", "true")
			r.Header.Set("X-Trim-Username", "local-dev")
			handlerWithoutDev := http.Handler(s)
			handlerWithoutDev.ServeHTTP(w, r)
		})
	}
	httpServer := &http.Server{Addr: address, Handler: handler, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	err := httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	setSecurityHeaders(w)
	r.URL.Path = trimGatewayPrefix(r.URL.Path)
	switch {
	case r.URL.Path == "/healthz" && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case r.URL.Path == "/api/config" && r.Method == http.MethodGet:
		s.requireAdmin(w, r, s.getConfig)
	case r.URL.Path == "/api/config" && r.Method == http.MethodPost:
		s.requireAdminMutation(w, r, s.saveConfig)
	case r.URL.Path == "/api/status" && r.Method == http.MethodGet:
		s.requireAdmin(w, r, s.getStatus)
	case r.URL.Path == "/api/proxy-test" && r.Method == http.MethodPost:
		s.requireAdminMutation(w, r, s.testProxy)
	case r.Method == http.MethodGet || r.Method == http.MethodHead:
		s.assets.ServeHTTP(w, r)
	default:
		writeError(w, http.StatusMethodNotAllowed, "不支持的请求方法")
	}
}

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Trim-Isadmin")), "true") {
		writeError(w, http.StatusForbidden, "仅飞牛管理员可以管理 Compose Updater")
		return
	}
	next(w, r)
}

func (s *Server) requireAdminMutation(w http.ResponseWriter, r *http.Request, next http.HandlerFunc) {
	s.requireAdmin(w, r, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Compose-Updater") != "web" {
			writeError(w, http.StatusForbidden, "请求来源校验失败")
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			parsed, err := url.Parse(origin)
			if err != nil || !strings.EqualFold(parsed.Host, r.Host) {
				writeError(w, http.StatusForbidden, "跨站请求已拒绝")
				return
			}
		}
		next(w, r)
	})
}

func (s *Server) getConfig(w http.ResponseWriter, _ *http.Request) {
	settings, err := LoadSettings(s.configPath)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"config": DefaultSettings(s.defaultPath), "authorized_paths": s.authorizedPaths,
			"config_error": err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"config": settings, "authorized_paths": s.authorizedPaths,
	})
}

func (s *Server) saveConfig(w http.ResponseWriter, r *http.Request) {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	var settings Settings
	if err := decodeJSON(r, &settings); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := SaveSettings(s.configPath, settings); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.supervisor.Restart()
	s.logger.Info("config_saved", "user", r.Header.Get("X-Trim-Username"))
	updated, _ := LoadSettings(s.configPath)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "config": updated})
}

func (s *Server) getStatus(w http.ResponseWriter, _ *http.Request) {
	response := map[string]any{
		"manager_running": true,
		"updater_running": s.supervisor.Running(),
		"version":         s.version,
	}
	if cfg, err := config.Load(s.configPath); err == nil {
		if schedule, parseErr := cronexpr.Parse(cfg.Schedule, cfg.Location()); parseErr == nil {
			if next, nextErr := schedule.Next(time.Now()); nextErr == nil {
				response["next_run"] = next.Format(time.RFC3339)
			}
		}
	} else {
		response["config_error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) testProxy(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Proxy string `json:"proxy"`
	}
	if err := decodeJSON(r, &request); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	proxyURL, err := url.Parse(strings.TrimSpace(request.Proxy))
	if err != nil || proxyURL.Host == "" {
		writeError(w, http.StatusBadRequest, "请输入有效的代理 URL")
		return
	}
	if proxyURL.Scheme != "http" && proxyURL.Scheme != "https" {
		writeError(w, http.StatusBadRequest, "页面连通性测试目前支持 HTTP/HTTPS 代理；SOCKS 代理会在更新任务中使用")
		return
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = http.ProxyURL(proxyURL)
	client := &http.Client{Transport: transport, Timeout: 12 * time.Second}
	requestCtx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	probe, _ := http.NewRequestWithContext(requestCtx, http.MethodGet, "https://registry-1.docker.io/v2/", nil)
	response, err := client.Do(probe)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("代理连接失败: %v", err))
		return
	}
	_ = response.Body.Close()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "status": response.Status})
}

func decodeJSON(r *http.Request, destination any) error {
	reader := io.LimitReader(r.Body, (1<<20)+1)
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("请求内容不是有效配置: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("请求只能包含一个 JSON 值")
	}
	return nil
}

func trimGatewayPrefix(requestPath string) string {
	const prefix = "/app/ComposeUpdater"
	if requestPath == prefix {
		return "/"
	}
	if strings.HasPrefix(requestPath, prefix+"/") {
		return strings.TrimPrefix(requestPath, prefix)
	}
	clean := path.Clean("/" + requestPath)
	if strings.HasSuffix(requestPath, "/") && clean != "/" {
		clean += "/"
	}
	return clean
}

func setSecurityHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'self'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "SAMEORIGIN")
	w.Header().Set("Cache-Control", "no-store")
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}
