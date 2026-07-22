package fnos

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
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
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", response.Code)
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
