package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/guchengod/compose-updater/internal/cronexpr"
)

const (
	defaultSchedule = "0 4 * * *"
	defaultTimezone = "Asia/Shanghai"
)

var (
	defaultConfigTimeout      = 90 * time.Second
	defaultRegistryTimeout    = 2 * time.Minute
	defaultPullTimeout        = 15 * time.Minute
	defaultUpdateTimeout      = 10 * time.Minute
	defaultHealthTimeout      = 3 * time.Minute
	defaultHealthPollInterval = 3 * time.Second
	defaultStableDuration     = 10 * time.Second
	defaultNotifyTimeout      = 15 * time.Second
)

type Config struct {
	Version       int        `json:"version"`
	Paths         []string   `json:"paths"`
	Depth         int        `json:"depth"`
	Schedule      string     `json:"schedule"`
	Timezone      string     `json:"timezone"`
	RunOnStart    bool       `json:"run_on_start"`
	StableOnly    bool       `json:"stable_only"`
	RegistryProxy string     `json:"registry_proxy"`
	Bark          BarkConfig `json:"bark"`

	NodeName           string       `json:"-"`
	LockFile           string       `json:"-"`
	LogLevel           string       `json:"-"`
	Docker             DockerConfig `json:"-"`
	location           *time.Location
	configTimeout      time.Duration
	registryTimeout    time.Duration
	pullTimeout        time.Duration
	updateTimeout      time.Duration
	healthTimeout      time.Duration
	healthPollInterval time.Duration
	stableDuration     time.Duration
	notifyTimeout      time.Duration
}

type fileConfig struct {
	Version       int        `json:"version"`
	Paths         []string   `json:"paths"`
	Depth         *int       `json:"depth"`
	Schedule      string     `json:"schedule"`
	Timezone      string     `json:"timezone"`
	RunOnStart    *bool      `json:"run_on_start"`
	StableOnly    *bool      `json:"stable_only"`
	RegistryProxy string     `json:"registry_proxy"`
	Bark          BarkConfig `json:"bark"`
}

type BarkConfig struct {
	Enabled        bool   `json:"enabled"`
	Endpoint       string `json:"endpoint"`
	DeviceKey      string `json:"device_key"`
	DeviceKeyEnv   string `json:"device_key_env"`
	Group          string `json:"group"`
	requestTimeout time.Duration
}

type DockerConfig struct {
	Command string
	Env     map[string]string
}

