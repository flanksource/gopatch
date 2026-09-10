package patchgen

import (
	"fmt"

	"github.com/uber-go/gopatch/internal/refactor"
	"go.uber.org/multierr"
)

// FieldPosition says where in a struct a new field is placed.
type FieldPosition uint8

// Supported field positions.
const (
	// PositionLast appends the field after the existing ones.
	PositionLast FieldPosition = iota
	// PositionFirst inserts the field before the existing ones.
	PositionFirst
)

// structTarget is the part of a field operation that names the struct, shared by
// every operation in this file.
type structTarget struct {
	// Struct is the struct type's name. The generated change is scoped to it,
	// which is what keeps a field operation from hitting a same-named field on
	// an unrelated type.
	Struct string

	// Field is the field's name.
	Field string

	// Package is the package clause of the package that declares the struct.
	// When empty the change matches the struct in any package, which is wrong as
	// soon as two packages declare the same name.
	Package string
}

func (t structTarget) validate() error {
	var errs error
	if _, err := refactor.ParseIdent("struct name", t.Struct); err != nil {
		errs = multierr.Append(errs, err)
	}
	if _, err := refactor.ParseIdent("field name", t.Field); err != nil {
		errs = multierr.Append(errs, err)
	}
	if t.Package != "" {
		if _, err := refactor.ParseIdent("package name", t.Package); err != nil {
			errs = multierr.Append(errs, err)
		}
	}
	return errs
}

// scope adds the package context line that confines a struct change to the
// package that declares it.
//
// Without it the change matches "type User struct" wherever it appears, so a
// same-named struct in another package covered by the given paths is rewritten
// too. Verified: adding the clause narrows the same change to one package.
func (t structTarget) scope(c *change) *change {
	if t.Package != "" {
		c.pkg(t.Package)
	}
	return c
}

// fieldTypeOrMetavar returns the text to write for a field's type, declaring a
// metavariable when the caller does not know it.
//
// Binding the type to an expression metavariable is what lets a field operation
// work without the caller naming the type.
func fieldTypeOrMetavar(c *change, declared string) string {
	if declared != "" {
		return declared
	}
	return c.expr(c.freshIdent("T"))
}

// fieldDecl renders a field declaration line.
//
// The tag is part of the declaration and cannot be elided or bound to a
// metavariable — the grammar requires a string literal there — so an operation
// that touches a tagged field has to reproduce its tag exactly or match nothing
// at all.
func fieldDecl(name, typ, tag string) string {
	decl := name + " " + typ
	if tag != "" {
		decl += " " + tag
	}
	return decl
}

// DeleteField removes a struct field, and the matching key from keyed composite
// literals of that struct.
type DeleteField struct {
	structTarget

	// Type is the field's declared type. When empty, it is bound to an
	// expression metavariable instead.
	Type string

	// Tag is the field's current struct tag including backquotes. It must be
	// given whenever the field has one, or the change matches nothing.
	Tag string

	// ImportPath and Qualifier extend the composite-literal change to importing
	// packages. When empty, only unqualified literals are rewritten.
	ImportPath string
	Qualifier  string
}

var _ Operation = DeleteField{}

// Validate reports every problem with the operation's arguments.
func (o DeleteField) Validate() error {
	errs := o.structTarget.validate()
	if o.Type != "" {
		if _, err := refactor.ParseTypeExpr("field type", o.Type); err != nil {
			errs = multierr.Append(errs, err)
		}
	}
	return multierr.Append(errs, validateLiteralScope(o.ImportPath, o.Qualifier))
}

// Describe summarizes the deletion.
func (o DeleteField) Describe() string {
	return fmt.Sprintf("delete field %v.%v and drop it from %v literals", o.Struct, o.Field, o.Struct)
}

// Patch renders the deletion.
func (o DeleteField) Patch() ([]byte, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}

	p := newProgram(o.Describe())

	decl := o.scope(p.change())
	decl.diff(
		ctx(0, "type "+o.Struct+" struct {"),
		dots(1),
		del(1, fieldDecl(o.Field, fieldTypeOrMetavar(decl, o.Type), o.Tag)),
		dots(1),
		ctx(0, "}"),
	)

	addLiteralChange(p, o.structTarget, o.ImportPath, o.Qualifier, func(c *change, lit string) {
		value := c.expr(c.freshIdent("v"))
		c.diff(
			ctx(0, lit+"{"),
			commaDots(1),
			del(1, o.Field+": "+value+","),
			commaDots(1),
			ctx(0, "}"),
		)
	})

	return p.Bytes()
}

// AddField adds a field to a struct.
type AddField struct {
	structTarget

	// Type is the new field's type.
	Type string

	// Tag is the new field's struct tag including backquotes, or empty.
	Tag string

	// Position says whether the field goes first or last.
	Position FieldPosition
}

var _ Operation = AddField{}

// Validate reports every problem with the operation's arguments.
func (o AddField) Validate() error {
	errs := o.structTarget.validate()
	if _, err := refactor.ParseTypeExpr("field type", o.Type); err != nil {
		errs = multierr.Append(errs, err)
	}
	return errs
}

// Describe summarizes the addition.
func (o AddField) Describe() string {
	where := "last"
	if o.Position == PositionFirst {
		where = "first"
	}
	return fmt.Sprintf("add field %v.%v %v as the %v field", o.Struct, o.Field, o.Type, where)
}

// Patch renders the addition.
func (o AddField) Patch() ([]byte, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}

	field := fieldDecl(o.Field, o.Type, o.Tag)

	p := newProgram(o.Describe())
	c := o.scope(p.change())

	lines := []line{ctx(0, "type "+o.Struct+" struct {")}
	if o.Position == PositionFirst {
		lines = append(lines, ins(1, field), dots(1))
	} else {
		lines = append(lines, dots(1), ins(1, field))
	}
	c.diff(append(lines, ctx(0, "}"))...)

	return p.Bytes()
}

