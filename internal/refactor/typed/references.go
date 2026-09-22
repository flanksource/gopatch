package typed

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/types"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dave/dst/decorator"
)

// SymbolQuery is one valid interpretation of an untyped symbol target.
type SymbolQuery struct {
	Owner   string
	Name    string
	Package string
	Dir     string
}

// SymbolRequest asks ResolveSymbol to select one declaration from all valid
// interpretations of Target.
type SymbolRequest struct {
	Target  string
	Queries []SymbolQuery
}

// Reference is one source location that defines or uses a symbol.
type Reference struct {
	Package    string
	File       string
	Line       int
	Column     int
	Definition bool
	SourceLine string
}

type symbolCandidate struct {
	symbol     *Symbol
	position   string
	suggestion string
}

type symbolCandidateKey struct {
	kind        Kind
	packagePath string
	owner       string
	name        string
}

type referenceGroup struct {
	items      map[*ast.Ident]types.Object
	definition bool
}

// ResolveSymbol resolves an untyped target to exactly one supported package
// declaration or member.
func (p *Program) ResolveSymbol(req SymbolRequest) (*Symbol, error) {
	if len(req.Queries) == 0 {
		return nil, fmt.Errorf("invalid symbol %q: no valid target interpretation", req.Target)
	}

	candidates, err := p.findSymbolCandidates(req.Queries)
	if err != nil {
		return nil, err
	}
	sort.Slice(candidates, func(i, j int) bool {
		left := candidates[i].symbol.Pkg + "." + candidates[i].symbol.Desc
		right := candidates[j].symbol.Pkg + "." + candidates[j].symbol.Desc
		return left < right
	})

	switch len(candidates) {
	case 0:
		return nil, fmt.Errorf("could not find symbol %q", req.Target)
	case 1:
		return candidates[0].symbol, nil
	default:
		lines := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			lines = append(lines, fmt.Sprintf("  %s in %s at %s; use %q",
				candidate.symbol.Desc, candidate.symbol.Pkg, candidate.position, candidate.suggestion))
		}
		return nil, fmt.Errorf("symbol %q is ambiguous:\n%s",
			req.Target, strings.Join(lines, "\n"))
	}
}

func (p *Program) findSymbolCandidates(queries []SymbolQuery) ([]*symbolCandidate, error) {
	found := make(map[symbolCandidateKey]*symbolCandidate)
	for _, query := range queries {
		for _, pkg := range p.pkgs {
			if pkg.Types == nil || !query.matchesPackage(pkg) {
				continue
			}
			obj, owner, kind, err := query.lookup(pkg)
			if err != nil {
				return nil, err
			}
			if obj == nil {
				continue
			}

			key := symbolCandidateKey{
				kind: kind, packagePath: pkg.PkgPath, owner: query.Owner, name: query.Name,
			}
			candidate := found[key]
			if candidate == nil {
				candidate = &symbolCandidate{
					symbol: &Symbol{
						Kind: kind, Pkg: pkg.PkgPath,
						Desc: describeSymbol(kind, query.Owner, query.Name),
						objs: make(map[types.Object]bool),
					},
					position:   p.pos(pkg, obj.Pos()),
					suggestion: p.symbolSuggestion(pkg, query),
				}
				found[key] = candidate
			}
			candidate.symbol.objs[obj] = true
			if owner != nil && !hasOwner(candidate.symbol.owners, owner) {
				candidate.symbol.owners = append(candidate.symbol.owners, owner)
			}
		}
	}
	candidates := make([]*symbolCandidate, 0, len(found))
	for _, candidate := range found {
		candidates = append(candidates, candidate)
	}
	return candidates, nil
}

func (q SymbolQuery) matchesPackage(pkg *decorator.Package) bool {
	if q.Package != "" && q.Package != pkg.Name && q.Package != pkg.PkgPath {
		return false
	}
	return q.Dir == "" || packageUnder(pkg, q.Dir)
}

