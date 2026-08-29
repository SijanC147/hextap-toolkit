package inventory

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"
)

const (
	statusANSIReset   = "\x1b[0m"
	statusANSITitle   = "\x1b[1;36m"
	statusANSISection = "\x1b[1m"
	statusANSIWarning = "\x1b[1;33m"
	statusANSIDim     = "\x1b[2m"
)

// RenderStatus writes a structured human-readable representation of every
// report field. ANSI styling is enabled only for a supported interactive TTY.
func RenderStatus(output io.Writer, report Report) {
	renderStatus(output, report, statusOutputColorEnabled(output))
}

func renderStatus(output io.Writer, report Report, styled bool) {
	renderer := statusRenderer{output: output, styled: styled}
	renderer.title("Hextap system status")
	renderer.field(1, "schema", strconv.Itoa(report.Schema))
	renderer.blank()

	renderer.section("CLI", false)
	renderer.field(1, "version", report.CLI.Version)
	renderer.field(1, "commit", report.CLI.Commit)
	renderer.field(1, "executable", report.CLI.Executable)
	renderer.blank()

	renderer.section("Homebrew", false)
	renderer.field(1, "executable", report.Homebrew.Executable)
	renderer.field(1, "prefix", report.Homebrew.Prefix)
	renderer.blank()

	renderer.section("Tap", false)
	renderer.field(1, "name", report.Tap.Name)
	renderer.field(1, "installed", strconv.FormatBool(report.Tap.Installed))
	renderer.field(1, "path", report.Tap.Path)
	renderer.field(1, "revision", report.Tap.Revision)
	renderer.field(1, "branch", report.Tap.Branch)
	renderer.field(1, "remote", report.Tap.Remote)
	renderer.blank()

	renderer.section(fmt.Sprintf("Registered projects (%d)", len(report.Projects)), false)
	if len(report.Projects) == 0 {
		renderer.empty(1, "None.")
	} else {
		for index, project := range report.Projects {
			renderer.entry(1, index, project.Name, false)
			renderer.project(project, 2)
		}
	}
	renderer.blank()

	renderer.section(fmt.Sprintf("Formulae (%d)", len(report.Formulae)), false)
	if len(report.Formulae) == 0 {
		renderer.empty(1, "None.")
	} else {
		for index, formula := range report.Formulae {
			renderer.entry(1, index, formula.Name, false)
			renderer.field(2, "full name", formula.FullName)
			renderer.field(2, "available version", formula.AvailableVersion)
			renderer.field(2, "installed", strconv.FormatBool(formula.Installed))
			renderer.list(2, "installed versions", formula.InstalledVersions)
			renderer.field(2, "outdated", strconv.FormatBool(formula.Outdated))
			renderer.field(2, "pinned", strconv.FormatBool(formula.Pinned))
			renderer.sectionAt(2, "service")
			renderer.field(3, "defined", strconv.FormatBool(formula.Service.Defined))
			renderer.field(3, "run type", formula.Service.RunType)
			renderer.field(3, "restart delay", strconv.Itoa(formula.Service.RestartDelay))
			renderer.list(3, "keep alive", formula.Service.KeepAlive)
			renderer.list(3, "environment variables", formula.Service.EnvironmentVariables)
		}
	}
	renderer.blank()

	renderer.section(fmt.Sprintf("Casks (%d)", len(report.Casks)), false)
	if len(report.Casks) == 0 {
		renderer.empty(1, "None.")
	} else {
		for index, cask := range report.Casks {
			renderer.entry(1, index, cask.Name, false)
			renderer.field(2, "full name", cask.FullName)
			renderer.field(2, "available version", cask.AvailableVersion)
			renderer.field(2, "installed", strconv.FormatBool(cask.Installed))
			renderer.field(2, "installed version", cask.InstalledVersion)
			renderer.field(2, "outdated", strconv.FormatBool(cask.Outdated))
			autoUpdates := ""
			if cask.AutoUpdates != nil {
				autoUpdates = strconv.FormatBool(*cask.AutoUpdates)
			}
			renderer.field(2, "auto updates", autoUpdates)
		}
	}
	renderer.blank()

	renderer.section(fmt.Sprintf("Skills (%d)", len(report.Skills)), false)
	if len(report.Skills) == 0 {
		renderer.empty(1, "None.")
	} else {
		for index, skill := range report.Skills {
			renderer.entry(1, index, skill.Agent, false)
			renderer.field(2, "scope", string(skill.Scope))
			renderer.field(2, "state", string(skill.State))
			renderer.list(2, "discovered by", skill.DiscoveredBy)
			renderer.field(2, "path", skill.Path)
			renderer.field(2, "installed version", skill.InstalledVersion)
			renderer.field(2, "available version", skill.AvailableVersion)
			renderer.field(2, "recommendation", string(skill.Recommendation))
		}
	}
	renderer.blank()

	renderer.section("Local project", false)
	if report.LocalProject == nil {
		renderer.empty(1, "None requested.")
	} else {
		renderer.entry(1, 0, report.LocalProject.Name, false)
		renderer.project(*report.LocalProject, 2)
	}
	renderer.blank()

	renderer.section(fmt.Sprintf("Warnings (%d)", len(report.Warnings)), len(report.Warnings) != 0)
	if len(report.Warnings) == 0 {
		renderer.empty(1, "None.")
	} else {
		for index, warning := range report.Warnings {
			renderer.entry(1, index, warning.Component, true)
			renderer.field(2, "message", warning.Message)
		}
	}
}

