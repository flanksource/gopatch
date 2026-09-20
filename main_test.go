// Copyright (c) 2021 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

// Package section is responsible for splitting a program into its different
// sections without attempting to parse the contents.s
package main

import (
	"bytes"
	"go/ast"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// discardCmd is a mainCmd whose output goes nowhere, for tests that only care
// about how arguments were parsed.
func discardCmd() *mainCmd {
	return &mainCmd{
		Stdin:  bytes.NewReader(nil),
		Stdout: io.Discard,
		Stderr: io.Discard,
		Getwd:  os.Getwd,
	}
}

func TestRootCommandFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc         string
		give         []string
		wantPatterns []string
		want         options
	}{
		{
			desc:         "-d",
			give:         []string{"-d", "-p", "testdata/patch/error.patch", "testdata/test_files/diff_example/error.go"},
			wantPatterns: []string{"testdata/test_files/diff_example/error.go"},
			want: options{
				Patches: []string{"testdata/patch/error.patch"},
				Diff:    true,
			},
		},
		{
			desc:         "diff",
			give:         []string{"--diff", "-p", "testdata/patch/error.patch", "testdata/test_files/diff_example/error.go"},
			wantPatterns: []string{"testdata/test_files/diff_example/error.go"},
			want: options{
				Patches: []string{"testdata/patch/error.patch"},
				Diff:    true,
			},
		},
		{
			desc:         "skip-generated",
			give:         []string{"--skip-generated", "-p", "testdata/patch/replace_to_with_ptr.patch", "testdata/test_files/skip_generated_files/test1.go"},
			wantPatterns: []string{"testdata/test_files/skip_generated_files/test1.go"},
			want: options{
				Patches:       []string{"testdata/patch/replace_to_with_ptr.patch"},
				SkipGenerated: true,
			},
		},
		{
			desc:         "skip-import-processing",
			give:         []string{"--skip-import-processing", "-p", "testdata/patch/replace_to_with_ptr.patch", "testdata/test_files/skip_import_processing_example/test1.go"},
			wantPatterns: []string{"testdata/test_files/skip_import_processing_example/test1.go"},
			want: options{
				Patches:              []string{"testdata/patch/replace_to_with_ptr.patch"},
				SkipImportProcessing: true,
			},
		},
		{
			// The e2e suite always passes the pattern before the -p flags, so
			// interspersed parsing has to keep working.
			desc:         "pattern before flags",
			give:         []string{"foo.go", "-p", "a.patch"},
			wantPatterns: []string{"foo.go"},
			want:         options{Patches: []string{"a.patch"}},
		},
		{
			desc:         "print-only with pattern in the middle",
			give:         []string{"--print-only", "foo.go", "-p", "a.patch"},
			wantPatterns: []string{"foo.go"},
			want: options{
				Patches: []string{"a.patch"},
				Print:   true,
			},
		},
		{
			desc:         "repeated patch flags accumulate in order",
			give:         []string{"-p", "a.patch", "-p", "b.patch", "foo.go"},
			wantPatterns: []string{"foo.go"},
			want:         options{Patches: []string{"a.patch", "b.patch"}},
		},
		{
			desc:         "long flag with equals",
			give:         []string{"--patch=a.patch", "foo.go"},
			wantPatterns: []string{"foo.go"},
			want:         options{Patches: []string{"a.patch"}},
		},
		{
			// A comma is a legal character in a path, so it must not split.
			desc:         "patch path containing a comma stays one path",
			give:         []string{"-p", "a,b.patch", "foo.go"},
			wantPatterns: []string{"foo.go"},
			want:         options{Patches: []string{"a,b.patch"}},
		},
		{
			desc:         "double dash keeps a subcommand name as a pattern",
			give:         []string{"--", "version"},
			wantPatterns: []string{"version"},
		},
		{
			desc:         "verbose",
			give:         []string{"-v", "foo.go", "-p", "a.patch"},
			wantPatterns: []string{"foo.go"},
			want: options{
				Patches: []string{"a.patch"},
				Verbose: true,
			},
		},
		{
			desc:         "patches-file",
			give:         []string{"-P", "patches.txt", "foo.go"},
			wantPatterns: []string{"foo.go"},
			want:         options{PatchesFile: "patches.txt"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()

			root, opts := discardCmd().newRootCommand()

			var gotPatterns []string
			root.RunE = func(_ *cobra.Command, args []string) error {
				gotPatterns = args
				return nil
			}
			root.SetArgs(tt.give)
			require.NoError(t, root.Execute())

			assert.Equal(t, tt.wantPatterns, gotPatterns, "patterns")
			assert.Equal(t, tt.want.Patches, opts.Patches, "--patch")
			assert.Equal(t, tt.want.PatchesFile, opts.PatchesFile, "--patches-file")
			assert.Equal(t, tt.want.Diff, opts.Diff, "--diff")
			assert.Equal(t, tt.want.Print, opts.Print, "--print-only")
			assert.Equal(t, tt.want.SkipGenerated, opts.SkipGenerated, "--skip-generated")
			assert.Equal(t, tt.want.SkipImportProcessing, opts.SkipImportProcessing, "--skip-import-processing")
			assert.Equal(t, tt.want.Verbose, opts.Verbose, "--verbose")

			// cobra claims -v for --version if nothing else has taken it.
			if sh := root.Flags().ShorthandLookup("v"); assert.NotNil(t, sh, "-v") {
				assert.Equal(t, "verbose", sh.Name, "-v belongs to --verbose")
			}
		})
	}
}

