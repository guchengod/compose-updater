package composefile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAndRewritePreservesExactFormatting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compose.yml")
	original := "# top comment\r\nservices:\r\n  app:\r\n    image: 'example/app:1.2.3' # keep me\r\n    environment:\r\n      image: not-a-service-image\r\n  web:\r\n    image: \"nginx:1.25.0\"\r\n"
	if err := os.WriteFile(path, []byte(original), 0o640); err != nil {
		t.Fatal(err)
	}
	document, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(document.Images) != 2 {
		t.Fatalf("expected 2 image nodes, got %#v", document.Images)
	}
	backup, restore, err := document.Rewrite([]Change{
		{Service: "app", OldImage: "example/app:1.2.3", NewImage: "example/app:2.0.0"},
		{Service: "web", OldImage: "nginx:1.25.0", NewImage: "nginx:1.27.0"},
	})
	if err != nil {
		t.Fatal(err)
	}
	updated, _ := os.ReadFile(path)
	text := string(updated)
	if !strings.Contains(text, "# top comment\r\n") || !strings.Contains(text, "# keep me\r\n") {
		t.Fatalf("comments or CRLF changed:\n%q", text)
	}
	if !strings.Contains(text, "'example/app:2.0.0'") || !strings.Contains(text, `"nginx:1.27.0"`) {
		t.Fatalf("image values not rewritten:\n%s", text)
	}
	if !strings.Contains(text, "image: not-a-service-image") {
		t.Fatalf("nested environment key changed:\n%s", text)
	}
	if _, err := os.Stat(backup); err != nil {
		t.Fatalf("backup missing: %v", err)
	}
	if err := restore(); err != nil {
		t.Fatal(err)
	}
	restored, _ := os.ReadFile(path)
	if string(restored) != original {
		t.Fatalf("restore mismatch")
	}
}

func TestLoadDetectsBuild(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compose.yml")
	content := "services:\n  app:\n    build: .\n    image: app:latest\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	document, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !document.Images["app"].HasBuild {
		t.Fatal("build was not detected")
	}
}
