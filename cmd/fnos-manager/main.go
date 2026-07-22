package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/guchengod/compose-updater/internal/fnos"
)

var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		slog.Error("fnos_manager_failed", "error", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) > 0 && args[0] == "init-config" {
		fs := flag.NewFlagSet("init-config", flag.ContinueOnError)
		configPath := fs.String("config", "", "config path")
		defaultPath := fs.String("default-path", "", "safe default scan path")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *configPath == "" || *defaultPath == "" {
			return fmt.Errorf("config and default-path are required")
		}
		return fnos.InitConfig(*configPath, *defaultPath)
	}

	fs := flag.NewFlagSet("fnos-manager", flag.ContinueOnError)
	configPath := fs.String("config", envOr("TRIM_PKGETC", ".")+"/config.json", "config path")
	socketPath := fs.String("socket", envOr("TRIM_APPDEST", ".")+"/compose-updater.sock", "gateway unix socket")
	updaterPath := fs.String("updater", envOr("TRIM_APPDEST", ".")+"/bin/compose-updater", "updater binary")
	listenAddress := fs.String("listen", "", "local development TCP address")
	devAdmin := fs.Bool("dev-admin", false, "inject an admin identity for loopback development")
	if err := fs.Parse(args); err != nil {
		return err
	}

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil)).With("component", "fnos-manager", "version", version)
	slog.SetDefault(logger)
	if err := fnos.InitConfig(*configPath, filepath.Dir(*socketPath)); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	supervisor := fnos.NewSupervisor(*updaterPath, *configPath, logger)
	go supervisor.Run(ctx)
	server := fnos.NewServer(fnos.ServerOptions{
		ConfigPath: *configPath, DefaultPath: filepath.Dir(*socketPath), Version: version,
		AuthorizedPaths: splitPaths(os.Getenv("TRIM_DATA_ACCESSIBLE_PATHS")),
		Supervisor:      supervisor, Logger: logger,
	})
	logger.Info("fnos_manager_started", "socket", *socketPath)
	if *listenAddress != "" {
		host, _, err := net.SplitHostPort(*listenAddress)
		ip := net.ParseIP(host)
		if err != nil || (host != "localhost" && (ip == nil || !ip.IsLoopback())) {
			return fmt.Errorf("development listen address must be loopback")
		}
		logger.Info("fnos_manager_development_listener", "address", *listenAddress, "dev_admin", *devAdmin)
		return server.ServeTCP(ctx, *listenAddress, *devAdmin)
	}
	return server.ServeUnix(ctx, *socketPath)
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func splitPaths(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.FieldsFunc(value, func(r rune) bool { return r == ':' || r == ';' || r == ',' })
}