type statusRenderer struct {
	output io.Writer
	styled bool
}

func (renderer statusRenderer) title(value string) {
	fmt.Fprintln(renderer.output, renderer.style(statusANSITitle, statusText(value)))
}

func (renderer statusRenderer) section(value string, warning bool) {
	style := statusANSISection
	if warning {
		style = statusANSIWarning
	}
	fmt.Fprintln(renderer.output, renderer.style(style, statusText(value)))
}

func (renderer statusRenderer) sectionAt(indent int, value string) {
	fmt.Fprintf(renderer.output, "%s%s\n", statusIndent(indent), renderer.style(statusANSISection, statusText(value)))
}

func (renderer statusRenderer) field(indent int, name, value string) {
	fmt.Fprintf(renderer.output, "%s%s: %s\n", statusIndent(indent), statusText(name), statusDisplay(value))
}

func (renderer statusRenderer) list(indent int, name string, values []string) {
	fmt.Fprintf(renderer.output, "%s%s (%d)\n", statusIndent(indent), statusText(name), len(values))
	if len(values) == 0 {
		renderer.empty(indent+1, "None.")
		return
	}
	for _, value := range values {
		fmt.Fprintf(renderer.output, "%s- %s\n", statusIndent(indent+1), statusDisplay(value))
	}
}

func (renderer statusRenderer) entry(indent, index int, value string, warning bool) {
	text := fmt.Sprintf("[%d] %s", index+1, statusDisplay(value))
	if warning {
		text = renderer.style(statusANSIWarning, text)
	}
	fmt.Fprintf(renderer.output, "%s%s\n", statusIndent(indent), text)
}

func (renderer statusRenderer) project(project ProjectInfo, indent int) {
	renderer.field(indent, "repository", project.Repository)
	renderer.field(indent, "binary", project.Binary)
	renderer.field(indent, "schema", strconv.Itoa(project.Schema))
	renderer.field(indent, "service enabled", strconv.FormatBool(project.ServiceEnabled))
	renderer.field(indent, "manifest path", project.ManifestPath)
	renderer.field(indent, "registration state", project.RegistrationState)
}

func (renderer statusRenderer) empty(indent int, value string) {
	text := statusText(value)
	if renderer.styled {
		text = statusANSIDim + text + statusANSIReset
	}
	fmt.Fprintf(renderer.output, "%s%s\n", statusIndent(indent), text)
}

func (renderer statusRenderer) blank() {
	_, _ = io.WriteString(renderer.output, "\n")
}

func (renderer statusRenderer) style(sequence, value string) string {
	if !renderer.styled {
		return value
	}
	return sequence + value + statusANSIReset
}

func statusIndent(level int) string {
	return strings.Repeat("  ", level)
}

func statusDisplay(value string) string {
	if value == "" {
		return "-"
	}
	return statusText(value)
}

func statusText(value string) string {
	var result strings.Builder
	for _, character := range value {
		switch character {
		case '\n':
			result.WriteString(`\n`)
		case '\r':
			result.WriteString(`\r`)
		case '\t':
			result.WriteString(`\t`)
		case '\x1b':
			result.WriteString(`\x1b`)
		default:
			if unicode.IsControl(character) {
				fmt.Fprintf(&result, `\u{%x}`, character)
			} else {
				result.WriteRune(character)
			}
		}
	}
	return result.String()
}

