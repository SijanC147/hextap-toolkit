package inventory

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

const maxCommandOutput = 4 * 1024 * 1024

type osRunner struct{}

func (osRunner) Run(parent context.Context, command Command) (Result, error) {
	timeout := command.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	process := exec.CommandContext(ctx, command.Name, command.Args...)
	process.Stdin = nil
	process.Env = commandEnvironment(command.Env)
	var stdout, stderr limitedBuffer
	process.Stdout = &stdout
	process.Stderr = &stderr
	err := process.Run()
	result := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if stdout.overflow || stderr.overflow {
		return result, fmt.Errorf("command output exceeded bound")
	}
	if ctx.Err() != nil {
		return result, fmt.Errorf("command timed out")
	}
	if err != nil {
		return result, fmt.Errorf("command failed")
	}
	return result, nil
}

type limitedBuffer struct {
	data     bytes.Buffer
	overflow bool
}

func (buffer *limitedBuffer) Write(data []byte) (int, error) {
	if buffer.data.Len()+len(data) > maxCommandOutput {
		remaining := maxCommandOutput - buffer.data.Len()
		if remaining > 0 {
			_, _ = buffer.data.Write(data[:remaining])
		}
		buffer.overflow = true
		return len(data), nil
	}
	return buffer.data.Write(data)
}

func (buffer *limitedBuffer) String() string { return buffer.data.String() }

func commandEnvironment(overrides map[string]string) []string {
	values := make(map[string]string)
	for _, entry := range os.Environ() {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "GIT_CONFIG_COUNT" || strings.HasPrefix(key, "GIT_CONFIG_KEY_") || strings.HasPrefix(key, "GIT_CONFIG_VALUE_") {
			continue
		}
		values[key] = value
	}
	for key, value := range overrides {
		values[key] = value
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

type osFileSystem struct{}

func (osFileSystem) Lstat(name string) (fs.FileInfo, error) { return os.Lstat(name) }

func (osFileSystem) ReadDir(ctx context.Context, name string, maximum int) ([]fs.DirEntry, bool, error) {
	if maximum <= 0 {
		return nil, false, fmt.Errorf("directory entry limit must be positive")
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	directory, err := os.Open(name)
	if err != nil {
		return nil, false, err
	}
	defer directory.Close()
	entries, err := directory.ReadDir(maximum + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, false, err
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	truncated := len(entries) > maximum
	if truncated {
		entries = entries[:maximum]
	}
	return entries, truncated, nil
}

func (osFileSystem) ReadFile(name string) ([]byte, error) { return os.ReadFile(name) }
