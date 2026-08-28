// Package commandmeta owns the installed Hextap command contract. Runtime
// flag registration, human help, and shell completion all consume this tree.
package commandmeta

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ValueKind selects the completion behavior for an option argument.
type ValueKind string

const (
	NoValue        ValueKind = ""
	StringValue    ValueKind = "string"
	DirectoryValue ValueKind = "directory"
	FileValue      ValueKind = "file"
	EnumValue      ValueKind = "enum"
)

// Choice is one documented and completable enum value.
type Choice struct {
	Name        string
	Description string
}

// Option describes one long flag and its required single-character alias.
type Option struct {
	Long        string
	Short       string
	ValueName   string
	Kind        ValueKind
	Description string
	Repeatable  bool
	Choices     []Choice
}

// Argument describes one positional argument.
type Argument struct {
	Name        string
	Description string
	Required    bool
	Repeatable  bool
}

// Example is one concrete invocation with an explanation.
type Example struct {
	Description string
	Command     string
}

// Command is one node in the installed Hextap command tree.
type Command struct {
	Name        string
	Summary     string
	Description string
	Arguments   []Argument
	Options     []Option
	Children    []Command
	Safety      []string
	Examples    []Example
}

var (
	helpOption = Option{
		Long:        "help",
		Short:       "h",
		Description: "Print this complete command help and exit successfully without performing any work.",
	}
	versionOption = Option{
		Long:        "version",
		Short:       "V",
		Description: "Print the installed Hextap version and exact source commit, then exit successfully.",
	}
	rootCommand = buildRoot()
)

// Root returns the authoritative installed command tree.
func Root() Command {
	return rootCommand
}

// Lookup resolves a command path. An empty path selects the root command.
func Lookup(path []string) (Command, bool) {
	current := rootCommand
	for _, name := range path {
		found := false
		for _, child := range current.Children {
			if child.Name == name {
				current = child
				found = true
				break
			}
		}
		if !found {
			return Command{}, false
		}
	}
	return current, true
}

// Help renders complete, invocation-aware help for one command path.
func Help(invocation string, path []string) string {
	command, ok := Lookup(path)
	if !ok {
		return ""
	}
	invocation = normalizeInvocation(invocation)
	fullCommand := strings.TrimSpace(strings.Join(append([]string{invocation}, path...), " "))

	var output strings.Builder
	output.WriteString("usage: ")
	output.WriteString(fullCommand)
	if len(command.Children) != 0 {
		output.WriteString(" <COMMAND>")
	}
	output.WriteString(" [OPTIONS]\n\n")

	output.WriteString("Purpose:\n  ")
	output.WriteString(command.Summary)
	output.WriteString("\n\n")
	output.WriteString("Description:\n  ")
	output.WriteString(command.Description)
	output.WriteString("\n\n")

	output.WriteString("Arguments:\n")
	if len(command.Arguments) == 0 && len(command.Children) == 0 {
		output.WriteString("  None. This command accepts options only.\n")
	} else {
		for _, argument := range command.Arguments {
			fmt.Fprintf(&output, "  %s", argument.Name)
			if argument.Repeatable {
				output.WriteString("...")
			}
			output.WriteByte('\n')
			qualifier := "Optional."
			if argument.Required {
				qualifier = "Required."
			}
			fmt.Fprintf(&output, "      %s %s\n", qualifier, argument.Description)
		}
		if len(command.Children) != 0 && len(command.Arguments) == 0 {
			output.WriteString("  COMMAND\n      Required. Select exactly one command listed below.\n")
		}
	}

	if len(command.Children) != 0 {
		output.WriteString("\nCommands:\n")
		for _, child := range command.Children {
			fmt.Fprintf(&output, "  %-12s %s\n", child.Name, child.Summary)
		}
	}

	output.WriteString("\nOptions:\n")
	for _, option := range command.Options {
		fmt.Fprintf(&output, "  -%s, --%s", option.Short, option.Long)
		if option.ValueName != "" {
			output.WriteByte(' ')
			output.WriteString(option.ValueName)
		}
		if option.Repeatable {
			output.WriteString(" (repeatable)")
		}
		output.WriteByte('\n')
		fmt.Fprintf(&output, "      %s\n", option.Description)
		if len(option.Choices) != 0 {
			output.WriteString("      Values:")
			for _, choice := range option.Choices {
				fmt.Fprintf(&output, " %s (%s);", choice.Name, choice.Description)
			}
			output.WriteByte('\n')
		}
	}

	output.WriteString("\nSafety:\n")
	for _, note := range command.Safety {
		fmt.Fprintf(&output, "  - %s\n", note)
	}

	output.WriteString("\nExamples:\n")
	for _, example := range command.Examples {
		fmt.Fprintf(&output, "  # %s\n", example.Description)
		fmt.Fprintf(&output, "  %s\n", strings.ReplaceAll(example.Command, "{command}", invocation))
	}
	return output.String()
}