func Load(path string) (*Config, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("打开配置文件 %q: %w", path, err)
	}
	defer file.Close()

	var raw fileConfig
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("解析配置文件 %q: %w", path, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("配置文件 %q 包含多个 JSON 值", path)
		}
		return nil, fmt.Errorf("配置文件 %q 尾部内容无效: %w", path, err)
	}

	cfg := &Config{
		Version:       raw.Version,
		Paths:         raw.Paths,
		Depth:         1,
		Schedule:      strings.TrimSpace(raw.Schedule),
		Timezone:      strings.TrimSpace(raw.Timezone),
		RunOnStart:    true,
		StableOnly:    true,
		RegistryProxy: strings.TrimSpace(raw.RegistryProxy),
		Bark:          raw.Bark,
	}
	if raw.Depth != nil {
		cfg.Depth = *raw.Depth
	}
	if raw.RunOnStart != nil {
		cfg.RunOnStart = *raw.RunOnStart
	}
	if raw.StableOnly != nil {
		cfg.StableOnly = *raw.StableOnly
	}
	if cfg.Schedule == "" {
		cfg.Schedule = defaultSchedule
	}
	if cfg.Timezone == "" {
		cfg.Timezone = defaultTimezone
	}
	if cfg.Version == 0 {
		cfg.Version = 1
	}
	if err := cfg.normalize(path); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) normalize(configPath string) error {
	if c.Version != 1 {
		return fmt.Errorf("不支持配置版本 %d，仅支持 1", c.Version)
	}
	if len(c.Paths) == 0 {
		return errors.New("paths 至少需要一个扫描目录")
	}
	if c.Depth < 0 || c.Depth > 5 {
		return fmt.Errorf("depth 必须在 0 到 5 之间，当前为 %d", c.Depth)
	}
	if c.RegistryProxy != "" {
		proxyURL, err := url.Parse(c.RegistryProxy)
		if err != nil || proxyURL.Host == "" {
			return fmt.Errorf("registry_proxy 不是有效代理 URL: %q", c.RegistryProxy)
		}
		switch strings.ToLower(proxyURL.Scheme) {
		case "http", "https", "socks5", "socks5h":
		default:
			return fmt.Errorf("registry_proxy 仅支持 http/https/socks5/socks5h，当前为 %q", proxyURL.Scheme)
		}
	}

	location, err := time.LoadLocation(c.Timezone)
	if err != nil {
		return fmt.Errorf("无效时区 %q: %w", c.Timezone, err)
	}
	c.location = location
	if _, err := cronexpr.Parse(c.Schedule, location); err != nil {
		return fmt.Errorf("无效 schedule %q: %w", c.Schedule, err)
	}

	seen := make(map[string]struct{}, len(c.Paths))
	normalized := make([]string, 0, len(c.Paths))
	for _, root := range c.Paths {
		root = strings.TrimSpace(root)
		if root == "" {
			return errors.New("paths 不能包含空路径")
		}
		if !filepath.IsAbs(root) {
			return fmt.Errorf("扫描目录必须是绝对路径: %q", root)
		}
		root = filepath.Clean(root)
		info, err := os.Stat(root)
		if err != nil {
			return fmt.Errorf("扫描目录不可访问 %q: %w", root, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("扫描路径不是目录: %q", root)
		}
		key := normalizePath(root)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, root)
	}
	c.Paths = normalized

	c.NodeName = strings.TrimSpace(os.Getenv("NODE_NAME"))
	if c.NodeName == "" {
		c.NodeName, _ = os.Hostname()
	}
	if strings.TrimSpace(c.NodeName) == "" {
		c.NodeName = "unknown-host"
	}
	c.LogLevel = strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL")))
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	switch c.LogLevel {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("LOG_LEVEL 必须为 debug/info/warn/error，当前为 %q", c.LogLevel)
	}

	dockerCommand := strings.TrimSpace(os.Getenv("DOCKER_COMMAND"))
	if dockerCommand == "" {
		dockerCommand = "docker"
	}
	c.Docker = DockerConfig{Command: dockerCommand, Env: map[string]string{"DOCKER_CLI_HINTS": "false"}}

	dataDir := strings.TrimSpace(os.Getenv("COMPOSE_UPDATER_DATA_DIR"))
	if dataDir == "" {
		absolute, err := filepath.Abs(configPath)
		if err != nil {
			return fmt.Errorf("解析配置文件绝对路径: %w", err)
		}
		dataDir = filepath.Join(filepath.Dir(absolute), "data")
	}
	if !filepath.IsAbs(dataDir) {
		absolute, err := filepath.Abs(dataDir)
		if err != nil {
			return fmt.Errorf("解析数据目录: %w", err)
		}
		dataDir = absolute
	}
	c.LockFile = filepath.Join(filepath.Clean(dataDir), "compose-updater.lock")

	var durationErr error
	if c.configTimeout, durationErr = durationFromEnv("COMPOSE_UPDATER_CONFIG_TIMEOUT", defaultConfigTimeout); durationErr != nil {
		return durationErr
	}
	if c.registryTimeout, durationErr = durationFromEnv("COMPOSE_UPDATER_REGISTRY_TIMEOUT", defaultRegistryTimeout); durationErr != nil {
		return durationErr
	}
	if c.pullTimeout, durationErr = durationFromEnv("COMPOSE_UPDATER_PULL_TIMEOUT", defaultPullTimeout); durationErr != nil {
		return durationErr
	}
	if c.updateTimeout, durationErr = durationFromEnv("COMPOSE_UPDATER_UPDATE_TIMEOUT", defaultUpdateTimeout); durationErr != nil {
		return durationErr
	}
	if c.healthTimeout, durationErr = durationFromEnv("COMPOSE_UPDATER_HEALTH_TIMEOUT", defaultHealthTimeout); durationErr != nil {
		return durationErr
	}
	if c.healthPollInterval, durationErr = durationFromEnv("COMPOSE_UPDATER_HEALTH_POLL_INTERVAL", defaultHealthPollInterval); durationErr != nil {
		return durationErr
	}
	if c.stableDuration, durationErr = durationFromEnv("COMPOSE_UPDATER_STABLE_DURATION", defaultStableDuration); durationErr != nil {
		return durationErr
	}
	if c.notifyTimeout, durationErr = durationFromEnv("COMPOSE_UPDATER_NOTIFY_TIMEOUT", defaultNotifyTimeout); durationErr != nil {
		return durationErr
	}

	c.Bark.requestTimeout = c.notifyTimeout

	if c.Bark.Enabled {
		if strings.TrimSpace(c.Bark.Endpoint) == "" {
			c.Bark.Endpoint = "https://api.day.app/push"
		}
		if strings.TrimSpace(c.Bark.DeviceKeyEnv) == "" {
			c.Bark.DeviceKeyEnv = "BARK_DEVICE_KEY"
		}
		if strings.TrimSpace(c.Bark.DeviceKey) == "" {
			c.Bark.DeviceKey = strings.TrimSpace(os.Getenv(c.Bark.DeviceKeyEnv))
		}
		if c.Bark.DeviceKey == "" {
			return fmt.Errorf("Bark 已启用，但 device_key 和环境变量 %s 均为空", c.Bark.DeviceKeyEnv)
		}
		if strings.TrimSpace(c.Bark.Group) == "" {
			c.Bark.Group = "Docker更新"
		}
	}
	return nil
}

func normalizePath(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}

func durationFromEnv(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("%s 必须是正数 Go 时长，当前为 %q", name, value)
	}
	return duration, nil
}

func (c *Config) Location() *time.Location          { return c.location }
func (c *Config) ConfigTimeout() time.Duration      { return c.configTimeout }
func (c *Config) RegistryTimeout() time.Duration    { return c.registryTimeout }
func (c *Config) PullTimeout() time.Duration        { return c.pullTimeout }
func (c *Config) UpdateTimeout() time.Duration      { return c.updateTimeout }
func (c *Config) HealthTimeout() time.Duration      { return c.healthTimeout }
func (c *Config) HealthPollInterval() time.Duration { return c.healthPollInterval }
func (c *Config) StableDuration() time.Duration     { return c.stableDuration }
func (b BarkConfig) RequestTimeout() time.Duration {
	if b.requestTimeout > 0 {
		return b.requestTimeout
	}
	return defaultNotifyTimeout
}
func (b BarkConfig) SuccessLevel() string   { return "active" }
func (b BarkConfig) FailureLevel() string   { return "timeSensitive" }
func (b BarkConfig) AvailableLevel() string { return "active" }
