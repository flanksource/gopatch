package refactor

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
)

// FuncSignature is a top-level function's parameter and result lists, rendered
// as they appear in source.
//
// A generated patch reproduces the signature exactly rather than eliding it:
// "..." in a result list makes go/format fail, and two "..." in one signature
// cross-connect and corrupt the output.
type FuncSignature struct {
	// Params is the parameter list without parentheses, such as "r *Request".
	Params string

	// Results is the result list including parentheses where Go requires them,
	// such as "(*Response, error)" or "error". Empty when there are none.
	Results string
}

// String renders the signature as it follows a function name.
func (s FuncSignature) String() string {
	if s.Results == "" {
		return "(" + s.Params + ")"
	}
	return "(" + s.Params + ") " + s.Results
}

// Search narrows a source lookup to the files, and optionally the package, a
// command was pointed at.
//
// The patch-generating commands read the current source before emitting a patch,
// because a signature and a field's type and tag have to be reproduced exactly.
// This is how a caller says which declaration it means when the same name exists
// in more than one place.
type Search struct {
	// Arg is the command line argument the lookup is for, so that errors name
	// what the user got wrong.
	Arg string

	// Files are the files to read.
	Files []string

	// Package is a package clause. Empty reads every file, which is an error
	// when the matches disagree.
	//
	// It is a clause rather than an import path because these commands work
	// without building anything, and a file's package clause is the only package
	// identity available from source alone.
	Package string
}

// parse reads the files in scope, skipping the ones that do not parse: a patch
// may well be the thing that fixes them.
func (s Search) parse(fset *token.FileSet) []*ast.File {
	out := make([]*ast.File, 0, len(s.Files))
	for _, path := range s.Files {
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		if s.Package != "" && f.Name.Name != s.Package {
			continue
		}
		out = append(out, f)
	}
	return out
}

// FuncSignature returns the signature of the top-level function called name.
//
// It fails when no such function is found, when the name belongs to a method,
// or when the matches disagree, rather than picking one: a patch built from the
// wrong signature would silently match nothing.
func (s Search) FuncSignature(name string) (FuncSignature, error) {
	var (
		found    []FuncSignature
		asMethod []string
	)

	fset := token.NewFileSet()
	for _, f := range s.parse(fset) {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != name {
				continue
			}
			if fn.Recv != nil {
				recv, _, _, err := RenderFields(fset, fn.Recv)
				if err == nil {
					asMethod = append(asMethod, recv)
				}
				continue
			}

			sig, err := signatureOf(fset, fn.Type)
			if err != nil {
				return FuncSignature{}, fmt.Errorf("could not read the signature of %q: %v", name, err)
			}
			found = append(found, sig)
		}
	}

	switch {
	case len(found) == 1:
		return found[0], nil

	case len(found) > 1:
		if distinct := distinctSignatures(found); len(distinct) > 1 {
			return FuncSignature{}, fmt.Errorf(
				"%q is declared with more than one signature in %s (%s); qualify %s with the "+
					"package or directory that declares the one you mean",
				name, s.scope(), strings.Join(distinct, " and "), s.Arg)
		}
		return found[0], nil

	case len(asMethod) > 0:
		// The patch engine cannot match a method through a plain func pattern,
		// and a receiver plus a parameter list in one patch cross-connect, so
		// this has to be an error rather than a silent no-op.
		return FuncSignature{}, fmt.Errorf(
			"%q is a method on %s, which this command cannot change; "+
				"use the type-aware commands for methods", name, strings.Join(asMethod, ", "))

	default:
		return FuncSignature{}, fmt.Errorf(
			"could not find a top-level func %q in %s", name, s.scope())
	}
}

// scope names what the search covered, for a message that has to say where it
// looked.
func (s Search) scope() string {
	if s.Package != "" {
		return fmt.Sprintf("package %v", s.Package)
	}
	return "the given paths"
}

func signatureOf(fset *token.FileSet, t *ast.FuncType) (FuncSignature, error) {
	params, _, _, err := RenderFields(fset, t.Params)
	if err != nil {
		return FuncSignature{}, err
	}

	results, arity, named, err := RenderFields(fset, t.Results)
	if err != nil {
		return FuncSignature{}, err
	}

	return FuncSignature{
		Params:  params,
		Results: ParenthesizeResults(results, arity, named),
	}, nil
}

