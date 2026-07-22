package updater

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/guchengod/compose-updater/internal/compose"
	"github.com/guchengod/compose-updater/internal/composefile"
	"github.com/guchengod/compose-updater/internal/config"
	"github.com/guchengod/compose-updater/internal/dockercli"
	"github.com/guchengod/compose-updater/internal/execx"
	"github.com/guchengod/compose-updater/internal/notify"
	registryresolver "github.com/guchengod/compose-updater/internal/registry"
	"github.com/guchengod/compose-updater/internal/scanner"
)

type Updater struct {
	cfg      *config.Config
	logger   *slog.Logger
	runner   execx.Runner
	docker   *dockercli.Client
	resolver *registryresolver.Resolver
	notifier notify.Notifier
}

type RunSummary struct {
	Mode                   string          `json:"mode"`
	Node                   string          `json:"node"`
	StartedAt              time.Time       `json:"started_at"`
	FinishedAt             time.Time       `json:"finished_at"`
	ComposeFilesDiscovered int             `json:"compose_files_discovered"`
	ProjectsChecked        int             `json:"projects_checked"`
	ProjectsUpdated        int             `json:"projects_updated"`
	ProjectsAvailable      int             `json:"projects_available"`
	ProjectsFailed         int             `json:"projects_failed"`
	ServicesUpdated        int             `json:"services_updated"`
	Results                []ProjectResult `json:"results"`
}

type ProjectResult struct {
	ComposeFile    string          `json:"compose_file"`
	Project        string          `json:"project,omitempty"`
	Status         string          `json:"status"`
	VersionChanges []VersionChange `json:"version_changes,omitempty"`
	ServiceChanges []ServiceChange `json:"service_changes,omitempty"`
	Skipped        []ServiceSkip   `json:"skipped,omitempty"`
	Warnings       []string        `json:"warnings,omitempty"`
	BackupFile     string          `json:"backup_file,omitempty"`
	DurationMS     int64           `json:"duration_ms"`
	Error          string          `json:"error,omitempty"`
}

type VersionChange struct {
	Service  string `json:"service"`
	OldImage string `json:"old_image"`
	NewImage string `json:"new_image"`
	OldTag   string `json:"old_tag"`
	NewTag   string `json:"new_tag"`
}

type ServiceChange struct {
	Service     string   `json:"service"`
	Image       string   `json:"image"`
	OldImageIDs []string `json:"old_image_ids"`
	NewImageID  string   `json:"new_image_id"`
}

type ServiceSkip struct {
	Service string `json:"service"`
	Reason  string `json:"reason"`
}

type serviceSnapshot struct {
	Name            string
	Image           string
	ContainerIDs    []string
	RunningImageIDs []string
	TargetImageID   string
}

func New(cfg *config.Config, logger *slog.Logger, notifier notify.Notifier) *Updater {
	runner := execx.Runner{}
	return &Updater{
		cfg:      cfg,
		logger:   logger,
		runner:   runner,
		docker:   dockercli.NewClient(cfg.Docker, runner),
		resolver: registryresolver.New(cfg.RegistryTimeout(), logger, cfg.RegistryProxy),
		notifier: notifier,
	}
}

func (u *Updater) CheckPrerequisites(ctx context.Context) error {
	return u.docker.Check(ctx)
}

func (u *Updater) Discover() ([]string, error) {
	return scanner.Discover(u.cfg.Paths, u.cfg.Depth, u.cfg.SkipDirs)
}

func (u *Updater) Run(ctx context.Context, apply bool) RunSummary {
	u.resolver.ResetCache()
	mode := "check"
	if apply {
		mode = "update"
	}
	summary := RunSummary{Mode: mode, Node: u.cfg.NodeName, StartedAt: time.Now()}
	u.logger.Info("update_cycle_started", "mode", mode)
	files, err := u.Discover()
	if err != nil {
		summary.ProjectsFailed = 1
		summary.Results = append(summary.Results, ProjectResult{Status: "failed", Error: err.Error()})
		summary.FinishedAt = time.Now()
		u.logSummary(summary)
		u.notifySummary(ctx, summary)
		return summary
	}
	summary.ComposeFilesDiscovered = len(files)
	skippedFiles := u.selfComposeFiles(ctx)
	for _, composeFile := range files {
		if _, skip := skippedFiles[normalizePath(composeFile)]; skip {
			u.logger.Warn("self_compose_file_skipped", "compose_file", composeFile)
			summary.Results = append(summary.Results, ProjectResult{ComposeFile: composeFile, Status: "skipped", Warnings: []string{"检测为 compose-updater 自身 Compose 文件"}})
			continue
		}
		summary.ProjectsChecked++
		result := u.runProject(ctx, composeFile, apply)
		u.logger.Info("project_check_finished",
			"compose_file", result.ComposeFile,
			"project", result.Project,
			"status", result.Status,
			"services", changedServiceNames(result.ServiceChanges),
			"version_changes", len(result.VersionChanges),
			"duration_ms", result.DurationMS,
			"error", result.Error,
		)
		summary.Results = append(summary.Results, result)
		switch result.Status {
		case "updated", "config_updated":
			summary.ProjectsUpdated++
			summary.ServicesUpdated += len(result.ServiceChanges)
		case "available":
			summary.ProjectsAvailable++
		case "failed":
			summary.ProjectsFailed++
		}
		if ctx.Err() != nil {
			break
		}
	}
	summary.FinishedAt = time.Now()
	u.logSummary(summary)
	u.notifySummary(ctx, summary)
	return summary
}

