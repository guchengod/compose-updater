package fnos

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeStoreBuildsCompletedRunFromUpdaterLogs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	store := NewRuntimeStore(path, slog.New(slog.NewTextHandler(io.Discard, nil)))
	lines := []string{
		`{"time":"2026-07-22T14:35:52+08:00","level":"INFO","msg":"scheduler_started","schedule":"0 4 * * *","timezone":"Asia/Shanghai"}`,
		`{"time":"2026-07-22T14:35:53+08:00","level":"INFO","msg":"update_cycle_started","mode":"update"}`,
		`{"time":"2026-07-22T14:35:54+08:00","level":"INFO","msg":"project_check_started","compose_file":"/home/gitea/docker-compose.yml"}`,
		`{"time":"2026-07-22T14:35:56+08:00","level":"INFO","msg":"project_check_finished","compose_file":"/home/gitea/docker-compose.yml","project":"gitea","status":"unchanged","duration_ms":2000}`,
		`{"time":"2026-07-22T14:35:57+08:00","level":"INFO","msg":"update_cycle_finished","mode":"update","compose_files_discovered":1,"projects_checked":1,"projects_updated":0,"projects_failed":0,"services_updated":0,"duration_ms":4000}`,
	}
	for _, line := range lines {
		if _, err := store.Write([]byte(line + "\n")); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := store.Snapshot()
	if !snapshot.Scheduler.Started || snapshot.Scheduler.Schedule != "0 4 * * *" {
		t.Fatalf("scheduler = %+v", snapshot.Scheduler)
	}
	if len(snapshot.Runs) != 1 || snapshot.Runs[0].Status != "success" {
		t.Fatalf("runs = %+v", snapshot.Runs)
	}
	run := snapshot.Runs[0]
	if run.ProjectsChecked != 1 || len(run.Projects) != 1 || run.Projects[0].Status != "unchanged" {
		t.Fatalf("run = %+v", run)
	}
	reloaded := NewRuntimeStore(path, slog.New(slog.NewTextHandler(io.Discard, nil))).Snapshot()
	if len(reloaded.Runs) != 1 || reloaded.Runs[0].ID != run.ID {
		t.Fatalf("reloaded = %+v", reloaded)
	}
}

func TestRuntimeStoreRetainsPartialJSONWrites(t *testing.T) {
	store := NewRuntimeStore("", slog.New(slog.NewTextHandler(io.Discard, nil)))
	line := `{"time":"2026-07-22T14:35:53+08:00","level":"INFO","msg":"update_cycle_started","mode":"update"}`
	cut := len(line) / 2
	_, _ = store.Write([]byte(line[:cut]))
	_, _ = store.Write([]byte(line[cut:] + "\n"))
	if len(store.Snapshot().Runs) != 1 {
		t.Fatal("split log line was not parsed")
	}
}

func TestRuntimeStoreMarksPersistedRunningCycleInterrupted(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	content := `{"runs":[{"id":"stale","mode":"update","status":"running","started_at":"2026-07-22T14:35:53+08:00","projects":[],"events":[],"raw":[]}]}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	store := NewRuntimeStore(path, slog.New(slog.NewTextHandler(io.Discard, nil)))
	run := store.Snapshot().Runs[0]
	if run.Status != "failed" || run.FinishedAt.IsZero() || len(run.Events) != 1 {
		t.Fatalf("run = %+v", run)
	}
}

func TestProxyTestMessageTreatsUnauthorizedAsExpectedConnectivity(t *testing.T) {
	message, authRequired := proxyTestMessage(401)
	if !authRequired || !strings.Contains(message, "代理连接正常") || !strings.Contains(message, "预期响应") {
		t.Fatalf("message = %q authRequired = %v", message, authRequired)
	}
}
