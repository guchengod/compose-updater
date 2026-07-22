package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/guchengod/compose-updater/internal/config"
	"github.com/guchengod/compose-updater/internal/execx"
)

type Client struct {
	dockerCommand string
	dockerEnv     map[string]string
	composeFile   string
	workingDir    string
	projectName   string
	runner        execx.Runner
}

type listedProject struct {
	Name        string `json:"Name"`
	Status      string `json:"Status"`
	ConfigFiles string `json:"ConfigFiles"`
}

func NewClient(docker config.DockerConfig, composeFile string, runner execx.Runner) *Client {
	return &Client{
		dockerCommand: docker.Command,
		dockerEnv:     docker.Env,
		composeFile:   filepath.Clean(composeFile),
		workingDir:    filepath.Dir(composeFile),
		runner:        runner,
	}
}

// ResolveProjectName finds the existing Compose project associated with this
// configuration file. This matters when a project was created with -p/--project-name
// (for example by a NAS UI), because Compose otherwise derives a different name
// from the directory and cannot see the already-running containers.
func (c *Client) ResolveProjectName(ctx context.Context) (string, execx.Result, error) {
	result, err := c.runner.Run(ctx, c.workingDir, c.dockerEnv, c.dockerCommand,
		"compose", "ls", "--all", "--format", "json")
	if err != nil {
		return "", result, err
	}
	var projects []listedProject
	if err := json.Unmarshal([]byte(result.Stdout), &projects); err != nil {
		return "", result, fmt.Errorf("解析 docker compose ls JSON: %w", err)
	}

	matches := make([]string, 0, 1)
	for _, project := range projects {
		if strings.TrimSpace(project.Name) == "" || !containsComposeFile(project.ConfigFiles, c.composeFile, c.workingDir) {
			continue
		}
		matches = appendUnique(matches, strings.TrimSpace(project.Name))
	}
	if len(matches) == 0 {
		return "", result, nil
	}
	if len(matches) > 1 {
		return "", result, fmt.Errorf("Compose 文件 %q 同时匹配多个项目: %s", c.composeFile, strings.Join(matches, ", "))
	}
	c.projectName = matches[0]
	return c.projectName, result, nil
}

func (c *Client) ParseConfig(ctx context.Context) (Model, execx.Result, error) {
	args := append(c.baseArgs(), "config", "--format", "json")
	result, err := c.runner.Run(ctx, c.workingDir, c.dockerEnv, c.dockerCommand, args...)
	if err != nil {
		return Model{}, result, err
	}
	var model Model
	if err := json.Unmarshal([]byte(result.Stdout), &model); err != nil {
		return Model{}, result, fmt.Errorf("解析 docker compose config JSON: %w", err)
	}
	if model.Services == nil {
		return Model{}, result, fmt.Errorf("docker compose config 未返回 services")
	}
	return model, result, nil
}

func (c *Client) Pull(ctx context.Context, services []string) (execx.Result, error) {
	args := append(c.baseArgs(), "pull", "--ignore-buildable")
	args = append(args, services...)
	return c.runner.Run(ctx, c.workingDir, c.dockerEnv, c.dockerCommand, args...)
}

func (c *Client) Up(ctx context.Context, services []string, waitTimeout time.Duration) (execx.Result, error) {
	seconds := int(math.Ceil(waitTimeout.Seconds()))
	if seconds < 1 {
		seconds = 1
	}
	args := append(c.baseArgs(), "up", "-d", "--no-deps", "--no-build", "--wait", "--wait-timeout", fmt.Sprintf("%d", seconds))
	args = append(args, services...)
	return c.runner.Run(ctx, c.workingDir, c.dockerEnv, c.dockerCommand, args...)
}

func (c *Client) ContainerIDs(ctx context.Context, service string) ([]string, execx.Result, error) {
	args := append(c.baseArgs(), "ps", "-q", service)
	result, err := c.runner.Run(ctx, c.workingDir, c.dockerEnv, c.dockerCommand, args...)
	if err != nil {
		return nil, result, err
	}
	var ids []string
	for _, line := range strings.Split(result.Stdout, "\n") {
		if id := strings.TrimSpace(line); id != "" {
			ids = append(ids, id)
		}
	}
	return ids, result, nil
}

func (c *Client) baseArgs() []string {
	args := []string{"compose"}
	if c.projectName != "" {
		args = append(args, "--project-name", c.projectName)
	}
	return append(args, "--project-directory", c.workingDir, "-f", c.composeFile)
}

func containsComposeFile(configFiles, target, workingDir string) bool {
	for _, candidate := range strings.Split(configFiles, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(workingDir, candidate)
		}
		if samePath(candidate, target) {
			return true
		}
	}
	return false
}

func samePath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
