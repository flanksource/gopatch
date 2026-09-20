package main

import (
	"bytes"
	"io"
	"os"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// _docTopics is what the tests below expect to find, with a sentinel string
// that only that topic's source document contains.
var _docTopics = []struct {
	name     string
	sentinel string
}{
	{"patches", "Metavariables"},
	{"metavariables", "identifiers"},
	{"examples", "time.Since"},
}

func docsCmd(stdout, stderr io.Writer) *mainCmd {
	return &mainCmd{
		Stdin:  bytes.NewReader(nil),
		Stdout: stdout,
		Stderr: stderr,
		Getwd:  os.Getwd,
	}
}

// TestDocTopicsAreRegistered pins that each topic resolves and is a cobra help
// topic rather than a runnable command, which is what makes its help print the
// document alone with no usage footer.
func TestDocTopicsAreRegistered(t *testing.T) {
	t.Parallel()

	for _, tt := range _docTopics {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root, _ := discardCmd().newRootCommand()
			topic, _, err := root.Find([]string{tt.name})
			require.NoError(t, err)
			require.Equal(t, tt.name, topic.Name())

			assert.True(t, topic.IsAdditionalHelpTopicCommand(),
				"%v must be a help topic, not a runnable command", tt.name)
			assert.NotEmpty(t, topic.Short)
			assert.Contains(t, topic.Long, tt.sentinel)
			assert.Greater(t, len(topic.Long), 1000,
				"%v looks truncated; the embed may have lost its content", tt.name)
		})
	}
}

func TestDocTopicPrints(t *testing.T) {
	t.Parallel()

	for _, tt := range _docTopics {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout, stderr bytes.Buffer
			require.NoError(t, docsCmd(&stdout, &stderr).Run([]string{"help", tt.name}))

			got := stdout.String()
			assert.Contains(t, got, tt.sentinel)
			assert.Empty(t, stderr.String())
			assert.NotContains(t, got, "Usage:",
				"a help topic must print the document alone")
			assert.NotContains(t, got, "Flags:")
		})
	}
}

// TestDocTopicPrintsNoMarkup pins the terminal transform and the conversion of
// the HTML tables that used to be in docs/PatchesInDepth.md. It is what fails
// if an upstream merge reintroduces one.
func TestDocTopicPrintsNoMarkup(t *testing.T) {
	t.Parallel()

	// Reference-style link definitions, which are addresses rather than prose.
	refDef := regexp.MustCompile(`(?m)^\s*\[[^\]]+\]:\s+\S`)

	for _, tt := range _docTopics {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout bytes.Buffer
			require.NoError(t, docsCmd(&stdout, io.Discard).Run([]string{"help", tt.name}))

			got := stdout.String()
			for _, unwanted := range []string{"<table", "<tr>", "<td>", "<thead", "</tbody", "<a name=", "](#"} {
				assert.NotContainsf(t, got, unwanted,
					"%v prints raw markup: %q", tt.name, unwanted)
			}
			assert.NotRegexp(t, refDef, got, "%v prints a link definition", tt.name)
		})
	}
}

// TestDocTopicFitsATerminal pins the claim that these documents can be printed
// without a wrapping library: they are hard-wrapped under 100 columns at source,
// and this fails when a doc edit stops being.
func TestDocTopicFitsATerminal(t *testing.T) {
	t.Parallel()

	const maxWidth = 100

	for _, tt := range _docTopics {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var stdout bytes.Buffer
			require.NoError(t, docsCmd(&stdout, io.Discard).Run([]string{"help", tt.name}))

			for i, line := range strings.Split(stdout.String(), "\n") {
				// Columns are runes: these documents contain em-dashes, which
				// len() would count as three.
				width := utf8.RuneCountInString(line)
				assert.LessOrEqualf(t, width, maxWidth,
					"%v line %d is %d columns:\n%s", tt.name, i+1, width, line)
			}
		})
	}
}

// TestExamplesTopicCarriesEveryPatch pins that the topic renders all five
// shipped patches, byte-for-byte as they are on disk. They are the patches the
// e2e suite runs, so what a user is shown is known to apply.
func TestExamplesTopicCarriesEveryPatch(t *testing.T) {
	t.Parallel()

	var stdout bytes.Buffer
	require.NoError(t, docsCmd(&stdout, io.Discard).Run([]string{"help", "examples"}))
	got := stdout.String()

	patches := examplePatches()
	require.Len(t, patches, 5)

	for _, ex := range patches {
		body, err := _examplesFS.ReadFile(ex.File)
		require.NoErrorf(t, err, "%v is not embedded", ex.File)

		onDisk, err := os.ReadFile(ex.File)
		require.NoError(t, err)
		assert.Equal(t, string(onDisk), string(body),
			"the embedded %v has drifted from the file on disk", ex.File)

		// The gloss is indented under its heading, so it is checked a line at a
		// time rather than as one block.
		for _, line := range strings.Split(ex.Gloss, "\n") {
			assert.Containsf(t, got, line, "%v: gloss line missing: %q", ex.File, line)
		}
		for _, line := range strings.Split(strings.TrimRight(string(body), "\n"), "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			assert.Containsf(t, got, line, "%v: line missing from the topic: %q", ex.File, line)
		}
	}
}

