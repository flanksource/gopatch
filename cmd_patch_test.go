package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPatchCommandHelp mirrors TestRootCommandHelp for the subcommand that now
// owns patch application: every flag description has to stay reachable from the
// help a user is pointed at.
func TestPatchCommandHelp(t *testing.T) {
	t.Parallel()

	root, _ := discardCmd().newRootCommand()
	patch, _, err := root.Find([]string{"patch"})
	require.NoError(t, err)
	require.Equal(t, "patch", patch.Name())

	got := patch.UsageString() + patch.Long
	wants := []string{
		"--patch", "--patches-file", "--diff", "--print-only",
		"--skip-import-processing", "--skip-generated", "--verbose",
		"Multiple patches may be provided",
		"Each file must be listed on its own line",
		"One or more files or directores containing Go code",

		// The patch language now lives in the binary rather than behind a path.
		"gopatch help patches",
		"gopatch help metavariables",
		"gopatch help examples",
		"must match the same code both times",
		"only the first instance in a given scope is replaced",
	}
	for _, want := range wants {
		assert.Contains(t, got, want)
	}

	assert.NotContains(t, got, "docs/",
		"help must not name a file the binary does not carry")
}

// TestPatchHelpStaysReadable bounds the Long text. Cobra prints it before the
// Usage, Examples and Flags blocks, so unbounded growth pushes -p and --diff
// off the first screen — which is the failure the topics exist to avoid.
func TestPatchHelpStaysReadable(t *testing.T) {
	t.Parallel()

	const maxLines = 34

	got := strings.Count(_patchDescLong, "\n") + 1
	assert.LessOrEqualf(t, got, maxLines,
		"patch --help Long is %d lines; move detail into `gopatch help patches`", got)
}

func TestPatchCommandApplies(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	cmd := mainCmd{
		Stdin:  bytes.NewReader(nil),
		Stdout: &stdout,
		Stderr: &stderr,
		Getwd:  os.Getwd,
	}
	err := cmd.Run([]string{
		"patch", "-d",
		"-p", "testdata/patch/error.patch",
		"testdata/test_files/diff_example/error.go",
	})
	require.NoError(t, err)

	assert.Contains(t, stdout.String(), "fmt.Errorf")
	assert.NotContains(t, stderr.String(), "deprecated",
		"the supported spelling must not warn")
}

// TestRootPatchIsDeprecated pins the shim: the root still applies patches, and
// says on stderr that it will stop. The warning must not reach stdout, which
// carries --diff output that gets piped into other tools.
func TestRootPatchIsDeprecated(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	cmd := mainCmd{
		Stdin:  bytes.NewReader(nil),
		Stdout: &stdout,
		Stderr: &stderr,
		Getwd:  os.Getwd,
	}
	err := cmd.Run([]string{
		"-d",
		"-p", "testdata/patch/error.patch",
		"testdata/test_files/diff_example/error.go",
	})
	require.NoError(t, err)

	assert.Contains(t, stdout.String(), "fmt.Errorf", "the shim must still apply the patch")
	assert.NotContains(t, stdout.String(), "deprecated", "the warning belongs on stderr")

	warning := stderr.String()
	assert.Contains(t, warning, "deprecated")
	assert.Contains(t, warning, _rootPatchRemovalVersion)
	assert.Contains(t, warning, "gopatch patch")
}

// TestRootWithoutPatternsDoesNotWarn keeps `gopatch` on its own — a user
// looking for help — from being told it is using something deprecated.
func TestRootWithoutPatternsDoesNotWarn(t *testing.T) {
	t.Parallel()

	var stderr bytes.Buffer
	cmd := mainCmd{
		Stdin:  bytes.NewReader(nil),
		Stdout: io.Discard,
		Stderr: &stderr,
		Getwd:  os.Getwd,
	}
	err := cmd.Run(nil)
	require.Error(t, err, "no patterns is still an error")
	assert.NotContains(t, stderr.String(), "deprecated")
}
