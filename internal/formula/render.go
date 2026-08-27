// Package formula renders and safely updates Hextap-owned Homebrew Formulae.
package formula

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/SijanC147/hextap-toolkit/internal/atomicfile"
	"github.com/SijanC147/hextap-toolkit/internal/manifest"
)

// Render returns a deterministic Formula for one stable project release.
func Render(project manifest.Manifest, version, arm64SHA, amd64SHA string) ([]byte, error) {
	if err := project.Validate(); err != nil {
		return nil, err
	}
	if project.Homebrew.FormulaProfile != "" {
		return nil, errors.New("tap-owned Formula profiles cannot be rendered from a source manifest")
	}
	if err := validateReleaseMetadata(version, arm64SHA, amd64SHA); err != nil {
		return nil, err
	}

	var out bytes.Buffer
	f := project.Formula
	fmt.Fprintf(&out, "class %s < Formula\n", f.Class)
	fmt.Fprintf(&out, "  desc %s\n", rubyString(f.Description))
	fmt.Fprintf(&out, "  homepage %s\n", rubyString(f.Homepage))
	out.WriteString("  # Homebrew derives the version from the release URLs.\n")
	fmt.Fprintf(&out, "  license %s\n\n", rubyString(f.License))
	out.WriteString("  if Hardware::CPU.arm?\n")
	fmt.Fprintf(&out, "    url %s\n", rubyString(releaseURL(project, version, f.Assets.DarwinARM64)))
	fmt.Fprintf(&out, "    sha256 %s\n", rubyString(arm64SHA))
	out.WriteString("  else\n")
	fmt.Fprintf(&out, "    url %s\n", rubyString(releaseURL(project, version, f.Assets.DarwinAMD64)))
	fmt.Fprintf(&out, "    sha256 %s\n", rubyString(amd64SHA))
	out.WriteString("  end\n")
	if project.Homebrew.MacOSOnly {
		out.WriteString("\n  depends_on :macos\n")
	}
	out.WriteString("\n  def install\n")
	fmt.Fprintf(&out, "    bin.install %s\n", rubyString(f.Binary))
	out.WriteString("  end\n")

	service := project.Homebrew.Service
	if service != nil && service.Enabled {
		out.WriteString("\n  service do\n")
		writeServiceRun(&out, f.Binary, service.RunArgs)
		if service.KeepAlive.SuccessfulExit != nil {
			fmt.Fprintf(&out, "    keep_alive successful_exit: %t\n", *service.KeepAlive.SuccessfulExit)
		} else {
			fmt.Fprintf(&out, "    keep_alive crashed: %t\n", *service.KeepAlive.Crashed)
		}
		fmt.Fprintf(&out, "    restart_delay %d\n", service.RestartDelay)
		writeEnvironment(&out, service.Environment)
		fmt.Fprintf(&out, "    log_path var/%s\n", rubyString(service.LogPath))
		fmt.Fprintf(&out, "    error_log_path var/%s\n", rubyString(service.ErrorLogPath))
		out.WriteString("  end\n")
	}

	if project.Homebrew.Caveats != "" {
		out.WriteString("\n  def caveats\n")
		out.WriteString("    <<~EOS\n")
		caveats := strings.NewReplacer("{{home}}", "#{Dir.home}", "{{var}}", "#{var}").Replace(project.Homebrew.Caveats)
		for _, line := range strings.Split(caveats, "\n") {
			fmt.Fprintf(&out, "      %s\n", line)
		}
		out.WriteString("    EOS\n")
		out.WriteString("  end\n")
	}

	out.WriteString("\n  test do\n")
	command := "#{bin}/" + f.Binary + " " + strings.Join(project.Homebrew.TestArgs, " ")
	fmt.Fprintf(&out, "    assert_match version.to_s, shell_output(%s)\n", rubyString(command))
	out.WriteString("  end\n")
	out.WriteString("end\n")
	return out.Bytes(), nil
}

func rubyString(value string) string {
	return strconv.Quote(value)
}

func releaseURL(project manifest.Manifest, version, asset string) string {
	return "https://github.com/" + project.RepositorySlug() + "/releases/download/v" + version + "/" + asset
}

func writeServiceRun(out *bytes.Buffer, binary string, args []string) {
	if len(args) == 0 {
		fmt.Fprintf(out, "    run opt_bin/%s\n", rubyString(binary))
		return
	}
	fmt.Fprintf(out, "    run [opt_bin/%s", rubyString(binary))
	for _, arg := range args {
		fmt.Fprintf(out, ", %s", rubyString(arg))
	}
	out.WriteString("]\n")
}

func writeEnvironment(out *bytes.Buffer, environment map[string]string) {
	if len(environment) == 0 {
		return
	}
	keys := make([]string, 0, len(environment))
	for key := range environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for index, key := range keys {
		prefix := "                          "
		if index == 0 {
			prefix = "    environment_variables "
		}
		comma := ","
		if index == len(keys)-1 {
			comma = ""
		}
		fmt.Fprintf(out, "%s%s: %s%s\n", prefix, key, rubyString(environment[key]), comma)
	}
}

// RenderFile atomically renders a Formula, preserving an existing file's mode.
func RenderFile(path string, project manifest.Manifest, version, arm64SHA, amd64SHA string) error {
	data, err := Render(project, version, arm64SHA, amd64SHA)
	if err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if info, statErr := os.Lstat(path); statErr == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("render Formula: destination %q is not a regular file", path)
		}
		mode = info.Mode().Perm()
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return fmt.Errorf("render Formula: inspect destination %q: %w", path, statErr)
	}
	if err := atomicfile.Write(path, data, mode); err != nil {
		return fmt.Errorf("render Formula: %w", err)
	}
	return nil
}
