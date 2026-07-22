package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/guchengod/compose-updater/internal/composefile"
	"github.com/guchengod/compose-updater/internal/config"
	"github.com/guchengod/compose-updater/internal/cronexpr"
	"github.com/guchengod/compose-updater/internal/fnos"
	filelock "github.com/guchengod/compose-updater/internal/lock"
	"github.com/guchengod/compose-updater/internal/notify"
	"github.com/guchengod/compose-updater/internal/platform"
	"github.com/guchengod/compose-updater/internal/updater"
)

var (
	version   = "dev"
	commit    = "none"
	buildDate = "unknown"
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		printUsage(os.Stderr)
		return 2
	}
	command := args[0]
	if command == "version" || command == "--version" || command == "-version" {
		fmt.Printf("compose-updater %s commit=%s build_date=%s\n", version, commit, buildDate)
		return 0
	}
	if command == "help" || command == "--help" || command == "-h" {
		printUsage(os.Stdout)
		return 0
	}

	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	configPath := fs.String("config", envOr("COMPOSE_UPDATER_CONFIG", "config.json"), "配置文件路径")
	forceRunOnStart := fs.Bool("run-on-start", false, "serve 启动后强制立即运行一次")
	webListen := fs.String("listen", envOr("COMPOSE_UPDATER_WEB_LISTEN", "127.0.0.1:8080"), "web 监听地址")
	webUsername := fs.String("web-username", envOr("COMPOSE_UPDATER_WEB_USERNAME", "admin"), "web 登录用户名")
	webPassword := fs.String("web-password", envOr("COMPOSE_UPDATER_WEB_PASSWORD", ""), "web 登录密码（推荐使用环境变量）")
	runtimeState := fs.String("runtime-state", envOr("COMPOSE_UPDATER_RUNTIME_STATE", ""), "运行记录文件路径")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	cfg, err := config.Load(*configPath)
	if err != nil {
		bootstrapLogger().Error("config_load_failed", "error", err, "path", *configPath)
		return 1
	}
	logger := newLogger(cfg.LogLevel).With("version", version, "node", cfg.NodeName)
	slog.SetDefault(logger)
	if command == "web" {
		if strings.TrimSpace(*runtimeState) == "" {
			*runtimeState = filepath.Join(filepath.Dir(cfg.LockFile), "runtime.json")
		}
		return runWeb(*configPath, cfg, logger, *webListen, *webUsername, *webPassword, *runtimeState)
	}
	notifier, err := buildNotifier(cfg)
	if err != nil {
		logger.Error("notifier_init_failed", "error", err)
		return 1
	}
	engine := updater.New(cfg, logger, notifier)

	switch command {
	case "validate":
		return validate(engine, logger)
	case "scan":
		files, err := engine.Discover()
		if err != nil {
			logger.Error("scan_failed", "error", err)
			return 1
		}
		printJSON(files)
		return 0
	case "check":
		return runOnce(cfg, engine, logger, false)
	case "run":
		return runOnce(cfg, engine, logger, true)
	case "serve":
		return serve(cfg, engine, logger, *forceRunOnStart)
	default:
		logger.Error("unknown_command", "command", command)
		printUsage(os.Stderr)
		return 2
	}
}

func runWeb(configPath string, cfg *config.Config, logger *slog.Logger, listen, username, password, runtimePath string) int {
	executable, err := os.Executable()
	if err != nil {
		logger.Error("web_executable_lookup_failed", "error", err)
		return 1
	}
	ctx, stop := platform.NotifyContext(context.Background())
	defer stop()
	runtimeStore := fnos.NewRuntimeStore(runtimePath, logger)
	supervisor := fnos.NewSupervisor(executable, configPath, logger, runtimeStore)
	go supervisor.Run(ctx)
	defaultPath := filepath.Dir(configPath)
	if len(cfg.Paths) > 0 {
		defaultPath = cfg.Paths[0]
	}
	server := fnos.NewServer(fnos.ServerOptions{
		ConfigPath: configPath, DefaultPath: defaultPath, Version: version,
		AuthorizedPaths: cfg.Paths, Supervisor: supervisor, Logger: logger,
	})
	logger.Info("web_started", "listen", listen, "runtime_state", runtimePath)
	if err := server.ServeStandalone(ctx, listen, username, password); err != nil {
		logger.Error("web_failed", "error", err)
		return 1
	}
	return 0
}

