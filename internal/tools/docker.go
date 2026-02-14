package tools

import (
	"bytes"
	"context"
	"fmt"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// DockerSandbox manages ephemeral containers for command execution.
type DockerSandbox struct {
	client      *client.Client
	image       string
	memoryMB    int64
	networkMode string
	workspace   string
}

// NewDockerSandbox creates a new sandbox manager.
func NewDockerSandbox(image string, memoryMB int64, networkMode, workspace string) (*DockerSandbox, error) {
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}

	if image == "" {
		image = "golang:alpine"
	}
	if memoryMB <= 0 {
		memoryMB = 512
	}
	if networkMode == "" {
		networkMode = "none"
	}

	return &DockerSandbox{
		client:      cli,
		image:       image,
		memoryMB:    memoryMB * 1024 * 1024,
		networkMode: networkMode,
		workspace:   workspace,
	}, nil
}

// Exec runs a command in an ephemeral container.
func (d *DockerSandbox) Exec(ctx context.Context, cmd, workDir string) (stdout, stderr string, exitCode int, err error) {
	// 1. Create container
	resp, err := d.client.ContainerCreate(ctx, &container.Config{
		Image:      d.image,
		Cmd:        []string{"sh", "-c", cmd},
		WorkingDir: "/workspace",
		Tty:        false,
	}, &container.HostConfig{
		Resources: container.Resources{
			Memory: d.memoryMB,
		},
		NetworkMode: container.NetworkMode(d.networkMode),
		Binds:       []string{fmt.Sprintf("%s:/workspace", d.workspace)},
		AutoRemove:  true, // Clean up automatically
	}, nil, nil, "")

	if err != nil {
		return "", "", -1, fmt.Errorf("create container: %w", err)
	}

	containerID := resp.ID

	// 2. Start container
	if err := d.client.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
		return "", "", -1, fmt.Errorf("start container: %w", err)
	}

	// 3. Wait for completion
	statusCh, errCh := d.client.ContainerWait(ctx, containerID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		return "", "", -1, fmt.Errorf("wait container error: %w", err)
	case status := <-statusCh:
		exitCode = int(status.StatusCode)
	case <-ctx.Done():
		// Force kill on timeout
		_ = d.client.ContainerKill(ctx, containerID, "SIGKILL")
		return "", "command timed out", -1, ctx.Err()
	}

	// 4. Get logs
	out, err := d.client.ContainerLogs(ctx, containerID, container.LogsOptions{ShowStdout: true, ShowStderr: true})
	if err != nil {
		return "", "", exitCode, fmt.Errorf("get logs: %w", err)
	}
	defer out.Close()

	var stdoutBuf, stderrBuf bytes.Buffer
	_, _ = stdcopy.StdCopy(&stdoutBuf, &stderrBuf, out)

	return stdoutBuf.String(), stderrBuf.String(), exitCode, nil
}

// Close closes the docker client.
func (d *DockerSandbox) Close() error {
	return d.client.Close()
}
