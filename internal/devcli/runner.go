package devcli

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

const maxCommandOutput = 8 * 1024 * 1024

// OSRunner executes bounded commands with inherited credentials kept opaque.
// Git's command-line config injection variables are scrubbed deliberately.
type OSRunner struct{}

func (OSRunner) Run(parent context.Context, command Command) (Result, error) {
	timeout := command.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	process := exec.CommandContext(ctx, command.Name, command.Args...)
	process.Dir = command.Dir
	process.Stdin = nil
	process.Env = commandEnvironment(command.Env)
	var stdout, stderr limitedBuffer
	process.Stdout = &stdout
	process.Stderr = &stderr
	err := process.Run()
	result := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if stdout.overflow || stderr.overflow {
		return result, fmt.Errorf("command %q exceeded bounded output", command.Name)
	}
	if ctx.Err() != nil {
		return result, fmt.Errorf("command %q timed out", command.Name)
	}
	if err != nil {
		return result, fmt.Errorf("command %q failed: %w", command.Name, err)
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
	result := make([]string, 0, len(values))
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := values[key]
		result = append(result, key+"="+value)
	}
	return result
}
