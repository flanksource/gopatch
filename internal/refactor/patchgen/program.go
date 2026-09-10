// Package patchgen renders gopatch programs from command line arguments.
//
// It exists so that the common mechanical refactorings do not require
// hand-authoring gopatch's diff-like DSL. Each operation is a struct whose
// fields are the command's arguments, and whose Patch method renders a program
// the existing engine can compile.
//
// The engine has several structural rules that a generated program must obey,
// and this package is where they are encoded:
//
//   - A change cannot consist only of a package clause or only of imports;
//     there must be something after it to match. See gopatch issue #5.
//   - A change carries at most one declaration, and its plus section can never
//     be empty. See internal/pgo/parse.go.
//   - "..." goes on its own context line, never inline as -foo(...)/+bar(...).
//     See docs/PatchesInDepth.md and gopatch issue #9.
//   - "..." is never written in a result list: doing so makes go/format fail.
//   - At most one "..." appears in a function signature; two cross-connect and
//     corrupt the output.
//   - An import name in a patch must be an identifier metavariable, or the
//     patch matches only named imports. See internal/engine/import.go.
package patchgen

import (
	"errors"
	"fmt"
	"strings"

	"github.com/uber-go/gopatch/internal/text"
)

// Operation is a single refactoring that can be rendered as a gopatch program.
type Operation interface {
	// Validate reports every problem with the operation's arguments. Callers
	// run it before touching the filesystem so that bad arguments fail first.
	Validate() error

	// Describe returns the one-line summary embedded as the program's leading
	// '#' comment, which the CLI shows on stderr in --diff and --print-only
	// mode.
	Describe() string

	// Patch renders the operation as gopatch program text.
	Patch() ([]byte, error)
}

// indentWidth is the number of spaces one nesting level adds inside a change
// body. Indentation is not significant to the patch parser — the body is
// parsed as Go — so a single consistent width keeps generated patches readable.
const indentWidth = 2

// line is one line of a change, carrying the diff prefix that says whether it
// belongs to the minus section, the plus section, or both.
type line struct {
	prefix byte // ' ', '-' or '+'
	depth  int
	text   string
}

// render returns the line as it appears in the program.
func (l line) render() string {
	if l.text == "" {
		return ""
	}
	return string(l.prefix) + strings.Repeat(" ", l.depth*indentWidth) + l.text
}

// ctx returns a context line, present in both the minus and plus sections.
func ctx(depth int, text string) line { return line{prefix: ' ', depth: depth, text: text} }

// del returns a line present only in the minus section.
func del(depth int, text string) line { return line{prefix: '-', depth: depth, text: text} }

// ins returns a line present only in the plus section.
func ins(depth int, text string) line { return line{prefix: '+', depth: depth, text: text} }

// dots returns an elision line. It is always a context line on its own: the
// engine mishandles "..." written inline on a minus or plus line.
func dots(depth int) line { return ctx(depth, "...") }

// commaDots returns an elision line inside a comma-separated list, such as the
// fields of a composite literal.
func commaDots(depth int) line { return ctx(depth, "...,") }

// program assembles the text of a gopatch program.
type program struct {
	description string
	changes     []*change
}

// newProgram starts a program whose changes each carry description as their
// leading '#' comment.
//
// The description is repeated on every change on purpose: the CLI keeps only
// the last matching change's comments, so repeating it is what makes the
// reported description independent of which change matched.
func newProgram(description string) *program {
	return &program{description: description}
}

// change appends a new change to the program and returns it for populating.
func (p *program) change() *change {
	c := &change{}
	p.changes = append(p.changes, c)
	return c
}

// Bytes renders the program, rejecting shapes the engine cannot compile.
func (p *program) Bytes() ([]byte, error) {
	if len(p.changes) == 0 {
		return nil, errors.New("patch has no changes")
	}

	var lines []string
	for i, c := range p.changes {
		if err := c.validate(i); err != nil {
			return nil, err
		}
		if i > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, c.render(p.description)...)
	}

	return text.Unlines(lines...), nil
}

// change is one "@@ ... @@" block: its metavariable declarations, an optional
// package/import header, and the transformation body.
type change struct {
	idents []string
	exprs  []string

	header []line
	body   []line
}

