package fnos

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/guchengod/compose-updater/internal/atomicfile"
	"github.com/guchengod/compose-updater/internal/config"
)

type Settings struct {
	Version       int          `json:"version"`
	Paths         []string     `json:"paths"`
	SkipDirs      []string     `json:"skip_dirs"`
	Depth         int          `json:"depth"`
	Schedule      string       `json:"schedule"`
	Timezone      string       `json:"timezone"`
	RunOnStart    bool         `json:"run_on_start"`
	StableOnly    bool         `json:"stable_only"`
	RegistryProxy string       `json:"registry_proxy"`
	Bark          BarkSettings `json:"bark"`
}

type BarkSettings struct {
	Enabled      bool   `json:"enabled"`
	Endpoint     string `json:"endpoint"`
	DeviceKey    string `json:"device_key,omitempty"`
	DeviceKeyEnv string `json:"device_key_env"`
	Group        string `json:"group"`
	ClearKey     bool   `json:"clear_device_key,omitempty"`
	KeySet       bool   `json:"device_key_set,omitempty"`
}

func DefaultSettings(defaultPath string) Settings {
	defaultPath = filepath.Clean(defaultPath)
	return Settings{
		Version:    1,
		Paths:      []string{defaultPath},
		SkipDirs:   []string{defaultPath},
		Depth:      2,
		Schedule:   "0 4 * * *",
		Timezone:   "Asia/Shanghai",
		RunOnStart: true,
		StableOnly: true,
		Bark: BarkSettings{
			Endpoint:     "https://api.day.app/push",
			DeviceKeyEnv: "BARK_DEVICE_KEY",
			Group:        "compose-updater",
		},
	}
}

func InitConfig(path, defaultPath string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("检查配置文件: %w", err)
	}
	settings := DefaultSettings(defaultPath)
	return writeSettings(path, settings)
}

func LoadSettings(path string) (Settings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Settings{}, fmt.Errorf("读取配置文件: %w", err)
	}
	var settings Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return Settings{}, fmt.Errorf("解析配置文件: %w", err)
	}
	settings.Bark.KeySet = strings.TrimSpace(settings.Bark.DeviceKey) != ""
	settings.Bark.DeviceKey = ""
	settings.Bark.ClearKey = false
	return settings, nil
}

func SaveSettings(path string, incoming Settings) error {
	existingKey := ""
	if data, err := os.ReadFile(path); err == nil {
		var current Settings
		if json.Unmarshal(data, &current) == nil {
			existingKey = current.Bark.DeviceKey
		}
	}

	incoming.Version = 1
	incoming.Bark.DeviceKey = strings.TrimSpace(incoming.Bark.DeviceKey)
	if incoming.Bark.ClearKey {
		incoming.Bark.DeviceKey = ""
	} else if incoming.Bark.DeviceKey == "" {
		incoming.Bark.DeviceKey = existingKey
	}
	incoming.Bark.ClearKey = false
	incoming.Bark.KeySet = false

	data, err := json.MarshalIndent(incoming, "", "  ")
	if err != nil {
		return fmt.Errorf("编码配置文件: %w", err)
	}
	data = append(data, '\n')
	testPath := filepath.Join(filepath.Dir(path), ".config.validate.json")
	if err := atomicfile.Write(testPath, data, 0o600); err != nil {
		return fmt.Errorf("创建待验证配置: %w", err)
	}
	defer os.Remove(testPath)
	if _, err := config.Load(testPath); err != nil {
		return err
	}
	if err := atomicfile.Write(path, data, 0o600); err != nil {
		return fmt.Errorf("保存配置文件: %w", err)
	}
	return nil
}

func writeSettings(path string, settings Settings) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicfile.Write(path, data, 0o600)
}
