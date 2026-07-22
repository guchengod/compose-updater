package scanner

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverDepthAndPriority(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "compose.yml"))
	mustWrite(t, filepath.Join(root, "docker-compose.yml"))
	mustWrite(t, filepath.Join(root, "one", "docker-compose.yml"))
	mustWrite(t, filepath.Join(root, "one", "compose.yaml"))
	mustWrite(t, filepath.Join(root, "one", "two", "compose.yml"))

	files, err := Discover([]string{root}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d: %#v", len(files), files)
	}
	if filepath.Base(files[0]) != "compose.yml" {
		t.Fatalf("root priority mismatch: %s", files[0])
	}
	if filepath.Base(files[1]) != "compose.yaml" {
		t.Fatalf("child priority mismatch: %s", files[1])
	}

	files, err = Discover([]string{root}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d: %#v", len(files), files)
	}
}

func TestDiscoverDeduplicatesOverlappingRoots(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "app")
	mustWrite(t, filepath.Join(child, "compose.yml"))
	files, err := Discover([]string{root, child}, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("expected one file, got %#v", files)
	}
}

func mustWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("services: {}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}
