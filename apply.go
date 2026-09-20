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
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pkg/diff"
	"go.uber.org/multierr"
	"golang.org/x/tools/imports"
)

// applier rewrites a single parsed Go file.
//
// It is the seam between the file-walking pipeline in runApply and whatever
// produces the rewrite: patchRunner compiles a gopatch program, while a
// refactoring subcommand may synthesize one.
type applier interface {
	// Apply rewrites f, reporting the patch description comments to display
	// and whether anything matched at all. A false matched means the file is
	// left alone.
	Apply(filename string, f *ast.File) (fout *ast.File, comments []string, matched bool)

	// Errors returns the errors accumulated over every Apply call.
	Errors() []error
}

// applyRequest is one run of the file-rewriting pipeline.
type applyRequest struct {
	// FileSet is shared with whatever compiled the Applier, so positions
	// recorded at compile time resolve against the files being rewritten.
	FileSet *token.FileSet

	// Applier performs the rewrite.
	Applier applier

	// Patterns are the files and directories named on the command line.
	Patterns []string

	// Options carries the output mode and skip flags.
	Options *options
}

// runApply enumerates the Go files named by req.Patterns, rewrites each with
// req.Applier, and emits the result per the selected output mode.
func (cmd *mainCmd) runApply(req applyRequest) error {
	opts := req.Options

	logOut := io.Discard
	if opts.Verbose {
		logOut = cmd.Stdout
	}
	log := log.New(logOut, "", 0)

	cwd, err := cmd.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	files, err := findFiles(cwd, req.Patterns)
	if err != nil {
		return err
	}

	var errs []error
	for _, path := range files {
		filename := path.Absolute
		content, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		f, err := parser.ParseFile(req.FileSet, filename, content /* src */, parser.AllErrors|parser.ParseComments)
		if err != nil {
			errs = append(errs, fmt.Errorf("could not parse %q: %v", filename, err))
			continue
		}

		if opts.SkipGenerated && checkGeneratedCode(f) {
			log.Printf("generated file %s: skipped", filename)
			continue
		}

		f, comments, ok := req.Applier.Apply(filename, f)
		// If at least one patch didn't match, there's nothing to do.
		// If --print-only was passed, print the contents out as-is.
		if !ok {
			if opts.Print {
				if _, err := cmd.Stdout.Write(content); err != nil {
					return err
				}
			}
			log.Printf("%s: skipped", filename)
			continue
		}

		var out bytes.Buffer
		if err := format.Node(&out, req.FileSet, f); err != nil {
			log.Printf("%s: failed: %v", filename, err)
			errs = append(errs, fmt.Errorf("failed to rewrite %q: %v", filename, err))
			continue
		}
		bs := out.Bytes()
		if !opts.SkipImportProcessing {
			bs, err = imports.Process(filename, bs, &imports.Options{
				Comments:   true,
				TabIndent:  true,
				TabWidth:   8,
				FormatOnly: true,
			})
			// This error shouldn't occur due to checks in
			// findFiles, loadPatches and format.Node()
			if err != nil {
				errs = append(errs, fmt.Errorf("reformat %q: %w", filename, err))
				continue
			}

		}

		if err := cmd.emit(opts, path, content, bs, comments); err != nil {
			log.Printf("%s: failed: %v", filename, err)
			errs = append(errs, err)
			continue
		}
		log.Printf("%s: patched", filename)
	}

	errs = append(errs, req.Applier.Errors()...)
	return multierr.Combine(errs...)
}

// emit renders one rewritten file per the selected output mode: --diff writes
// a unified diff to stdout, --print-only writes the new contents to stdout,
// and the default rewrites the file on disk.
//
// Every engine goes through here, so output looks the same regardless of which
// one produced the rewrite.
func (cmd *mainCmd) emit(
	opts *options,
	path sourcePath,
	original, modified []byte,
	comments []string,
) error {
	switch {
	case opts.Diff:
		return cmd.preview(path.Provided, original, modified, comments)
	case opts.Print:
		cmd.printComments(path.Provided, comments)
		_, err := cmd.Stdout.Write(modified)
		return err
	default:
		return os.WriteFile(path.Absolute, modified, 0o644)
	}
}

