package fnos

import (
	"context"
	"crypto/subtle"
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
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/guchengod/compose-updater/internal/config"
	"github.com/guchengod/compose-updater/internal/cronexpr"
)

//go:embed web
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
	return serveTCP(ctx, address, handler)
}

// ServeStandalone exposes the shared runtime UI for native binary and Docker
// deployments. Non-loopback listeners require HTTP Basic authentication because
// this UI can modify configuration and trigger Docker updates.
func (s *Server) ServeStandalone(ctx context.Context, address, username, password string) error {
	if err := validateStandaloneAddress(address, password); err != nil {
		return err
	}
	return serveTCP(ctx, address, s.standaloneHandler(username, password))
}

func serveTCP(ctx context.Context, address string, handler http.Handler) error {
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

func validateStandaloneAddress(address, password string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("无效的 Web 监听地址 %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	loopback := strings.EqualFold(host, "localhost") || (ip != nil && ip.IsLoopback())
	if !loopback && strings.TrimSpace(password) == "" {
		return errors.New("Web 界面监听非本机地址时必须设置 COMPOSE_UPDATER_WEB_PASSWORD 或 -web-password")
	}
	return nil
}

func (s *Server) standaloneHandler(username, password string) http.Handler {
	username = strings.TrimSpace(username)
	if username == "" {
		username = "admin"
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		identity := username
		if password != "" {
			providedUser, providedPassword, ok := r.BasicAuth()
			userOK := subtle.ConstantTimeCompare([]byte(providedUser), []byte(username)) == 1
			passwordOK := subtle.ConstantTimeCompare([]byte(providedPassword), []byte(password)) == 1
			if !ok || !userOK || !passwordOK {
				w.Header().Set("WWW-Authenticate", `Basic realm="Compose Updater"`)
				http.Error(w, "需要登录 Compose Updater", http.StatusUnauthorized)
				return
			}
			identity = providedUser
		}
		r.Header.Set("X-Trim-Isadmin", "true")
		r.Header.Set("X-Trim-Username", identity)
		s.ServeHTTP(w, r)
	})
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
	case r.URL.Path == "/api/directories" && r.Method == http.MethodGet:
		s.requireAdmin(w, r, s.listDirectories)
	case r.URL.Path == "/api/status" && r.Method == http.MethodGet:
		s.requireAdmin(w, r, s.getStatus)
	case r.URL.Path == "/api/runtime" && r.Method == http.MethodGet:
		s.requireAdmin(w, r, s.getRuntime)
	case r.URL.Path == "/api/run-now" && r.Method == http.MethodPost:
		s.requireAdminMutation(w, r, s.runNow)
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
		// fnOS proxies the browser request through a Unix socket and may replace
		// Host with the gateway's internal upstream. Comparing Origin with r.Host
		// therefore rejects legitimate same-origin requests. The custom header
		// above already forces a browser CORS preflight for cross-origin callers;
		// Sec-Fetch-Site lets us additionally reject an explicit cross-site fetch.
		if strings.EqualFold(strings.TrimSpace(r.Header.Get("Sec-Fetch-Site")), "cross-site") {
			writeError(w, http.StatusForbidden, "跨站请求已拒绝")
			return
		}
		next(w, r)
	})
}

func (s *Server) listDirectories(w http.ResponseWriter, r *http.Request) {
	directory := strings.TrimSpace(r.URL.Query().Get("path"))
	if directory == "" {
		directory = strings.TrimSpace(s.defaultPath)
	}
	if directory == "" {
		directory = string(filepath.Separator)
	}
	if !filepath.IsAbs(directory) {
		writeError(w, http.StatusBadRequest, "目录必须是绝对路径")
		return
	}

	directory = filepath.Clean(directory)
	info, err := os.Stat(directory)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("无法访问目录 %q: %v", directory, err))
		return
	}
	if !info.IsDir() {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("%q 不是目录", directory))
		return
	}

	entries, err := os.ReadDir(directory)
	if err != nil {
		writeError(w, http.StatusForbidden, fmt.Sprintf("无法读取目录 %q: %v", directory, err))
		return
	}
	directories := make([]map[string]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		directories = append(directories, map[string]string{
			"name": entry.Name(),
			"path": filepath.Join(directory, entry.Name()),
		})
	}
	sort.Slice(directories, func(i, j int) bool {
		return strings.ToLower(directories[i]["name"]) < strings.ToLower(directories[j]["name"])
	})
	parent := filepath.Dir(directory)
	if parent == directory {
		parent = ""
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path": directory, "parent": parent, "directories": directories, "locations": s.directoryLocations(),
	})
}