func (u *Updater) logSummary(summary RunSummary) {
	u.logger.Info("update_cycle_finished",
		"mode", summary.Mode,
		"compose_files_discovered", summary.ComposeFilesDiscovered,
		"projects_checked", summary.ProjectsChecked,
		"projects_updated", summary.ProjectsUpdated,
		"projects_available", summary.ProjectsAvailable,
		"projects_failed", summary.ProjectsFailed,
		"services_updated", summary.ServicesUpdated,
		"duration_ms", summary.FinishedAt.Sub(summary.StartedAt).Milliseconds(),
	)
}

func changedServiceNames(changes []ServiceChange) []string {
	result := make([]string, 0, len(changes))
	for _, change := range changes {
		result = append(result, change.Service)
	}
	sort.Strings(result)
	return result
}

// NotifyFailure reports failures that happen outside an update cycle, such as
// Docker prerequisite or scheduler initialization errors.
func (u *Updater) NotifyFailure(mode string, err error) {
	if err == nil {
		return
	}
	now := time.Now()
	summary := RunSummary{
		Mode:           mode,
		Node:           u.cfg.NodeName,
		StartedAt:      now,
		FinishedAt:     now,
		ProjectsFailed: 1,
		Results:        []ProjectResult{{Status: "failed", Error: err.Error()}},
	}
	u.notifySummary(context.Background(), summary)
}

