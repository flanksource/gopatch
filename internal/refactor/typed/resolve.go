package typed

import (
	"fmt"
	"go/types"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dave/dst/decorator"
)

// Kind is the sort of declaration a typed operation acts on.
type Kind int

// The declarations the typed engine can resolve.
const (
	// KindType is a named type: a struct, an interface, or an alias target.
	KindType Kind = iota + 1

	// KindFunc is a top-level function.
	KindFunc

	// KindField is a field declared directly by a struct type.
	KindField

	// KindMethod is a method declared on a named type.
	KindMethod

	// KindVar is a package-level variable.
	KindVar

	// KindConst is a package-level constant.
	KindConst
)

// String names the kind as it appears in messages.
func (k Kind) String() string {
	switch k {
	case KindType:
		return "type"
	case KindFunc:
		return "func"
	case KindField:
		return "field"
	case KindMethod:
		return "method"
	case KindVar:
		return "var"
	case KindConst:
		return "const"
	default:
		return fmt.Sprintf("Kind(%d)", int(k))
	}
}

// Query names the declaration a typed operation acts on.
type Query struct {
	// Kind is the sort of declaration to look for.
	Kind Kind

	// Owner is the named type that declares the member, for KindField and
	// KindMethod. It is empty for a top-level declaration.
	Owner string

	// Name is the declaration's name.
	Name string

	// Package narrows the search to one package, by package clause or by import
	// path. Empty searches every loaded package, which is an error when more
	// than one declares the name.
	Package string

	// Dir narrows the search to the package whose files are, or are under, this
	// path. It is an absolute path to a file or a directory.
	//
	// Naming a path is often easier than naming a package: you can see the file
	// in front of you without knowing what its package is called.
	Dir string
}

// Symbol is a resolved declaration.
type Symbol struct {
	// Kind is the sort of declaration this is.
	Kind Kind

	// Pkg is the import path of the declaring package.
	Pkg string

	// Desc names the declaration the way a user wrote it, for messages.
	Desc string

	// objs holds one types.Object per package variant that type-checked the
	// declaration. A package and its test variant are checked separately and
	// produce distinct objects for the same source, so matching a reference
	// means testing against all of them.
	//
	// A method rename grows this set to the interface methods it implements and
	// their other implementations; see linkMethodSet.
	objs map[types.Object]bool

	// owners are the named types that declare a field or method. There is more
	// than one once a method rename has been extended across an interface.
	owners []*types.Named

	// editablePackages are packages owned by another module participating in a
	// cross-module rename. Their objects may appear in this load through export
	// data and are safe to link because that module will be edited separately.
	editablePackages map[string]bool
}

// Resolve finds the declaration q names.
//
// It fails rather than choosing when more than one package declares the name,
// naming the flag that disambiguates: a rename applied to the wrong package
// would be silently wrong everywhere.
func (p *Program) Resolve(q Query) (*Symbol, error) {
	sym := &Symbol{
		Kind: q.Kind,
		Desc: q.describe(),
		objs: make(map[types.Object]bool),
	}

	paths := make(map[string]bool)
	var (
		scoped    bool // some package matched q.Package
		ownerOnly bool // the owner type was found but not the member
	)

	for _, pkg := range p.pkgs {
		if pkg.Types == nil {
			continue
		}
		if !q.matchesPackage(pkg) {
			continue
		}
		scoped = true

		obj, owner, err := q.lookup(pkg)
		if err != nil {
			return nil, err
		}
		if obj == nil {
			if owner != nil {
				ownerOnly = true
			}
			continue
		}

		sym.objs[obj] = true
		if owner != nil && !hasOwner(sym.owners, owner) {
			sym.owners = append(sym.owners, owner)
		}
		paths[pkg.PkgPath] = true
	}

	switch {
	case len(paths) == 1:
		for path := range paths {
			sym.Pkg = path
		}
		return sym, nil

	case len(paths) > 1:
		names := sortedKeys(paths)
		return nil, fmt.Errorf(
			"%s is declared in more than one package (%s); qualify the target with the one "+
				"you mean, as in %q, or with the directory it is in",
			sym.Desc, strings.Join(names, ", "), names[0]+"."+q.qualifiedName())

	case !scoped:
		return nil, fmt.Errorf(
			"nothing in this module matches %v; a package is named by its clause or its "+
				"import path, a directory by a path under the module",
			q.describeScope())

	case ownerOnly:
		return nil, fmt.Errorf("%v has no %v %q", q.Owner, q.Kind, q.Name)

	default:
		return nil, fmt.Errorf("could not find %s", sym.Desc)
	}
}

