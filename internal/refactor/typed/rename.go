package typed

import (
	"fmt"
	"go/ast"
	"go/types"
	"strings"

	"github.com/dave/dst"
	"github.com/dave/dst/decorator"
	"golang.org/x/tools/refactor/satisfy"
)

// Rename renames every reference that denotes sym, reporting how many source
// positions it changed.
//
// Because the target is a types.Object, this touches exactly the declaration
// and its references — a field declaration, every x.Field selector on that
// type, and every key in a keyed composite literal of it — and nothing that
// merely shares the name.
//
// Renaming a method also renames the interface methods it implements and their
// other implementations, because renaming one half of that relationship
// compiles the declaring package and breaks the assignment somewhere else.
func (p *Program) Rename(sym *Symbol, to string) (int, error) {
	if sym.Kind == KindMethod {
		if err := p.linkMethodSet(sym); err != nil {
			return 0, err
		}
	}
	if err := p.checkNameFree(sym, to); err != nil {
		return 0, err
	}

	changed := make(map[string]bool)
	for _, pkg := range p.pkgs {
		if pkg.TypesInfo == nil || pkg.Decorator == nil {
			continue
		}
		for _, refs := range []map[*ast.Ident]types.Object{pkg.TypesInfo.Defs, pkg.TypesInfo.Uses} {
			for id, obj := range refs {
				if !sym.objs[obj] {
					continue
				}
				if pos, ok := p.setIdentName(pkg, id, to); ok {
					changed[pos] = true
				}
			}
		}
	}

	if len(changed) == 0 {
		return 0, fmt.Errorf("found %s but no identifier to rewrite", sym.Desc)
	}
	return len(changed), nil
}

// setIdentName rewrites one identifier in the dst tree, returning the position
// it changed so callers count distinct source positions rather than one hit per
// package variant.
func (p *Program) setIdentName(pkg *decorator.Package, id *ast.Ident, to string) (string, bool) {
	node, ok := pkg.Decorator.Dst.Nodes[id]
	if !ok {
		// The decorator only covers pkg.GoFiles, so a cgo-preprocessed file has
		// syntax but no source on disk to rewrite.
		return "", false
	}
	ident, ok := node.(*dst.Ident)
	if !ok {
		return "", false
	}
	df := p.ownedFile(pkg, id)
	if df == nil {
		return "", false
	}

	ident.Name = to
	p.mark(df)
	return pkg.Fset.Position(id.Pos()).String(), true
}

// checkNameFree refuses a rename that would collide with a declaration already
// visible in the same scope.
func (p *Program) checkNameFree(sym *Symbol, to string) error {
	if sym.Kind == KindField || sym.Kind == KindMethod {
		for _, owner := range sym.owners {
			if kind := memberKind(owner, to); kind != "" {
				return fmt.Errorf("%v already has a %v called %q",
					types.TypeString(owner, types.RelativeTo(owner.Obj().Pkg())), kind, to)
			}
		}
		return nil
	}

	for _, pkg := range p.pkgs {
		if pkg.Types == nil || pkg.PkgPath != sym.Pkg {
			continue
		}
		if obj := pkg.Types.Scope().Lookup(to); obj != nil {
			return fmt.Errorf("package %v already declares %q at %v",
				pkg.Name, to, p.pos(pkg, obj.Pos()))
		}
	}
	return nil
}

// memberKind reports whether a named type already has a field or method called
// name, and which it is.
func memberKind(named *types.Named, name string) string {
	for i := 0; i < named.NumMethods(); i++ {
		if named.Method(i).Name() == name {
			return "method"
		}
	}
	switch u := named.Underlying().(type) {
	case *types.Struct:
		for i := 0; i < u.NumFields(); i++ {
			if u.Field(i).Name() == name {
				return "field"
			}
		}
	case *types.Interface:
		for i := 0; i < u.NumMethods(); i++ {
			if u.Method(i).Name() == name {
				return "method"
			}
		}
	}
	return ""
}