func normalizeInvocation(invocation string) string {
	name := filepath.Base(strings.TrimSpace(invocation))
	if name == "." || name == "" {
		return "brew-hextap"
	}
	return name
}

func withHelp(options ...Option) []Option {
	return append(options, helpOption)
}

func commandArgument(description string) []Argument {
	return []Argument{{Name: "COMMAND", Description: description, Required: true}}
}

func buildRoot() Command {
	scopeChoices := []Choice{
		{Name: "user", Description: "operate on the selected user-level skill directory"},
		{Name: "project", Description: "operate on the Git top-level project skill directory"},
	}
	agentChoices := []Choice{
		{Name: "agents", Description: "shared .agents skill path discovered by agents, Codex, and Cursor"},
		{Name: "all", Description: "all nonredundant managed paths; requires overlap acknowledgement"},
		{Name: "claude-code", Description: "Claude Code user or project skill path"},
		{Name: "codex", Description: "Codex-compatible shared .agents skill path"},
		{Name: "cursor", Description: "Cursor-native skill path when it is not redundant"},
	}
	bumpChoices := []Choice{
		{Name: "patch", Description: "compatible correction"},
		{Name: "minor", Description: "backward-compatible feature"},
		{Name: "major", Description: "incompatible contract change"},
	}
	inventoryKindChoices := []Choice{
		{Name: "all", Description: "include every inventory category"},
		{Name: "project", Description: "include registered projects and an optional local project"},
		{Name: "formula", Description: "include Formulae registered in the Hextap tap"},
		{Name: "cask", Description: "include Casks registered in the Hextap tap"},
		{Name: "skill", Description: "include managed Hextap agent-skill installations"},
	}

	project := func(description string) Option {
		return Option{Long: "project", Short: "p", ValueName: "PATH", Kind: DirectoryValue, Description: description}
	}
	jsonOutput := func(description string) Option {
		return Option{Long: "json", Short: "j", Description: description}
	}
	agent := func(description string) Option {
		return Option{Long: "agent", Short: "a", ValueName: "ID", Kind: EnumValue, Description: description, Repeatable: true, Choices: agentChoices}
	}
	scope := func(description string) Option {
		return Option{Long: "scope", Short: "s", ValueName: "SCOPE", Kind: EnumValue, Description: description, Choices: scopeChoices}
	}
	dryRun := func(description string) Option {
		return Option{Long: "dry-run", Short: "n", Description: description}
	}
	overlap := Option{Long: "allow-overlapping-discovery", Short: "O", Description: "Acknowledge that selected agent targets share discovery roots and permit the documented nonredundant path resolution."}
	skillAgent := func(description string) Option {
		return Option{Long: "skill-agent", Short: "s", ValueName: "ID", Kind: EnumValue, Description: description, Repeatable: true, Choices: agentChoices}
	}
	bump := Option{Long: "bump", Short: "b", ValueName: "LEVEL", Kind: EnumValue, Description: "Select the required semantic-version increment from the latest immutable stable release.", Choices: bumpChoices}

	version := Command{
		Name:        "version",
		Summary:     "Print the installed build version and source commit",
		Description: "Reports the same immutable build identity as the global --version and -V flags. The displayed command name follows the executable used to invoke Hextap.",
		Options:     withHelp(),
		Safety:      []string{"Read-only; it performs no filesystem, network, Homebrew, Git, or service operations."},
		Examples: []Example{
			{Description: "Print the installed build identity", Command: "{command} version"},
			{Description: "Use the equivalent global shorthand", Command: "{command} -V"},
		},
	}

	status := Command{
		Name:        "status",
		Summary:     "Summarize local Hextap installations and registrations",
		Description: "Builds one offline, read-only snapshot of the running CLI, its owning Homebrew installation, the canonical Hextap tap, registered projects, Formulae, Casks, managed skills, and an optional local project. Missing components are reported as warnings instead of hiding partial results.",
		Options: withHelp(
			project("Inspect this local project manifest in addition to the system-wide Hextap inventory; omit it for system inventory only."),
			jsonOutput("Emit the complete versioned inventory document as JSON instead of the concise human summary."),
		),
		Safety: []string{"Read-only and offline by default; it disables Homebrew auto-update, API access, and analytics and never runs service operations.", "It reports environment-variable names where useful but never reads or prints their values, dependency stderr, or credentials."},
		Examples: []Example{
			{Description: "Summarize the local Hextap system", Command: "{command} status"},
			{Description: "Include the current project and emit versioned JSON", Command: "{command} status --project . --json"},
		},
	}

	info := Command{
		Name:        "info",
		Summary:     "Print detailed local Hextap inventory",
		Description: "Prints the full offline inventory with exact package, project, skill, tap, Git, version, installation, outdated, pinned, and safe service-definition metadata. Category and exact-name filters can narrow the report without changing the underlying collection contract.",
		Options: withHelp(
			project("Inspect this local project manifest in addition to the system-wide Hextap inventory; omit it for system inventory only."),
			Option{Long: "kind", Short: "k", ValueName: "KIND", Kind: EnumValue, Description: "Limit detailed output to one inventory category; defaults to all.", Choices: inventoryKindChoices},
			Option{Long: "name", Short: "n", ValueName: "NAME", Kind: StringValue, Description: "Require an exact project, Formula, Cask, or skill name after applying the category filter."},
			jsonOutput("Emit the filtered versioned inventory document as JSON instead of the detailed human report."),
		),
		Safety: []string{"Read-only and offline by default; it never updates Homebrew, contacts GitHub, starts services, or changes registrations.", "Unavailable dependencies produce generic warnings; credential values and dependency stderr are never exposed."},
		Examples: []Example{
			{Description: "Print every available local inventory detail", Command: "{command} info"},
			{Description: "Inspect one registered Formula", Command: "{command} info --kind formula --name hextap"},
			{Description: "Inspect managed skills as JSON", Command: "{command} info -k skill -j"},
		},
	}

	onboard := Command{
		Name:        "onboard",
		Summary:     "Plan or create deterministic local Hextap onboarding artifacts",
		Description: "Validates project identity and release metadata, then plans or writes the manifest, reusable workflow, release adapter, rulesets, tap-registration copy, and setup guide as one conflict-safe local transaction.",
		Options: withHelp(
			project("Select the Git project root to inspect and onboard; defaults to the current directory."),
			Option{Long: "repository", Short: "r", ValueName: "OWNER/REPO", Kind: StringValue, Description: "Require this exact canonical GitHub repository identity instead of relying only on the origin remote."},
			Option{Long: "formula", Short: "f", ValueName: "NAME", Kind: StringValue, Description: "Set the lowercase Homebrew Formula name; when omitted it is derived from the repository name."},
			Option{Long: "binary", Short: "b", ValueName: "NAME", Kind: StringValue, Description: "Set the installed executable basename; when omitted it is derived from the Formula name."},
			Option{Long: "description", Short: "d", ValueName: "TEXT", Kind: StringValue, Description: "Provide the required one-line Homebrew Formula description without control characters or Ruby interpolation."},
			Option{Long: "license", Short: "l", ValueName: "SPDX", Kind: StringValue, Description: "Provide the Formula license identifier, normally the repository's SPDX license expression."},
			Option{Long: "go-package", Short: "g", ValueName: "PACKAGE", Kind: StringValue, Description: "Select the narrow Go main package built by the generated adapter; defaults to an inferred package when unambiguous."},
			Option{Long: "version-symbol", Short: "v", ValueName: "SYMBOL", Kind: StringValue, Description: "Set the package-qualified Go variable injected with the normalized release version; defaults to main.version."},
			Option{Long: "commit-symbol", Short: "c", ValueName: "SYMBOL", Kind: StringValue, Description: "Set the package-qualified Go variable injected with the exact release commit; defaults to main.commit."},
			Option{Long: "toolkit-version", Short: "t", ValueName: "vX.Y.Z", Kind: StringValue, Description: "Pin generated artifacts to this stable Hextap toolkit tag; stable installed builds provide their own tag by default."},
			Option{Long: "toolkit-sha", Short: "s", ValueName: "FULL_SHA", Kind: StringValue, Description: "Pin the reusable workflow and toolkit provenance to this exact full source commit; release builds provide their own commit by default."},
			Option{Long: "linux", Short: "x", Description: "Include paired Linux arm64 and amd64 release archives; defaults to true and accepts Go boolean flag syntax such as --linux=false."},
			dryRun("Validate and print every CREATE, UNCHANGED, or VALIDATED action without writing any project file."),
			Option{Long: "required-check", Short: "R", ValueName: "CONTEXT", Kind: StringValue, Description: "Add one exact protected-branch status-check context to generated policy; specify the flag once per required check.", Repeatable: true},
		),
		Safety: []string{
			"Without --dry-run, this command writes only the documented project-local onboarding files and refuses conflicts atomically.",
			"It does not create repositories, rulesets, pull requests, tags, releases, tap entries, installations, or service changes.",
		},
		Examples: []Example{
			{Description: "Review a complete onboarding plan without writes", Command: "{command} onboard --project . --description \"Example CLI\" --license MIT --required-check test --dry-run"},
			{Description: "Apply the reviewed plan with explicit immutable toolkit provenance", Command: "{command} onboard -p . -d \"Example CLI\" -l MIT -R test -t v0.4.2 -s FULL_TOOLKIT_SHA"},
		},
	}

	validate := Command{
		Name:        "validate",
		Summary:     "Validate local onboarding artifacts and optionally smoke-build archives",
		Description: "Checks the manifest, generated files, exact pins, repository identity, and local release contract. The optional build gate executes the trusted project adapter and verifies its complete archive set.",
		Options: withHelp(
			project("Select the onboarded Git project root; defaults to the current directory."),
			Option{Long: "build", Short: "b", Description: "Execute the project's trusted build adapter and perform bounded binary and archive smoke verification after structural validation."},
		),
		Safety: []string{"Default validation is read-only.", "--build executes trusted project code in a bounded temporary release build and must not be used on untrusted repositories."},
		Examples: []Example{
			{Description: "Run offline structural validation", Command: "{command} validate --project ."},
			{Description: "Run structural validation plus trusted build smoke", Command: "{command} validate -p . -b"},
		},
	}

	doctor := Command{
		Name:        "doctor",
		Summary:     "Check local prerequisites and optionally inspect GitHub read-only",
		Description: "Reports each satisfied prerequisite for an onboarded project. Online mode adds bounded GitHub identity, policy, tag, release, registration, and Formula contract checks without remote mutation.",
		Options: withHelp(
			project("Select the onboarded Git project root; defaults to the current directory."),
			Option{Long: "online", Short: "o", Description: "Add authenticated, bounded, read-only GitHub checks for the canonical repository, release, rulesets, tap registration, and Formula."},
		),
		Safety: []string{"Local mode is read-only and makes no GitHub calls.", "--online performs bounded read-only GitHub access; it never changes rulesets, secrets, releases, tags, tap files, or services."},
		Examples: []Example{
			{Description: "Check local prerequisites only", Command: "{command} doctor -p ."},
			{Description: "Add bounded read-only GitHub verification", Command: "{command} doctor --project . --online"},
		},
	}

	skillsInstall := Command{
		Name:        "install",
		Summary:     "Install the bundled Hextap skill into explicit agent targets",
		Description: "Resolves reviewed user or project skill directories, detects shared discovery roots, and creates only absent Hextap-managed bundles for the explicitly selected targets.",
		Options: withHelp(
			agent("Select an agent target to install; repeat for multiple concrete targets or use all by itself."),
			scope("Require the installation scope; user writes below the home directory and project writes below the Git top-level."),
			project("Resolve project scope from this path's Git top-level; defaults to the current directory and is ignored for user scope."),
			dryRun("Print the exact target actions and paths without creating or changing any skill bundle."),
			overlap,
		),
		Safety: []string{"This command can create local managed skill files; use --dry-run to review exact paths first.", "It never overwrites unmanaged, drifted, different-version, or newer skill content."},
		Examples: []Example{
			{Description: "Review a user-scoped Claude Code installation", Command: "{command} skills install -a claude-code -s user -n"},
			{Description: "Install into the project-scoped shared agents path", Command: "{command} skills install --agent agents --scope project --project ."},
		},
	}

	skillsStatus := Command{
		Name:        "status",
		Summary:     "Inspect managed Hextap skill installations",
		Description: "Inventories concrete managed skill paths, discovery coverage, installed and available versions, integrity state, and the recommended nonmutating follow-up for user or project scope.",
		Options: withHelp(
			agent("Limit inventory to one or more agent targets; when omitted, all concrete managed paths are inspected."),
			scope("Select user or project inspection scope; defaults to user."),
			project("Resolve project scope from this path's Git top-level; defaults to the current directory and is ignored for user scope."),
			jsonOutput("Emit the complete inventory as stable, versioned JSON instead of the human-readable records."),
		),
		Safety: []string{"Read-only; it does not create, upgrade, repair, remove, or rewrite any skill path."},
		Examples: []Example{
			{Description: "Inspect every user-scoped managed path", Command: "{command} skills status"},
			{Description: "Inspect one project target as JSON", Command: "{command} skills status -a codex -s project -p . -j"},
		},
	}

	skillsTargets := Command{
		Name:        "targets",
		Summary:     "List supported agent target identifiers and paths",
		Description: "Prints the stable identifiers accepted by skill install, status, and upgrade, including whether a target is virtual and its user and project directory conventions.",
		Options:     withHelp(),
		Safety:      []string{"Read-only; it reads the built-in target registry and performs no filesystem discovery or mutation."},
		Examples: []Example{
			{Description: "List every supported skill target", Command: "{command} skills targets"},
			{Description: "Review detailed target help", Command: "{command} skills targets -h"},
		},
	}

	skillsUpgrade := Command{
		Name:        "upgrade",
		Summary:     "Upgrade intact managed Hextap skill bundles",
		Description: "Moves an intact older marker-owned bundle forward to the version embedded in this CLI, preserving the exact previous bundle in a reported recovery path before publication.",
		Options: withHelp(
			agent("Select an agent target to upgrade; repeat for multiple concrete targets or use all by itself."),
			scope("Require the upgrade scope: user or project."),
			project("Resolve project scope from this path's Git top-level; defaults to the current directory and is ignored for user scope."),
			dryRun("Report eligible upgrades, versions, and paths without changing any bundle."),
			overlap,
		),
		Safety: []string{"This command mutates only intact, older, marker-owned bundles and retains a recovery copy.", "It refuses newer, drifted, unmanaged, invalid, or same-version-different content instead of replacing it."},
		Examples: []Example{
			{Description: "Review a user-scoped upgrade", Command: "{command} skills upgrade -a claude-code -s user -n"},
			{Description: "Apply the reviewed upgrade", Command: "{command} skills upgrade --agent claude-code --scope user"},
		},
	}

	skills := Command{
		Name:        "skills",
		Summary:     "Inspect and manage bundled Hextap agent skills",
		Description: "Provides explicit, path-aware inventory, installation, and forward-upgrade operations for the portable Hextap skill bundle across supported agent discovery conventions.",
		Arguments:   commandArgument("Choose install, status, targets, or upgrade."),
		Options:     withHelp(),
		Children:    []Command{skillsInstall, skillsStatus, skillsTargets, skillsUpgrade},
		Safety:      []string{"status and targets are read-only; install and upgrade can mutate only the explicitly selected local skill paths.", "No skills command changes Homebrew, GitHub, repositories, services, proxies, or endpoint configuration."},
		Examples: []Example{
			{Description: "Inspect all user skill installations", Command: "{command} skills status"},
			{Description: "Review an installation without writes", Command: "{command} skills install --agent claude-code --scope user --dry-run"},
		},
	}

	devStatus := Command{
		Name:        "status",
		Summary:     "Inspect toolkit repository and release state",
		Description: "Validates the canonical toolkit checkout and writable remotes, then reports branch, head, cleanliness, authenticated GitHub owner, latest immutable stable release, next semantic versions, and installed CLI identity.",
		Options: withHelp(
			project("Select the Hextap toolkit Git checkout to inspect; defaults to the current directory."),
			jsonOutput("Emit the complete developer status as stable, versioned JSON."),
		),
		Safety: []string{"Read-only; it inspects local Git and bounded GitHub release metadata without modifying either."},
		Examples: []Example{
			{Description: "Inspect a toolkit checkout", Command: "{command} dev status -p ."},
			{Description: "Emit machine-readable status", Command: "{command} dev status --project . --json"},
		},
	}

	devValidate := Command{
		Name:        "validate",
		Summary:     "Run local toolkit validation gates",
		Description: "Runs formatting, tests, vetting, builds, shell checks, workflow validation, Git diff checks, and worktree mutation detection. Full mode includes the Go race detector.",
		Options: withHelp(
			project("Select the Hextap toolkit Git checkout to validate; defaults to the current directory."),
			Option{Long: "quick", Short: "q", Description: "Skip the race detector for a faster iteration gate while retaining the remaining deterministic checks."},
			jsonOutput("Emit the validation result as stable, versioned JSON."),
		),
		Safety: []string{"Executes trusted toolkit source and local developer tools.", "It snapshots Git-visible state and fails if validation changes the working tree."},
		Examples: []Example{
			{Description: "Run the full local quality gate", Command: "{command} dev validate --project ."},
			{Description: "Run the quick iteration gate", Command: "{command} dev validate -p . -q"},
		},
	}

	devPlan := Command{
		Name:        "plan",
		Summary:     "Compute and display an exact next release",
		Description: "Reads canonical toolkit and immutable release state, applies the selected semantic-version increment, and prints the exact commit, tag, and confirmation arguments required by release or deploy.",
		Options: withHelp(
			project("Select the clean Hextap toolkit Git checkout to plan from; defaults to the current directory."),
			bump,
			jsonOutput("Emit the complete release plan as stable, versioned JSON."),
		),
		Safety: []string{"Read-only; planning does not push, open or merge a pull request, create a tag or release, update the tap, or install anything."},
		Examples: []Example{
			{Description: "Plan the next backward-compatible feature release", Command: "{command} dev plan -p . -b minor"},
			{Description: "Emit the plan as JSON", Command: "{command} dev plan --project . --bump patch --json"},
		},
	}

	confirmTag := Option{Long: "confirm-tag", Short: "c", ValueName: "vX.Y.Z", Kind: StringValue, Description: "Confirm the exact freshly computed release tag; stale or mismatched confirmations fail before mutation."}
	executeRelease := Option{Long: "execute", Short: "e", Description: "Explicitly authorize the command's documented remote release mutations after all preconditions pass."}
	installAfter := Option{Long: "install", Short: "i", Description: "After complete remote proof, upgrade and verify only the local Hextap Formula and any explicitly selected managed skills."}

	devRelease := Command{
		Name:        "release",
		Summary:     "Publish a confirmed release from clean canonical main",
		Description: "Revalidates a clean canonical main checkout, requires an exact fresh semantic-version confirmation, creates or reuses only that annotated tag, verifies the immutable release, and requires the release workflow's Homebrew gate.",
		Options: withHelp(
			project("Select the canonical Hextap toolkit checkout; defaults to the current directory."),
			bump,
			confirmTag,
			executeRelease,
			installAfter,
			skillAgent("Select a concrete user-scoped managed skill target to reconcile after an explicitly requested local installation."),
			jsonOutput("Emit versioned release evidence as JSON."),
		),
		Safety: []string{"Remote mutation requires both --execute and the exact --confirm-tag value from a fresh plan.", "Without --install it does not change local Homebrew or skills; it never changes services, proxies, certificates, ports, or endpoints."},
		Examples: []Example{
			{Description: "Publish the exact freshly planned release", Command: "{command} dev release -p . -b minor -c vNEXT -e"},
			{Description: "Publish and then reconcile one local skill target", Command: "{command} dev release --project . --bump patch --confirm-tag vNEXT --execute --install --skill-agent claude-code"},
		},
	}

	devDeploy := Command{
		Name:        "deploy",
		Summary:     "Run the protected pull-request and release workflow",
		Description: "Validates and pushes only the current feature branch, creates or reuses its exact pull request, waits for updated-head checks and protected merge, proves merged-main CI, then performs the same confirmed immutable release and optional local installation gates.",
		Options: withHelp(
			project("Select the Hextap toolkit feature-branch checkout; defaults to the current directory."),
			bump,
			confirmTag,
			Option{Long: "execute", Short: "e", Description: "Explicitly authorize the protected branch push, pull-request, merge, tag, release, and tap workflow after every gate passes."},
			installAfter,
			Option{Long: "pr-title", Short: "t", ValueName: "TEXT", Kind: StringValue, Description: "Set the pull-request title; when omitted, use the latest commit subject."},
			skillAgent("Select a concrete user-scoped managed skill target to reconcile after an explicitly requested local installation."),
			jsonOutput("Emit versioned deployment and release evidence as JSON."),
		),
		Safety: []string{"Remote mutation requires --execute plus the exact fresh --confirm-tag and never uses admin or auto-merge bypass.", "It stops on review feedback, unresolved threads, absent checks, dirty state, stale tags, or incomplete immutable-release and tap evidence."},
		Examples: []Example{
			{Description: "Deploy a reviewed feature as the next minor release", Command: "{command} dev deploy -p . -b minor -c vNEXT -e"},
			{Description: "Use an explicit pull-request title and JSON evidence", Command: "{command} dev deploy --project . --bump patch --confirm-tag vNEXT --execute --pr-title \"fix: release contract\" --json"},
		},
	}

	devInstall := Command{
		Name:        "install",
		Summary:     "Install and verify one immutable Hextap release",
		Description: "Selects the Homebrew installation that owns the active brew-hextap, updates only Hextap tap metadata, upgrades only sean/hextap/hextap, verifies exact version and commit, runs the Formula test, and optionally reconciles named managed skill targets.",
		Options: withHelp(
			project("Select the Hextap toolkit checkout used to verify release metadata; defaults to the current directory."),
			Option{Long: "tag", Short: "t", ValueName: "vX.Y.Z", Kind: StringValue, Description: "Select the exact published stable immutable release tag to install."},
			Option{Long: "commit", Short: "c", ValueName: "FULL_SHA", Kind: StringValue, Description: "Require the installed CLI to report this exact full released source commit."},
			Option{Long: "execute", Short: "e", Description: "Explicitly authorize the local Hextap Formula and selected skill mutations after release proof passes."},
			skillAgent("Select a concrete user-scoped managed skill target to reconcile after the Formula is verified."),
			jsonOutput("Emit versioned installation evidence as JSON."),
		),
		Safety: []string{"Without --execute it fails before local mutation.", "It changes only the Hextap Formula and explicitly selected managed skill copies; it never invokes brew services or alters another Formula, proxy, certificate, port, or runtime."},
		Examples: []Example{
			{Description: "Install one exact immutable release", Command: "{command} dev install -p . -t vX.Y.Z -c FULL_RELEASE_SHA -e"},
			{Description: "Install and reconcile a Codex-managed skill", Command: "{command} dev install --project . --tag vX.Y.Z --commit FULL_RELEASE_SHA --execute --skill-agent codex"},
		},
	}

	dev := Command{
		Name:        "dev",
		Summary:     "Develop, validate, release, and install Hextap itself",
		Description: "Provides the protected toolkit-maintainer workflow from read-only status and planning through local validation, pull-request deployment, immutable release proof, and tightly scoped optional installation.",
		Arguments:   commandArgument("Choose status, validate, plan, release, deploy, or install."),
		Options:     withHelp(),
		Children:    []Command{devStatus, devValidate, devPlan, devRelease, devDeploy, devInstall},
		Safety:      []string{"status and plan are read-only; validate executes trusted toolkit code; release, deploy, and install require explicit execution authorization.", "No developer command has standing permission to change services, proxies, certificates, ports, or endpoint configuration."},
		Examples: []Example{
			{Description: "Inspect current toolkit state", Command: "{command} dev status --project ."},
			{Description: "Run the full local validation gate", Command: "{command} dev validate --project ."},
		},
	}

	completionZsh := Command{
		Name:        "zsh",
		Summary:     "Print the native Zsh completion definition",
		Description: "Generates a compinit-compatible #compdef function for both hextap and brew-hextap from the same authoritative command metadata used by every help page.",
		Options:     withHelp(),
		Safety:      []string{"Read-only; it writes the completion definition only to standard output and does not install or source it."},
		Examples: []Example{
			{Description: "Inspect the generated completion", Command: "{command} completion zsh"},
			{Description: "Install manually into a user completion directory", Command: "{command} completion zsh > ~/.zfunc/_hextap"},
		},
	}

	completion := Command{
		Name:        "completion",
		Summary:     "Generate native shell completion definitions",
		Description: "Selects an agent-neutral shell format and emits a deterministic completion definition derived from the installed command metadata, including nested commands, aliases, values, descriptions, paths, and repeatable options.",
		Arguments:   commandArgument("Choose the shell completion format; currently zsh."),
		Options:     withHelp(),
		Children:    []Command{completionZsh},
		Safety:      []string{"Read-only; generation prints to standard output and never edits shell startup files or completion directories."},
		Examples: []Example{
			{Description: "Generate native Zsh completion", Command: "{command} completion zsh"},
			{Description: "Read completion-specific help", Command: "{command} completion zsh --help"},
		},
	}

	return Command{
		Summary:     "Deterministic onboarding, validation, release, and installation for Hextap projects",
		Description: "Hextap provides one versioned command surface for project onboarding, offline and online validation, managed agent skills, toolkit development, and shell integration. The same binary supports direct hextap and Homebrew external-command brew hextap invocation.",
		Arguments:   commandArgument("Choose one top-level operation."),
		Options:     []Option{helpOption, versionOption},
		Children:    []Command{version, status, info, onboard, validate, doctor, skills, dev, completion},
		Safety:      []string{"Help, version, completion output, status, targets, default validation, and default doctor paths are read-only.", "Commands that write locally, execute trusted code, access GitHub, or mutate releases clearly identify that boundary in their own help and require their documented authorization flags."},
		Examples: []Example{
			{Description: "Show the complete top-level command map", Command: "{command} --help"},
			{Description: "Run bounded read-only project diagnostics", Command: "{command} doctor --project ."},
		},
	}
}
