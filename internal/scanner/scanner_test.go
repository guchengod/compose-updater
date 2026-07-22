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

	files, err := Discover([]string{root}, 1, nil)
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

	files, err = Discover([]string{root}, 2, nil)
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
	files, err := Discover([]string{root, child}, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 {
		t.Fatalf("expected one file, got %#v", files)
	}
}

func TestDiscoverPrunesSkippedDirectoryRegardlessOfDepth(t *testing.T) {
	root := t.TempDir()
	skipped := filepath.Join(root, "skip")
	mustWrite(t, filepath.Join(root, "keep", "compose.yml"))
	mustWrite(t, filepath.Join(skipped, "compose.yml"))
	mustWrite(t, filepath.Join(skipped, "nested", "compose.yml"))

	files, err := Discover([]string{root}, 5, []string{skipped})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || filepath.Dir(files[0]) != filepath.Join(root, "keep") {
		t.Fatalf("skipped directory was traversed: %#v", files)
	}
}

func TestDiscoverSkipsRootInsideSkippedDirectory(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "skip", "nested")
	mustWrite(t, filepath.Join(root, "compose.yml"))

	files, err := Discover([]string{root}, 5, []string{filepath.Join(parent, "skip")})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("root inside skipped directory must be ignored: %#v", files)
	}
}

func TestDiscoverSkipDoesNotMatchPathPrefixSibling(t *testing.T) {
	root := t.TempDir()
	skipped := filepath.Join(root, "app")
	mustWrite(t, filepath.Join(skipped, "compose.yml"))
	mustWrite(t, filepath.Join(root, "app-data", "compose.yml"))

	files, err := Discover([]string{root}, 1, []string{skipped})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || filepath.Dir(files[0]) != filepath.Join(root, "app-data") {
		t.Fatalf("path-prefix sibling must not be skipped: %#v", files)
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