// ident declares identifier metavariables and returns the last name, so that
// call sites can read as c.importSpec(c.ident("http"), path).
func (c *change) ident(names ...string) string {
	c.idents = append(c.idents, names...)
	return names[len(names)-1]
}

// expr declares expression metavariables and returns the last name.
func (c *change) expr(names ...string) string {
	c.exprs = append(c.exprs, names...)
	return names[len(names)-1]
}

// pkg adds a context package clause, scoping the change to one package.
func (c *change) pkg(name string) *change {
	c.header = append(c.header, ctx(0, "package "+name))
	return c
}

// renamePkg changes the package clause.
func (c *change) renamePkg(from, to string) *change {
	c.header = append(c.header,
		del(0, "package "+from),
		ins(0, "package "+to),
	)
	return c
}

// importSpec adds a context import. An empty name means an unnamed import.
func (c *change) importSpec(name, path string) *change {
	c.header = append(c.header, ctx(0, importLine(name, path)))
	return c
}

// changeImport rewrites an import's name and path.
func (c *change) changeImport(fromName, fromPath, toName, toPath string) *change {
	c.header = append(c.header,
		del(0, importLine(fromName, fromPath)),
		ins(0, importLine(toName, toPath)),
	)
	return c
}

// deleteImport removes an import.
func (c *change) deleteImport(name, path string) *change {
	c.header = append(c.header, del(0, importLine(name, path)))
	return c
}

// addImport introduces a new import.
func (c *change) addImport(name, path string) *change {
	c.header = append(c.header, ins(0, importLine(name, path)))
	return c
}

func importLine(name, path string) string {
	if name == "" {
		return fmt.Sprintf("import %q", path)
	}
	return fmt.Sprintf("import %s %q", name, path)
}

// diff appends lines to the transformation body.
func (c *change) diff(lines ...line) *change {
	c.body = append(c.body, lines...)
	return c
}

// filler appends the minimal body that keeps a header-only change legal.
//
// A change consisting only of a package clause or imports has nothing to
// match, so the engine rejects it. A lone identifier metavariable matches any
// identifier in the file, which is the cheapest legal body.
func (c *change) filler() *change {
	return c.diff(ctx(0, c.ident(c.freshIdent("x"))))
}

// freshIdent returns a metavariable name based on base that is not already
// declared in this change and does not appear in its text.
func (c *change) freshIdent(base string) string {
	used := func(name string) bool {
		for _, n := range append(append([]string{}, c.idents...), c.exprs...) {
			if n == name {
				return true
			}
		}
		for _, l := range append(append([]line{}, c.header...), c.body...) {
			if strings.Contains(l.text, name) {
				return true
			}
		}
		return false
	}

	name := base
	for i := 2; used(name); i++ {
		name = fmt.Sprintf("%s%d", base, i)
	}
	return name
}

// validate reports whether the change is one the engine can compile.
func (c *change) validate(index int) error {
	if len(c.body) == 0 {
		if len(c.header) == 0 {
			return fmt.Errorf("change %d is empty", index+1)
		}
		// gopatch issue #5.
		return fmt.Errorf(
			"change %d matches only a package clause or imports, which gopatch rejects; "+
				"it needs a body", index+1)
	}

	var minus, plus int
	for _, l := range append(append([]line{}, c.header...), c.body...) {
		switch l.prefix {
		case ' ':
			minus++
			plus++
		case '-':
			minus++
		case '+':
			plus++
		}
	}
	if minus == 0 {
		return fmt.Errorf("change %d has an empty minus section", index+1)
	}
	if plus == 0 {
		return fmt.Errorf("change %d has an empty plus section", index+1)
	}

	return nil
}

// render returns the change's lines, including its metavariable section.
func (c *change) render(description string) []string {
	var lines []string
	if description != "" {
		lines = append(lines, "# "+description)
	}

	lines = append(lines, "@@")
	if len(c.idents) > 0 {
		lines = append(lines, "var "+strings.Join(c.idents, ", ")+" identifier")
	}
	if len(c.exprs) > 0 {
		lines = append(lines, "var "+strings.Join(c.exprs, ", ")+" expression")
	}
	lines = append(lines, "@@")

	for _, l := range c.header {
		lines = append(lines, l.render())
	}
	if len(c.header) > 0 && len(c.body) > 0 {
		lines = append(lines, "")
	}
	for _, l := range c.body {
		lines = append(lines, l.render())
	}

	return lines
}
