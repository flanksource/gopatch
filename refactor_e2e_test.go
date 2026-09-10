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

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"

	"github.com/rogpeppe/go-internal/txtar"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRefactorIntegration runs the refactoring subcommands end to end over the
// txtar archives in testdata/refactor.
//
// These archives are shaped differently from the ones in testdata: a
// refactoring command is invoked once over the whole extracted tree rather than
// once per Go file, because that is how it is used and because a single command
// may legitimately change several files. TestIntegration skips directories, so
// it never sees these.
func TestRefactorIntegration(t *testing.T) {
	t.Parallel()

	const dir = "testdata/refactor"

	entries, err := os.ReadDir(dir)
	require.NoErrorf(t, err, "failed to ls %q", dir)

	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "README.md" {
			continue
		}

		t.Run(entry.Name(), func(t *testing.T) {
			t.Parallel()
			runRefactorTest(t, filepath.Join(dir, entry.Name()))
		})
	}
}

// refactorArchive is a parsed refactoring test case.
type refactorArchive struct {
	// Args is the command line to run, without the trailing patterns.
	Args []string

	// WantPatch is the expected output of --print-patch. It is absent for the
	// type-aware commands, which generate no patch; those archives skip that
	// mode instead of asserting on it.
	WantPatch []byte

	// WantError is a substring the command's error must contain. When set, the
	// command must fail and leave every file untouched.
	WantError string

	// Patterns are the trailing file patterns to append to Args. They default to
	// every Go file in the archive; an archive whose command takes its target as
	// a positional declares an empty "patterns" member instead.
	Patterns []string

	// WantDiff is the expected stdout of --diff, when the archive pins it.
	WantDiff []byte

	// WantStderr is the expected stderr, when the archive pins it.
	WantStderr []byte

	// Files are the Go files to extract, and what they should contain
	// afterwards.
	Files []refactorFile

	archive *txtar.Archive
}

type refactorFile struct {
	// Name is the file's path within the extracted tree.
	Name string

	// Want is the expected content after the command runs. A file with no
	// ".out.go" counterpart must come out byte-identical, which is what catches
	// a command that changes more than it should.
	Want []byte
}

func loadRefactorArchive(t *testing.T, path string) *refactorArchive {
	t.Helper()

	archive, err := txtar.ParseFile(path)
	require.NoErrorf(t, err, "failed to open %q", path)

	ta := &refactorArchive{archive: archive}
	outputs := make(map[string][]byte)
	var inputs []string
	var patternsGiven bool

	kept := archive.Files[:0] // zero-alloc filtering
	for _, f := range archive.Files {
		switch {
		case f.Name == "args":
			ta.Args = splitArgs(t, string(f.Data))
			continue
		case f.Name == "patterns":
			ta.Patterns = strings.Fields(string(f.Data))
			patternsGiven = true
			continue
		// txtar gives every member a trailing newline, so the goldens are
		// normalized the same way the archives in testdata are.
		case f.Name == "want.patch":
			ta.WantPatch = singleTrailingNewline(f.Data)
			continue
		case f.Name == "want.diff":
			ta.WantDiff = singleTrailingNewline(f.Data)
			continue
		case f.Name == "want.stderr":
			ta.WantStderr = singleTrailingNewline(f.Data)
			continue
		case f.Name == "want.error":
			ta.WantError = strings.TrimSpace(string(f.Data))
			continue

		// A go.mod makes the extracted tree a module, which the type-aware
		// commands need. A ".txt" member is a file the command reads rather than
		// rewrites, which is what "--content @<path>" needs. Both are written out
		// but are not refactoring targets.
		case f.Name == "go.mod", strings.HasSuffix(f.Name, ".txt"):

		case strings.HasSuffix(f.Name, ".out.go"):
			outputs[strings.TrimSuffix(f.Name, ".out.go")] = singleTrailingNewline(f.Data)
			continue

		case strings.HasSuffix(f.Name, ".in.go"):
			name := strings.TrimSuffix(f.Name, ".in.go")
			f.Name = name + ".go"
			f.Data = singleTrailingNewline(f.Data)
			inputs = append(inputs, name)

		default:
			require.Failf(t, "unknown file", "%q in %q", f.Name, path)
		}

		kept = append(kept, f)
	}
	archive.Files = kept

	require.NotEmptyf(t, ta.Args, "%q has no args", path)
	require.NotEmptyf(t, inputs, "%q has no input files", path)
	if ta.WantPatch == nil && ta.WantError == "" && len(outputs) == 0 {
		// Every mode would then assert only that nothing happened, which is
		// never what a refactoring archive is for.
		require.Failf(t, "archive asserts nothing",
			"%q has no want.patch, no want.error, and no .out.go", path)
	}

	for _, name := range inputs {
		want, ok := outputs[name]
		if !ok {
			// No .out.go means the file must not change.
			for _, f := range archive.Files {
				if f.Name == name+".go" {
					want = f.Data
				}
			}
		}
		delete(outputs, name)
		ta.Files = append(ta.Files, refactorFile{Name: name + ".go", Want: want})
	}

	for name := range outputs {
		require.Failf(t, "output without an input", "%q.out.go in %q", name, path)
	}

	if !patternsGiven {
		for _, f := range ta.Files {
			ta.Patterns = append(ta.Patterns, f.Name)
		}
	}

	return ta
}