func validate(engine *updater.Updater, logger *slog.Logger) int {
	files, err := engine.Discover()
	if err != nil {
		logger.Error("scan_failed", "error", err)
		return 1
	}
	for _, file := range files {
		if _, err := composefile.Load(file); err != nil {
			logger.Error("compose_yaml_invalid", "compose_file", file, "error", err)
			return 1
		}
	}
	logger.Info("config_valid", "compose_files", len(files))
	printJSON(map[string]any{"valid": true, "compose_files": files})
	return 0
}

func runOnce(cfg *config.Config, engine *updater.Updater, logger *slog.Logger, apply bool) int {
	lock, err := filelock.Acquire(cfg.LockFile)
	if err != nil {
		if errors.Is(err, filelock.ErrAlreadyLocked) {
			logger.Warn("instance_already_running", "lock_file", cfg.LockFile)
			return 3
		}
		logger.Error("lock_acquire_failed", "error", err)
		engine.NotifyFailure(runMode(apply), fmt.Errorf("获取运行锁: %w", err))
		return 1
	}
	defer lock.Close()
	ctx, stop := platform.NotifyContext(context.Background())
	defer stop()
	checkCtx, cancel := context.WithTimeout(ctx, cfg.ConfigTimeout())
	err = engine.CheckPrerequisites(checkCtx)
	cancel()
	if err != nil {
		logger.Error("prerequisite_check_failed", "error", err)
		engine.NotifyFailure(runMode(apply), fmt.Errorf("运行前检查: %w", err))
		return 1
	}
	summary := engine.Run(ctx, apply)
	printJSON(summary)
	if updater.HasFailures(summary) {
		return 1
	}
	return 0
}

func serve(cfg *config.Config, engine *updater.Updater, logger *slog.Logger, forceRunOnStart bool) int {
	lock, err := filelock.Acquire(cfg.LockFile)
	if err != nil {
		if errors.Is(err, filelock.ErrAlreadyLocked) {
			logger.Error("instance_already_running", "lock_file", cfg.LockFile)
			return 3
		}
		logger.Error("lock_acquire_failed", "error", err)
		engine.NotifyFailure("update", fmt.Errorf("获取运行锁: %w", err))
		return 1
	}
	defer lock.Close()
	ctx, stop := platform.NotifyContext(context.Background())
	defer stop()
	checkCtx, cancel := context.WithTimeout(ctx, cfg.ConfigTimeout())
	err = engine.CheckPrerequisites(checkCtx)
	cancel()
	if err != nil {
		logger.Error("prerequisite_check_failed", "error", err)
		engine.NotifyFailure("update", fmt.Errorf("运行前检查: %w", err))
		return 1
	}
	schedule, err := cronexpr.Parse(cfg.Schedule, cfg.Location())
	if err != nil {
		logger.Error("cron_parse_failed", "error", err)
		engine.NotifyFailure("update", fmt.Errorf("解析调度配置: %w", err))
		return 1
	}
	logger.Info("scheduler_started", "schedule", cfg.Schedule, "timezone", cfg.Timezone, "run_on_start", cfg.RunOnStart || forceRunOnStart)
	if cfg.RunOnStart || forceRunOnStart {
		engine.Run(ctx, true)
	}
	for {
		next, err := schedule.Next(time.Now())
		if err != nil {
			logger.Error("cron_next_failed", "error", err)
			engine.NotifyFailure("update", fmt.Errorf("计算下次运行时间: %w", err))
			return 1
		}
		logger.Info("next_run_scheduled", "next_run", next.Format(time.RFC3339))
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			logger.Info("shutdown_received")
			return 0
		case <-timer.C:
			engine.Run(ctx, true)
		}
	}
}

func runMode(apply bool) string {
	if apply {
		return "update"
	}
	return "check"
}

func buildNotifier(cfg *config.Config) (notify.Notifier, error) {
	if !cfg.Bark.Enabled {
		return notify.Noop{}, nil
	}
	return notify.NewBark(cfg.Bark)
}

func newLogger(levelText string) *slog.Logger {
	var level slog.Level
	switch levelText {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level}))
}

func bootstrapLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(os.Stderr, nil)) }

func printJSON(value any) {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		fmt.Fprintf(os.Stderr, "输出 JSON 失败: %v\n", err)
	}
}

func envOr(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func printUsage(out *os.File) {
	fmt.Fprintln(out, `compose-updater

命令：
  compose-updater validate -config config.json
  compose-updater scan     -config config.json
  compose-updater check    -config config.json
  compose-updater run      -config config.json
  compose-updater serve    -config config.json
  compose-updater web      -config config.json [-listen 127.0.0.1:8080]
  compose-updater version`)
}