func (u *Updater) runProject(parent context.Context, composeFile string, apply bool) (result ProjectResult) {
	started := time.Now()
	result = ProjectResult{ComposeFile: composeFile, Status: "unchanged"}
	defer func() { result.DurationMS = time.Since(started).Milliseconds() }()
	log := u.logger.With("compose_file", composeFile)
	log.Info("project_check_started", "apply", apply)

	document, err := composefile.Load(composeFile)
	if err != nil {
		return fail(result, err)
	}
	composeClient := compose.NewClient(u.cfg.Docker, composeFile, u.runner)
	resolveCtx, resolveCancel := context.WithTimeout(parent, u.cfg.ConfigTimeout())
	projectName, _, resolveErr := composeClient.ResolveProjectName(resolveCtx)
	resolveCancel()
	if resolveErr != nil {
		log.Warn("compose_project_resolve_failed", "error", resolveErr)
	} else if projectName != "" {
		log.Info("compose_project_resolved", "project", projectName)
	}
	configCtx, cancel := context.WithTimeout(parent, u.cfg.ConfigTimeout())
	model, _, err := composeClient.ParseConfig(configCtx)
	cancel()
	if err != nil {
		return fail(result, fmt.Errorf("解析 Compose 配置: %w", err))
	}
	result.Project = model.Name

	snapshots := make(map[string]*serviceSnapshot)
	activeNames := make([]string, 0)
	for _, serviceName := range model.SortedServiceNames() {
		service := model.Services[serviceName]
		switch {
		case strings.TrimSpace(service.Image) == "":
			result.Skipped = append(result.Skipped, ServiceSkip{Service: serviceName, Reason: "未声明 image"})
			continue
		case service.HasBuild():
			result.Skipped = append(result.Skipped, ServiceSkip{Service: serviceName, Reason: "包含 build，本地构建服务不自动更新"})
			continue
		}
		inspectCtx, inspectCancel := context.WithTimeout(parent, u.cfg.ConfigTimeout())
		containerIDs, _, err := composeClient.ContainerIDs(inspectCtx, serviceName)
		inspectCancel()
		if err != nil {
			return fail(result, fmt.Errorf("获取服务 %s 容器: %w", serviceName, err))
		}
		if len(containerIDs) == 0 {
			result.Skipped = append(result.Skipped, ServiceSkip{Service: serviceName, Reason: "服务当前未运行，不自动启动"})
			continue
		}
		snapshot := &serviceSnapshot{Name: serviceName, Image: service.Image, ContainerIDs: containerIDs}
		for _, containerID := range containerIDs {
			imageCtx, imageCancel := context.WithTimeout(parent, u.cfg.ConfigTimeout())
			imageID, err := u.docker.ContainerImageID(imageCtx, containerID)
			imageCancel()
			if err != nil {
				return fail(result, fmt.Errorf("读取服务 %s 运行镜像 ID: %w", serviceName, err))
			}
			snapshot.RunningImageIDs = appendUnique(snapshot.RunningImageIDs, imageID)
		}
		snapshots[serviceName] = snapshot
		activeNames = append(activeNames, serviceName)
	}
	if len(activeNames) == 0 {
		result.Status = "skipped"
		log.Info("project_has_no_running_image_services")
		return result
	}
	sort.Strings(activeNames)

	for _, serviceName := range activeNames {
		snapshot := snapshots[serviceName]
		literal, ok := document.Images[serviceName]
		if !ok || literal.HasBuild {
			if tag := imageTag(snapshot.Image); tag != "" && !strings.EqualFold(tag, "latest") {
				result.Warnings = append(result.Warnings, fmt.Sprintf("服务 %s 的 image 不是主 Compose 文件中的可直接修改标量，无法查询并回写目标标签", serviceName))
			}
			continue
		}
		decision, resolveErr := u.resolver.Resolve(parent, literal.Image, u.cfg.StableOnly)
		if resolveErr != nil {
			log.Warn("registry_version_resolve_failed", "service", serviceName, "image", literal.Image, "error", resolveErr)
			return fail(result, fmt.Errorf("服务 %s 查询目标版本: %w", serviceName, resolveErr))
		}
		if !decision.Changed {
			continue
		}
		result.VersionChanges = append(result.VersionChanges, VersionChange{
			Service:  serviceName,
			OldImage: literal.Image,
			NewImage: decision.Image,
			OldTag:   decision.CurrentTag,
			NewTag:   decision.LatestTag,
		})
		snapshot.Image = decision.Image
	}

	var restore func() error
	if apply && len(result.VersionChanges) > 0 {
		changes := make([]composefile.Change, 0, len(result.VersionChanges))
		for _, change := range result.VersionChanges {
			changes = append(changes, composefile.Change{Service: change.Service, OldImage: change.OldImage, NewImage: change.NewImage})
		}
		backup, restoreFn, err := document.Rewrite(changes)
		if err != nil {
			return fail(result, fmt.Errorf("修改 Compose 版本号: %w", err))
		}
		result.BackupFile = backup
		restore = restoreFn
		validationCtx, validationCancel := context.WithTimeout(parent, u.cfg.ConfigTimeout())
		updatedModel, _, validationErr := composeClient.ParseConfig(validationCtx)
		validationCancel()
		if validationErr != nil {
			restoreErr := restore()
			if restoreErr != nil {
				return fail(result, fmt.Errorf("修改后的 Compose 校验失败: %v；恢复原文件失败: %v", validationErr, restoreErr))
			}
			return fail(result, fmt.Errorf("修改后的 Compose 校验失败，已恢复原文件: %w", validationErr))
		}
		for name, snapshot := range snapshots {
			if updatedService, ok := updatedModel.Services[name]; ok && strings.TrimSpace(updatedService.Image) != "" {
				snapshot.Image = updatedService.Image
			}
		}
	}

	restoreOnFailure := func(cause error, rollbackServices []string) ProjectResult {
		messages := []string{cause.Error()}
		if restore != nil {
			if err := restore(); err != nil {
				messages = append(messages, "恢复 Compose 文件失败: "+err.Error())
			} else if len(rollbackServices) > 0 {
				rollbackCtx, rollbackCancel := context.WithTimeout(context.Background(), u.cfg.UpdateTimeout())
				_, rollbackErr := composeClient.Up(rollbackCtx, rollbackServices, u.cfg.HealthTimeout())
				rollbackCancel()
				if rollbackErr != nil {
					messages = append(messages, "恢复旧版本容器失败: "+rollbackErr.Error())
				}
			}
		}
		result.Status = "failed"
		result.Error = strings.Join(messages, "；")
		return result
	}

	if apply {
		pullCtx, pullCancel := context.WithTimeout(parent, u.cfg.PullTimeout())
		_, err = composeClient.Pull(pullCtx, activeNames)
		pullCancel()
		if err != nil {
			return restoreOnFailure(fmt.Errorf("拉取 Compose 镜像: %w", err), nil)
		}
	} else {
		images := uniqueImages(activeNames, snapshots)
		for _, image := range images {
			pullCtx, pullCancel := context.WithTimeout(parent, u.cfg.PullTimeout())
			_, err = u.docker.PullImage(pullCtx, image)
			pullCancel()
			if err != nil {
				return restoreOnFailure(fmt.Errorf("拉取镜像 %s: %w", image, err), nil)
			}
		}
	}

	changedNames := make([]string, 0)
	for _, serviceName := range activeNames {
		snapshot := snapshots[serviceName]
		imageCtx, imageCancel := context.WithTimeout(parent, u.cfg.ConfigTimeout())
		targetID, err := u.docker.ImageID(imageCtx, snapshot.Image)
		imageCancel()
		if err != nil {
			return restoreOnFailure(fmt.Errorf("读取服务 %s 目标镜像 %q: %w", serviceName, snapshot.Image, err), nil)
		}
		snapshot.TargetImageID = targetID
		if !allEqual(snapshot.RunningImageIDs, targetID) {
			changedNames = append(changedNames, serviceName)
			result.ServiceChanges = append(result.ServiceChanges, ServiceChange{
				Service:     serviceName,
				Image:       snapshot.Image,
				OldImageIDs: append([]string(nil), snapshot.RunningImageIDs...),
				NewImageID:  targetID,
			})
		}
	}

	if !apply {
		if len(result.VersionChanges) > 0 || len(changedNames) > 0 {
			result.Status = "available"
		} else {
			result.Status = "unchanged"
		}
		return result
	}
	if len(changedNames) == 0 {
		if len(result.VersionChanges) > 0 {
			result.Status = "config_updated"
			log.Info("compose_version_updated_same_digest", "version_changes", result.VersionChanges)
		} else {
			result.Status = "unchanged"
			log.Info("project_images_unchanged")
		}
		return result
	}
	sort.Strings(changedNames)
	updateCtx, updateCancel := context.WithTimeout(parent, u.cfg.UpdateTimeout())
	_, err = composeClient.Up(updateCtx, changedNames, u.cfg.HealthTimeout())
	updateCancel()
	if err != nil {
		return restoreOnFailure(fmt.Errorf("重建服务失败: %w", err), changedNames)
	}
	if err := u.verifyServices(parent, composeClient, changedNames); err != nil {
		return restoreOnFailure(fmt.Errorf("健康检查失败: %w", err), changedNames)
	}
	result.Status = "updated"
	log.Info("project_updated", "services", changedNames, "version_changes", result.VersionChanges)
	return result
}