func (s *Server) directoryLocations() []map[string]string {
	seen := make(map[string]struct{})
	seenUniqueKinds := make(map[string]struct{})
	locations := make([]map[string]string, 0)
	add := func(label, locationPath, kind string) {
		appendDirectoryLocation(&locations, seen, seenUniqueKinds, label, locationPath, kind)
	}

	for _, authorized := range s.authorizedPaths {
		clean := filepath.Clean(authorized)
		parts := strings.Split(strings.TrimPrefix(clean, string(filepath.Separator)), string(filepath.Separator))
		if len(parts) >= 2 && strings.HasPrefix(parts[0], "vol") {
			volume := filepath.Join(string(filepath.Separator), parts[0])
			userRoot := filepath.Join(volume, parts[1])
			add("我的文件", userRoot, "personal")
			add("存储空间 "+strings.TrimPrefix(parts[0], "vol"), volume, "storage")
		}
		add(filepath.Base(clean), clean, "authorized")
	}

	volumes, _ := filepath.Glob("/vol[0-9]*")
	sort.Strings(volumes)
	for _, volume := range volumes {
		number := strings.TrimPrefix(filepath.Base(volume), "vol")
		add("存储空间 "+number, volume, "storage")
		add("我的文件", filepath.Join(volume, "1000"), "personal")
		add("团队文件", filepath.Join(volume, "1001"), "team")
	}
	if len(locations) == 0 {
		add("当前授权目录", s.defaultPath, "authorized")
	}
	return locations
}

func appendDirectoryLocation(locations *[]map[string]string, seenPaths, seenUniqueKinds map[string]struct{}, label, locationPath, kind string) {
	locationPath = filepath.Clean(strings.TrimSpace(locationPath))
	if !filepath.IsAbs(locationPath) {
		return
	}
	if info, err := os.Stat(locationPath); err != nil || !info.IsDir() {
		return
	}
	if _, ok := seenPaths[locationPath]; ok {
		return
	}
	if kind == "personal" || kind == "team" {
		if _, ok := seenUniqueKinds[kind]; ok {
			return
		}
		seenUniqueKinds[kind] = struct{}{}
	}
	seenPaths[locationPath] = struct{}{}
	*locations = append(*locations, map[string]string{"label": label, "path": locationPath, "kind": kind})
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

func (s *Server) getRuntime(w http.ResponseWriter, _ *http.Request) {
	snapshot := s.supervisor.Runtime()
	writeJSON(w, http.StatusOK, map[string]any{
		"manager_running": true,
		"updater_running": s.supervisor.Running(),
		"version":         s.version,
		"runtime":         snapshot,
	})
}

func (s *Server) runNow(w http.ResponseWriter, _ *http.Request) {
	if err := s.supervisor.TriggerRun(); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "message": "已请求立即运行，更新服务正在准备本轮检查"})
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
	message, authRequired := proxyTestMessage(response.StatusCode)
	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "status": response.Status, "status_code": response.StatusCode,
		"registry_auth_required": authRequired, "message": message,
	})
}

func proxyTestMessage(statusCode int) (string, bool) {
	if statusCode == http.StatusUnauthorized {
		return "代理连接正常，Registry 要求认证（这是 Docker Registry 的预期响应）", true
	}
	if statusCode >= 200 && statusCode < 400 {
		return "代理连接正常，Docker Registry 可以访问", false
	}
	return fmt.Sprintf("代理已连通，Registry 返回 HTTP %d", statusCode), false
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