// TestNoHelpTextPointsOutsideTheBinary is the regression test for the bug this
// change fixes: help that names a file in the repository is useless to anyone
// who installed a binary. Every documentation path named anywhere in the
// command tree has to be one the binary carries.
func TestNoHelpTextPointsOutsideTheBinary(t *testing.T) {
	t.Parallel()

	pathRe := regexp.MustCompile(`(docs|examples)/[\w.\-/]+\.(md|patch)`)

	var walk func(*cobra.Command)
	walk = func(c *cobra.Command) {
		for _, text := range []string{c.Short, c.Long, c.Example} {
			for _, m := range pathRe.FindAllString(text, -1) {
				_, docErr := _docsFS.ReadFile(m)
				_, exErr := _examplesFS.ReadFile(m)
				assert.Truef(t, docErr == nil || exErr == nil,
					"%q help names %v, which is not embedded; a user who ran "+
						"`go install` has no such file", c.CommandPath(), m)
			}
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(mustRootCommand(t))
}

// TestTopicNamesDoNotShadowPaths pins the one hazard of naming topics after
// plausible directories.
//
// Under `gopatch patch` a positional is always a path, because that command has
// no children for cobra to resolve against — which is the supported spelling.
// The root command does resolve it as a topic, so a directory named "examples"
// needs the "--" separator there. That only affects the deprecated spelling.
func TestTopicNamesDoNotShadowPaths(t *testing.T) {
	t.Parallel()

	t.Run("patch subcommand treats it as a path", func(t *testing.T) {
		t.Parallel()

		var stdout bytes.Buffer
		err := docsCmd(&stdout, io.Discard).Run([]string{
			"patch", "-d", "-p", "testdata/patch/error.patch", "examples",
		})
		require.NoError(t, err)
		assert.Empty(t, stdout.String(), "examples/ holds no Go files")
	})

	t.Run("root needs the -- separator", func(t *testing.T) {
		t.Parallel()

		var stdout bytes.Buffer
		err := docsCmd(&stdout, io.Discard).Run([]string{
			"-d", "-p", "testdata/patch/error.patch", "--", "examples",
		})
		require.NoError(t, err)
		assert.Empty(t, stdout.String())
	})
}

// TestRootHasTopLevelCommands pins root's children, which TestSubcommandTree
// deliberately does not cover.
func TestRootHasTopLevelCommands(t *testing.T) {
	t.Parallel()

	root, _ := discardCmd().newRootCommand()

	var got []string
	for _, c := range root.Commands() {
		got = append(got, c.Name())
	}

	assert.ElementsMatch(t, []string{
		"patch", "rename", "add", "delete", "edit", "replace", "move",
		"install", "version",
		"patches", "metavariables", "examples",
	}, got)
}

func TestTerminalize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc string
		give string
		want string
	}{
		{
			desc: "drops a table of contents",
			give: "# Table of contents\n\n- [A](#a)\n\n# Real\n\nprose\n",
			want: "# Real\n\nprose",
		},
		{
			desc: "keeps a document whose first heading is not a contents list",
			give: "# Appendix\n\nprose\n",
			want: "# Appendix\n\nprose",
		},
		{
			desc: "drops reference definitions and unwraps the reference",
			give: "# T\n\nsee [**ids**] for more\n\n  [**ids**]: https://example.com\n",
			want: "# T\n\nsee **ids** for more",
		},
		{
			desc: "unwraps an inline link",
			give: "# T\n\nsee [Metavariables](#metavariables) below\n",
			want: "# T\n\nsee Metavariables below",
		},
		{
			desc: "drops html anchors and comments",
			give: "# T\n\n<a name=\"x\"></a>\n<!-- note -->\nprose\n",
			want: "# T\n\nprose",
		},
		{
			desc: "keeps blank lines inside a fence",
			give: "# T\n\n```go\npackage foo\n\nimport \"x\"\n```\n",
			want: "# T\n\n```go\npackage foo\n\nimport \"x\"\n```",
		},
		{
			desc: "recognises a fence indented inside a list item",
			give: "# T\n\n- item\n\n    ```\n    a  b\n\n    c  d\n    ```\n",
			want: "# T\n\n- item\n\n    ```\n    a  b\n\n    c  d\n    ```",
		},
		{
			desc: "leaves a pipe table alone",
			give: "# T\n\n| a | b |\n|---|---|\n| 1 | 2 |\n",
			want: "# T\n\n| a | b |\n|---|---|\n| 1 | 2 |",
		},
		{
			desc: "collapses the hole a dropped definition leaves",
			give: "# T\n\nprose\n\n  [a]: https://example.com\n\nmore\n",
			want: "# T\n\nprose\n\nmore",
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, terminalize([]byte(tt.give)))
		})
	}
}
