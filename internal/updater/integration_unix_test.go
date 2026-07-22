//go:build linux || darwin

package updater

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/guchengod/compose-updater/internal/config"
	"github.com/guchengod/compose-updater/internal/notify"
)

func TestLatestTagPullsAndRecreatesWithoutEditingCompose(t *testing.T) {
	root := t.TempDir()
	composePath := filepath.Join(root, "compose.yml")
	original := "services:\n  web:\n    image: example/web:latest\n"
	mustWriteFile(t, composePath, original, 0o600)
	cfg := loadIntegrationConfig(t, root)
	statePath, logPath := installFakeDocker(t, cfg, root)
	mustWriteFile(t, statePath, "sha256:old\n", 0o600)

	engine := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), notify.Noop{})
	if err := engine.CheckPrerequisites(context.Background()); err != nil {
		t.Fatal(err)
	}
	summary := engine.Run(context.Background(), true)
	if summary.ProjectsFailed != 0 || summary.ProjectsUpdated != 1 || summary.ServicesUpdated != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	content, _ := os.ReadFile(composePath)
	if string(content) != original {
		t.Fatalf("latest compose file must not change:\n%s", content)
	}
	calls, _ := os.ReadFile(logPath)
	if !strings.Contains(string(calls), " pull --ignore-buildable ") || !strings.Contains(string(calls), " up -d ") {
		t.Fatalf("pull/up not called:\n%s", calls)
	}
}

func TestCustomComposeProjectNameIsResolvedFromConfigFile(t *testing.T) {
	root := t.TempDir()
	composePath := filepath.Join(root, "compose.yml")
	mustWriteFile(t, composePath, "services:\n  web:\n    image: example/web:latest\n", 0o600)
	cfg := loadIntegrationConfig(t, root)
	statePath, logPath := installFakeDocker(t, cfg, root)
	mustWriteFile(t, statePath, "sha256:old\n", 0o600)
	t.Setenv("FAKE_COMPOSE_LS", `[{"Name":"myflatnas","Status":"running(1)","ConfigFiles":`+jsonQuote(composePath)+`}]`)
	t.Setenv("FAKE_REQUIRE_PROJECT_NAME", "myflatnas")

	engine := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), notify.Noop{})
	summary := engine.Run(context.Background(), true)
	if summary.ProjectsFailed != 0 || summary.ProjectsUpdated != 1 || summary.ServicesUpdated != 1 {
		t.Fatalf("custom project was not updated: %+v", summary)
	}
	if got := summary.Results[0].Project; got != "myflatnas" {
		t.Fatalf("project = %q, want myflatnas", got)
	}
	calls, _ := os.ReadFile(logPath)
	if !strings.Contains(string(calls), "compose --project-name myflatnas --project-directory") {
		t.Fatalf("resolved project name not applied to compose commands:\n%s", calls)
	}
}

func TestNumericTagUpdatesComposeAndRecreates(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/team/app/tags/list" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "team/app", "tags": []string{"1.0.0", "1.5.0", "2.0.0"}})
	}))
	defer server.Close()
	registryHost := strings.TrimPrefix(server.URL, "http://")

	root := t.TempDir()
	composePath := filepath.Join(root, "compose.yml")
	oldImage := registryHost + "/team/app:1.0.0"
	newImage := registryHost + "/team/app:2.0.0"
	mustWriteFile(t, composePath, "services:\n  web:\n    image: "+oldImage+" # keep\n", 0o600)
	t.Setenv("COMPOSE_UPDATER_INSECURE_REGISTRIES", registryHost)
	cfg := loadIntegrationConfig(t, root)
	statePath, _ := installFakeDocker(t, cfg, root)
	mustWriteFile(t, statePath, "sha256:old\n", 0o600)

	engine := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), notify.Noop{})
	summary := engine.Run(context.Background(), true)
	if summary.ProjectsFailed != 0 || summary.ProjectsUpdated != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	content, _ := os.ReadFile(composePath)
	if !strings.Contains(string(content), newImage+" # keep") {
		t.Fatalf("compose image not updated or comment lost:\n%s", content)
	}
	if _, err := os.Stat(composePath + ".compose-updater.bak"); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
}

func TestSHATagUpdatesComposeToLatestStable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/team/app/tags/list" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"name": "team/app", "tags": []string{"sha-old", "v1.9.0", "v2.0.0-beta", "v1.10.0"}})
	}))
	defer server.Close()
	registryHost := strings.TrimPrefix(server.URL, "http://")

	root := t.TempDir()
	composePath := filepath.Join(root, "compose.yml")
	oldImage := registryHost + "/team/app:sha-old"
	newImage := registryHost + "/team/app:v1.10.0"
	mustWriteFile(t, composePath, "services:\n  web:\n    image: "+oldImage+"\n", 0o600)
	t.Setenv("COMPOSE_UPDATER_INSECURE_REGISTRIES", registryHost)
	cfg := loadIntegrationConfig(t, root)
	statePath, _ := installFakeDocker(t, cfg, root)
	mustWriteFile(t, statePath, "sha256:old\n", 0o600)

	notifier := &recordingNotifier{enabled: true}
	engine := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), notifier)
	summary := engine.Run(context.Background(), true)
	if summary.ProjectsFailed != 0 || summary.ProjectsUpdated != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	content, _ := os.ReadFile(composePath)
	if !strings.Contains(string(content), newImage) {
		t.Fatalf("compose SHA tag was not updated to stable release:\n%s", content)
	}
	if len(notifier.titles) != 1 || notifier.titles[0] != "Docker 更新成功" {
		t.Fatalf("success notification missing: %#v", notifier.titles)
	}
}

