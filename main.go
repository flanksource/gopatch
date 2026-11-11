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
	"fmt"
	"go/token"
	"io"
	"os"

	"github.com/uber-go/gopatch/internal/engine"
)

func main() {
	os.Exit(runMain())
}

// loadPatches loads patches specified by command line options.
func loadPatches(fset *token.FileSet, opts *options, stdin io.Reader) ([]*engine.Program, error) {
	loader := newPatchLoader(fset)
	if len(opts.Patches) == 0 && len(opts.PatchesFile) == 0 {
		// If -p and -P are unset, read from stdin.
		if err := loader.LoadReader("stdin", stdin); err != nil {
			return nil, fmt.Errorf("load patch from stdin: %w", err)
		}
	}

	for _, path := range opts.Patches {
		if err := loader.LoadFile(path); err != nil {
			return nil, fmt.Errorf("load patch %q: %w", path, err)
		}
	}

	if file := opts.PatchesFile; len(file) > 0 {
		if err := loader.LoadFileList(file); err != nil {
			return nil, fmt.Errorf("load patches file %q: %w", file, err)
		}
	}

	return loader.Programs(), nil
}

type mainCmd struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	Getwd func() (string, error) // == os.Getwd

	// UserHomeDir locates the directory that holds the per-user agent
	// configuration `gopatch install` writes into. It is a field so that the
	// installer's tests never touch the real ~/.claude or ~/.codex.
	UserHomeDir func() (string, error) // == os.UserHomeDir
}

func runMain() (exitCode int) {
	cmd := mainCmd{
		Stdin:       os.Stdin,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		Getwd:       os.Getwd,
		UserHomeDir: os.UserHomeDir,
	}
	if err := cmd.Run(os.Args[1:]); err != nil {
		fmt.Fprintln(cmd.Stderr, err)
		return 1
	}
	return 0
}

// normalize fills in the streams and working-directory lookup that a caller
// left unset. Tests construct a mainCmd with only the fields they assert on.
func (cmd *mainCmd) normalize() {
	if cmd.Stdin == nil {
		cmd.Stdin = os.Stdin
	}
	if cmd.Stdout == nil {
		cmd.Stdout = os.Stdout
	}
	if cmd.Stderr == nil {
		cmd.Stderr = os.Stderr
	}
	if cmd.Getwd == nil {
		cmd.Getwd = os.Getwd
	}
	if cmd.UserHomeDir == nil {
		cmd.UserHomeDir = os.UserHomeDir
	}
}

func (cmd *mainCmd) Run(args []string) error {
	cmd.normalize()

	// cobra reads os.Args when handed a nil argument slice, which in a test
	// binary means parsing the -test.* flags.
	if args == nil {
		args = []string{}
	}

	root, _ := cmd.newRootCommand()
	root.SetArgs(args)

	return root.Execute()
}
