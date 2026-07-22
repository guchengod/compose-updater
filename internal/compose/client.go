package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
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
	runner        execx.Runner
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
	return []string{"compose", "--project-directory", c.workingDir, "-f", c.composeFile}
}