// TestRootCommandHelp guards the flag descriptions, which nothing else covers:
// they are easy to drop silently when a flag is re-registered.
func TestRootCommandHelp(t *testing.T) {
	t.Parallel()

	root, _ := discardCmd().newRootCommand()
	got := root.UsageString() + root.Long

	wants := []string{
		"--patch", "--patches-file", "--diff", "--print-only",
		"--skip-import-processing", "--skip-generated", "--verbose", "--version",
		"Multiple patches may be provided",
		"Each file must be listed on its own line",
		"Print a diff of the proposed changes",
		"Print files to stdout without modifying them",
		"Skips processing of imports",
		"Skips running on files with generated code",
		"for each file found",
		"Display the version of gopatch",
		"One or more files or directores containing Go code",
	}
	for _, want := range wants {
		assert.Contains(t, got, want)
	}
}

// TestSubcommandTree pins which verb/noun pairs exist, so that a command lost
// in a refactor shows up here rather than as a missing feature.
func TestSubcommandTree(t *testing.T) {
	t.Parallel()

	want := map[string][]string{
		"rename": {"struct", "type", "func", "package", "import", "field", "method"},
		"add":    {"field", "param", "struct", "func", "method"},
		"delete": {"field", "struct", "type", "func", "method"},
		"edit":   {"field", "func", "method"},
		"move":   {"file", "struct", "type", "func", "var", "const", "method"},
	}

	root, _ := discardCmd().newRootCommand()
	for verb, nouns := range want {
		verbCmd, _, err := root.Find([]string{verb})
		require.NoErrorf(t, err, "no %q command", verb)

		var got []string
		for _, c := range verbCmd.Commands() {
			got = append(got, c.Name())
		}
		assert.ElementsMatch(t, nouns, got, "subcommands of %q", verb)
	}
}

// TestExamplesAreValidInvocations parses every command line in every command's
// Example block against the command tree.
//
// Help text is not compiled, so an example that names a flag that does not
// exist, or gives too few arguments, is a documentation bug nothing else would
// catch. This resolves each line to its command and validates its flags and
// positional count without running the refactoring.
func TestExamplesAreValidInvocations(t *testing.T) {
	t.Parallel()

	var lines []string

	var collect func(*cobra.Command)
	collect = func(c *cobra.Command) {
		for _, line := range strings.Split(c.Example, "\n") {
			line = strings.TrimSpace(line)
			switch {
			case !strings.HasPrefix(line, "gopatch "):
				// A comment, a blank line, or another tool in a two-step recipe.
			case strings.ContainsAny(line, "<>|"):
				// Shell redirection: the operator is not an argument.
			default:
				lines = append(lines, line)
			}
		}
		for _, sub := range c.Commands() {
			collect(sub)
		}
	}
	collect(mustRootCommand(t))

	require.NotEmpty(t, lines, "no examples found; the help text lost its Example blocks")

	for _, line := range lines {
		t.Run(line, func(t *testing.T) {
			t.Parallel()

			// A fresh tree per line: pflag appends to array flags, so a reused
			// one would accumulate values across examples.
			args := splitArgs(t, strings.TrimPrefix(line, "gopatch "))
			target, rest, err := mustRootCommand(t).Find(args)
			require.NoError(t, err)
			require.NoError(t, target.ParseFlags(rest))
			require.NoError(t, target.ValidateArgs(target.Flags().Args()))
		})
	}
}

func mustRootCommand(t *testing.T) *cobra.Command {
	t.Helper()

	root, _ := discardCmd().newRootCommand()
	return root
}

