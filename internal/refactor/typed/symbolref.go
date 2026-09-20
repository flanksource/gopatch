package typed

import (
	"go/ast"
	"go/types"
	"sort"
)

// SymbolRef identifies a field or method across go/packages loads.
type SymbolRef struct {
	ImportPath string
	Recv       string
	Name       string
}

// RefQuery resolves portable symbol identities in one loaded module.
type RefQuery struct {
	Kind             Kind
	Refs             []SymbolRef
	Desc             string
	EditablePackages map[string]bool
}

// Refs returns the portable identities represented by s.
func (s *Symbol) Refs() []SymbolRef {
	refs := make(map[SymbolRef]bool)
	for obj := range s.objs {
		owner := symbolOwner(s, obj)
		if obj.Pkg() == nil || owner == nil {
			continue
		}
		refs[SymbolRef{
			ImportPath: obj.Pkg().Path(),
			Recv:       owner.Obj().Name(),
			Name:       obj.Name(),
		}] = true
	}

	out := make([]SymbolRef, 0, len(refs))
	for ref := range refs {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ImportPath != out[j].ImportPath {
			return out[i].ImportPath < out[j].ImportPath
		}
		if out[i].Recv != out[j].Recv {
			return out[i].Recv < out[j].Recv
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// ResolveRefs resolves portable identities against this module and its imports.
func (p *Program) ResolveRefs(q RefQuery) *Symbol {
	sym := &Symbol{
		Kind:             q.Kind,
		Desc:             q.Desc,
		objs:             make(map[types.Object]bool),
		editablePackages: q.EditablePackages,
	}
	packages := p.typesPackages()
	for _, ref := range q.Refs {
		for _, pkg := range packages[ref.ImportPath] {
			obj, owner := lookupRef(pkg, q.Kind, ref)
			if obj == nil {
				continue
			}
			sym.objs[obj] = true
			if !hasOwner(sym.owners, owner) {
				sym.owners = append(sym.owners, owner)
			}
			if sym.Pkg == "" {
				sym.Pkg = ref.ImportPath
			}
		}
	}
	if len(sym.objs) == 0 {
		return nil
	}
	return sym
}

// ExpandRefs adds method identities linked by interface satisfaction.
func (p *Program) ExpandRefs(sym *Symbol) []SymbolRef {
	if sym == nil {
		return nil
	}
	if sym.Kind == KindMethod {
		p.linkMethodSet(sym)
	}
	return sym.Refs()
}

// HasSourceIdentifiers reports whether Rename can edit an identifier in p.
func (p *Program) HasSourceIdentifiers(sym *Symbol) bool {
	if sym == nil {
		return false
	}
	for _, pkg := range p.pkgs {
		if pkg.TypesInfo == nil || pkg.Decorator == nil {
			continue
		}
		for _, refs := range []map[*ast.Ident]types.Object{pkg.TypesInfo.Defs, pkg.TypesInfo.Uses} {
			for id, obj := range refs {
				if sym.objs[obj] && p.ownedFile(pkg, id) != nil {
					return true
				}
			}
		}
	}
	return false
}

// PackagePaths returns the import paths owned by this module.
func (p *Program) PackagePaths() []string {
	paths := make(map[string]bool)
	for _, pkg := range p.pkgs {
		paths[pkg.PkgPath] = true
	}
	return sortedKeys(paths)
}

func (p *Program) typesPackages() map[string][]*types.Package {
	found := make(map[string][]*types.Package)
	seen := make(map[*types.Package]bool)
	queue := make([]*types.Package, 0, len(p.pkgs))
	for _, pkg := range p.pkgs {
		if pkg.Types != nil {
			queue = append(queue, pkg.Types)
		}
	}
	for len(queue) > 0 {
		pkg := queue[0]
		queue = queue[1:]
		if seen[pkg] {
			continue
		}
		seen[pkg] = true
		found[pkg.Path()] = append(found[pkg.Path()], pkg)
		queue = append(queue, pkg.Imports()...)
	}
	return found
}

func lookupRef(pkg *types.Package, kind Kind, ref SymbolRef) (types.Object, *types.Named) {
	ownerObj := pkg.Scope().Lookup(ref.Recv)
	if ownerObj == nil {
		return nil, nil
	}
	owner, ok := ownerObj.Type().(*types.Named)
	if !ok {
		return nil, nil
	}
	if kind == KindMethod {
		if obj, _, _ := types.LookupFieldOrMethod(owner, true, pkg, ref.Name); obj != nil {
			if method, ok := obj.(*types.Func); ok {
				return method, owner
			}
		}
		return nil, nil
	}
	st, ok := owner.Underlying().(*types.Struct)
	if !ok {
		return nil, nil
	}
	for i := 0; i < st.NumFields(); i++ {
		if field := st.Field(i); field.Name() == ref.Name {
			return field, owner
		}
	}
	return nil, nil
}

func symbolOwner(sym *Symbol, obj types.Object) *types.Named {
	if method, ok := obj.(*types.Func); ok {
		return receiverNamed(method)
	}
	for _, owner := range sym.owners {
		st, ok := owner.Underlying().(*types.Struct)
		if !ok {
			continue
		}
		for i := 0; i < st.NumFields(); i++ {
			if st.Field(i) == obj {
				return owner
			}
		}
	}
	return nil
}
