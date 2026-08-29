package inventory

import (
	"fmt"
	"io"
	"strings"
)

// RenderStatus writes a concise human-readable overview of one report.
func RenderStatus(output io.Writer, report Report) {
	fmt.Fprintf(output, "HEXTAP version=%s commit=%s executable=%s\n", display(report.CLI.Version), display(report.CLI.Commit), display(report.CLI.Executable))
	fmt.Fprintf(output, "HOMEBREW executable=%s prefix=%s\n", display(report.Homebrew.Executable), display(report.Homebrew.Prefix))
	fmt.Fprintf(output, "TAP name=%s installed=%t path=%s revision=%s branch=%s remote=%s\n", report.Tap.Name, report.Tap.Installed, display(report.Tap.Path), display(report.Tap.Revision), display(report.Tap.Branch), display(report.Tap.Remote))
	installedFormulae := 0
	for _, formula := range report.Formulae {
		if formula.Installed {
			installedFormulae++
		}
	}
	installedCasks := 0
	for _, cask := range report.Casks {
		if cask.Installed {
			installedCasks++
		}
	}
	currentSkills := 0
	for _, skill := range report.Skills {
		if skill.State == "CURRENT" {
			currentSkills++
		}
	}
	fmt.Fprintf(output, "INVENTORY projects=%d formulae=%d formulae_installed=%d casks=%d casks_installed=%d skills=%d skills_current=%d warnings=%d\n", len(report.Projects), len(report.Formulae), installedFormulae, len(report.Casks), installedCasks, len(report.Skills), currentSkills, len(report.Warnings))
	if report.LocalProject != nil {
		fmt.Fprintf(output, "PROJECT name=%s repository=%s registration=%s manifest=%s\n", report.LocalProject.Name, report.LocalProject.Repository, report.LocalProject.RegistrationState, report.LocalProject.ManifestPath)
	}
	for _, warning := range report.Warnings {
		fmt.Fprintf(output, "WARNING %s: %s\n", warning.Component, warning.Message)
	}
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
