package typed

import (
	"fmt"
	"go/ast"
	"strings"

	"github.com/dave/dst"
	"github.com/dave/dst/decorator"
)

// _maxReported caps how many reference positions an error lists before saying
// how many more there are.
const _maxReported = 10

// Delete removes the declaration sym denotes, reporting the files it changed.
//
// It refuses when anything still refers to the declaration. That is the whole
// point of doing this with type information: the patch language cannot delete a
// declaration at all, and a text-level delete would leave the module unbuildable
// without saying so.
func (p *Program) Delete(sym *Symbol) (int, error) {
	if err := p.checkUnreferenced(sym); err != nil {
		return 0, err
	}

	removed := make(map[string]bool)
	for _, pkg := range p.pkgs {
		if pkg.TypesInfo == nil || pkg.Decorator == nil {
			continue
		}
		for id, obj := range pkg.TypesInfo.Defs {
			if !sym.objs[obj] {
				continue
			}
			path, err := p.deleteDeclaring(pkg, id)
			if err != nil {
				return 0, err
			}
			if path != "" {
				removed[path] = true
			}
		}
	}

	if len(removed) == 0 {
		return 0, fmt.Errorf("found %s but could not locate its declaration to remove", sym.Desc)
	}
	return len(removed), nil
}

// checkUnreferenced refuses when the declaration is still used somewhere other
// than inside itself, so that a recursive function is still deletable.
func (p *Program) checkUnreferenced(sym *Symbol) error {
	spans := p.declSpans(sym)

	used := make(map[string]bool)
	for _, pkg := range p.pkgs {
		if pkg.TypesInfo == nil {
			continue
		}
		for id, obj := range pkg.TypesInfo.Uses {
			if !sym.objs[obj] {
				continue
			}
			at := pkg.Fset.Position(id.Pos())
			if within(spans, at.Filename, at.Offset) {
				continue
			}
			used[p.pos(pkg, id.Pos())] = true
		}
	}
	if len(used) == 0 {
		return nil
	}

	return fmt.Errorf("%s is still used at %s; remove those references first",
		sym.Desc, summarize(sortedKeys(used)))
}

// span is a half-open byte range in one file.
type span struct {
	filename   string
	start, end int
}

// declSpans returns the source range of each of the symbol's declarations, so
// that references from within the declaration itself can be ignored.
func (p *Program) declSpans(sym *Symbol) []span {
	var spans []span
	for _, pkg := range p.pkgs {
		if pkg.TypesInfo == nil {
			continue
		}
		for id, obj := range pkg.TypesInfo.Defs {
			if !sym.objs[obj] {
				continue
			}
			node := enclosingDecl(pkg, id)
			if node == nil {
				continue
			}
			start := pkg.Fset.Position(node.Pos())
			end := pkg.Fset.Position(node.End())
			spans = append(spans, span{filename: start.Filename, start: start.Offset, end: end.Offset})
		}
	}
	return spans
}

func within(spans []span, filename string, offset int) bool {
	for _, s := range spans {
		if s.filename == filename && offset >= s.start && offset < s.end {
			return true
		}
	}
	return false
}

// enclosingDecl returns the top-level declaration that declares id.
func enclosingDecl(pkg *decorator.Package, id *ast.Ident) ast.Node {
	for _, f := range pkg.Package.Syntax {
		if f.Pos() > id.Pos() || id.Pos() > f.End() {
			continue
		}
		for _, decl := range f.Decls {
			if decl.Pos() <= id.Pos() && id.Pos() < decl.End() {
				return decl
			}
		}
	}
	return nil
}

// deleteDeclaring removes the declaration that declares id from its file's dst
// tree, returning the path of the file it changed.
//
// A type declared on its own goes with its whole "type" block, so its doc
// comment goes with it. One spec out of a group takes only its own line.
func (p *Program) deleteDeclaring(pkg *decorator.Package, id *ast.Ident) (string, error) {
	node, ok := pkg.Decorator.Dst.Nodes[id]
	if !ok {
		return "", nil
	}
	ident, ok := node.(*dst.Ident)
	if !ok {
		return "", nil
	}

	df := p.ownedFile(pkg, id)
	if df == nil {
		return "", nil
	}

	for i, decl := range df.file.Decls {
		switch d := decl.(type) {
		case *dst.FuncDecl:
			if d.Name != ident {
				continue
			}
			df.file.Decls = append(df.file.Decls[:i:i], df.file.Decls[i+1:]...)
			p.mark(df)
			return df.path, nil

		case *dst.GenDecl:
			idx := specIndex(d, ident)
			if idx < 0 {
				continue
			}
			if len(d.Specs) == 1 {
				df.file.Decls = append(df.file.Decls[:i:i], df.file.Decls[i+1:]...)
			} else {
				d.Specs = append(d.Specs[:idx:idx], d.Specs[idx+1:]...)
			}
			p.mark(df)
			return df.path, nil
		}
	}

	return "", fmt.Errorf("could not find the declaration of %v in %v", ident.Name, df.path)
}

// specIndex returns the index of the spec in d that declares ident.
func specIndex(d *dst.GenDecl, ident *dst.Ident) int {
	for i, spec := range d.Specs {
		switch s := spec.(type) {
		case *dst.TypeSpec:
			if s.Name == ident {
				return i
			}
		case *dst.ValueSpec:
			for _, n := range s.Names {
				if n == ident {
					return i
				}
			}
		}
	}
	return -1
}

// summarize renders a capped list of positions.
func summarize(items []string) string {
	if len(items) <= _maxReported {
		return strings.Join(items, ", ")
	}
	return fmt.Sprintf("%s and %d more",
		strings.Join(items[:_maxReported], ", "), len(items)-_maxReported)
}
