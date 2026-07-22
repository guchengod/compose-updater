package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsAndNormalize(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{"version":1,"paths":[` + quote(root) + `],"bark":{"enabled":false}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Depth != 1 || cfg.Schedule != "0 4 * * *" || !cfg.RunOnStart || !cfg.StableOnly {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
	if !filepath.IsAbs(cfg.LockFile) {
		t.Fatalf("lock file must be absolute: %s", cfg.LockFile)
	}
}

func TestLoadAllowsNewestPublishedPolicy(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{"version":1,"paths":[` + quote(root) + `],"stable_only":false,"bark":{"enabled":false}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StableOnly {
		t.Fatal("stable_only=false was not preserved")
	}
}

func TestLoadRegistryProxy(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{"version":1,"paths":[` + quote(root) + `],"registry_proxy":"socks5://127.0.0.1:1080","bark":{"enabled":false}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.RegistryProxy != "socks5://127.0.0.1:1080" {
		t.Fatalf("unexpected proxy: %q", cfg.RegistryProxy)
	}
}

func TestLoadNormalizesAndDeduplicatesSkipDirs(t *testing.T) {
	root := t.TempDir()
	skip := filepath.Join(root, "skip")
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{"version":1,"paths":[` + quote(root) + `],"skip_dirs":[` + quote(" "+skip+" ") + `,` + quote(skip) + `],"bark":{"enabled":false}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.SkipDirs) != 1 || cfg.SkipDirs[0] != filepath.Clean(skip) {
		t.Fatalf("unexpected skip dirs: %#v", cfg.SkipDirs)
	}
}

func TestLoadRejectsRelativeSkipDir(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{"version":1,"paths":[` + quote(root) + `],"skip_dirs":["relative"],"bark":{"enabled":false}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected relative skip directory error")
	}
}

func TestLoadRejectsInvalidRegistryProxy(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{"version":1,"paths":[` + quote(root) + `],"registry_proxy":"ftp://proxy.example.com","bark":{"enabled":false}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected registry proxy validation error")
	}
}

func TestLoadRejectsDepthOverFive(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{"version":1,"paths":[` + quote(root) + `],"depth":6,"bark":{"enabled":false}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected depth error")
	}
}

func TestLoadRejectsUnknownField(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "config.json")
	content := `{"version":1,"paths":[` + quote(root) + `],"unknown":true,"bark":{"enabled":false}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected unknown field error")
	}
}

func quote(value string) string {
	result := `"`
	for _, r := range value {
		if r == '\\' || r == '"' {
			result += `\`
		}
		result += string(r)
	}
	return result + `"`
}
