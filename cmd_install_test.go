package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// installCmd builds a mainCmd whose home directory is a temporary one, so the
// tests never touch the real ~/.claude or ~/.codex.
func installCmd(home string, stdout, stderr io.Writer) *mainCmd {
	return &mainCmd{
		Stdin:       bytes.NewReader(nil),
		Stdout:      stdout,
		Stderr:      stderr,
		Getwd:       os.Getwd,
		UserHomeDir: func() (string, error) { return home, nil },
	}
}

// TestEmbeddedSkills pins that the binary actually carries the skill tree.
//
// An embed pattern that matches nothing fails the build, but a directory that
// has been emptied or renamed underneath the pattern will still embed happily,
// so this asserts the files a user is meant to receive.
func TestEmbeddedSkills(t *testing.T) {
	t.Parallel()

	names, err := embeddedSkills()
	require.NoError(t, err)
	assert.Contains(t, names, "gopatch")

	body, err := _skillsFS.ReadFile(_skillsRoot + "/gopatch/SKILL.md")
	require.NoError(t, err, "the gopatch skill lost its SKILL.md")
	assert.Contains(t, string(body), "name: gopatch",
		"SKILL.md must carry the frontmatter agents key off")
	assert.Contains(t, string(body), "description:")

	for _, ref := range []string{"commands.md", "patches.md", "from-sed.md"} {
		_, err := _skillsFS.ReadFile(_skillsRoot + "/gopatch/references/" + ref)
		assert.NoErrorf(t, err, "reference %q is not embedded", ref)
	}
}

func TestInstallWritesDetectedAgentsOnly(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	// Claude and Codex are "installed"; Cursor is not.
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".claude"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".codex"), 0o755))

	var stdout bytes.Buffer
	err := installCmd(home, &stdout, io.Discard).Run([]string{"install"})
	require.NoError(t, err)

	for _, agent := range []string{".claude", ".codex"} {
		got, err := os.ReadFile(filepath.Join(home, agent, "skills", "gopatch", "SKILL.md"))
		require.NoErrorf(t, err, "skill not installed into %v", agent)

		want, err := _skillsFS.ReadFile(_skillsRoot + "/gopatch/SKILL.md")
		require.NoError(t, err)
		assert.Equal(t, string(want), string(got), "installed copy differs from the embedded one")

		_, err = os.Stat(filepath.Join(home, agent, "skills", "gopatch", "references", "commands.md"))
		assert.NoError(t, err, "references were not installed into %v", agent)
	}

	_, err = os.Stat(filepath.Join(home, ".cursor"))
	assert.True(t, os.IsNotExist(err),
		"an agent that is not installed must not have its directory created")

	assert.Contains(t, stdout.String(), "created")
}

// TestInstallIsIdempotent pins that a second run reports the files as unchanged
// rather than rewriting them, which is what makes it safe in a shell profile.
func TestInstallIsIdempotent(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".claude"), 0o755))

	require.NoError(t, installCmd(home, io.Discard, io.Discard).Run([]string{"install"}))

	var stdout bytes.Buffer
	require.NoError(t, installCmd(home, &stdout, io.Discard).Run([]string{"install"}))
	assert.Contains(t, stdout.String(), "unchanged")
	assert.NotContains(t, stdout.String(), "created")
}

// TestInstallOverwritesAModifiedCopy pins that the binary is the source of
// truth: an edited installed skill is brought back in line and reported as
// updated rather than silently left stale.
func TestInstallOverwritesAModifiedCopy(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".claude"), 0o755))
	require.NoError(t, installCmd(home, io.Discard, io.Discard).Run([]string{"install"}))

	skill := filepath.Join(home, ".claude", "skills", "gopatch", "SKILL.md")
	require.NoError(t, os.WriteFile(skill, []byte("stale\n"), 0o644))

	var stdout bytes.Buffer
	require.NoError(t, installCmd(home, &stdout, io.Discard).Run([]string{"install"}))
	assert.Contains(t, stdout.String(), "updated")

	got, err := os.ReadFile(skill)
	require.NoError(t, err)
	assert.NotEqual(t, "stale\n", string(got))
}

// TestInstallLeavesASymlinkAlone pins that a skill directory symlinked at a
// checkout is not written through. That is how a repository keeps its own skill
// live, and overwriting it would replace the working tree with whatever this
// binary was built from.
func TestInstallLeavesASymlinkAlone(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	checkout := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(checkout, "SKILL.md"), []byte("mine\n"), 0o644))

	link := filepath.Join(home, ".claude", "skills", "gopatch")
	require.NoError(t, os.MkdirAll(filepath.Dir(link), 0o755))
	require.NoError(t, os.Symlink(checkout, link))

	var stdout bytes.Buffer
	require.NoError(t, installCmd(home, &stdout, io.Discard).Run([]string{"install", "-a", "claude"}))

	assert.Contains(t, stdout.String(), "symlink, left alone")

	got, err := os.ReadFile(filepath.Join(checkout, "SKILL.md"))
	require.NoError(t, err)
	assert.Equal(t, "mine\n", string(got), "install wrote through the symlink")
}