func (q SymbolQuery) lookup(pkg *decorator.Package) (types.Object, *types.Named, Kind, error) {
	if q.Owner == "" {
		obj := pkg.Types.Scope().Lookup(q.Name)
		kind, ok := declarationKind(obj)
		if !ok {
			return nil, nil, 0, nil
		}
		return obj, nil, kind, nil
	}

	ownerObj := pkg.Types.Scope().Lookup(q.Owner)
	if ownerObj == nil {
		return nil, nil, 0, nil
	}
	owner, ok := ownerObj.Type().(*types.Named)
	if !ok {
		return nil, nil, 0, nil
	}
	for i := 0; i < owner.NumMethods(); i++ {
		if method := owner.Method(i); method.Name() == q.Name {
			return method, owner, KindMethod, nil
		}
	}
	if iface, ok := owner.Underlying().(*types.Interface); ok {
		for i := 0; i < iface.NumMethods(); i++ {
			if method := iface.Method(i); method.Name() == q.Name {
				return method, owner, KindMethod, nil
			}
		}
	}
	if strct, ok := owner.Underlying().(*types.Struct); ok {
		for i := 0; i < strct.NumFields(); i++ {
			if field := strct.Field(i); field.Name() == q.Name {
				return field, owner, KindField, nil
			}
		}
	}
	return nil, owner, 0, nil
}

func declarationKind(obj types.Object) (Kind, bool) {
	switch obj.(type) {
	case *types.TypeName:
		return KindType, true
	case *types.Func:
		return KindFunc, true
	case *types.Var:
		return KindVar, true
	case *types.Const:
		return KindConst, true
	default:
		return 0, false
	}
}

func describeSymbol(kind Kind, owner, name string) string {
	if owner != "" {
		name = owner + "." + name
	}
	return kind.String() + " " + name
}

func (p *Program) symbolSuggestion(pkg *decorator.Package, query SymbolQuery) string {
	name := query.Name
	if query.Owner != "" {
		name = query.Owner + "." + name
	}
	if len(pkg.GoFiles) == 0 {
		return pkg.PkgPath + "." + name
	}
	dir := filepath.Dir(realPath(pkg.GoFiles[0]))
	rel, err := filepath.Rel(p.root, dir)
	if err != nil || strings.HasPrefix(rel, "..") {
		return pkg.PkgPath + "." + name
	}
	if rel == "." {
		return ".:" + name
	}
	return "./" + filepath.ToSlash(rel) + ":" + name
}

// References returns each source definition and use of sym owned by p.
func (p *Program) References(sym *Symbol) ([]Reference, error) {
	if sym == nil {
		return nil, fmt.Errorf("cannot find references for a nil symbol")
	}

	seen := make(map[string]bool)
	var references []Reference
	for _, pkg := range p.pkgs {
		if pkg.TypesInfo == nil {
			continue
		}
		for _, group := range []referenceGroup{
			{items: pkg.TypesInfo.Defs, definition: true},
			{items: pkg.TypesInfo.Uses},
		} {
			if err := p.appendReferenceGroup(pkg, sym, group, seen, &references); err != nil {
				return nil, err
			}
		}
	}
	sortTypedReferences(references)
	return references, nil
}

func (p *Program) appendReferenceGroup(pkg *decorator.Package, sym *Symbol, group referenceGroup, seen map[string]bool, references *[]Reference) error {
	for id, obj := range group.items {
		if !sym.objs[obj] {
			continue
		}
		df := p.ownedFile(pkg, id)
		if df == nil {
			continue
		}
		position := pkg.Fset.Position(id.Pos())
		key := fmt.Sprintf("%s:%d:%t", df.path, position.Offset, group.definition)
		if seen[key] {
			continue
		}
		source, err := p.source(df.path)
		if err != nil {
			return err
		}
		line, err := referenceSourceLine(source, position.Offset)
		if err != nil {
			return fmt.Errorf("read reference at %s: %w", position, err)
		}
		seen[key] = true
		*references = append(*references, Reference{
			Package: pkg.PkgPath, File: df.path, Line: position.Line, Column: position.Column,
			Definition: group.definition, SourceLine: line,
		})
	}
	return nil
}

func sortTypedReferences(references []Reference) {
	sort.Slice(references, func(i, j int) bool {
		left, right := references[i], references[j]
		if left.Package != right.Package {
			return left.Package < right.Package
		}
		if left.File != right.File {
			return left.File < right.File
		}
		if left.Line != right.Line {
			return left.Line < right.Line
		}
		if left.Column != right.Column {
			return left.Column < right.Column
		}
		return left.Definition && !right.Definition
	})
}

func referenceSourceLine(source []byte, offset int) (string, error) {
	if offset < 0 || offset > len(source) {
		return "", fmt.Errorf("offset %d is outside a %d-byte file", offset, len(source))
	}
	start := bytes.LastIndexByte(source[:offset], '\n') + 1
	end := len(source)
	if relative := bytes.IndexByte(source[offset:], '\n'); relative >= 0 {
		end = offset + relative
	}
	return strings.TrimSpace(string(source[start:end])), nil
}