func TestRegistryFailureFailsProjectAndNotifies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "registry unavailable", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	registryHost := strings.TrimPrefix(server.URL, "http://")

	root := t.TempDir()
	composePath := filepath.Join(root, "compose.yml")
	mustWriteFile(t, composePath, "services:\n  web:\n    image: "+registryHost+"/team/app:sha-old\n", 0o600)
	t.Setenv("COMPOSE_UPDATER_INSECURE_REGISTRIES", registryHost)
	cfg := loadIntegrationConfig(t, root)
	statePath, logPath := installFakeDocker(t, cfg, root)
	mustWriteFile(t, statePath, "sha256:old\n", 0o600)

	notifier := &recordingNotifier{enabled: true}
	engine := New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), notifier)
	summary := engine.Run(context.Background(), true)
	if summary.ProjectsFailed != 1 || summary.Results[0].Status != "failed" {
		t.Fatalf("registry error must fail project: %+v", summary)
	}
	if len(notifier.titles) != 1 || notifier.titles[0] != "Docker 更新失败" {
		t.Fatalf("failure notification missing: %#v", notifier.titles)
	}
	calls, _ := os.ReadFile(logPath)
	if strings.Contains(string(calls), " pull ") || strings.Contains(string(calls), " up -d ") {
		t.Fatalf("pull/up must not run after registry failure:\n%s", calls)
	}
}

type recordingNotifier struct {
	enabled bool
	titles  []string
	bodies  []string
}

func (n *recordingNotifier) Enabled() bool { return n.enabled }

func (n *recordingNotifier) Send(_ context.Context, title, body, _ string) error {
	n.titles = append(n.titles, title)
	n.bodies = append(n.bodies, body)
	return nil
}

func loadIntegrationConfig(t *testing.T, root string) *config.Config {
	t.Helper()
	configPath := filepath.Join(root, "config.json")
	content := `{"version":1,"paths":[` + jsonQuote(root) + `],"depth":0,"run_on_start":false,"bark":{"enabled":false}}`
	mustWriteFile(t, configPath, content, 0o600)
	t.Setenv("COMPOSE_UPDATER_DATA_DIR", filepath.Join(root, "data"))
	t.Setenv("COMPOSE_UPDATER_CONFIG_TIMEOUT", "2s")
	t.Setenv("COMPOSE_UPDATER_REGISTRY_TIMEOUT", "2s")
	t.Setenv("COMPOSE_UPDATER_PULL_TIMEOUT", "2s")
	t.Setenv("COMPOSE_UPDATER_UPDATE_TIMEOUT", "2s")
	t.Setenv("COMPOSE_UPDATER_HEALTH_TIMEOUT", "1s")
	t.Setenv("COMPOSE_UPDATER_HEALTH_POLL_INTERVAL", "5ms")
	t.Setenv("COMPOSE_UPDATER_STABLE_DURATION", "15ms")
	cfg, err := config.Load(configPath)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

func installFakeDocker(t *testing.T, cfg *config.Config, root string) (statePath, logPath string) {
	t.Helper()
	statePath = filepath.Join(root, "docker-state")
	logPath = filepath.Join(root, "docker-calls")
	scriptPath := filepath.Join(root, "docker")
	script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> ` + shellQuote(logPath) + `
state=` + shellQuote(statePath) + `
if [ "$1" = "version" ]; then echo 26.0.0; exit 0; fi
if [ "$1" = "compose" ] && [ "$2" = "version" ]; then echo "Docker Compose version v2.40.0"; exit 0; fi
if [ "$1" = "container" ] && [ "$2" = "inspect" ]; then
  case "$*" in
    *project.config_files*) exit 1 ;;
    *'{{.Image}}'*) cat "$state"; exit 0 ;;
    *'{{json .State}}'*) echo '{"Status":"running","Running":true,"Restarting":false,"ExitCode":0,"Health":{"Status":"healthy","FailingStreak":0}}'; exit 0 ;;
  esac
fi
if [ "$1" = "image" ] && [ "$2" = "inspect" ]; then echo sha256:new; exit 0; fi
if [ "$1" = "pull" ]; then echo pulled; exit 0; fi
if [ "$1" = "compose" ]; then
  compose_file=""
  project_name="test"
  previous=""
  for arg in "$@"; do
    if [ "$previous" = "-f" ]; then compose_file="$arg"; fi
    if [ "$previous" = "--project-name" ]; then project_name="$arg"; fi
    previous="$arg"
  done
  case "$*" in
    *' ls --all --format json'*)
      printf '%s\n' "${FAKE_COMPOSE_LS:-[]}"
      exit 0
      ;;
    *' config --format json'*)
      image=$(sed -n 's/^[[:space:]]*image:[[:space:]]*["'"'"']\{0,1\}\([^ "'"'"'#]*\).*/\1/p' "$compose_file" | head -1)
      printf '{"name":"%s","services":{"web":{"image":"%s"}}}\n' "$project_name" "$image"
      exit 0
      ;;
    *' ps -q web'*)
      if [ -n "${FAKE_REQUIRE_PROJECT_NAME:-}" ] && [ "$project_name" != "$FAKE_REQUIRE_PROJECT_NAME" ]; then exit 0; fi
      echo c1
      exit 0
      ;;
    *' pull '*) echo pulled; exit 0 ;;
    *' up -d '*) echo sha256:new > "$state"; echo updated; exit 0 ;;
  esac
fi
echo "unsupported fake docker call: $*" >&2
exit 2
`
	mustWriteFile(t, scriptPath, script, 0o755)
	t.Setenv("DOCKER_COMMAND", scriptPath)
	cfg.Docker.Command = scriptPath
	return statePath, logPath
}

func mustWriteFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func jsonQuote(value string) string {
	data, _ := json.Marshal(value)
	return string(data)
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
