package environment

import (
	"archive/tar"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"unsafe"

	"github.com/cloudwego/eino-ext/components/tool/commandline"
	"github.com/cloudwego/eino-ext/components/tool/commandline/sandbox"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

// Env is the environment interface for running commands and managing files
// inside an isolated sandbox. It extends commandline.Operator with lifecycle
// management and binary file copy operations.
type Env interface {
	commandline.Operator

	// Create initialises the environment (e.g. starts the Docker container).
	Create(ctx context.Context) error
	// Cleanup tears down the environment.
	Cleanup(ctx context.Context)
	// CopyFile copies a local file into the environment at destPath.
	CopyFile(ctx context.Context, localPath, destPath string) error
	// CopyReader copies from a reader into the environment at destPath.
	CopyReader(ctx context.Context, reader io.Reader, destPath string, size int64) error
}

// ---------------------------------------------------------------------------
// DockerEnv — Env implementation backed by sandbox.DockerSandbox
// ---------------------------------------------------------------------------

// DockerEnv wraps a sandbox.DockerSandbox and exposes all of its capabilities
// through the Env interface.
type DockerEnv struct {
	*sandbox.DockerSandbox
	cli         *client.Client
	containerID string
}

// shadowDockerSandbox mirrors sandbox.DockerSandbox's memory layout
// so we can access the unexported docker client and container ID.
// MUST stay in sync with sandbox.DockerSandbox struct definition.
type shadowDockerSandbox struct {
	config      sandbox.Config
	client      *client.Client
	containerID string
}

// dockerInternals extracts the docker client and container ID from a DockerSandbox
// via unsafe pointer cast (the fields are unexported).
func dockerInternals(ds *sandbox.DockerSandbox) (*client.Client, string) {
	shadow := (*shadowDockerSandbox)(unsafe.Pointer(ds))
	return shadow.client, shadow.containerID
}

// NewDockerEnv creates and starts a Docker-based environment.
func NewDockerEnv(ctx context.Context) (*DockerEnv, error) {
	ds, err := sandbox.NewDockerSandbox(ctx, &sandbox.Config{Image: "pcapchu/sandbox:amd64"})
	if err != nil {
		return nil, fmt.Errorf("new docker sandbox: %w", err)
	}
	if err := ds.Create(ctx); err != nil {
		return nil, fmt.Errorf("create docker sandbox: %w", err)
	}
	cli, containerID := dockerInternals(ds)
	return &DockerEnv{DockerSandbox: ds, cli: cli, containerID: containerID}, nil
}

// CopyFile copies a local file into the running container at destPath.
func (d *DockerEnv) CopyFile(ctx context.Context, localPath, destPath string) error {
	if d.cli == nil || d.containerID == "" {
		return fmt.Errorf("sandbox not initialized")
	}

	data, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("read local file %s: %w", localPath, err)
	}

	parentDir := filepath.Dir(destPath)
	if parentDir != "" && parentDir != "/" {
		if _, err := d.RunCommand(ctx, []string{"mkdir", "-p", parentDir}); err != nil {
			return fmt.Errorf("create directory %s: %w", parentDir, err)
		}
	}

	return d.copyTar(ctx, destPath, data)
}

// CopyReader copies from an io.Reader into the container at destPath.
func (d *DockerEnv) CopyReader(ctx context.Context, reader io.Reader, destPath string, size int64) error {
	if d.cli == nil || d.containerID == "" {
		return fmt.Errorf("sandbox not initialized")
	}

	data, err := io.ReadAll(reader)
	if err != nil {
		return fmt.Errorf("read data: %w", err)
	}

	return d.copyTar(ctx, destPath, data)
}

// copyTar builds a single-file tar archive and uploads it to the container.
func (d *DockerEnv) copyTar(ctx context.Context, destPath string, data []byte) error {
	tarBuf := new(bytes.Buffer)
	tw := tar.NewWriter(tarBuf)
	hdr := &tar.Header{
		Name: filepath.Base(destPath),
		Mode: 0644,
		Size: int64(len(data)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("write tar header: %w", err)
	}
	if _, err := tw.Write(data); err != nil {
		return fmt.Errorf("write tar content: %w", err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("close tar writer: %w", err)
	}

	destDir := filepath.Dir(destPath)
	if destDir == "" {
		destDir = "/"
	}
	return d.cli.CopyToContainer(ctx, d.containerID, destDir,
		bytes.NewReader(tarBuf.Bytes()), container.CopyToContainerOptions{})
}

// GetOperator creates and returns a Docker sandbox as commandline.Operator.
// Deprecated: use NewDockerEnv instead.
func GetOperator(ctx context.Context) (commandline.Operator, error) {
	return NewDockerEnv(ctx)
}
