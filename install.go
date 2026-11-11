package main

import (
	"bytes"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// _skillsFS carries the agent skills that ship with gopatch, so that a binary
// installed with `go install` can write them out without the repository.
//
// The `all:` prefix is required: the tree lives under a dot directory, which
// the default embed patterns skip.
//
//go:embed all:.agents/skills
var _skillsFS embed.FS

// _skillsRoot is where the skills live inside _skillsFS.
const _skillsRoot = ".agents/skills"

// skillTarget is one agent that reads skills from a directory on disk.
//
// Agents are identified by their configuration directory rather than by a
// binary on $PATH: an agent can be installed as an IDE extension or an app with
// nothing on the path, but it always has somewhere it keeps its configuration.
type skillTarget struct {
	// ID is what --agent takes.
	ID string

	// ConfigDir is the agent's configuration directory, relative to the user's
	// home. Its presence is how we decide the agent is installed here.
	ConfigDir string

	// SkillsDir is where that agent looks for skills, relative to the user's
	// home.
	SkillsDir string
}

// skillTargets lists the agents gopatch knows how to install into, in the order
// they are reported.
//
// "agents" is the client-agnostic ~/.agents/skills convention that several
// tools read, and is deliberately first: it is the one place a skill reaches
// more than one client.
func skillTargets() []skillTarget {
	return []skillTarget{
		{ID: "agents", ConfigDir: ".agents", SkillsDir: filepath.Join(".agents", "skills")},
		{ID: "claude", ConfigDir: ".claude", SkillsDir: filepath.Join(".claude", "skills")},
		{ID: "codex", ConfigDir: ".codex", SkillsDir: filepath.Join(".codex", "skills")},
		{ID: "cursor", ConfigDir: ".cursor", SkillsDir: filepath.Join(".cursor", "skills")},
		{ID: "gemini", ConfigDir: ".gemini", SkillsDir: filepath.Join(".gemini", "skills")},
		{
			ID:        "opencode",
			ConfigDir: filepath.Join(".config", "opencode"),
			SkillsDir: filepath.Join(".config", "opencode", "skills"),
		},
	}
}

func skillTargetIDs() []string {
	targets := skillTargets()
	ids := make([]string, len(targets))
	for i, t := range targets {
		ids[i] = t.ID
	}
	return ids
}

// lookupSkillTarget finds a target by ID.
func lookupSkillTarget(id string) (skillTarget, bool) {
	for _, t := range skillTargets() {
		if t.ID == id {
			return t, true
		}
	}
	return skillTarget{}, false
}

// detected reports whether the agent's configuration directory exists.
func (t skillTarget) detected(home string) bool {
	info, err := os.Stat(filepath.Join(home, t.ConfigDir))
	return err == nil && info.IsDir()
}

// fileAction is what installing one file did.
type fileAction string

const (
	actionCreated   fileAction = "created"
	actionUpdated   fileAction = "updated"
	actionUnchanged fileAction = "unchanged"
)

// installedFile is one file an install wrote, or would have written.
type installedFile struct {
	// Path is relative to the skill's own directory, so it reads the same for
	// every agent.
	Path   string
	Action fileAction
}

// installedSkill is one skill written into one directory.
type installedSkill struct {
	Skill string

	// Dir is the absolute path of the installed skill directory.
	Dir string

	// LinkedTo is set when Dir is a symlink, in which case nothing was written.
	// Linking a skill directory at a checkout is how a repository keeps its own
	// skill live, and writing through the link would overwrite that checkout
	// with whatever this binary happens to carry.
	LinkedTo string

	Files []installedFile
}

// summary describes what happened to the skill as a whole: every file created,
// every file unchanged, or a mix.
func (s installedSkill) summary() string {
	counts := map[fileAction]int{}
	for _, f := range s.Files {
		counts[f.Action]++
	}

	// Reported in decreasing order of interest: a single changed file in an
	// otherwise unchanged skill is the thing worth seeing.
	var parts []string
	for _, a := range []fileAction{actionCreated, actionUpdated, actionUnchanged} {
		if n := counts[a]; n > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n, a))
		}
	}
	return strings.Join(parts, ", ")
}

// embeddedSkills lists the skills this binary carries.
func embeddedSkills() ([]string, error) {
	entries, err := fs.ReadDir(_skillsFS, _skillsRoot)
	if err != nil {
		return nil, fmt.Errorf("read embedded skills: %w", err)
	}

	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

// installSkill copies one embedded skill into destDir/<skill>.
//
// Files are compared before they are written so that a re-run reports what it
// actually changed: the command is meant to be safe to leave in a shell profile
// or a Makefile, where "wrote 12 files" every time tells the user nothing.
func installSkill(skill, destDir string, dryRun bool) (installedSkill, error) {
	out := installedSkill{Skill: skill, Dir: filepath.Join(destDir, skill)}
	src := path.Join(_skillsRoot, skill)

	// A symlinked skill directory belongs to whatever it points at.
	if info, err := os.Lstat(out.Dir); err == nil && info.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(out.Dir)
		if err != nil {
			return installedSkill{}, fmt.Errorf("read link %v: %w", out.Dir, err)
		}
		out.LinkedTo = target
		return out, nil
	}

	err := fs.WalkDir(_skillsFS, src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}

		rel, err := filepath.Rel(filepath.FromSlash(src), filepath.FromSlash(p))
		if err != nil {
			return err
		}

		want, err := _skillsFS.ReadFile(p)
		if err != nil {
			return err
		}

		dest := filepath.Join(out.Dir, rel)
		action := actionCreated
		switch got, err := os.ReadFile(dest); {
		case err == nil && bytes.Equal(got, want):
			action = actionUnchanged
		case err == nil:
			action = actionUpdated
		case !os.IsNotExist(err):
			return fmt.Errorf("read %v: %w", dest, err)
		}

		out.Files = append(out.Files, installedFile{Path: rel, Action: action})

		if dryRun || action == actionUnchanged {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("create %v: %w", filepath.Dir(dest), err)
		}
		if err := os.WriteFile(dest, want, 0o644); err != nil {
			return fmt.Errorf("write %v: %w", dest, err)
		}
		return nil
	})
	if err != nil {
		return installedSkill{}, err
	}

	sort.Slice(out.Files, func(i, j int) bool { return out.Files[i].Path < out.Files[j].Path })
	return out, nil
}

// displayPath shortens a path under the user's home to ~/..., which is how the
// user thinks of these directories and keeps the output narrow enough to scan.
func displayPath(home, p string) string {
	if home == "" {
		return p
	}
	rel, err := filepath.Rel(home, p)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return p
	}
	return filepath.Join("~", rel)
}
