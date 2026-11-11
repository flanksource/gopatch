package typed

import (
	"fmt"

	"github.com/dave/dst"
)

// Signature is a replacement parameter or result list.
//
// An empty Params or Results that was not asked for is distinguished from one
// that was by SetParams and SetResults, so that changing only the results keeps
// the parameters as they are.
type Signature struct {
	// Params is the new parameter list without parentheses. Ignored unless
	// SetParams is set.
	Params    string
	SetParams bool

	// Results is the new result list, parenthesized where Go requires it.
	// Ignored unless SetResults is set.
	Results    string
	SetResults bool

	// Imports are the packages Params or Results refer to that the file does not
	// import yet.
	Imports []ImportRef
}

// SetSignature replaces the signature of the func or method sym denotes,
// reporting how many declarations it changed.
//
// Call sites are not updated, because there is no way to decide what to pass at
// one that gains a parameter. Callers are expected to say so.
func (p *Program) SetSignature(sym *Symbol, sig Signature) (int, error) {
	if !sig.SetParams && !sig.SetResults {
		return 0, fmt.Errorf("nothing to change in the signature of %v", sym.Desc)
	}

	return p.funcDecls(sym, func(df *decoratedFile, fn *dst.FuncDecl) error {
		return p.applySignature(df, fn, sig)
	})
}

// funcDecls calls visit for the declaration of sym in every file that declares
// it, marks those files modified, and reports how many there were.
//
// A func or method is declared once, but a file compiled into both a package and
// its test variant is reached through two type graphs; ownedFile keeps the edit
// on the tree that is actually rendered.
func (p *Program) funcDecls(sym *Symbol, visit func(*decoratedFile, *dst.FuncDecl) error) (int, error) {
	if sym.Kind != KindFunc && sym.Kind != KindMethod {
		return 0, fmt.Errorf("%v is not a func or a method", sym.Desc)
	}

	changed := make(map[string]bool)
	for _, pkg := range p.pkgs {
		if pkg.TypesInfo == nil || pkg.Decorator == nil {
			continue
		}
		for id, obj := range pkg.TypesInfo.Defs {
			if !sym.objs[obj] {
				continue
			}

			node, ok := pkg.Decorator.Dst.Nodes[id]
			if !ok {
				continue
			}
			ident, ok := node.(*dst.Ident)
			if !ok {
				continue
			}
			df := p.ownedFile(pkg, id)
			if df == nil {
				continue
			}

			fn := funcDeclNamed(df.file, ident)
			if fn == nil {
				// An interface method is a Field in the interface's type, not a
				// FuncDecl, so it has no declaration of its own to change.
				return 0, fmt.Errorf(
					"%v is declared in an interface, not as a function, so it has no "+
						"declaration of its own to change; changing an interface method "+
						"would have to change every implementation too", sym.Desc)
			}

			if err := visit(df, fn); err != nil {
				return 0, err
			}
			p.mark(df)
			changed[df.path] = true
		}
	}

	if len(changed) == 0 {
		return 0, fmt.Errorf("found %v but not its declaration", sym.Desc)
	}
	return len(changed), nil
}

// applySignature rewrites one declaration's parameter and result lists.
func (p *Program) applySignature(df *decoratedFile, fn *dst.FuncDecl, sig Signature) error {
	// Parsing a whole function is the only way to get go/parser to accept the
	// grouping, names and "..." a real parameter list may use.
	src := fmt.Sprintf("func _(%v)%v {}\n", sig.Params, resultClause(sig.Results))
	decl, err := parseDecl(src)
	if err != nil {
		return fmt.Errorf("invalid signature %q: %w", src, err)
	}
	parsed, ok := decl.(*dst.FuncDecl)
	if !ok {
		return fmt.Errorf("invalid signature %q", src)
	}

	if err := p.resolveTypeQualifiers(df, parsed.Type, sig.Imports); err != nil {
		return err
	}

	if sig.SetParams {
		fn.Type.Params = parsed.Type.Params
	}
	if sig.SetResults {
		fn.Type.Results = parsed.Type.Results
	}
	return nil
}

// funcDeclNamed returns the file's function declaration whose name is ident.
func funcDeclNamed(file *dst.File, ident *dst.Ident) *dst.FuncDecl {
	for _, decl := range file.Decls {
		if fn, ok := decl.(*dst.FuncDecl); ok && fn.Name == ident {
			return fn
		}
	}
	return nil
}