// EditFieldType changes a struct field's declared type.
type EditFieldType struct {
	structTarget

	// NewType is the replacement type.
	NewType string

	// OldType pins the current type. When empty, it is bound to an expression
	// metavariable instead.
	OldType string

	// Tag is the field's current struct tag including backquotes. It must be
	// given whenever the field has one, or the change matches nothing, and it is
	// carried through to the replacement unchanged.
	Tag string
}

var _ Operation = EditFieldType{}

// Validate reports every problem with the operation's arguments.
func (o EditFieldType) Validate() error {
	errs := o.structTarget.validate()
	if _, err := refactor.ParseTypeExpr("--type", o.NewType); err != nil {
		errs = multierr.Append(errs, err)
	}
	if o.OldType != "" {
		if _, err := refactor.ParseTypeExpr("current field type", o.OldType); err != nil {
			errs = multierr.Append(errs, err)
		}
	}
	if o.OldType != "" && o.OldType == o.NewType {
		errs = multierr.Append(errs, fmt.Errorf("%v.%v is already %v", o.Struct, o.Field, o.NewType))
	}
	return errs
}

// Describe summarizes the change.
func (o EditFieldType) Describe() string {
	return fmt.Sprintf("change the type of field %v.%v to %v", o.Struct, o.Field, o.NewType)
}

// Patch renders the change.
func (o EditFieldType) Patch() ([]byte, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}

	p := newProgram(o.Describe())
	c := o.scope(p.change())
	c.diff(
		ctx(0, "type "+o.Struct+" struct {"),
		dots(1),
		del(1, fieldDecl(o.Field, fieldTypeOrMetavar(c, o.OldType), o.Tag)),
		ins(1, fieldDecl(o.Field, o.NewType, o.Tag)),
		dots(1),
		ctx(0, "}"),
	)

	return p.Bytes()
}

// EditFieldTag sets, replaces, or clears a struct field's tag.
//
// A tag cannot be bound to a metavariable — the parser requires a string
// literal in that position — so replacing an existing tag needs the current one
// pinned exactly. Callers resolve it with refactor.FindStructField.
type EditFieldTag struct {
	structTarget

	// Tag is the replacement tag including backquotes. Empty clears the tag.
	Tag string

	// OldTag is the field's current tag including backquotes, or empty when the
	// field has none.
	OldTag string

	// Type pins the field's declared type. When empty, it is bound to an
	// expression metavariable instead.
	Type string
}

var _ Operation = EditFieldTag{}

// Validate reports every problem with the operation's arguments.
func (o EditFieldTag) Validate() error {
	errs := o.structTarget.validate()
	if o.Tag == "" && o.OldTag == "" {
		errs = multierr.Append(errs, fmt.Errorf("%v.%v has no tag to clear", o.Struct, o.Field))
	}
	if o.Tag != "" && o.Tag == o.OldTag {
		errs = multierr.Append(errs, fmt.Errorf("%v.%v already has that tag", o.Struct, o.Field))
	}
	if o.Type != "" {
		if _, err := refactor.ParseTypeExpr("field type", o.Type); err != nil {
			errs = multierr.Append(errs, err)
		}
	}
	return errs
}

// Describe summarizes the change.
func (o EditFieldTag) Describe() string {
	if o.Tag == "" {
		return fmt.Sprintf("clear the struct tag on field %v.%v", o.Struct, o.Field)
	}
	return fmt.Sprintf("set the struct tag on field %v.%v to %v", o.Struct, o.Field, o.Tag)
}

// Patch renders the change.
func (o EditFieldTag) Patch() ([]byte, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}

	p := newProgram(o.Describe())
	c := o.scope(p.change())

	typ := fieldTypeOrMetavar(c, o.Type)
	c.diff(
		ctx(0, "type "+o.Struct+" struct {"),
		dots(1),
		del(1, fieldDecl(o.Field, typ, o.OldTag)),
		ins(1, fieldDecl(o.Field, typ, o.Tag)),
		dots(1),
		ctx(0, "}"),
	)

	return p.Bytes()
}

// validateLiteralScope checks the optional import scoping shared by the field
// operations that also rewrite composite literals.
func validateLiteralScope(importPath, qualifier string) error {
	var errs error
	if importPath != "" {
		if _, err := refactor.ParseImportPath("--import", importPath); err != nil {
			errs = multierr.Append(errs, err)
		}
	}
	if qualifier != "" {
		if _, err := refactor.ParseIdent("--qualifier", qualifier); err != nil {
			errs = multierr.Append(errs, err)
		}
	}
	if importPath == "" && qualifier != "" {
		errs = multierr.Append(errs, fmt.Errorf("--qualifier needs --import"))
	}
	return errs
}

// addLiteralChange appends the composite-literal change, or two of them when an
// import path is given: one for unqualified literals in the declaring package,
// one for qualified literals in importing packages.
func addLiteralChange(
	p *program,
	target structTarget,
	importPath, qualifier string,
	body func(c *change, literal string),
) {
	structName := target.Struct

	// The local literal change is scoped to the declaring package for the same
	// reason the declaration change is; the consumer change is scoped by the
	// import instead, which already names the package.
	body(target.scope(p.change()), structName)

	if importPath == "" {
		return
	}

	q := qualifier
	if q == "" {
		if derived, err := refactor.DerivePackageName("--import", importPath); err == nil {
			q = derived
		} else {
			return
		}
	}

	consumers := p.change()
	consumers.importSpec(consumers.ident(q), importPath)
	body(consumers, q+"."+structName)
}