func TestInstallDryRunWritesNothing(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".claude"), 0o755))

	var stdout bytes.Buffer
	require.NoError(t, installCmd(home, &stdout, io.Discard).Run([]string{"install", "--dry-run"}))

	assert.Contains(t, stdout.String(), "created")
	_, err := os.Stat(filepath.Join(home, ".claude", "skills"))
	assert.True(t, os.IsNotExist(err), "--dry-run wrote to disk")
}

func TestInstallExplicitAgent(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".claude"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".codex"), 0o755))

	require.NoError(t, installCmd(home, io.Discard, io.Discard).
		Run([]string{"install", "--agent", "codex"}))

	_, err := os.Stat(filepath.Join(home, ".codex", "skills", "gopatch", "SKILL.md"))
	assert.NoError(t, err)

	_, err = os.Stat(filepath.Join(home, ".claude", "skills"))
	assert.True(t, os.IsNotExist(err), "--agent must not write to other agents")
}

// TestInstallExplicitAgentNotDetected pins that naming an agent installs into
// it even when its directory is absent: an explicit request is not a guess.
func TestInstallExplicitAgentNotDetected(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	require.NoError(t, installCmd(home, io.Discard, io.Discard).
		Run([]string{"install", "--agent", "cursor"}))

	_, err := os.Stat(filepath.Join(home, ".cursor", "skills", "gopatch", "SKILL.md"))
	assert.NoError(t, err)
}

func TestInstallDir(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	dest := t.TempDir()

	require.NoError(t, installCmd(home, io.Discard, io.Discard).
		Run([]string{"install", "--dir", dest}))

	_, err := os.Stat(filepath.Join(dest, "gopatch", "SKILL.md"))
	assert.NoError(t, err)
}

func TestInstallErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc     string
		mkAgents []string
		give     []string
		wants    []string
	}{
		{
			desc:  "no agent detected",
			give:  []string{"install"},
			wants: []string{"no agent", ".claude", "--dir"},
		},
		{
			desc:  "unknown agent",
			give:  []string{"install", "--agent", "emacs"},
			wants: []string{"unknown agent", "emacs", "claude"},
		},
		{
			desc:     "unknown skill",
			mkAgents: []string{".claude"},
			give:     []string{"install", "nosuchskill"},
			wants:    []string{"no skill", "nosuchskill", "gopatch"},
		},
		{
			desc:     "--dir with --agent",
			mkAgents: []string{".claude"},
			give:     []string{"install", "--dir", "somewhere", "--agent", "claude"},
			wants:    []string{"--dir", "--agent"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()

			home := t.TempDir()
			for _, a := range tt.mkAgents {
				require.NoError(t, os.MkdirAll(filepath.Join(home, a), 0o755))
			}

			err := installCmd(home, io.Discard, io.Discard).Run(tt.give)
			require.Error(t, err)
			for _, want := range tt.wants {
				assert.Contains(t, err.Error(), want)
			}
		})
	}
}

func TestInstallList(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".claude"), 0o755))

	var stdout bytes.Buffer
	require.NoError(t, installCmd(home, &stdout, io.Discard).Run([]string{"install", "--list"}))

	got := stdout.String()
	assert.Contains(t, got, "gopatch")
	assert.Contains(t, got, "claude")
	assert.Contains(t, got, "codex")

	_, err := os.Stat(filepath.Join(home, ".claude", "skills"))
	assert.True(t, os.IsNotExist(err), "--list wrote to disk")
}

// TestInstallDisplaysHomeRelativePaths keeps the output readable: an agent
// directory under the user's home is reported as ~/... rather than as an
// absolute path nobody needs to read.
func TestInstallDisplaysHomeRelativePaths(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".claude"), 0o755))

	var stdout bytes.Buffer
	require.NoError(t, installCmd(home, &stdout, io.Discard).Run([]string{"install"}))

	got := stdout.String()
	assert.Contains(t, got, filepath.Join("~", ".claude", "skills", "gopatch"))
	assert.NotContains(t, got, home)
}

func TestInstallVerboseListsFiles(t *testing.T) {
	t.Parallel()

	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, ".claude"), 0o755))

	var stdout bytes.Buffer
	require.NoError(t, installCmd(home, &stdout, io.Discard).Run([]string{"install", "-v"}))

	got := stdout.String()
	assert.Contains(t, got, "SKILL.md")
	assert.Contains(t, got, filepath.Join("references", "commands.md"))
	assert.Greater(t, strings.Count(got, "\n"), 3, "-v should report each file")
}