func (q Query) describe() string {
	return fmt.Sprintf("%v %v", q.Kind, q.qualifiedName())
}

// qualifiedName renders the dotted name a command line would carry, so that a
// message can suggest the qualified form of what was typed.
func (q Query) qualifiedName() string {
	if q.Owner != "" {
		return q.Owner + "." + q.Name
	}
	return q.Name
}

func (q Query) matchesPackage(pkg *decorator.Package) bool {
	if q.Package != "" && q.Package != pkg.Name && q.Package != pkg.PkgPath {
		return false
	}
	return q.Dir == "" || packageUnder(pkg, q.Dir)
}

// packageUnder reports whether any of the package's files is dir, or lies under
// it when dir is a directory.
func packageUnder(pkg *decorator.Package, dir string) bool {
	dir = realPath(dir)
	for _, path := range pkg.GoFiles {
		path = realPath(path)
		if path == dir || strings.HasPrefix(path, dir+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// describeScope names the filters a query narrowed the search with.
func (q Query) describeScope() string {
	switch {
	case q.Package != "" && q.Dir != "":
		return fmt.Sprintf("package %q under %v", q.Package, q.Dir)
	case q.Package != "":
		return fmt.Sprintf("package %q", q.Package)
	default:
		return q.Dir
	}
}

// lookup finds the declaration in one package, also reporting the owner type
// when the query names a member and the owner exists.
func (q Query) lookup(pkg *decorator.Package) (types.Object, *types.Named, error) {
	scope := pkg.Types.Scope()

	if q.Kind == KindType || q.Kind == KindFunc || q.Kind == KindVar || q.Kind == KindConst {
		obj := scope.Lookup(q.Name)
		if obj == nil {
			return nil, nil, nil
		}
		switch obj.(type) {
		case *types.TypeName:
			if q.Kind != KindType {
				return nil, nil, fmt.Errorf("%q is a type, not a %v", q.Name, q.Kind)
			}
		case *types.Func:
			if q.Kind != KindFunc {
				return nil, nil, fmt.Errorf("%q is a func, not a %v", q.Name, q.Kind)
			}
		case *types.Var:
			if q.Kind != KindVar {
				return nil, nil, fmt.Errorf("%q is a var, not a %v", q.Name, q.Kind)
			}
		case *types.Const:
			if q.Kind != KindConst {
				return nil, nil, fmt.Errorf("%q is a const, not a %v", q.Name, q.Kind)
			}
		default:
			return nil, nil, fmt.Errorf(
				"%q is a %s, which cannot be renamed or deleted by this command",
				q.Name, objectKind(obj))
		}
		return obj, nil, nil
	}

	ownerObj := scope.Lookup(q.Owner)
	if ownerObj == nil {
		return nil, nil, nil
	}
	named, ok := ownerObj.Type().(*types.Named)
	if !ok {
		return nil, nil, fmt.Errorf("%q is not a named type", q.Owner)
	}

	if q.Kind == KindMethod {
		for i := 0; i < named.NumMethods(); i++ {
			if m := named.Method(i); m.Name() == q.Name {
				return m, named, nil
			}
		}
		// An interface's methods hang off its underlying type, not off the named
		// type, so they need a second look. Renaming one of these renames every
		// implementation with it; see linkMethodSet.
		if iface, ok := named.Underlying().(*types.Interface); ok {
			for i := 0; i < iface.NumMethods(); i++ {
				if m := iface.Method(i); m.Name() == q.Name {
					return m, named, nil
				}
			}
		}
		return nil, named, nil
	}

	st, ok := named.Underlying().(*types.Struct)
	if !ok {
		return nil, nil, fmt.Errorf("%q is not a struct type", q.Owner)
	}
	for i := 0; i < st.NumFields(); i++ {
		if f := st.Field(i); f.Name() == q.Name {
			return f, named, nil
		}
	}
	return nil, named, nil
}

// objectKind names what an object is, for an error that has to explain why a
// name cannot be used.
func objectKind(obj types.Object) string {
	switch obj.(type) {
	case *types.Const:
		return "constant"
	case *types.Var:
		return "variable"
	case *types.PkgName:
		return "package name"
	case *types.Builtin:
		return "builtin"
	case *types.Label:
		return "label"
	default:
		return "declaration"
	}
}

func hasOwner(owners []*types.Named, want *types.Named) bool {
	for _, o := range owners {
		if o.Obj() == want.Obj() {
			return true
		}
	}
	return false
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