// splitArgs splits an archive's args line the way a shell would, so that a flag
// value containing spaces can be written with quotes.
func splitArgs(t *testing.T, s string) []string {
	t.Helper()

	var (
		args    []string
		cur     strings.Builder
		quote   rune
		started bool
	)
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				continue
			}
			cur.WriteRune(r)
		case r == '\'' || r == '"':
			quote = r
			started = true
		case unicode.IsSpace(r):
			if started {
				args = append(args, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteRune(r)
			started = true
		}
	}

	require.Zerof(t, quote, "unterminated quote in args: %q", s)
	if started {
		args = append(args, cur.String())
	}
	return args
}

func runRefactorTest(t *testing.T, path string) {
	ta := loadRefactorArchive(t, path)
	patterns := ta.Patterns

	// run extracts a fresh copy of the archive so each mode starts clean.
	run := func(extra ...string) (dir, stdout, stderr string, err error) {
		dir = t.TempDir()
		require.NoErrorf(t, txtar.Write(ta.archive, dir),
			"could not write archive to %q", dir)

		var out, errBuf bytes.Buffer
		cmd := mainCmd{
			Stdin:  bytes.NewReader(nil),
			Stdout: &out,
			Stderr: &errBuf,
			Getwd:  func() (string, error) { return dir, nil },
		}
		err = cmd.Run(append(append([]string{}, ta.Args...), extra...))
		return dir, out.String(), errBuf.String(), err
	}

	// assertUntouched checks that a run left every extracted file as it was.
	assertUntouched := func(t *testing.T, dir string) {
		t.Helper()
		for _, af := range ta.archive.Files {
			got, err := os.ReadFile(filepath.Join(dir, af.Name))
			require.NoError(t, err)
			assert.Equal(t, string(af.Data), string(got), "%v was modified", af.Name)
		}
	}

	if ta.WantError != "" {
		t.Run("refuses", func(t *testing.T) {
			dir, _, _, err := run(patterns...)
			require.Error(t, err, "the command was expected to fail")
			assert.Contains(t, err.Error(), ta.WantError)
			assertUntouched(t, dir)
		})
		return
	}

	if ta.WantPatch != nil {
		t.Run("printPatch", func(t *testing.T) {
			_, stdout, _, err := run(append(patterns, "--print-patch")...)
			require.NoError(t, err, "could not generate the patch")
			assert.Equal(t, string(ta.WantPatch), stdout, "generated patch")
		})
	}

	t.Run("diff", func(t *testing.T) {
		dir, stdout, stderr, err := run(append(patterns, "-d")...)
		require.NoError(t, err, "could not run in diff mode")
		if ta.WantDiff != nil {
			assert.Equal(t, string(ta.WantDiff), stdout, "diff")
		}
		if ta.WantStderr != nil {
			assert.Equal(t, string(ta.WantStderr), stderr, "stderr")
		}

		assertUntouched(t, dir)
	})

	t.Run("inPlace", func(t *testing.T) {
		dir, _, _, err := run(patterns...)
		require.NoError(t, err, "could not apply")

		for _, f := range ta.Files {
			got, err := os.ReadFile(filepath.Join(dir, f.Name))
			require.NoErrorf(t, err, "failed to read %q", f.Name)
			assert.Equal(t, string(f.Want), string(got), f.Name)
		}
	})
}
