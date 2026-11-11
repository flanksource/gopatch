package typed

import (
	"fmt"
	"go/ast"
	"regexp"
	"strings"

	"github.com/dave/dst"
)

// Body is replacement source for a function body.
type Body struct {
	// Content is the source that goes between the braces. Empty makes the body
	// empty, which the compiler accepts only when the signature declares no
	// results.
	Content string

	// Imports are the packages Content refers to that the file does not import
	// yet.
	Imports []ImportRef
}

// Substitution is a regular-expression replacement applied inside a function
// body.
type Substitution struct {
	// Match is the expression to find, applied to the body's source between the
	// braces.
	Match *regexp.Regexp

	// Replace is what to put in place of each match. "$1" and "${name}" expand
	// the corresponding capture group.
	Replace string

	// Imports are the packages Replace refers to that the file does not import
	// yet.
	Imports []ImportRef
}

// SetBody replaces the body of the func or method sym denotes, reporting how
// many declarations it changed.
//
// The content is parsed as Go before anything is written, so source that does
// not compose into a body is refused rather than producing a file that does not
// build. It is not type-checked, though: a body that compiles on its own but
// refers to a name that is not in scope here still fails at build time.
func (p *Program) SetBody(sym *Symbol, body Body) (int, error) {
	return p.funcDecls(sym, func(df *decoratedFile, fn *dst.FuncDecl) error {
		if fn.Body == nil {
			return fmt.Errorf(
				"%v is declared without a body, so it is implemented elsewhere — "+
					"in assembly, or by the compiler — and giving it one here would "+
					"break that link", sym.Desc)
		}
		return p.setBody(df, fn, body.Content, body.Imports)
	})
}

// SubstituteInBody replaces every match of sub inside the body of the func or
// method sym denotes, reporting how many declarations it changed and how many
// matches it replaced.
//
// A pattern that matches nothing is an error. Exiting 0 having changed nothing
// is the failure this reports instead: a regular expression is easy to get
// subtly wrong, and silence looks exactly like success.
func (p *Program) SubstituteInBody(sym *Symbol, sub Substitution) (decls, matches int, err error) {
	if sub.Match == nil {
		return 0, 0, fmt.Errorf("no pattern to match against the body of %v", sym.Desc)
	}

	decls, err = p.funcDecls(sym, func(df *decoratedFile, fn *dst.FuncDecl) error {
		if fn.Body == nil {
			return fmt.Errorf("%v is declared without a body, so there is nothing to match", sym.Desc)
		}

		current, err := p.bodySource(df, fn)
		if err != nil {
			return err
		}

		replaced, n := replaceAll(sub.Match, current, sub.Replace)
		if n == 0 {
			return nil
		}
		matches += n

		if err := p.setBody(df, fn, replaced, sub.Imports); err != nil {
			return fmt.Errorf("%w\n\nThe substitution produced:\n%s", err, replaced)
		}
		return nil
	})
	if err != nil {
		return 0, 0, err
	}

	if matches == 0 {
		return 0, 0, fmt.Errorf(
			"nothing in the body of %v matches %v; nothing was changed",
			sym.Desc, sub.Match)
	}
	return decls, matches, nil
}

// setBody parses content as a function body and swaps it in.
func (p *Program) setBody(df *decoratedFile, fn *dst.FuncDecl, content string, imports []ImportRef) error {
	// Wrapping in a whole function is the only way to get go/parser to accept a
	// statement list. The newlines around the content are trimmed first, because
	// they are already there when the content came out of a body's source, and
	// gofmt preserves the blank line that doubling them would leave.
	src := "func _() {\n}\n"
	if trimmed := strings.Trim(content, "\n"); trimmed != "" {
		src = "func _() {\n" + trimmed + "\n}\n"
	}

	decl, err := parseDecl(src)
	if err != nil {
		return fmt.Errorf("the content is not a valid function body: %w", err)
	}
	parsed, ok := decl.(*dst.FuncDecl)
	if !ok || parsed.Body == nil {
		return fmt.Errorf("the content is not a valid function body")
	}

	if err := p.resolveBodyQualifiers(df, parsed.Body, imports); err != nil {
		return err
	}
	fn.Body = parsed.Body
	return nil
}

// bodySource returns the source between the braces of fn's body, exactly as it
// is written on disk.
//
// A pattern is matched against what the author wrote rather than against a
// re-rendered body, so an expression developed by looking at the file behaves
// the way it looks like it will. That also carries the comments inside the body
// through the substitution, because they are part of the text.
func (p *Program) bodySource(df *decoratedFile, fn *dst.FuncDecl) (string, error) {
	node, ok := df.pkg.Decorator.Ast.Nodes[fn]
	if !ok {
		return "", fmt.Errorf("internal error: no syntax recorded for a declaration in %v", df.path)
	}
	decl, ok := node.(*ast.FuncDecl)
	if !ok || decl.Body == nil {
		return "", fmt.Errorf("internal error: %v holds no function body to read", df.path)
	}

	src, err := p.source(df.path)
	if err != nil {
		return "", err
	}

	// One past the "{", up to the "}".
	lo := df.pkg.Fset.Position(decl.Body.Lbrace).Offset + 1
	hi := df.pkg.Fset.Position(decl.Body.Rbrace).Offset
	if lo < 0 || hi > len(src) || lo > hi {
		return "", fmt.Errorf(
			"internal error: the body at %v lies outside %v, which has %d bytes",
			p.pos(df.pkg, decl.Body.Lbrace), df.path, len(src))
	}
	return string(src[lo:hi]), nil
}

// replaceAll applies re to src, also reporting how many matches it replaced,
// which regexp.ReplaceAllString does not.
//
// The count is what lets a pattern that matched nothing be reported instead of
// passing silently.
func replaceAll(re *regexp.Regexp, src, repl string) (string, int) {
	n := len(re.FindAllStringIndex(src, -1))
	if n == 0 {
		return src, 0
	}
	return re.ReplaceAllString(src, repl), n
}
