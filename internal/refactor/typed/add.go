package typed

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dave/dst"
	"github.com/dave/dst/decorator"
	"github.com/dave/dst/dstutil"
)

// Declaration describes a declaration to append to a file.
type Declaration struct {
	// File is the file to append to. It must be one of the loaded files.
	File string

	// Kind is KindType for a struct, KindFunc for a function, or KindMethod for
	// a method.
	Kind Kind

	// Name is the new declaration's name.
	Name string

	// Recv is the method's receiver, such as "u *User", for KindMethod.
	Recv string

	// Params is the parameter list without parentheses, for a func or method.
	Params string

	// Results is the result list, parenthesized where Go requires it.
	Results string

	// Imports are the packages Params or Results refer to that the file does not
	// import yet.
	Imports []ImportRef
}

// ImportRef names a package a new or rewritten declaration refers to.
type ImportRef struct {
	// Path is the import path.
	Path string

	// Qualifier is the name the declaration uses for the package.
	Qualifier string

	// Alias reports whether Qualifier has to be written into the import
	// declaration. It is false when Qualifier is the package's own name, which
	// is the usual case; a caller that cannot know the package's name says so by
	// naming the qualifier explicitly, and gets an aliased import.
	Alias bool
}

// _stubBody is the body every generated function gets.
//
// An empty body would not compile as soon as the signature declares a result,
// and guessing a zero value would produce a function that silently returns the
// wrong thing. A panic is honest and compiles either way.
const _stubBody = `panic("not implemented")`

// Add appends a declaration to a file, reporting the path it changed.
//
// This is the one operation the patch language cannot express even in principle:
// a patch has to match something to anchor its change, and a new declaration
// matches nothing.
func (p *Program) Add(decl Declaration) (string, error) {
	path, err := filepath.Abs(decl.File)
	if err != nil {
		return "", err
	}
	df := p.files[realPath(path)]
	if df == nil {
		return "", fmt.Errorf(
			"%v is not a Go file in this module, or it is excluded from the build", decl.File)
	}

	if err := p.checkUndeclared(df.pkg, decl); err != nil {
		return "", err
	}

	src, err := decl.render()
	if err != nil {
		return "", err
	}

	added, err := parseDecl(src)
	if err != nil {
		// A signature that parses on its own but not in place is a bug here, not
		// bad input, so show what was generated.
		return "", fmt.Errorf("could not build the declaration: %w\n\n%s", err, src)
	}

	if err := p.resolveTypeQualifiers(df, added, decl.Imports); err != nil {
		return "", err
	}

	df.file.Decls = append(df.file.Decls, added)
	p.mark(df)
	return df.path, nil
}

// checkUndeclared refuses to add a name the package already has.
func (p *Program) checkUndeclared(pkg *decorator.Package, decl Declaration) error {
	if pkg.Types == nil {
		return nil
	}

	if decl.Kind == KindMethod {
		owner, err := decl.receiverType()
		if err != nil {
			return err
		}
		q := Query{Kind: KindMethod, Owner: owner, Name: decl.Name, Package: pkg.PkgPath}
		obj, named, err := q.lookup(pkg)
		if err != nil {
			return err
		}
		if named == nil {
			return fmt.Errorf("package %v does not declare a type called %v", pkg.Name, owner)
		}
		if obj != nil {
			return fmt.Errorf("%v already has a method called %v", owner, decl.Name)
		}
		if kind := memberKind(named, decl.Name); kind != "" {
			return fmt.Errorf("%v already has a %v called %v", owner, kind, decl.Name)
		}
		return nil
	}

	if obj := pkg.Types.Scope().Lookup(decl.Name); obj != nil {
		return fmt.Errorf("package %v already declares %q at %v",
			pkg.Name, decl.Name, p.pos(pkg, obj.Pos()))
	}
	return nil
}

// receiverType returns the named type a receiver declaration refers to.
func (d Declaration) receiverType() (string, error) {
	recv := strings.TrimSpace(d.Recv)
	if recv == "" {
		return "", fmt.Errorf("a method needs a receiver")
	}

	// Take the last word and drop a pointer star: "u *User" and "*User" both
	// name User.
	fields := strings.Fields(recv)
	name := strings.TrimPrefix(fields[len(fields)-1], "*")
	if name == "" {
		return "", fmt.Errorf("invalid receiver %q", d.Recv)
	}
	return name, nil
}

// render builds the source of the new declaration.
func (d Declaration) render() (string, error) {
	switch d.Kind {
	case KindType:
		// Two lines rather than "struct{}": gofmt keeps a one-line empty struct
		// one-line, so a later `gopatch add field` would produce
		// "struct{ A int }" instead of a field per line.
		return fmt.Sprintf("type %v struct {\n}\n", d.Name), nil

	case KindFunc:
		return fmt.Sprintf("func %v(%v)%v {\n\t%v\n}\n",
			d.Name, d.Params, resultClause(d.Results), _stubBody), nil

	case KindMethod:
		return fmt.Sprintf("func (%v) %v(%v)%v {\n\t%v\n}\n",
			strings.TrimSpace(d.Recv), d.Name, d.Params, resultClause(d.Results), _stubBody), nil

	default:
		return "", fmt.Errorf("cannot add a %v", d.Kind)
	}
}