func (u *Updater) verifyServices(parent context.Context, composeClient *compose.Client, services []string) error {
	ctx, cancel := context.WithTimeout(parent, u.cfg.HealthTimeout())
	defer cancel()
	ticker := time.NewTicker(u.cfg.HealthPollInterval())
	defer ticker.Stop()
	var readySince time.Time
	var lastDetail string
	for {
		ready, detail, err := u.servicesReady(ctx, composeClient, services)
		if err != nil {
			return err
		}
		lastDetail = detail
		if ready {
			if readySince.IsZero() {
				readySince = time.Now()
			}
			if time.Since(readySince) >= u.cfg.StableDuration() {
				return nil
			}
		} else {
			readySince = time.Time{}
			u.logger.Debug("services_not_ready", "detail", detail)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待 running/healthy 超时: %s", lastDetail)
		case <-ticker.C:
		}
	}
}

func (u *Updater) servicesReady(ctx context.Context, composeClient *compose.Client, services []string) (bool, string, error) {
	for _, service := range services {
		ids, _, err := composeClient.ContainerIDs(ctx, service)
		if err != nil {
			return false, "", err
		}
		if len(ids) == 0 {
			return false, "服务没有运行容器: " + service, nil
		}
		for _, id := range ids {
			state, err := u.docker.ContainerState(ctx, id)
			if err != nil {
				return false, "", err
			}
			if !state.Running || state.Restarting || state.Status != "running" {
				return false, fmt.Sprintf("容器 %s 状态=%s restarting=%v", id, state.Status, state.Restarting), nil
			}
			if state.Health != nil && state.Health.Status != "healthy" {
				return false, fmt.Sprintf("容器 %s health=%s", id, state.Health.Status), nil
			}
		}
	}
	return true, "ready", nil
}

