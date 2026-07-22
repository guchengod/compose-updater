package fnos

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSaveSettingsPreservesWriteOnlyDeviceKey(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.json")
	settings := DefaultSettings(root)
	settings.Bark.Enabled = true
	settings.Bark.DeviceKey = "secret-value"
	if err := SaveSettings(path, settings); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Bark.DeviceKey != "" || !loaded.Bark.KeySet {
		t.Fatalf("device key must be write-only: %#v", loaded.Bark)
	}

	loaded.Depth = 3
	if err := SaveSettings(path, loaded); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw Settings
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw.Bark.DeviceKey != "secret-value" {
		t.Fatalf("saved secret was lost: %q", raw.Bark.DeviceKey)
	}
}

func TestSaveSettingsCanClearDeviceKey(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.json")
	settings := DefaultSettings(root)
	settings.Bark.DeviceKey = "secret-value"
	if err := SaveSettings(path, settings); err != nil {
		t.Fatal(err)
	}
	settings.Bark.ClearKey = true
	settings.Bark.DeviceKey = ""
	if err := SaveSettings(path, settings); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSettings(path)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Bark.KeySet {
		t.Fatal("device key should be cleared")
	}
}

func TestSaveSettingsRejectsInvalidConfigWithoutReplacingFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config.json")
	settings := DefaultSettings(root)
	if err := SaveSettings(path, settings); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	settings.Paths = []string{filepath.Join(root, "missing")}
	if err := SaveSettings(path, settings); err == nil {
		t.Fatal("expected inaccessible path to fail validation")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(before) {
		t.Fatal("invalid config replaced the existing file")
	}
}