func distinctSignatures(sigs []FuncSignature) []string {
	seen := make(map[string]struct{}, len(sigs))
	for _, s := range sigs {
		seen[s.String()] = struct{}{}
	}

	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// RequireImportBlock reports an error when a file declaring the top-level
// function name has no import declaration.
//
// gopatch cannot add the first import to a file: with no import block to extend,
// the change is applied as an inserted declaration and the new import is
// dropped, leaving the file with the function declared twice. Callers that would
// add an import have to refuse rather than produce that.
func (s Search) RequireImportBlock(name, importPath string) error {
	fset := token.NewFileSet()

	var bare []string
	for _, f := range s.parse(fset) {
		if !declaresFunc(f, name) {
			continue
		}
		if len(f.Imports) == 0 {
			bare = append(bare, filepath.Base(fset.Position(f.Pos()).Filename))
		}
	}

	if len(bare) == 0 {
		return nil
	}

	sort.Strings(bare)
	return fmt.Errorf(
		"cannot add import %q: %v declare(s) %q but has no imports yet, and gopatch "+
			"cannot add the first import to a file. Add the import by hand, then re-run "+
			"without --import",
		importPath, strings.Join(bare, ", "), name)
}

func declaresFunc(f *ast.File, name string) bool {
	for _, decl := range f.Decls {
		if fn, ok := decl.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Name.Name == name {
			return true
		}
	}
	return false
}

// StructField describes one field of a struct declaration.
type StructField struct {
	// Type is the field's declared type, such as "string" or "*http.Client".
	Type string

	// Tag is the field's struct tag including its backquotes, or empty when it
	// has none.
	Tag string
}

// StructField returns the named field of the named struct.
//
// A field declared in a group such as "Start, End token.Pos" is reported as an
// error: the patch engine does not match a grouped declaration, so a patch
// built for it would silently do nothing. So is a field that two packages
// declare differently, because the patch would be built from one and applied to
// both.
func (s Search) StructField(structName, fieldName string) (StructField, error) {
	fset := token.NewFileSet()

	var (
		grouped bool
		found   []StructField
	)
	for _, f := range s.parse(fset) {
		st := findStructType(f, structName)
		if st == nil {
			continue
		}

		for _, field := range st.Fields.List {
			for _, n := range field.Names {
				if n.Name != fieldName {
					continue
				}
				if len(field.Names) > 1 {
					grouped = true
					continue
				}

				typ, _, _, err := RenderFields(fset, &ast.FieldList{List: []*ast.Field{{Type: field.Type}}})
				if err != nil {
					return StructField{}, fmt.Errorf(
						"could not read the type of %v.%v: %v", structName, fieldName, err)
				}

				var tag string
				if field.Tag != nil {
					tag = field.Tag.Value
				}
				found = append(found, StructField{Type: typ, Tag: tag})
			}
		}
	}

	switch {
	case len(found) == 1:
		return found[0], nil

	case len(found) > 1:
		if distinct := distinctFields(found); len(distinct) > 1 {
			return StructField{}, fmt.Errorf(
				"%v.%v is declared differently in %s (%s); qualify %s with the package or "+
					"directory that declares the one you mean",
				structName, fieldName, s.scope(), strings.Join(distinct, " and "), s.Arg)
		}
		return found[0], nil

	case grouped:
		return StructField{}, fmt.Errorf(
			"%v.%v is declared in a group such as \"A, B string\", which gopatch does not match; "+
				"split the declaration first", structName, fieldName)

	default:
		return StructField{}, fmt.Errorf(
			"could not find field %v.%v in %s", structName, fieldName, s.scope())
	}
}

// distinctFields renders the distinct declarations among a set of matches.
func distinctFields(fields []StructField) []string {
	seen := make(map[string]struct{}, len(fields))
	for _, f := range fields {
		seen[strings.TrimSpace(f.Type+" "+f.Tag)] = struct{}{}
	}

	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func findStructType(f *ast.File, name string) *ast.StructType {
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.TYPE {
			continue
		}
		for _, spec := range gen.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok || ts.Name.Name != name {
				continue
			}
			if st, ok := ts.Type.(*ast.StructType); ok {
				return st
			}
		}
	}
	return nil
}
