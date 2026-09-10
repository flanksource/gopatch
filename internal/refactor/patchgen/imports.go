package patchgen

import (
	"fmt"

	"github.com/uber-go/gopatch/internal/refactor"
	"go.uber.org/multierr"
)

// RenameImport rewrites an import path and, when the package name changes with
// it, every qualified reference to that package.
//
// The generated program follows testdata/rewrite_import_paths when the local
// name is preserved, and testdata/replace_any_import_with_unnamed when the
// qualifier changes.
type RenameImport struct {
	// FromPath is the import path to replace.
	FromPath string

	// ToPath is the replacement import path.
	ToPath string

	// FromName is the package name the importing files currently use. Derived
	// from FromPath when empty.
	FromName string

	// ToName is the package name to use after the change. Derived from ToPath
	// when empty. When it equals FromName, references are left alone and only
	// the import line changes.
	ToName string
}

var _ Operation = RenameImport{}

// names resolves the before and after package names.
func (o RenameImport) names() (from, to string, err error) {
	var errs error

	from = o.FromName
	if from == "" {
		if from, err = refactor.DerivePackageName("import path", o.FromPath); err != nil {
			errs = multierr.Append(errs, err)
		}
	}

	to = o.ToName
	if to == "" {
		if to, err = refactor.DerivePackageName("new import path", o.ToPath); err != nil {
			errs = multierr.Append(errs, err)
		}
	}

	return from, to, errs
}

// Validate reports every problem with the operation's arguments.
func (o RenameImport) Validate() error {
	var errs error
	if _, err := refactor.ParseImportPath("import path", o.FromPath); err != nil {
		errs = multierr.Append(errs, err)
	}
	if _, err := refactor.ParseImportPath("new import path", o.ToPath); err != nil {
		errs = multierr.Append(errs, err)
	}
	if o.FromPath == o.ToPath && o.FromName == o.ToName {
		errs = multierr.Append(errs, fmt.Errorf("%q is already the new import path", o.FromPath))
	}
	if o.FromName != "" {
		if _, err := refactor.ParseIdent("--name", o.FromName); err != nil {
			errs = multierr.Append(errs, err)
		}
	}
	if o.ToName != "" {
		if _, err := refactor.ParseIdent("--to-name", o.ToName); err != nil {
			errs = multierr.Append(errs, err)
		}
	}
	if errs != nil {
		return errs
	}

	_, _, err := o.names()
	return err
}

// Describe summarizes the rewrite.
func (o RenameImport) Describe() string {
	from, to, err := o.names()
	if err != nil || from == to {
		return fmt.Sprintf("rewrite import %v -> %v", o.FromPath, o.ToPath)
	}
	return fmt.Sprintf("rewrite import %v -> %v and references %v.X -> %v.X",
		o.FromPath, o.ToPath, from, to)
}

// Patch renders the rewrite.
func (o RenameImport) Patch() ([]byte, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}

	from, to, err := o.names()
	if err != nil {
		return nil, err
	}

	p := newProgram(o.Describe())
	c := p.change()

	if from == to {
		// The local name is unchanged, so references need no rewrite. The
		// change still needs a body, and a reference through the package is the
		// filler that also scopes it to files that actually use the package.
		name := c.ident(from)
		c.changeImport(name, o.FromPath, name, o.ToPath)
		c.diff(ctx(0, name+"."+c.ident(c.freshIdent("X"))))

		return p.Bytes()
	}

	// The qualifier changes, so every reference has to move with it. The old
	// name is a metavariable so that the change matches whether or not the
	// importing file named the import explicitly.
	oldName := c.ident(from)

	// An import whose name matches the path's last segment is written unnamed,
	// which is what gofmt-ed code looks like.
	newName := to
	if derived, err := refactor.DerivePackageName("new import path", o.ToPath); err == nil && derived == to {
		newName = ""
	}
	c.changeImport(oldName, o.FromPath, newName, o.ToPath)

	ref := c.ident(c.freshIdent("X"))
	c.diff(
		del(0, oldName+"."+ref),
		ins(0, to+"."+ref),
	)

	return p.Bytes()
}