func resultClause(results string) string {
	if strings.TrimSpace(results) == "" {
		return ""
	}
	return " " + strings.TrimSpace(results)
}

// parseDecl parses one declaration into a dst node by wrapping it in a file.
func parseDecl(src string) (dst.Decl, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "generated.go", "package p\n\n"+src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	if len(f.Decls) != 1 {
		return nil, fmt.Errorf("expected one declaration, found %d", len(f.Decls))
	}

	dec := decorator.NewDecorator(fset)
	df, err := dec.DecorateFile(f)
	if err != nil {
		return nil, err
	}
	return df.Decls[0], nil
}

// resolveTypeQualifiers resolves the package qualifiers in a type expression.
//
// Every pkg.Name selector in a signature or a type is a package qualifier —
// there are no values there to select a field from — so one that does not resolve
// is an error.
func (p *Program) resolveTypeQualifiers(df *decoratedFile, node dst.Node, extra []ImportRef) error {
	unresolved := p.resolveQualifiers(df, node, extra)
	if len(unresolved) == 0 {
		return nil
	}

	return fmt.Errorf(
		"cannot resolve the package qualifier %s; pass --import <path>, or "+
			"--import <qualifier>=<path> when the package's name is not the last "+
			"segment of its path",
		strings.Join(sortedKeys(unresolved), " and "))
}

// resolveBodyQualifiers resolves the package qualifiers in a function body.
//
// Unlike a type expression, a body is full of selectors that are not package
// qualifiers at all: c.Host selects a field, err.Error() a method. Nothing in a
// reparsed body distinguishes those from a package reference, so a name that is
// neither one of the file's imports nor an --import is left exactly as written
// and the compiler has the last word. A body that names a package the file does
// not import therefore needs --import, and saying so is what this cannot do.
func (p *Program) resolveBodyQualifiers(df *decoratedFile, node dst.Node, extra []ImportRef) error {
	p.resolveQualifiers(df, node, extra)
	return nil
}

// resolveQualifiers turns pkg.Name selectors in new source into the single
// path-carrying identifier dst uses for a remote reference, reporting the
// qualifiers it could not resolve.
//
// The restorer rebuilds a file's import block from the paths its identifiers
// carry, so a selector parsed from a flag value — which carries none — would
// render as written with no import to back it.
func (p *Program) resolveQualifiers(df *decoratedFile, node dst.Node, extra []ImportRef) map[string]bool {
	paths := p.fileImports(df)
	for _, ref := range extra {
		paths[ref.Qualifier] = ref.Path
		if _, ok := p.names[ref.Path]; !ok {
			p.names[ref.Path] = ref.Qualifier
		}
		if ref.Alias {
			p.alias(df, ref.Path, ref.Qualifier)
		}
	}

	unresolved := make(map[string]bool)
	dstutil.Apply(node, func(c *dstutil.Cursor) bool {
		sel, ok := c.Node().(*dst.SelectorExpr)
		if !ok {
			return true
		}
		x, ok := sel.X.(*dst.Ident)
		if !ok {
			return true
		}

		path, ok := paths[x.Name]
		if !ok {
			unresolved[x.Name] = true
			return false
		}
		c.Replace(&dst.Ident{Name: sel.Sel.Name, Path: path})
		return false
	}, nil)

	return unresolved
}

// fileImports maps the qualifier each of a file's imports is referred to by to
// its import path.
func (p *Program) fileImports(df *decoratedFile) map[string]string {
	out := make(map[string]string, len(df.file.Imports))
	for _, spec := range df.file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil {
			continue
		}
		if spec.Name != nil {
			out[spec.Name.Name] = path
			continue
		}
		if name := p.importedName(df, spec, path); name != "" {
			out[name] = path
		}
	}
	return out
}

// importedName returns the name an unnamed import introduces.
//
// It is the imported package's own name, not the last segment of its path: the
// two differ often enough — gopkg.in/yaml.v3 is "yaml" — that guessing would
// resolve a qualifier to the wrong import, or leave one that is right looking
// unresolvable. go/types recorded the real name when it checked the file, so ask
// it; p.names is the fallback for a spec that has no type information, such as
// one this run added.
func (p *Program) importedName(df *decoratedFile, spec *dst.ImportSpec, path string) string {
	if df.pkg.TypesInfo != nil && df.pkg.Decorator != nil {
		if node, ok := df.pkg.Decorator.Ast.Nodes[spec]; ok {
			if astSpec, ok := node.(*ast.ImportSpec); ok {
				if name, ok := df.pkg.TypesInfo.Implicits[astSpec].(*types.PkgName); ok {
					return name.Imported().Name()
				}
			}
		}
	}
	return p.names[path]
}
