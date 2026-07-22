package fnos

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/guchengod/compose-updater/internal/atomicfile"
)

const (
	maxRuntimeRuns = 30
	maxRawEvents   = 180
)

type RuntimeStore struct {
	path   string
	logger *slog.Logger

	mu       sync.RWMutex
	buffer   []byte
	snapshot RuntimeSnapshot
}

type RuntimeSnapshot struct {
	UpdatedAt time.Time        `json:"updated_at"`
	Scheduler RuntimeScheduler `json:"scheduler"`
	Runs      []RuntimeRun     `json:"runs"`
}

type RuntimeScheduler struct {
	Started   bool      `json:"started"`
	Schedule  string    `json:"schedule,omitempty"`
	Timezone  string    `json:"timezone,omitempty"`
	NextRun   time.Time `json:"next_run,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
}

type RuntimeRun struct {
	ID                     string           `json:"id"`
	Mode                   string           `json:"mode"`
	Status                 string           `json:"status"`
	StartedAt              time.Time        `json:"started_at"`
	FinishedAt             time.Time        `json:"finished_at,omitempty"`
	ComposeFilesDiscovered int              `json:"compose_files_discovered"`
	ProjectsChecked        int              `json:"projects_checked"`
	ProjectsUpdated        int              `json:"projects_updated"`
	ProjectsAvailable      int              `json:"projects_available"`
	ProjectsFailed         int              `json:"projects_failed"`
	ServicesUpdated        int              `json:"services_updated"`
	DurationMS             int64            `json:"duration_ms"`
	Projects               []RuntimeProject `json:"projects"`
	Events                 []RuntimeEvent   `json:"events"`
	Raw                    []string         `json:"raw"`
}

type RuntimeProject struct {
	ComposeFile    string   `json:"compose_file"`
	Project        string   `json:"project,omitempty"`
	Status         string   `json:"status"`
	Services       []string `json:"services,omitempty"`
	VersionChanges int      `json:"version_changes,omitempty"`
	DurationMS     int64    `json:"duration_ms,omitempty"`
	Error          string   `json:"error,omitempty"`
}

type RuntimeEvent struct {
	Time        time.Time `json:"time"`
	Kind        string    `json:"kind"`
	Status      string    `json:"status"`
	Title       string    `json:"title"`
	Detail      string    `json:"detail,omitempty"`
	ComposeFile string    `json:"compose_file,omitempty"`
}

func NewRuntimeStore(path string, logger *slog.Logger) *RuntimeStore {
	store := &RuntimeStore{path: path, logger: logger}
	if content, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(content, &store.snapshot); err != nil {
			logger.Warn("runtime_state_load_failed", "path", path, "error", err)
		} else if len(store.snapshot.Runs) > 0 && store.snapshot.Runs[0].Status == "running" {
			now := time.Now()
			store.snapshot.Runs[0].Status = "failed"
			store.snapshot.Runs[0].FinishedAt = now
			store.snapshot.Runs[0].Events = append(store.snapshot.Runs[0].Events, RuntimeEvent{Time: now, Kind: "error", Status: "failed", Title: "运行被中断", Detail: "管理服务重启时检测到未完成的运行记录"})
			store.snapshot.UpdatedAt = now
			store.persistLocked()
		}
	} else if !os.IsNotExist(err) {
		logger.Warn("runtime_state_load_failed", "path", path, "error", err)
	}
	return store
}

func (s *RuntimeStore) Snapshot() RuntimeSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	content, _ := json.Marshal(s.snapshot)
	var result RuntimeSnapshot
	_ = json.Unmarshal(content, &result)
	return result
}

func (s *RuntimeStore) Active() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.snapshot.Runs) > 0 && s.snapshot.Runs[0].Status == "running"
}

func (s *RuntimeStore) Write(content []byte) (int, error) {
	s.mu.Lock()
	s.buffer = append(s.buffer, content...)
	for {
		index := bytes.IndexByte(s.buffer, '\n')
		if index < 0 {
			break
		}
		line := strings.TrimSpace(string(s.buffer[:index]))
		s.buffer = s.buffer[index+1:]
		if line != "" {
			s.consumeLocked(line)
		}
	}
	s.mu.Unlock()
	return len(content), nil
}

func (s *RuntimeStore) consumeLocked(line string) {
	var event map[string]any
	if err := json.Unmarshal([]byte(line), &event); err != nil {
		return
	}
	message := stringValue(event["msg"])
	if message == "" {
		return
	}
	now := timeValue(event["time"])
	if now.IsZero() {
		now = time.Now()
	}
	s.snapshot.UpdatedAt = now

	switch message {
	case "scheduler_started":
		s.snapshot.Scheduler = RuntimeScheduler{
			Started: true, Schedule: stringValue(event["schedule"]), Timezone: stringValue(event["timezone"]), StartedAt: now,
		}
	case "next_run_scheduled":
		s.snapshot.Scheduler.Started = true
		s.snapshot.Scheduler.NextRun = timeValue(event["next_run"])
	case "update_cycle_started":
		if len(s.snapshot.Runs) > 0 && s.snapshot.Runs[0].Status == "running" {
			s.snapshot.Runs[0].Status = "failed"
			s.snapshot.Runs[0].FinishedAt = now
			s.snapshot.Runs[0].Events = append(s.snapshot.Runs[0].Events, RuntimeEvent{Time: now, Kind: "error", Status: "failed", Title: "上一轮运行被中断", Detail: "新的更新周期已开始"})
		}
		run := RuntimeRun{
			ID: fmt.Sprintf("%d", now.UnixNano()), Mode: defaultString(stringValue(event["mode"]), "update"), Status: "running", StartedAt: now,
			Events: []RuntimeEvent{{Time: now, Kind: "cycle", Status: "running", Title: "开始更新检查", Detail: "正在扫描 Compose 项目"}},
		}
		s.snapshot.Runs = append([]RuntimeRun{run}, s.snapshot.Runs...)
		if len(s.snapshot.Runs) > maxRuntimeRuns {
			s.snapshot.Runs = s.snapshot.Runs[:maxRuntimeRuns]
		}
	case "project_check_started":
		run := s.activeRunLocked(now)
		composeFile := stringValue(event["compose_file"])
		run.Projects = append(run.Projects, RuntimeProject{ComposeFile: composeFile, Project: projectName(composeFile), Status: "running"})
		run.Events = append(run.Events, RuntimeEvent{Time: now, Kind: "project", Status: "running", Title: projectName(composeFile), Detail: "正在检查运行镜像", ComposeFile: composeFile})
	case "project_check_finished":
		run := s.activeRunLocked(now)
		composeFile := stringValue(event["compose_file"])
		project := s.projectLocked(run, composeFile)
		project.Project = defaultString(stringValue(event["project"]), projectName(composeFile))
		project.Status = defaultString(stringValue(event["status"]), "unknown")
		project.Services = stringSlice(event["services"])
		project.VersionChanges = intValue(event["version_changes"])
		project.DurationMS = int64Value(event["duration_ms"])
		project.Error = stringValue(event["error"])
		for index := len(run.Events) - 1; index >= 0; index-- {
			if run.Events[index].ComposeFile == composeFile && run.Events[index].Status == "running" {
				run.Events[index].Time = now
				run.Events[index].Status = project.Status
				run.Events[index].Title = project.Project
				run.Events[index].Detail = projectDetail(*project)
				break
			}
		}
	case "self_compose_file_skipped":
		run := s.activeRunLocked(now)
		composeFile := stringValue(event["compose_file"])
		run.Events = append(run.Events, RuntimeEvent{Time: now, Kind: "project", Status: "skipped", Title: projectName(composeFile), Detail: "检测为 Compose Updater 自身项目，已跳过", ComposeFile: composeFile})
	case "update_cycle_finished":
		run := s.activeRunLocked(now)
		run.Status = "success"
		run.FinishedAt = now
		run.Mode = defaultString(stringValue(event["mode"]), run.Mode)
		run.ComposeFilesDiscovered = intValue(event["compose_files_discovered"])
		run.ProjectsChecked = intValue(event["projects_checked"])
		run.ProjectsUpdated = intValue(event["projects_updated"])
		run.ProjectsAvailable = intValue(event["projects_available"])
		run.ProjectsFailed = intValue(event["projects_failed"])
		run.ServicesUpdated = intValue(event["services_updated"])
		run.DurationMS = int64Value(event["duration_ms"])
		if run.ProjectsFailed > 0 {
			run.Status = "failed"
		}
		run.Events = append(run.Events, RuntimeEvent{Time: now, Kind: "cycle", Status: run.Status, Title: cycleFinishedTitle(*run), Detail: cycleFinishedDetail(*run)})
	default:
		if len(s.snapshot.Runs) > 0 && s.snapshot.Runs[0].Status == "running" {
			run := &s.snapshot.Runs[0]
			run.Raw = append(run.Raw, line)
			if len(run.Raw) > maxRawEvents {
				run.Raw = run.Raw[len(run.Raw)-maxRawEvents:]
			}
			if strings.EqualFold(stringValue(event["level"]), "ERROR") {
				run.Events = append(run.Events, RuntimeEvent{Time: now, Kind: "error", Status: "failed", Title: "运行错误", Detail: stringValue(event["error"]), ComposeFile: stringValue(event["compose_file"])})
			}
		}
	}
	s.persistLocked()
}

func (s *RuntimeStore) activeRunLocked(now time.Time) *RuntimeRun {
	if len(s.snapshot.Runs) == 0 || s.snapshot.Runs[0].Status != "running" {
		run := RuntimeRun{ID: fmt.Sprintf("%d", now.UnixNano()), Mode: "update", Status: "running", StartedAt: now}
		s.snapshot.Runs = append([]RuntimeRun{run}, s.snapshot.Runs...)
	}
	return &s.snapshot.Runs[0]
}

func (s *RuntimeStore) projectLocked(run *RuntimeRun, composeFile string) *RuntimeProject {
	for index := len(run.Projects) - 1; index >= 0; index-- {
		if run.Projects[index].ComposeFile == composeFile {
			return &run.Projects[index]
		}
	}
	run.Projects = append(run.Projects, RuntimeProject{ComposeFile: composeFile, Project: projectName(composeFile), Status: "running"})
	return &run.Projects[len(run.Projects)-1]
}

func (s *RuntimeStore) persistLocked() {
	if strings.TrimSpace(s.path) == "" {
		return
	}
	content, err := json.MarshalIndent(s.snapshot, "", "  ")
	if err != nil {
		return
	}
	if err := atomicfile.Write(s.path, append(content, '\n'), 0o600); err != nil {
		s.logger.Warn("runtime_state_save_failed", "path", s.path, "error", err)
	}
}

func projectName(composeFile string) string {
	directory := filepath.Base(filepath.Dir(composeFile))
	if directory == "." || directory == string(filepath.Separator) || directory == "" {
		return filepath.Base(composeFile)
	}
	return directory
}

func projectDetail(project RuntimeProject) string {
	switch project.Status {
	case "updated", "config_updated":
		if len(project.Services) > 0 {
			return "已更新服务：" + strings.Join(project.Services, "、")
		}
		return "Compose 配置已更新"
	case "unchanged":
		return "运行镜像已是最新版本"
	case "skipped":
		return "没有符合自动更新条件的运行服务"
	case "failed":
		return defaultString(project.Error, "项目检查失败")
	case "available":
		return "检测到可用更新"
	default:
		return "项目检查完成"
	}
}

func cycleFinishedTitle(run RuntimeRun) string {
	if run.Status == "failed" {
		return "更新检查完成，存在失败"
	}
	if run.ProjectsUpdated > 0 {
		return "更新完成"
	}
	return "检查完成，所有镜像均为最新"
}

func cycleFinishedDetail(run RuntimeRun) string {
	return fmt.Sprintf("检查 %d 个项目，更新 %d 个服务，失败 %d 个", run.ProjectsChecked, run.ServicesUpdated, run.ProjectsFailed)
}

func stringValue(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	return fmt.Sprint(value)
}

func intValue(value any) int { return int(int64Value(value)) }

func int64Value(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case int:
		return int64(typed)
	default:
		return 0
	}
}

func timeValue(value any) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, stringValue(value))
	return parsed
}

func stringSlice(value any) []string {
	values, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, item := range values {
		if text := stringValue(item); text != "" {
			result = append(result, text)
		}
	}
	sort.Strings(result)
	return result
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