// linkMethodSet extends sym to every method that has to be renamed with it.
//
// satisfy reports the assignments that make one type's method set stand in for
// an interface. Every such pair has to move together, so the closure is walked
// until it stops growing; a method outside the loaded module cannot be renamed,
// so the operation is refused instead of half-applied.
func (p *Program) linkMethodSet(sym *Symbol) error {
	name := sym.Name()

	var finder satisfy.Finder
	for _, pkg := range p.pkgs {
		if pkg.TypesInfo == nil {
			continue
		}
		// decorator.Package.Syntax shadows the go/ast files with dst trees, and
		// the constraint finder needs the trees the type info was built from.
		finder.Find(pkg.TypesInfo, pkg.Package.Syntax)
	}

	constraints := make([]satisfy.Constraint, 0, len(finder.Result))
	for c := range finder.Result {
		constraints = append(constraints, c)
	}

	owners := map[*types.TypeName]*types.Named{}
	for _, owner := range sym.owners {
		owners[owner.Obj()] = owner
	}

	for grew := true; grew; {
		grew = false
		for _, c := range constraints {
			lhs := p.methodNamed(c.LHS, name, sym)
			rhs := p.methodNamed(c.RHS, name, sym)
			if lhs == nil || rhs == nil {
				continue
			}
			if sym.objs[lhs] == sym.objs[rhs] {
				continue
			}
			sym.objs[lhs] = true
			sym.objs[rhs] = true
			for _, m := range []*types.Func{lhs, rhs} {
				if owner := receiverNamed(m); owner != nil {
					owners[owner.Obj()] = owner
				}
			}
			grew = true
		}
	}

	sym.owners = sym.owners[:0]
	for _, owner := range owners {
		sym.owners = append(sym.owners, owner)
	}

	return p.checkRenamable(sym)
}

// methodNamed returns t's method called name, or nil when it has none.
func (p *Program) methodNamed(t types.Type, name string, sym *Symbol) *types.Func {
	var pkg *types.Package
	for obj := range sym.objs {
		pkg = obj.Pkg()
		break
	}

	obj, _, _ := types.LookupFieldOrMethod(t, true /* addressable */, pkg, name)
	fn, _ := obj.(*types.Func)
	return fn
}

// receiverNamed returns the named type a method is declared on.
func receiverNamed(fn *types.Func) *types.Named {
	sig, ok := fn.Type().(*types.Signature)
	if !ok || sig.Recv() == nil {
		return nil
	}
	t := sig.Recv().Type()
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}
	named, _ := t.(*types.Named)
	return named
}

// checkRenamable refuses when part of the symbol lives outside the loaded
// module, which is where a rename would stop and the build would break.
func (p *Program) checkRenamable(sym *Symbol) error {
	loaded := make(map[string]bool, len(p.pkgs))
	for _, pkg := range p.pkgs {
		loaded[pkg.PkgPath] = true
	}

	outside := make(map[string]bool)
	for obj := range sym.objs {
		if obj.Pkg() == nil {
			outside["the language itself"] = true
			continue
		}
		if !loaded[obj.Pkg().Path()] {
			outside[describeMethod(obj)] = true
		}
	}
	if len(outside) == 0 {
		return nil
	}

	return fmt.Errorf(
		"%s implements %s, which is outside this module and cannot be renamed; "+
			"renaming only this side would break the assignment",
		sym.Desc, strings.Join(sortedKeys(outside), " and "))
}

// describeMethod names a method by the type it hangs off, so that an interface
// method reads as "fmt.Stringer.String" rather than as its package path.
func describeMethod(obj types.Object) string {
	sig, ok := obj.Type().(*types.Signature)
	if !ok || sig.Recv() == nil {
		return fmt.Sprintf("%v.%v", obj.Pkg().Path(), obj.Name())
	}
	return fmt.Sprintf("%v.%v", types.TypeString(sig.Recv().Type(), nil), obj.Name())
}

// Name returns the declaration's own name.
func (s *Symbol) Name() string {
	for obj := range s.objs {
		return obj.Name()
	}
	return ""
}
