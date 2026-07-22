package fnos

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	if err := InitConfig(configPath, root); err != nil {
		t.Fatal(err)
	}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	supervisor := NewSupervisor("/not-used", configPath, logger)
	return NewServer(ServerOptions{ConfigPath: configPath, DefaultPath: root, Version: "v-test", Supervisor: supervisor, Logger: logger}), configPath
}

func TestConfigAPIRequiresFnOSAdmin(t *testing.T) {
	server, _ := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/app/ComposeUpdater/api/config", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
}

func TestConfigAPISavesViaGatewayPrefix(t *testing.T) {
	server, configPath := newTestServer(t)
	settings, err := LoadSettings(configPath)
	if err != nil {
		t.Fatal(err)
	}
	settings.Depth = 4
	body, _ := json.Marshal(settings)
	request := httptest.NewRequest(http.MethodPost, "https://nas.example/app/ComposeUpdater/api/config", bytes.NewReader(body))
	request.Header.Set("X-Trim-Isadmin", "true")
	request.Header.Set("X-Trim-Username", "admin")
	request.Header.Set("X-Compose-Updater", "web")
	request.Header.Set("Origin", "https://nas.example")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	updated, err := LoadSettings(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Depth != 4 {
		t.Fatalf("depth = %d, want 4", updated.Depth)
	}
}

func TestMutationRejectsCrossSiteOrigin(t *testing.T) {
	server, configPath := newTestServer(t)
	settings, _ := LoadSettings(configPath)
	body, _ := json.Marshal(settings)
	request := httptest.NewRequest(http.MethodPost, "https://nas.example/api/config", bytes.NewReader(body))
	request.Header.Set("X-Trim-Isadmin", "true")
	request.Header.Set("X-Compose-Updater", "web")
	request.Header.Set("Origin", "https://attacker.example")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
	}
}

func TestMutationAllowsFnOSGatewayHostRewrite(t *testing.T) {
	server, configPath := newTestServer(t)
	settings, _ := LoadSettings(configPath)
	body, _ := json.Marshal(settings)
	request := httptest.NewRequest(http.MethodPost, "http://unix-upstream/api/config", bytes.NewReader(body))
	request.Header.Set("X-Trim-Isadmin", "true")
	request.Header.Set("X-Compose-Updater", "web")
	request.Header.Set("Origin", "http://192.168.3.7:5666")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
}

func TestDirectoryAPIListsOnlyDirectories(t *testing.T) {
	server, configPath := newTestServer(t)
	root := filepath.Dir(configPath)
	if err := os.Mkdir(filepath.Join(root, "docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "compose.yml"), []byte("services: {}"), 0o644); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/directories?path="+url.QueryEscape(root), nil)
	request.Header.Set("X-Trim-Isadmin", "true")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"name":"docker"`) || strings.Contains(response.Body.String(), "compose.yml") {
		t.Fatalf("unexpected directory response: %s", response.Body.String())
	}
}

func TestDirectoryLocationsKeepOnePersonalAndTeamEntry(t *testing.T) {
	root := t.TempDir()
	paths := []string{filepath.Join(root, "personal-a"), filepath.Join(root, "personal-b"), filepath.Join(root, "team-a"), filepath.Join(root, "team-b")}
	for _, path := range paths {
		if err := os.Mkdir(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	locations := make([]map[string]string, 0)
	seenPaths := make(map[string]struct{})
	seenKinds := make(map[string]struct{})
	appendDirectoryLocation(&locations, seenPaths, seenKinds, "我的文件", paths[0], "personal")
	appendDirectoryLocation(&locations, seenPaths, seenKinds, "我的文件", paths[1], "personal")
	appendDirectoryLocation(&locations, seenPaths, seenKinds, "团队文件", paths[2], "team")
	appendDirectoryLocation(&locations, seenPaths, seenKinds, "团队文件", paths[3], "team")
	if len(locations) != 2 || locations[0]["kind"] != "personal" || locations[1]["kind"] != "team" {
		t.Fatalf("locations = %#v, want one personal and one team entry", locations)
	}
}

func TestRuntimeAPIRequiresAdminAndReturnsSupervisorState(t *testing.T) {
	server, _ := newTestServer(t)
	unauthorized := httptest.NewRequest(http.MethodGet, "/api/runtime", nil)
	unauthorizedResponse := httptest.NewRecorder()
	server.ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusForbidden {
		t.Fatalf("unauthorized status = %d", unauthorizedResponse.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/runtime", nil)
	request.Header.Set("X-Trim-Isadmin", "true")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"runtime"`) || !strings.Contains(response.Body.String(), `"version":"v-test"`) {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
}

func TestStandaloneHandlerRequiresConfiguredBasicAuth(t *testing.T) {
	server, _ := newTestServer(t)
	handler := server.standaloneHandler("operator", "secret")

	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/status", nil))
	if unauthorized.Code != http.StatusUnauthorized || unauthorized.Header().Get("WWW-Authenticate") == "" {
		t.Fatalf("unauthorized status = %d, challenge = %q", unauthorized.Code, unauthorized.Header().Get("WWW-Authenticate"))
	}

	request := httptest.NewRequest(http.MethodGet, "/api/status", nil)
	request.SetBasicAuth("operator", "secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"manager_running":true`) {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
}

func TestStandaloneAddressRequiresPasswordOffLoopback(t *testing.T) {
	if err := validateStandaloneAddress("127.0.0.1:8080", ""); err != nil {
		t.Fatalf("loopback address rejected: %v", err)
	}
	if err := validateStandaloneAddress("0.0.0.0:8080", ""); err == nil {
		t.Fatal("non-loopback address without password was accepted")
	}
	if err := validateStandaloneAddress("0.0.0.0:8080", "secret"); err != nil {
		t.Fatalf("authenticated non-loopback address rejected: %v", err)
	}
}

func TestRunNowAPIQueuesForcedRun(t *testing.T) {
	server, _ := newTestServer(t)
	request := httptest.NewRequest(http.MethodPost, "/api/run-now", strings.NewReader(`{}`))
	request.Header.Set("X-Trim-Isadmin", "true")
	request.Header.Set("X-Compose-Updater", "web")
	request.Header.Set("Sec-Fetch-Site", "same-origin")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), "已请求立即运行") {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
}

func TestEmbeddedPageHasNoInlineScript(t *testing.T) {
	server, _ := newTestServer(t)
	request := httptest.NewRequest(http.MethodGet, "/app/ComposeUpdater/", nil)
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d", response.Code)
	}
	body := response.Body.String()
	if !strings.Contains(body, "Compose Updater") || strings.Contains(body, "<script>") {
		t.Fatal("unexpected embedded page")
	}
}

func TestConfigAPIRemainsUsableWhenConfigIsBroken(t *testing.T) {
	server, configPath := newTestServer(t)
	if err := os.WriteFile(configPath, []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/config", nil)
	request.Header.Set("X-Trim-Isadmin", "true")
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "config_error") {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
}