func (u *Updater) selfComposeFiles(ctx context.Context) map[string]struct{} {
	result := make(map[string]struct{})
	for _, path := range filepath.SplitList(os.Getenv("COMPOSE_UPDATER_SKIP_FILES")) {
		if strings.TrimSpace(path) != "" {
			result[normalizePath(path)] = struct{}{}
		}
	}
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		return result
	}
	inspectCtx, cancel := context.WithTimeout(ctx, u.cfg.ConfigTimeout())
	commandResult, err := u.runner.Run(inspectCtx, "", u.cfg.Docker.Env, u.cfg.Docker.Command,
		"container", "inspect", "--format", `{{ index .Config.Labels "com.docker.compose.project.config_files" }}`, hostname)
	cancel()
	if err != nil {
		return result
	}
	for _, path := range strings.Split(commandResult.Stdout, ",") {
		if strings.TrimSpace(path) != "" && strings.TrimSpace(path) != "<no value>" {
			result[normalizePath(path)] = struct{}{}
		}
	}
	return result
}

func (u *Updater) notifySummary(ctx context.Context, summary RunSummary) {
	if !u.notifier.Enabled() {
		return
	}
	var title, level string
	switch {
	case summary.ProjectsFailed > 0:
		title = "Docker 更新失败"
		level = u.cfg.Bark.FailureLevel()
	case summary.Mode == "check" && summary.ProjectsAvailable > 0:
		title = "Docker 有可用更新"
		level = u.cfg.Bark.AvailableLevel()
	case summary.Mode == "update" && summary.ProjectsUpdated > 0:
		title = "Docker 更新成功"
		level = u.cfg.Bark.SuccessLevel()
	default:
		return
	}
	body := summaryBody(summary)
	notifyCtx, cancel := context.WithTimeout(context.Background(), u.cfg.Bark.RequestTimeout())
	defer cancel()
	if err := u.notifier.Send(notifyCtx, title, body, level); err != nil {
		u.logger.Error("bark_notification_failed", "error", err)
	}
}

func summaryBody(summary RunSummary) string {
	lines := []string{fmt.Sprintf("主机：%s", summary.Node), fmt.Sprintf("模式：%s", summary.Mode)}
	for _, result := range summary.Results {
		name := result.Project
		if name == "" && result.ComposeFile != "" {
			name = filepath.Base(filepath.Dir(result.ComposeFile))
		}
		if result.Error != "" {
			lines = append(lines, fmt.Sprintf("[%s] 失败：%s", name, result.Error))
			continue
		}
		for _, change := range result.VersionChanges {
			lines = append(lines, fmt.Sprintf("[%s/%s] %s → %s", name, change.Service, change.OldTag, change.NewTag))
		}
		if len(result.ServiceChanges) > 0 {
			services := make([]string, 0, len(result.ServiceChanges))
			for _, change := range result.ServiceChanges {
				services = append(services, change.Service)
			}
			verb := "可更新"
			if summary.Mode == "update" {
				verb = "已更新"
			}
			lines = append(lines, fmt.Sprintf("[%s] %s：%s", name, verb, strings.Join(services, ", ")))
		} else if result.Status == "config_updated" {
			lines = append(lines, fmt.Sprintf("[%s] Compose 版本号已更新，镜像摘要未变化", name))
		}
	}
	return strings.Join(lines, "\n")
}

func fail(result ProjectResult, err error) ProjectResult {
	result.Status = "failed"
	result.Error = err.Error()
	return result
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func allEqual(values []string, target string) bool {
	if len(values) == 0 {
		return false
	}
	for _, value := range values {
		if value != target {
			return false
		}
	}
	return true
}

func uniqueImages(serviceNames []string, snapshots map[string]*serviceSnapshot) []string {
	seen := make(map[string]struct{})
	images := make([]string, 0, len(serviceNames))
	for _, name := range serviceNames {
		image := snapshots[name].Image
		if _, ok := seen[image]; ok {
			continue
		}
		seen[image] = struct{}{}
		images = append(images, image)
	}
	sort.Strings(images)
	return images
}

func normalizePath(path string) string {
	absolute, err := filepath.Abs(path)
	if err == nil {
		path = absolute
	}
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}

func imageTag(image string) string {
	if strings.Contains(image, "@") {
		return ""
	}
	lastSlash := strings.LastIndex(image, "/")
	lastColon := strings.LastIndex(image, ":")
	if lastColon > lastSlash {
		return image[lastColon+1:]
	}
	return "latest"
}

func HasFailures(summary RunSummary) bool { return summary.ProjectsFailed > 0 }
