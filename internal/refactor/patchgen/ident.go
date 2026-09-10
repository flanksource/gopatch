package patchgen

import (
	"fmt"

	"github.com/uber-go/gopatch/internal/refactor"
	"go.uber.org/multierr"
)

// DeclKind names the sort of declaration an operation targets. It affects only
// the wording of Describe: gopatch has no type information, so renaming a
// struct, a named type and a function are the same identifier rewrite.
type DeclKind uint8

// Supported declaration kinds.
const (
	KindStruct DeclKind = iota + 1
	KindType
	KindFunc
)

func (k DeclKind) String() string {
	switch k {
	case KindStruct:
		return "struct"
	case KindType:
		return "type"
	case KindFunc:
		return "func"
	default:
		return "declaration"
	}
}

// Scope names the package that declares the symbol an operation targets.
type Scope struct {
	// Package is the declaring package's package clause, such as "http". It is
	// what keeps a rename from also hitting a same-named symbol in an
	// unrelated package.
	Package string

	// ImportPath is the declaring package's import path. When empty, the
	// generated program only covers the declaring package and Describe says
	// so: consumers in other packages are left alone.
	ImportPath string

	// Qualifier is the name importing packages use for ImportPath. It defaults
	// to Package.
	//
	// gopatch cannot resolve a package name from an import path, so this has
	// to be the name the importing files actually use.
	Qualifier string
}

// qualifier returns the qualifier to use for consumers of ImportPath.
func (s Scope) qualifier() string {
	if s.Qualifier != "" {
		return s.Qualifier
	}
	return s.Package
}

func (s Scope) validate() error {
	var errs error
	if _, err := refactor.ParseIdent("--package", s.Package); err != nil {
		errs = multierr.Append(errs, err)
	}
	if s.ImportPath != "" {
		if _, err := refactor.ParseImportPath("--import", s.ImportPath); err != nil {
			errs = multierr.Append(errs, err)
		}
	}
	if s.Qualifier != "" {
		if _, err := refactor.ParseIdent("--qualifier", s.Qualifier); err != nil {
			errs = multierr.Append(errs, err)
		}
	}
	// No need to derive a name from the import path: the qualifier falls back
	// to Package, which is validated above.
	return errs
}

// RenameIdent renames a top-level identifier and every reference to it.
//
// It backs rename struct, rename type and rename func, which are all the same
// transformation. The generated program follows examples/destutter.patch: one
// change scoped to the declaring package, and — when the import path is known —
// one change for qualified references in importing packages.
type RenameIdent struct {
	Kind  DeclKind
	From  string
	To    string
	Scope Scope
}

var _ Operation = RenameIdent{}

// Validate reports every problem with the operation's arguments.
func (o RenameIdent) Validate() error {
	var errs error
	if _, err := refactor.ParseIdent("name", o.From); err != nil {
		errs = multierr.Append(errs, err)
	}
	if _, err := refactor.ParseIdent("new name", o.To); err != nil {
		errs = multierr.Append(errs, err)
	}
	if o.From == o.To {
		errs = multierr.Append(errs, fmt.Errorf("%q is already the new name", o.From))
	}
	return multierr.Append(errs, o.Scope.validate())
}

// Describe summarizes the rename, naming what is left alone when the import
// path is unknown.
func (o RenameIdent) Describe() string {
	base := fmt.Sprintf("rename %v %v -> %v in package %v", o.Kind, o.From, o.To, o.Scope.Package)
	if o.Scope.ImportPath == "" {
		return base + " (declaring package only; pass --import to update consumers)"
	}
	return base + " and in consumers of " + o.Scope.ImportPath
}

// Patch renders the rename.
func (o RenameIdent) Patch() ([]byte, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}

	p := newProgram(o.Describe())

	local := p.change()
	local.pkg(o.Scope.Package)
	local.diff(
		del(0, o.From),
		ins(0, o.To),
	)

	if path := o.Scope.ImportPath; path != "" {
		q := o.Scope.qualifier()

		consumers := p.change()
		// The import name must be a metavariable, or the change matches only
		// files that import the package under exactly this name.
		consumers.importSpec(consumers.ident(q), path)
		consumers.diff(
			del(0, q+"."+o.From),
			ins(0, q+"."+o.To),
		)
	}

	return p.Bytes()
}

// RenamePackage changes a package clause.
//
// The change needs a body because gopatch rejects a patch that matches only a
// package clause, so a lone identifier metavariable is used as the filler.
type RenamePackage struct {
	From string
	To   string
}

var _ Operation = RenamePackage{}

// Validate reports every problem with the operation's arguments.
func (o RenamePackage) Validate() error {
	var errs error
	if _, err := refactor.ParseIdent("package name", o.From); err != nil {
		errs = multierr.Append(errs, err)
	}
	if _, err := refactor.ParseIdent("new package name", o.To); err != nil {
		errs = multierr.Append(errs, err)
	}
	if o.From == o.To {
		errs = multierr.Append(errs, fmt.Errorf("%q is already the new name", o.From))
	}
	return errs
}

// Describe summarizes the rename.
//
// Renaming the clause does not update importing packages: their qualifiers
// follow the import path, which this operation does not change.
func (o RenamePackage) Describe() string {
	return fmt.Sprintf(
		"rename package clause %v -> %v (importing packages are unchanged; see rename import)",
		o.From, o.To)
}

// Patch renders the rename.
func (o RenamePackage) Patch() ([]byte, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}

	p := newProgram(o.Describe())
	c := p.change()
	c.renamePkg(o.From, o.To)
	c.filler()

	return p.Bytes()
}
