package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/spf13/pflag"
	"github.com/uber-go/gopatch/internal/refactor"
	"github.com/uber-go/gopatch/internal/refactor/patchgen"
)

const _printPatchDesc = "Print the generated gopatch program to stdout instead of applying it."

// opOptions holds the flags every subcommand that generates a patch accepts.
type opOptions struct {
	apply      options
	PrintPatch bool
}

// addOpFlags registers the flags common to every generated-patch subcommand.
func addOpFlags(fs *pflag.FlagSet, opts *opOptions) {
	addApplyFlags(fs, &opts.apply)
	fs.BoolVar(&opts.PrintPatch, "print-patch", false, _printPatchDesc)
}

// literalScopeOptions holds the flags that extend a struct-field operation to
// packages that import the struct.
type literalScopeOptions struct {
	ImportPath string
	Qualifier  string
}

func addLiteralScopeFlags(fs *pflag.FlagSet, opts *literalScopeOptions) {
	fs.StringVar(&opts.ImportPath, "import", "",
		"Import path of the package that declares the struct. When given, composite "+
			"literals in importing packages are rewritten too.")
	fs.StringVar(&opts.Qualifier, "qualifier", "",
		"Name importing packages use for --import. Defaults to the package name.")
}

// resolveFiles returns the absolute paths of the Go files the patterns name.
//
// The refactoring commands read the target files before generating a patch, to
// resolve a signature or a field's current type and tag. That is what lets the
// generated patch reproduce them exactly instead of eliding them, which the
// engine mishandles.
func (cmd *mainCmd) resolveFiles(patterns []string) ([]string, error) {
	if len(patterns) == 0 {
		return nil, errors.New("please provide at least one pattern")
	}

	cwd, err := cmd.Getwd()
	if err != nil {
		return nil, fmt.Errorf("getwd: %w", err)
	}

	found, err := findFiles(cwd, patterns)
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(found))
	for _, f := range found {
		paths = append(paths, f.Absolute)
	}
	return paths, nil
}

// absPath makes a path from the command line absolute against the command's
// working directory, which the tests inject.
func (cmd *mainCmd) absPath(path string) (string, error) {
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}

	cwd, err := cmd.Getwd()
	if err != nil {
		return "", fmt.Errorf("getwd: %w", err)
	}
	return filepath.Join(cwd, path), nil
}

// targetSearch builds the source lookup a patch-generating command reads the
// current declaration from, narrowed to whatever the target was qualified with.
func (cmd *mainCmd) targetSearch(arg string, t refactor.Target, patterns []string) (refactor.Search, error) {
	files, err := cmd.resolveFiles(patterns)
	if err != nil {
		return refactor.Search{}, err
	}

	if t.Dir != "" {
		dir, err := cmd.absPath(t.Dir)
		if err != nil {
			return refactor.Search{}, err
		}
		if files, err = filesUnder(files, dir); err != nil {
			return refactor.Search{}, err
		}
	}

	pkg, err := t.PackageName(arg)
	if err != nil {
		return refactor.Search{}, err
	}
	return refactor.Search{Arg: arg, Files: files, Package: pkg}, nil
}

// findField reads a struct field's current type and tag, and returns the package
// clause the generated change has to be scoped to.
//
// The change is always scoped. Without a package context line it matches
// "type User struct" in every package the given paths cover, which silently
// rewrites a same-named struct elsewhere — verified against two packages both
// declaring User. When the target did not name the package, it is inferred from
// the files, and an ambiguous name is an error rather than a coin flip.
func (cmd *mainCmd) findField(arg string, t refactor.Target, patterns []string) (refactor.StructField, string, error) {
	search, err := cmd.targetSearch(arg, t, patterns)
	if err != nil {
		return refactor.StructField{}, "", err
	}

	if search.Package == "" {
		pkg, err := refactor.InferPackage(arg, search.Files, t)
		if err != nil {
			return refactor.StructField{}, "", err
		}
		search.Package = pkg
	}

	found, err := search.StructField(t.Owner(), t.Name())
	if err != nil {
		return refactor.StructField{}, "", err
	}
	return found, search.Package, nil
}

// findSignature reads a function's current signature, returning the search it
// resolved through so that the caller can reuse its scope.
//
// The signature is reproduced exactly rather than elided, and the change is
// scoped to the declaring package for the same reason a field change is.
func (cmd *mainCmd) findSignature(
	arg string, t refactor.Target, patterns []string,
) (refactor.FuncSignature, refactor.Search, error) {
	search, err := cmd.targetSearch(arg, t, patterns)
	if err != nil {
		return refactor.FuncSignature{}, search, err
	}

	if search.Package == "" {
		pkg, err := refactor.InferPackage(arg, search.Files, t)
		if err != nil {
			return refactor.FuncSignature{}, search, err
		}
		search.Package = pkg
	}

	sig, err := search.FuncSignature(t.Name())
	return sig, search, err
}

// requireUnqualified refuses a qualified target on a command whose file argument
// already determines the package.
func requireUnqualified(arg string, t refactor.Target) error {
	if t.Qualifier == "" && t.Dir == "" {
		return nil
	}
	return fmt.Errorf(
		"%s cannot be qualified here: the file argument already says which package the "+
			"declaration goes in", arg)
}

// findStructPackage returns the package clause a struct change is scoped to,
// without reading anything out of the struct.
func (cmd *mainCmd) findStructPackage(arg string, t refactor.Target, patterns []string) (string, error) {
	search, err := cmd.targetSearch(arg, t, patterns)
	if err != nil {
		return "", err
	}
	if search.Package != "" {
		return search.Package, nil
	}
	return refactor.InferPackage(arg, search.Files, t)
}

// filesUnder narrows a list of absolute paths to the ones at or under dir.
//
// An empty result is an error: a path qualifier that matched nothing would
// otherwise widen the search back to every file, which is the opposite of what
// naming it asked for.
func filesUnder(files []string, dir string) ([]string, error) {
	out := make([]string, 0, len(files))
	for _, f := range files {
		if f == dir || strings.HasPrefix(f, dir+string(filepath.Separator)) {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf(
			"none of the Go files you named is at or under %v; the paths on the command "+
				"line have to cover the target you qualified", dir)
	}
	return out, nil
}

// runOperation renders op and either prints the generated program or applies it
// through the same pipeline as a hand-written patch file.
func (cmd *mainCmd) runOperation(op patchgen.Operation, opts *opOptions, patterns []string) error {
	src, err := op.Patch()
	if err != nil {
		return err
	}

	if opts.PrintPatch {
		_, err := cmd.Stdout.Write(src)
		return err
	}

	if len(patterns) == 0 {
		return errors.New("please provide at least one pattern")
	}

	fset := token.NewFileSet()
	loader := newPatchLoader(fset)
	name := fmt.Sprintf("<generated: %s>", op.Describe())
	if err := loader.LoadReader(name, bytes.NewReader(src)); err != nil {
		// A generated patch that does not compile is a bug in gopatch, not in
		// the user's arguments, so say so and show the program.
		return fmt.Errorf("generated patch is invalid: %w\n\n%s", err, src)
	}

	return cmd.runApply(applyRequest{
		FileSet:  fset,
		Applier:  newPatchRunner(fset, loader.Programs()),
		Patterns: patterns,
		Options:  &opts.apply,
	})
}
