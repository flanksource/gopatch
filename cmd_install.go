package main

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const (
	_installAgentDesc = "Agent to install into, by id. Repeat for more than one. " +
		"Defaults to every agent with a configuration directory in your home."

	_installDirDesc = "Install into this directory instead of an agent's skills directory. " +
		"The skill is written to <dir>/<skill>."

	_installDryRunDesc = "Report what would be written without writing it."

	_installListDesc = "List the skills this binary carries and the agents detected here, " +
		"then exit."
)

// installOptions holds the values of the install command's flags.
type installOptions struct {
	Agents  []string
	Dir     string
	DryRun  bool
	List    bool
	Verbose bool
}

func (cmd *mainCmd) newInstallCommand() *cobra.Command {
	var opts installOptions

	c := &cobra.Command{
		Use:   "install [skill...]",
		Short: "Install gopatch's agent skills into your AI coding agents.",
		Long: "Install the agent skills that ship inside this binary.\n\n" +
			"A skill teaches an AI coding agent to reach for gopatch instead of " +
			"`sed -i` when it has to change Go code in more than one place. The " +
			"files are embedded in the binary, so this works from a `go install` " +
			"build with no checkout and no network.\n\n" +
			"With no arguments it installs every skill into every agent it finds a " +
			"configuration directory for, and re-running is safe: files that already " +
			"match are reported as unchanged. The binary is the source of truth, so " +
			"an installed skill that has been edited is put back.\n\n" +
			"Agent ids: " + strings.Join(skillTargetIDs(), ", ") + ".",
		Example: examples(
			"# install every skill into every agent detected here",
			"gopatch install",
			"",
			"# see what it would do first",
			"gopatch install --dry-run",
			"",
			"# just one agent, whether or not it was detected",
			"gopatch install --agent claude",
			"gopatch install -a claude -a codex",
			"",
			"# somewhere else entirely, such as a repository's own skills directory",
			"gopatch install --dir ./.agents/skills",
			"",
			"# what does this binary carry, and what is installed here?",
			"gopatch install --list",
		),

		Args: cobra.ArbitraryArgs,

		SilenceUsage:  true,
		SilenceErrors: true,

		DisableFlagsInUseLine: true,

		RunE: func(c *cobra.Command, args []string) error {
			return cmd.runInstall(&opts, args)
		},
	}

	f := c.Flags()
	f.StringArrayVarP(&opts.Agents, "agent", "a", nil, _installAgentDesc)
	f.StringVar(&opts.Dir, "dir", "", _installDirDesc)
	f.BoolVar(&opts.DryRun, "dry-run", false, _installDryRunDesc)
	f.BoolVar(&opts.List, "list", false, _installListDesc)
	f.BoolVarP(&opts.Verbose, "verbose", "v", false,
		"Report every file written rather than one line per agent.")

	return c
}

// runInstall writes the embedded skills into the requested destinations.
func (cmd *mainCmd) runInstall(opts *installOptions, args []string) error {
	available, err := embeddedSkills()
	if err != nil {
		return err
	}

	home, err := cmd.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}

	if opts.List {
		return cmd.listSkills(home, available)
	}

	skills, err := chooseSkills(args, available)
	if err != nil {
		return err
	}

	dests, err := chooseDestinations(opts, home)
	if err != nil {
		return err
	}

	for _, d := range dests {
		for _, skill := range skills {
			got, err := installSkill(skill, d, opts.DryRun)
			if err != nil {
				return err
			}
			cmd.reportInstall(home, got, opts.Verbose)
		}
	}

	if opts.DryRun {
		fmt.Fprintln(cmd.Stdout, "\n--dry-run: nothing was written.")
	}
	return nil
}

// chooseSkills resolves the positional arguments against what the binary
// carries, refusing a name it does not have rather than installing nothing and
// reporting success.
func chooseSkills(args, available []string) ([]string, error) {
	if len(args) == 0 {
		return available, nil
	}

	have := make(map[string]bool, len(available))
	for _, s := range available {
		have[s] = true
	}

	for _, name := range args {
		if !have[name] {
			return nil, fmt.Errorf(
				"no skill named %q in this binary; it carries: %s",
				name, strings.Join(available, ", "))
		}
	}
	return args, nil
}

// chooseDestinations resolves --dir and --agent into the directories that
// skills are written under.
func chooseDestinations(opts *installOptions, home string) ([]string, error) {
	if opts.Dir != "" {
		if len(opts.Agents) > 0 {
			return nil, fmt.Errorf(
				"--dir and --agent name different destinations; pass one or the other")
		}
		abs, err := filepath.Abs(opts.Dir)
		if err != nil {
			return nil, err
		}
		return []string{abs}, nil
	}

	// An agent named explicitly is installed into whether or not it was
	// detected: the user knows something the filesystem does not, such as an
	// agent they are about to install.
	if len(opts.Agents) > 0 {
		var dests []string
		for _, id := range opts.Agents {
			target, ok := lookupSkillTarget(id)
			if !ok {
				return nil, fmt.Errorf("unknown agent %q; known agents are: %s",
					id, strings.Join(skillTargetIDs(), ", "))
			}
			dests = append(dests, filepath.Join(home, target.SkillsDir))
		}
		return dests, nil
	}

	var dests []string
	for _, t := range skillTargets() {
		if t.detected(home) {
			dests = append(dests, filepath.Join(home, t.SkillsDir))
		}
	}
	if len(dests) == 0 {
		var looked []string
		for _, t := range skillTargets() {
			looked = append(looked, displayPath(home, filepath.Join(home, t.ConfigDir)))
		}
		return nil, fmt.Errorf(
			"no agent found on this machine; looked for %s\n"+
				"name one with --agent, or pass --dir to install somewhere of your choosing",
			strings.Join(looked, ", "))
	}
	return dests, nil
}

// listSkills reports what the binary carries and where it would go.
func (cmd *mainCmd) listSkills(home string, available []string) error {
	fmt.Fprintf(cmd.Stdout, "skills in this binary:\n")
	for _, s := range available {
		fmt.Fprintf(cmd.Stdout, "  %s\n", s)
	}

	fmt.Fprintf(cmd.Stdout, "\nagents:\n")
	for _, t := range skillTargets() {
		state := "not found"
		if t.detected(home) {
			state = "detected"
		}
		fmt.Fprintf(cmd.Stdout, "  %-10s %-12s %s\n",
			t.ID, state, displayPath(home, filepath.Join(home, t.SkillsDir)))
	}
	return nil
}

// reportInstall prints one line per skill, or one line per file under -v.
func (cmd *mainCmd) reportInstall(home string, got installedSkill, verbose bool) {
	dir := displayPath(home, got.Dir)

	if got.LinkedTo != "" {
		fmt.Fprintf(cmd.Stdout, "%s: symlink, left alone  %s -> %s\n",
			got.Skill, dir, displayPath(home, got.LinkedTo))
		return
	}

	fmt.Fprintf(cmd.Stdout, "%s: %s  %s\n", got.Skill, got.summary(), dir)

	if !verbose {
		return
	}
	for _, f := range got.Files {
		fmt.Fprintf(cmd.Stdout, "  %-9s %s\n", f.Action, filepath.Join(dir, f.Path))
	}
}
