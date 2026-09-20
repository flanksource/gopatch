// Package typed implements gopatch's type-aware refactoring engine.
//
// It exists for the operations gopatch's patch language cannot express
// correctly. A patch matches by name, so rewriting x.Field or x.Method() would
// rewrite every same-named member of every unrelated type in the file; and a
// patch cannot delete a declaration at all, because its replacement section may
// not be empty.
//
// This engine resolves the target with go/types instead, so a rename touches
// exactly the references that denote the same object, and rewrites the source
// with github.com/dave/dst, which preserves comment attachment across the edit.
//
// The trade-off is the one go/types imposes: the code has to type-check. Where
// the patch engine happily rewrites a file that does not compile, this engine
// refuses. Operations never fall back from one engine to the other — a partly
// applied rename is worse than none.
package typed

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dave/dst"
	"github.com/dave/dst/decorator"
	"github.com/dave/dst/decorator/resolver/simple"
	"golang.org/x/tools/go/packages"
)

// _loadMode is what the engine needs from go/packages.
//
// NeedDeps is deliberately absent: decorator.Load decorates every package that
// has syntax, so asking for dependencies would build a dst tree for the entire
// standard library on every run.
const _loadMode = packages.NeedName |
	packages.NeedFiles |
	packages.NeedCompiledGoFiles |
	packages.NeedImports |
	packages.NeedTypes |
	packages.NeedSyntax |
	packages.NeedTypesInfo |
	packages.NeedTypesSizes |
	packages.NeedModule

// Request describes the code a typed refactoring runs over.
type Request struct {
	// Dir is where to start looking for the module. Loading happens from the
	// module root, whether or not that is Dir: every package in the module is
	// loaded, because a reference to the symbol being changed can live in any of
	// them, including one above the directory the user is standing in.
	Dir string

	// Env replaces the environment the go command runs with.
	//
	// packages.Config.Env is all-or-nothing, so this is the whole environment,
	// not an overlay. Leave it nil to inherit the process environment with
	// module mode forced on.
	Env []string
}

// FileEdit is one file's contents before and after a refactoring, ready for
// the same output switch that renders a patch.
type FileEdit struct {
	// Path is the absolute path of the file.
	Path string

	// Original is the file's contents as they are on disk.
	Original []byte

	// Modified is the contents after the refactoring.
	Modified []byte

	// Create and Delete describe edits whose path did not or will not exist.
	Create bool
	Delete bool

	// Mode is retained for moves and used when a new file is created.
	Mode os.FileMode
}

// Program is a loaded, type-checked module ready to be refactored.
type Program struct {
	pkgs  []*decorator.Package
	names map[string]string // import path -> package name, for the restorer

	// root is the module directory, used to report positions the way the rest of
	// gopatch's output does rather than as absolute paths.
	root string

	// modulePath is the import-path prefix declared by the root go.mod.
	modulePath string

	// files indexes the decorated syntax by absolute path. A file compiled into
	// more than one package — the package and its test variant — is indexed
	// once; every variant applies the same edits, so the first is enough.
	files map[string]*decoratedFile

	// order lists file paths in a stable order, so output does not depend on
	// map iteration.
	order []string

	touched map[string]bool

	// aliases records, per file, the import paths that must be written with an
	// explicit alias. The restorer otherwise trusts its resolver for a package's
	// name, which would emit an unaliased import next to a qualifier that does
	// not match it.
	aliases map[string]map[string]string

	// sources caches file contents for the operations that read source text
	// rather than syntax, keyed by the same absolute path as files.
	sources map[string][]byte
}

// decoratedFile is one source file's dst tree and the package it was decorated
// as part of.
type decoratedFile struct {
	// path is the file's absolute path with symlinks resolved, which is the key
	// every other index uses.
	path string

	pkg  *decorator.Package
	file *dst.File
}

// Load type-checks every package in the module rooted at req.Dir.
//
// It fails when any package does not build: a rename resolved from a partial
// type graph would miss references and leave the module broken.
func Load(req Request) (*Program, error) {
	root, err := moduleRoot(req.Dir)
	if err != nil {
		return nil, err
	}
	root = realPath(root)

	env := req.Env
	if env == nil {
		// Module mode is not optional. In GOPATH mode packages get synthetic
		// "_/abs/path" import paths and cannot refer to one another, so no
		// cross-package reference would ever be found.
		env = append(os.Environ(), "GO111MODULE=on")
	}

	cfg := &packages.Config{
		Dir:   root,
		Env:   env,
		Mode:  _loadMode,
		Tests: true,
	}

	loaded, err := decorator.Load(cfg, "./...")
	if err != nil {
		return nil, fmt.Errorf("load packages in %v: %w", root, err)
	}
	if len(loaded) == 0 {
		return nil, fmt.Errorf("no Go packages found under %v", root)
	}

	if err := loadErrors(loaded); err != nil {
		return nil, err
	}

	p := &Program{
		root:    root,
		names:   make(map[string]string),
		files:   make(map[string]*decoratedFile),
		touched: make(map[string]bool),
		aliases: make(map[string]map[string]string),
		sources: make(map[string][]byte),
	}

	for _, pkg := range loaded {
		// The test binary's synthesized main is not on disk and has nothing a
		// user can refactor.
		if strings.HasSuffix(pkg.PkgPath, ".test") {
			continue
		}
		p.pkgs = append(p.pkgs, pkg)
		if p.modulePath == "" && pkg.Module != nil && realPath(pkg.Module.Dir) == realPath(root) {
			p.modulePath = pkg.Module.Path
		}

		if pkg.Name != "" {
			p.names[pkg.PkgPath] = pkg.Name
		}
		for path, imp := range pkg.Imports {
			if imp.Name != "" {
				p.names[path] = imp.Name
			}
		}

		if pkg.Decorator == nil {
			continue
		}
		for _, f := range pkg.Syntax {
			path := realPath(pkg.Decorator.Filenames[f])
			if path == "" {
				continue
			}
			if _, ok := p.files[path]; ok {
				continue
			}
			p.files[path] = &decoratedFile{path: path, pkg: pkg, file: f}
			p.order = append(p.order, path)
		}
	}

	sort.Strings(p.order)
	return p, nil
}

