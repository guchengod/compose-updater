package dockercli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/guchengod/compose-updater/internal/config"
	"github.com/guchengod/compose-updater/internal/execx"
)

type Client struct {
	command string
	env     map[string]string
	runner  execx.Runner
}

type ContainerState struct {
	Status     string       `json:"Status"`
	Running    bool         `json:"Running"`
	Restarting bool         `json:"Restarting"`
	ExitCode   int          `json:"ExitCode"`
	Error      string       `json:"Error"`
	Health     *HealthState `json:"Health,omitempty"`
}

type HealthState struct {
	Status        string `json:"Status"`
	FailingStreak int    `json:"FailingStreak"`
}

func NewClient(docker config.DockerConfig, runner execx.Runner) *Client {
	return &Client{command: docker.Command, env: docker.Env, runner: runner}
}

func (c *Client) Check(ctx context.Context) error {
	if _, err := c.runner.Run(ctx, "", c.env, c.command, "version", "--format", "{{.Server.Version}}"); err != nil {
		return fmt.Errorf("Docker Engine 不可用: %w", err)
	}
	if _, err := c.runner.Run(ctx, "", c.env, c.command, "compose", "version"); err != nil {
		return fmt.Errorf("Docker Compose v2 不可用: %w", err)
	}
	return nil
}

func (c *Client) ImageID(ctx context.Context, image string) (string, error) {
	result, err := c.runner.Run(ctx, "", c.env, c.command, "image", "inspect", "--format", "{{.Id}}", image)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(result.Stdout)
	if id == "" {
		return "", fmt.Errorf("镜像 %q inspect 未返回 ID", image)
	}
	return id, nil
}

func (c *Client) ContainerImageID(ctx context.Context, containerID string) (string, error) {
	result, err := c.runner.Run(ctx, "", c.env, c.command, "container", "inspect", "--format", "{{.Image}}", containerID)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(result.Stdout)
	if id == "" {
		return "", fmt.Errorf("容器 %q inspect 未返回镜像 ID", containerID)
	}
	return id, nil
}

func (c *Client) ContainerState(ctx context.Context, containerID string) (ContainerState, error) {
	result, err := c.runner.Run(ctx, "", c.env, c.command, "container", "inspect", "--format", "{{json .State}}", containerID)
	if err != nil {
		return ContainerState{}, err
	}
	var state ContainerState
	if err := json.Unmarshal([]byte(strings.TrimSpace(result.Stdout)), &state); err != nil {
		return ContainerState{}, fmt.Errorf("解析容器 %q 状态: %w", containerID, err)
	}
	return state, nil
}

func (c *Client) PullImage(ctx context.Context, image string) (execx.Result, error) {
	return c.runner.Run(ctx, "", c.env, c.command, "pull", image)
}
