package compose

import (
	"path/filepath"
	"testing"
)

func TestContainsComposeFile(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "flatnas", "docker-compose.yml")
	workingDir := filepath.Dir(target)
	tests := []struct {
		name        string
		configFiles string
		want        bool
	}{
		{name: "exact absolute path", configFiles: target, want: true},
		{name: "one file in compose list", configFiles: filepath.Join(root, "base.yml") + ", " + target, want: true},
		{name: "relative to project directory", configFiles: "docker-compose.yml", want: true},
		{name: "different file", configFiles: filepath.Join(root, "other", "docker-compose.yml"), want: false},
		{name: "path prefix is not a match", configFiles: target + ".backup", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := containsComposeFile(test.configFiles, target, workingDir); got != test.want {
				t.Fatalf("containsComposeFile(%q) = %v, want %v", test.configFiles, got, test.want)
			}
		})
	}
}