// moduleRoot returns the directory at or above dir that holds go.mod.
//
// The whole module is the unit of loading, so running from a subdirectory must
// not silently narrow the search: a reference in a sibling package is exactly
// what this engine exists to find.
func moduleRoot(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}

	for cur := abs; ; {
		if _, err := os.Stat(filepath.Join(cur, "go.mod")); err == nil {
			return cur, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return "", fmt.Errorf(
				"no go.mod at or above %v; the type-aware commands need a module, because "+
					"outside one a package has no import path other packages can name", abs)
		}
		cur = parent
	}
}

// loadErrors reports every package error as one failure.
//
// Degrading to a partial type graph is not an option: the engine's whole value
// is that it finds every reference, and a package that did not type-check may
// hold references it cannot see.
func loadErrors(pkgs []*decorator.Package) error {
	seen := make(map[string]bool)
	var msgs []string
	for _, pkg := range pkgs {
		for _, e := range pkg.Errors {
			msg := e.Error()
			if seen[msg] {
				continue
			}
			seen[msg] = true
			msgs = append(msgs, "  "+msg)
		}
	}
	if len(msgs) == 0 {
		return nil
	}

	sort.Strings(msgs)
	return fmt.Errorf(
		"the code does not type-check, so a type-aware refactoring cannot be trusted:\n%s\n\n"+
			"Fix the build first, or use one of the patch-generating commands, which "+
			"work on code that does not compile", strings.Join(msgs, "\n"))
}

// ownedFile returns the decorated file that holds a node, but only when pkg is
// the package that owns it.
//
// A file compiled into both a package and its test variant is decorated twice,
// into two unrelated dst trees, and only one of them is rendered. Edits made
// through the other variant's tree would be silently thrown away, so callers
// skip a node whose file belongs to someone else; the owning variant reaches the
// same node through its own type information.
func (p *Program) ownedFile(pkg *decorator.Package, n ast.Node) *decoratedFile {
	df := p.files[realPath(pkg.Fset.Position(n.Pos()).Filename)]
	if df == nil || df.pkg != pkg {
		return nil
	}
	return df
}

// realPath resolves symlinks so that a path from the go command and a path the
// caller built by walking directories compare equal.
//
// On macOS the temporary directory is reached through a symlink, so the two
// spellings of the same file differ; when the path cannot be resolved there is
// nothing better to compare than what was given.
func realPath(path string) string {
	if path == "" {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

// mark records that a file's tree has been modified.
func (p *Program) mark(df *decoratedFile) {
	p.touched[df.path] = true
}

// source returns a file's contents as they are on disk.
//
// One operation runs per invocation, so the contents cannot go stale underneath
// a caller: what is on disk is what the loaded syntax was built from.
func (p *Program) source(path string) ([]byte, error) {
	if src, ok := p.sources[path]; ok {
		return src, nil
	}

	src, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read %v: %w", path, err)
	}
	p.sources[path] = src
	return src, nil
}

// alias records that one of a file's imports has to carry an explicit name.
func (p *Program) alias(df *decoratedFile, importPath, name string) {
	if p.aliases[df.path] == nil {
		p.aliases[df.path] = make(map[string]string)
	}
	p.aliases[df.path][importPath] = name
}

// pos renders a position relative to the module root, so that a message naming
// a file reads like the rest of gopatch's output.
func (p *Program) pos(pkg *decorator.Package, at token.Pos) string {
	position := pkg.Fset.Position(at)
	if rel, err := filepath.Rel(p.root, position.Filename); err == nil {
		position.Filename = rel
	}
	return position.String()
}

// Edits renders every modified file.
//
// Only files the refactoring touched are rendered, so an unrelated file that
// happens not to be gofmt-clean is left exactly as it was.
func (p *Program) Edits() ([]FileEdit, error) {
	var edits []FileEdit
	for _, path := range p.order {
		if !p.touched[path] {
			continue
		}

		df := p.files[path]
		original, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read %v: %w", path, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("stat %v: %w", path, err)
		}

		r := decorator.NewRestorerWithImports(df.pkg.PkgPath, simple.New(p.names))
		fr := r.FileRestorer()
		for importPath, name := range p.aliases[path] {
			fr.Alias[importPath] = name
		}

		restored, err := fr.RestoreFile(df.file)
		if err != nil {
			return nil, fmt.Errorf("render %v: %w", path, err)
		}

		var buf bytes.Buffer
		if err := format.Node(&buf, r.Fset, restored); err != nil {
			return nil, fmt.Errorf("format %v: %w", path, err)
		}

		if bytes.Equal(original, buf.Bytes()) {
			continue
		}
		edits = append(edits, FileEdit{
			Path:     path,
			Original: original,
			Modified: buf.Bytes(),
			Mode:     info.Mode().Perm(),
		})
	}
	return edits, nil
}

// Root returns the canonical root directory of the loaded module.
func (p *Program) Root() string {
	return p.root
}

// FilePaths returns every source file owned by the loaded module.
func (p *Program) FilePaths() []string {
	return append([]string(nil), p.order...)
}

// ModulePath returns the import path declared by the loaded module.
func (p *Program) ModulePath() string {
	return p.modulePath
}