// RenderInfo writes detailed human-readable inventory without exposing service
// environment values.
func RenderInfo(output io.Writer, report Report) {
	_, _ = io.WriteString(output, "CLI\n")
	fmt.Fprintf(output, "  version=%s commit=%s\n", display(report.CLI.Version), display(report.CLI.Commit))
	fmt.Fprintf(output, "  executable=%s\n", display(report.CLI.Executable))
	_, _ = io.WriteString(output, "HOMEBREW\n")
	fmt.Fprintf(output, "  executable=%s prefix=%s\n", display(report.Homebrew.Executable), display(report.Homebrew.Prefix))
	_, _ = io.WriteString(output, "TAP\n")
	fmt.Fprintf(output, "  name=%s installed=%t\n", report.Tap.Name, report.Tap.Installed)
	fmt.Fprintf(output, "  path=%s\n", display(report.Tap.Path))
	fmt.Fprintf(output, "  revision=%s branch=%s\n", display(report.Tap.Revision), display(report.Tap.Branch))
	fmt.Fprintf(output, "  remote=%s\n", display(report.Tap.Remote))
	if len(report.Projects) != 0 {
		fmt.Fprintf(output, "REGISTERED PROJECTS (%d)\n", len(report.Projects))
		for _, project := range report.Projects {
			fmt.Fprintf(output, "  %s repository=%s binary=%s schema=%d service=%t\n", project.Name, project.Repository, project.Binary, project.Schema, project.ServiceEnabled)
			fmt.Fprintf(output, "    manifest=%s\n", project.ManifestPath)
		}
	}
	if len(report.Formulae) != 0 {
		fmt.Fprintf(output, "FORMULAE (%d)\n", len(report.Formulae))
		for _, formula := range report.Formulae {
			fmt.Fprintf(output, "  %s available=%s installed=%s outdated=%t pinned=%t\n", formula.Name, display(formula.AvailableVersion), displayVersions(formula.InstalledVersions), formula.Outdated, formula.Pinned)
			fmt.Fprintf(output, "    full_name=%s service=%t", formula.FullName, formula.Service.Defined)
			if formula.Service.Defined {
				fmt.Fprintf(output, " run_type=%s restart_delay=%d keep_alive=%s environment_variables=%s", display(formula.Service.RunType), formula.Service.RestartDelay, displayVersions(formula.Service.KeepAlive), displayVersions(formula.Service.EnvironmentVariables))
			}
			_, _ = io.WriteString(output, "\n")
		}
	}
	if len(report.Casks) != 0 {
		fmt.Fprintf(output, "CASKS (%d)\n", len(report.Casks))
		for _, cask := range report.Casks {
			fmt.Fprintf(output, "  %s available=%s installed=%s outdated=%t", cask.Name, display(cask.AvailableVersion), display(cask.InstalledVersion), cask.Outdated)
			if cask.AutoUpdates != nil {
				fmt.Fprintf(output, " auto_updates=%t", *cask.AutoUpdates)
			}
			fmt.Fprintf(output, "\n    full_name=%s\n", cask.FullName)
		}
	}
	if len(report.Skills) != 0 {
		fmt.Fprintf(output, "SKILLS (%d)\n", len(report.Skills))
		for _, skill := range report.Skills {
			fmt.Fprintf(output, "  %s scope=%s state=%s discovered_by=%s installed=%s available=%s action=%s\n", skill.Agent, skill.Scope, skill.State, displayVersions(skill.DiscoveredBy), display(skill.InstalledVersion), display(skill.AvailableVersion), skill.Recommendation)
			fmt.Fprintf(output, "    path=%s\n", skill.Path)
		}
	}
	if report.LocalProject != nil {
		_, _ = io.WriteString(output, "LOCAL PROJECT\n")
		project := report.LocalProject
		fmt.Fprintf(output, "  %s repository=%s binary=%s schema=%d service=%t registration=%s\n", project.Name, project.Repository, project.Binary, project.Schema, project.ServiceEnabled, project.RegistrationState)
		fmt.Fprintf(output, "    manifest=%s\n", project.ManifestPath)
	}
	if len(report.Warnings) != 0 {
		fmt.Fprintf(output, "WARNINGS (%d)\n", len(report.Warnings))
		for _, warning := range report.Warnings {
			fmt.Fprintf(output, "  %s: %s\n", warning.Component, warning.Message)
		}
	}
}

func display(value string) string {
	if value == "" {
		return "-"
	}
	return value
}

func displayVersions(values []string) string {
	if len(values) == 0 {
		return "-"
	}
	return strings.Join(values, ",")
}
