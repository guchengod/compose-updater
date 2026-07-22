package scanner

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

var composePriorities = map[string]int{
	"compose.yml":         0,
	"compose.yaml":        1,
	"docker-compose.yml":  2,
	"docker-compose.yaml": 3,
}

type candidate struct {
	path     string
	priority int
}

// Discover scans each root up to maxDepth directory levels while pruning every
// directory listed in skipDirs. Depth 0 means the root directory itself; depth
// 1 also includes direct child directories.
func Discover(roots []string, maxDepth int, skipDirs []string) ([]string, error) {
	if maxDepth < 0 || maxDepth > 5 {
		return nil, fmt.Errorf("扫描深度必须在 0 到 5 之间，当前为 %d", maxDepth)
	}

	found := make(map[string]candidate)
	for _, root := range roots {
		if err := walkRoot(root, maxDepth, skipDirs, found); err != nil {
			return nil, err
		}
	}

	files := make([]string, 0, len(found))
	for _, item := range found {
		files = append(files, item.path)
	}
	sort.Strings(files)
	return files, nil
}

func walkRoot(root string, maxDepth int, skipDirs []string, found map[string]candidate) error {
	root = filepath.Clean(root)
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return fmt.Errorf("访问 %q: %w", path, walkErr)
		}
		if entry.IsDir() && isSkippedDir(path, skipDirs) {
			return filepath.SkipDir
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if relative != "." && depth(relative) > maxDepth {
				return filepath.SkipDir
			}
			return nil
		}

		priority, ok := composePriorities[strings.ToLower(entry.Name())]
		if !ok {
			return nil
		}
		parentRelative, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		if depth(parentRelative) > maxDepth {
			return nil
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		key := normalize(filepath.Dir(absolute))
		current, exists := found[key]
		if !exists || priority < current.priority {
			found[key] = candidate{path: filepath.Clean(absolute), priority: priority}
		}
		return nil
	})
}

func isSkippedDir(path string, skipDirs []string) bool {
	path = normalize(path)
	for _, skipDir := range skipDirs {
		relative, err := filepath.Rel(normalize(skipDir), path)
		if err != nil || filepath.IsAbs(relative) {
			continue
		}
		if relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))) {
			return true
		}
	}
	return false
}

func depth(relative string) int {
	relative = filepath.Clean(relative)
	if relative == "." || relative == "" {
		return 0
	}
	return len(strings.Split(relative, string(os.PathSeparator)))
}

func normalize(path string) string {
	path = filepath.Clean(path)
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}
