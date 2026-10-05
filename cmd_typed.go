package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/pflag"
	"github.com/uber-go/gopatch/internal/refactor"
	"github.com/uber-go/gopatch/internal/refactor/typed"
)

// Descriptions of the flags the type-aware subcommands accept.
//
// They take a deliberately smaller set than the patch-generating commands.
// --print-patch has nothing to print, because there is no patch; and imports and
// generated files are not the caller's business here, because the engine
// rewrites whole declarations and re-derives the import block from what the
// result actually references. Cobra rejects a flag a command does not register,
// which reports the mismatch better than accepting it and doing nothing.
const (
	_typedPackageDesc = "Package that declares the symbol, by package clause or import path. " +
		"Needed only when more than one package declares the name."

	_typedLongSuffix = "\n\nThis command uses type information, so it changes exactly the " +
		"references that mean the same symbol and leaves same-named members of other " +
		"types alone. Symbol renames require a current UIR index of the selected modules. The module has to compile: if it does not, the " +
		"command reports the build errors and changes nothing."
)

// typedOptions holds the flags every type-aware subcommand accepts.
type typedOptions struct {
	apply   options
	Package string
}

func addTypedFlags(fs *pflag.FlagSet, opts *typedOptions) {
	fs.BoolVarP(&opts.apply.Diff, "diff", "d", false, _diffDesc)
	fs.BoolVar(&opts.apply.Print, "print-only", false, _printOnlyDesc)
	fs.BoolVarP(&opts.apply.Verbose, "verbose", "v", false, _verboseDesc)
	fs.StringVar(&opts.Package, "package", "", _typedPackageDesc)
}

// query builds the lookup for a target the user qualified, merging it with the
// --package flag that names the same thing.
//
// Giving both is refused rather than resolved by precedence: one of the two is
// certainly not what the caller meant, and picking either silently would apply
// the change somewhere they did not ask for.
func (cmd *mainCmd) query(arg string, kind typed.Kind, t refactor.Target, opts *typedOptions) (typed.Query, error) {
	if t.Qualifier != "" && opts.Package != "" {
		return typed.Query{}, fmt.Errorf(
			"%s is already qualified with %q, so --package %q is ambiguous; drop one",
			arg, t.Qualifier, opts.Package)
	}

	q := typed.Query{
		Kind:    kind,
		Owner:   t.Owner(),
		Name:    t.Name(),
		Package: t.Qualifier,
	}
	if q.Package == "" {
		q.Package = opts.Package
	}

	if t.Dir != "" {
		dir, err := cmd.absPath(t.Dir)
		if err != nil {
			return typed.Query{}, err
		}
		q.Dir = dir
	}
	return q, nil
}

// typedRequest is one type-aware operation.
type typedRequest struct {
	// Options carries the output mode and the package filter.
	Options *typedOptions

	// Patterns are the files and directories named on the command line. They
	// bound which files the operation may rewrite; the engine still loads the
	// whole module, because that is the only way to know whether a reference
	// exists outside them.
	Patterns []string

	// Run performs the operation and returns a one-line description of what it
	// did, which is reported the same way a patch's description comment is.
	Run func(p *typed.Program) (string, error)
}

// runTyped loads the module, performs one type-aware operation, and emits the
// result through the same output switch as a patch.
func (cmd *mainCmd) runTyped(req typedRequest) error {
	if len(req.Patterns) == 0 {
		return errors.New("please provide at least one pattern")
	}

	cwd, err := cmd.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}

	allowed, err := cmd.resolveFiles(req.Patterns)
	if err != nil {
		return err
	}

	prog, err := typed.Load(typed.Request{Dir: cwd})
	if err != nil {
		return err
	}

	summary, err := req.Run(prog)
	if err != nil {
		return err
	}

	edits, err := prog.Edits()
	if err != nil {
		return err
	}
	if len(edits) == 0 {
		return fmt.Errorf("%s changed nothing", summary)
	}

	if err := checkEditScope(cwd, edits, allowed); err != nil {
		return err
	}

	logOut := io.Discard
	if req.Options.apply.Verbose {
		logOut = cmd.Stdout
	}
	logger := log.New(logOut, "", 0)

	changes := make([]sourceChange, 0, len(edits))
	for _, e := range edits {
		path := sourcePath{Absolute: e.Path, Provided: e.Path}
		if rel, err := filepath.Rel(cwd, e.Path); err == nil {
			path.Provided = rel
		}
		changes = append(changes, sourceChange{
			Path: path, Original: e.Original, Modified: e.Modified, Comments: []string{summary},
		})
	}
	if err := cmd.emitChanges(&req.Options.apply, changes); err != nil {
		return err
	}
	for _, e := range edits {
		logger.Printf("%s: patched", e.Path)
	}

	return nil
}

// checkEditScope refuses to write a file the command line did not name.
//
// A type-aware rename has to change every reference or none: writing only the
// ones inside the given paths would leave the module unbuildable, and doing it
// silently would hide that. So the paths bound what may change, and a reference
// outside them is reported rather than skipped.
func checkEditScope(cwd string, edits []typed.FileEdit, allowed []string) error {
	inScope := make(map[string]bool, len(allowed))
	for _, path := range allowed {
		// The engine reports paths as the go command does, with symlinks
		// resolved; findFiles reports them as the user walked into them. On
		// macOS the temporary directory differs between the two spellings.
		if resolved, err := filepath.EvalSymlinks(path); err == nil {
			path = resolved
		}
		inScope[path] = true
	}

	var outside []string
	for _, e := range edits {
		if inScope[e.Path] {
			continue
		}
		display := e.Path
		if rel, err := filepath.Rel(cwd, e.Path); err == nil {
			display = rel
		}
		outside = append(outside, display)
	}
	if len(outside) == 0 {
		return nil
	}

	sort.Strings(outside)
	return fmt.Errorf(
		"this change also affects %s, which the given paths do not cover. "+
			"Applying it to only part of the module would break the build, so nothing "+
			"was written; re-run with paths that include them, or with \"./...\"",
		strings.Join(outside, ", "))
}
