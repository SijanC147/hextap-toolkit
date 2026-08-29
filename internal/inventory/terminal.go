package inventory

import (
	"io"
	"os"
	"runtime"
	"strings"
)

type statusTerminalContext struct {
	GOOS      string
	IsTTY     bool
	LookupEnv func(string) (string, bool)
}

func statusOutputColorEnabled(output io.Writer) bool {
	return statusColorEnabled(statusTerminalContext{
		GOOS:      runtime.GOOS,
		IsTTY:     writerIsTerminal(output),
		LookupEnv: os.LookupEnv,
	})
}

func statusColorEnabled(context statusTerminalContext) bool {
	if !context.IsTTY || context.GOOS == "windows" || context.LookupEnv == nil {
		return false
	}
	if _, disabled := context.LookupEnv("NO_COLOR"); disabled {
		return false
	}
	if terminal, ok := context.LookupEnv("TERM"); ok && strings.EqualFold(strings.TrimSpace(terminal), "dumb") {
		return false
	}
	return true
}

func writerIsTerminal(output io.Writer) bool {
	file, ok := output.(*os.File)
	return ok && fileIsTerminal(file)
}
