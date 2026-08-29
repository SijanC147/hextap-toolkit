package rollback

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

const maximumCommandOutput = 4 * 1024 * 1024

type osRunner struct{}

func (osRunner) Run(parent context.Context, command Command) (Result, error) {
	timeout := command.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	process := exec.CommandContext(ctx, command.Name, command.Args...)
	process.Stdin = nil
	process.Dir = command.Dir
	process.Env = commandEnvironment(command.Env)
	configureProcess(process)
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
	if buffer.data.Len()+len(data) > maximumCommandOutput {
		remaining := maximumCommandOutput - buffer.data.Len()
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

func homebrewEnvironment() map[string]string {
	return map[string]string{
		"HOMEBREW_NO_ANALYTICS":                  "1",
		"HOMEBREW_NO_AUTO_UPDATE":                "1",
		"HOMEBREW_NO_ENV_HINTS":                  "1",
		"HOMEBREW_NO_GITHUB_API":                 "1",
		"HOMEBREW_NO_INSTALL_CLEANUP":            "1",
		"HOMEBREW_NO_INSTALL_FROM_API":           "1",
		"HOMEBREW_NO_INSTALLED_DEPENDENTS_CHECK": "1",
	}
}
