package typed

import (
	"fmt"
	"go/ast"
	"go/types"
	"strconv"

	"github.com/dave/dst"
	"github.com/dave/dst/decorator"
)

// ResolvePackage identifies one package clause named from in the selected files.
func (p *Program) ResolvePackage(from string, files []string) (string, error) {
	allowed := make(map[string]bool, len(files))
	for _, path := range files {
		allowed[realPath(path)] = true
	}
	paths := make(map[string]bool)
	for path, file := range p.files {
		if allowed[path] && file.pkg.Name == from {
			paths[file.pkg.PkgPath] = true
		}
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("could not find package %q in the selected files", from)
	}
	if len(paths) > 1 {
		return "", fmt.Errorf("package %q matches more than one import path (%v)", from, sortedKeys(paths))
	}
	for path := range paths {
		return path, nil
	}
	return "", nil
}

// RenamePackageClause changes every file in one package and its unaliased uses.
func (p *Program) RenamePackageClause(importPath, from, to string) (int, error) {
	changed := 0
	for _, path := range p.order {
		file := p.files[path]
		if file.pkg.PkgPath != importPath || file.file.Name.Name != from {
			continue
		}
		file.file.Name.Name = to
		p.mark(file)
		changed++
	}
	uses, err := p.RenamePackageUses(importPath, to)
	if err != nil {
		return 0, err
	}
	if changed == 0 {
		return 0, fmt.Errorf("could not find package clause %q for %v", from, importPath)
	}
	return changed + uses, nil
}

// RenamePackageUses changes qualifiers for unaliased imports of importPath.
func (p *Program) RenamePackageUses(importPath, to string) (int, error) {
	objects := make(map[types.Object]bool)
	for _, pkg := range p.pkgs {
		if pkg.TypesInfo == nil {
			continue
		}
		for _, file := range pkg.Package.Syntax {
			for _, spec := range file.Imports {
				path, err := strconv.Unquote(spec.Path.Value)
				if err != nil {
					return 0, err
				}
				if path != importPath || spec.Name != nil {
					continue
				}
				if object := pkg.TypesInfo.Implicits[spec]; object != nil {
					objects[object] = true
				}
			}
		}
	}
	p.names[importPath] = to
	return p.rewritePackageObjects(objects, importPath), nil
}

// RenameImportRequest describes one exact import-path rewrite.
type RenameImportRequest struct {
	FromPath        string
	ToPath          string
	FromName        string
	ToName          string
	OverrideAliases bool
}

// RenameImport rewrites exact import specs and their bound qualifiers.
func (p *Program) RenameImport(req RenameImportRequest) (int, error) {
	changed := 0
	for _, pkg := range p.pkgs {
		if pkg.TypesInfo == nil || pkg.Decorator == nil {
			continue
		}
		for _, file := range pkg.Package.Syntax {
			for _, spec := range file.Imports {
				matched, err := p.renameImportSpec(pkg, spec, req)
				if err != nil {
					return 0, err
				}
				changed += matched
			}
		}
	}
	if changed == 0 {
		return 0, nil
	}
	p.names[req.ToPath] = req.ToName
	return changed, nil
}

func (p *Program) renameImportSpec(pkg *decorator.Package, spec *ast.ImportSpec, req RenameImportRequest) (int, error) {
	path, err := strconv.Unquote(spec.Path.Value)
	if err != nil || path != req.FromPath {
		return 0, err
	}
	file := p.ownedFile(pkg, spec)
	if file == nil {
		return 0, nil
	}
	node, ok := pkg.Decorator.Dst.Nodes[spec].(*dst.ImportSpec)
	if !ok {
		return 0, fmt.Errorf("cannot rewrite import %q in %v", req.FromPath, file.path)
	}
	object := importObject(pkg, spec)
	if object != nil && req.FromName != "" && object.Name() != req.FromName {
		return 0, fmt.Errorf("import %q uses name %q, not %q", req.FromPath, object.Name(), req.FromName)
	}
	node.Path.Value = strconv.Quote(req.ToPath)
	changed := 1
	if spec.Name == nil {
		changed += p.rewritePackageObject(object, req.ToPath)
	} else {
		alias := spec.Name.Name
		if req.OverrideAliases {
			alias = req.ToName
			node.Name.Name = alias
		}
		changed += p.rewritePackageObject(object, req.ToPath)
		p.alias(file, req.ToPath, alias)
	}
	p.mark(file)
	return changed, nil
}

func importObject(pkg *decorator.Package, spec *ast.ImportSpec) types.Object {
	if spec.Name == nil {
		return pkg.TypesInfo.Implicits[spec]
	}
	return pkg.TypesInfo.Defs[spec.Name]
}

func (p *Program) rewritePackageObjects(objects map[types.Object]bool, importPath string) int {
	changed := make(map[string]bool)
	for _, pkg := range p.pkgs {
		if pkg.TypesInfo == nil || pkg.Decorator == nil {
			continue
		}
		for _, syntax := range pkg.Package.Syntax {
			ast.Inspect(syntax, func(node ast.Node) bool {
				selector, ok := node.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				qualifier, ok := selector.X.(*ast.Ident)
				if !ok || !objects[pkg.TypesInfo.Uses[qualifier]] {
					return true
				}
				ident, ok := pkg.Decorator.Dst.Nodes[selector].(*dst.Ident)
				file := p.ownedFile(pkg, selector)
				if !ok || file == nil {
					return true
				}
				ident.Path = importPath
				p.mark(file)
				changed[pkg.Fset.Position(selector.Pos()).String()] = true
				return true
			})
		}
	}
	return len(changed)
}

func (p *Program) rewritePackageObject(object types.Object, importPath string) int {
	if object == nil {
		return 0
	}
	return p.rewritePackageObjects(map[types.Object]bool{object: true}, importPath)
}