// TestTypedCommandsRejectPatchFlags pins that the type-aware commands do not
// accept flags that cannot mean anything for them. There is no patch to print,
// and the engine re-derives each file's imports from what it still refers to, so
// accepting these and ignoring them would be worse than refusing.
func TestTypedCommandsRejectPatchFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		args []string
		want string
	}{
		{
			args: []string{"rename", "field", "User.Name", "Title", "--print-patch", "."},
			want: "unknown flag: --print-patch",
		},
		{
			args: []string{"rename", "method", "User.Save", "Store", "--skip-import-processing", "."},
			want: "unknown flag: --skip-import-processing",
		},
		{
			args: []string{"delete", "func", "run", "--print-patch", "."},
			want: "unknown flag: --print-patch",
		},
		{
			args: []string{"add", "struct", "Config", "x.go", "--print-patch"},
			want: "unknown flag: --print-patch",
		},
	}

	for _, tt := range tests {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			t.Parallel()

			err := discardCmd().Run(tt.args)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestLoadPatches(t *testing.T) {
	t.Parallel()

	t.Run("single patch", func(t *testing.T) {
		opts := &options{
			Patches: []string{"testdata/patch/error.patch"},
			Diff:    true,
		}
		patch, err := loadPatches(token.NewFileSet(), opts, bytes.NewReader(nil))
		assert.Len(t, patch, 1)
		require.NoError(t, err)
	})

	t.Run("multiple patches", func(t *testing.T) {
		opts := &options{
			Patches: []string{"testdata/patch/error.patch", "testdata/patch/time.patch"},
			Diff:    true,
		}
		patch, err := loadPatches(token.NewFileSet(), opts, bytes.NewReader(nil))
		require.NoError(t, err)
		assert.Equal(t, 2, len(patch))
	})

	t.Run("patches file", func(t *testing.T) {
		patchesFile := writeFile(t, filepath.Join(t.TempDir(), "patches.txt"),
			"testdata/patch/error.patch",
			"testdata/patch/time.patch",
		)
		opts := &options{
			PatchesFile: patchesFile,
			Diff:        true,
		}

		patch, err := loadPatches(token.NewFileSet(), opts, bytes.NewReader(nil))
		require.NoError(t, err)
		assert.Equal(t, 2, len(patch))
	})

	t.Run("error", func(t *testing.T) {
		path := "testdata/patch/error1.patch"
		opts := &options{
			Patches: []string{path},
			Diff:    true,
		}
		_, err := loadPatches(token.NewFileSet(), opts, bytes.NewReader(nil))
		assert.ErrorContains(t, err, path)
		assert.ErrorContains(t, err, "no such file or directory")
	})
}

func TestPreview(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		before   string
		after    string
		want     string
		filename string
	}{
		{
			name:   "preview",
			before: "single line",
			after:  "different line",
			want: "--- testdata/test_files/lint_example/time.go\n" +
				"+++ testdata/test_files/lint_example/time.go\n" +
				"@@ -1,1 +0,0 @@\n" +
				"-single line\n" +
				"@@ -0,0 +1,1 @@\n" +
				"+different line\n",
		},
		{
			name: "empty file",
			want: "--- testdata/test_files/lint_example/time.go\n" +
				"+++ testdata/test_files/lint_example/time.go\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stderr, stdout bytes.Buffer
			cmd := &mainCmd{
				Stdout: &stdout,
				Stderr: &stderr,
				Getwd:  os.Getwd,
			}

			comment := []string{"example comment"}
			err := cmd.preview("testdata/test_files/lint_example/time.go", []byte(tt.before), []byte(tt.after), comment)
			require.NoError(t, err)

			assert.Equal(t, "testdata/test_files/lint_example/time.go:example comment\n", stderr.String())
			assert.Equal(t, tt.want, stdout.String())
		})
	}

	t.Run("nil for strings", func(t *testing.T) {
		var stderr, stdout bytes.Buffer
		cmd := &mainCmd{
			Stdout: &stdout,
			Stderr: &stderr,
			Getwd:  os.Getwd,
		}

		comment := []string{"example comment"}
		err := cmd.preview("testdata/test_files/lint_example/time.go", nil, nil, comment)
		require.NoError(t, err)

		assert.Equal(t, "testdata/test_files/lint_example/time.go:example comment\n", stderr.String())
		assert.Equal(t,
			"--- testdata/test_files/lint_example/time.go\n"+
				"+++ testdata/test_files/lint_example/time.go\n",
			stdout.String())
	})
}

func TestCheckGeneratedCode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		file *ast.File
		want bool
	}{
		{
			name: "@generated",
			file: &ast.File{
				Doc: &ast.CommentGroup{
					List: []*ast.Comment{
						{Text: "@generated"},
					},
				},
			},
			want: true,
		},
		{
			name: "non-generated code",
			file: &ast.File{
				Doc: &ast.CommentGroup{
					List: []*ast.Comment{
						{Text: "sad times"},
						{Text: "not generated code"},
					},
				},
			},
		},
		{
			name: "non-generated code with no package comment",
			file: &ast.File{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := checkGeneratedCode(tt.file)
			assert.Equal(t, tt.want, got)
		})
	}
}