// sourcePath is the path to a Go source file.
type sourcePath struct {
	// Form closest to what was provided by the user.
	// If they provided a relative path, this will be relative.
	Provided string

	// Absolute path to the file.
	Absolute string
}

// sourceChange is a fully rendered file edit ready for output.
type sourceChange struct {
	Path               sourcePath
	Original, Modified []byte
	Comments           []string
}

// emitChanges validates a complete edit set before emitting its first file.
func (cmd *mainCmd) emitChanges(opts *options, changes []sourceChange) error {
	seen := make(map[string]bool, len(changes))
	for _, change := range changes {
		if seen[change.Path.Absolute] {
			return fmt.Errorf("duplicate edit for %v", change.Path.Absolute)
		}
		seen[change.Path.Absolute] = true
		current, err := os.ReadFile(change.Path.Absolute)
		if err != nil {
			return fmt.Errorf("re-read %v before emitting: %w", change.Path.Absolute, err)
		}
		if !bytes.Equal(current, change.Original) {
			return fmt.Errorf("%v changed after it was loaded; nothing was written", change.Path.Absolute)
		}
	}

	for _, change := range changes {
		if err := cmd.emit(opts, change.Path, change.Original, change.Modified, change.Comments); err != nil {
			return err
		}
	}
	return nil
}

func findGoFiles(cwd, path string) (_ []sourcePath, err error) {
	// Users may expect "./..."-stlye patterns to work.
	path = strings.TrimSuffix(path, "...")

	var relativeTo string // empty if path was absolute
	if !filepath.IsAbs(path) {
		relativeTo = cwd
		path = filepath.Join(relativeTo, path)
	} else {
		path = filepath.Clean(path) // drop extraneous ., .., etc.
	}

	var paths []sourcePath
	err = filepath.Walk(path, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		mode := info.Mode()
		switch {
		case mode.IsRegular() && strings.HasSuffix(path, ".go"):
			sp := sourcePath{Absolute: path, Provided: path}
			if p, err := filepath.Rel(relativeTo, path); err == nil {
				sp.Provided = p
			}
			paths = append(paths, sp)

		case mode.IsDir():
			base := filepath.Base(path)
			switch {
			case len(base) == 0,
				base[0] == '.',
				base[0] == '_',
				base == "testdata",
				base == "vendor":
				return filepath.SkipDir
			}
		}

		return nil
	})

	return paths, err
}

func findFiles(cwd string, patterns []string) (_ []sourcePath, err error) {
	files := make(map[string]sourcePath)

	for _, pat := range patterns {
		fs, findErr := findGoFiles(cwd, pat)
		if findErr != nil {
			err = multierr.Append(err, fmt.Errorf("enumerating Go files in %q: %v", pat, err))
			continue
		}

		for _, f := range fs {
			files[f.Absolute] = f
		}
	}

	sortedPaths := make([]sourcePath, 0, len(files))
	for _, p := range files {
		sortedPaths = append(sortedPaths, p)
	}
	sort.Slice(sortedPaths, func(i, j int) bool {
		return sortedPaths[i].Absolute < sortedPaths[j].Absolute
	})

	return sortedPaths, err
}

func checkGeneratedCode(f *ast.File) bool {
	if ast.IsGenerated(f) {
		return true
	}
	if f.Doc == nil {
		return false
	}
	for _, comm := range f.Doc.List {
		if strings.Contains(comm.Text, "@generated") {
			return true
		}
	}
	return false
}

func (cmd *mainCmd) preview(
	filename string,
	originalContent, modifiedContent []byte,
	comments []string,
) error {
	cmd.printComments(filename, comments)
	return diff.Text(filename, filename, originalContent, modifiedContent, cmd.Stdout)
}

func (cmd *mainCmd) printComments(filename string, comments []string) {
	for _, c := range comments {
		fmt.Fprintf(cmd.Stderr, "%v:%v\n", filename, c)
	}
}
