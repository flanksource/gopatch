// Package refactor holds the argument vocabulary shared by gopatch's
// refactoring subcommands.
//
// The commands are served by two engines with different contracts — one
// generates gopatch programs and works on code that does not compile, the
// other resolves symbols with go/types and requires a build that type-checks —
// but both take the same command line arguments, so parsing and validating
// them belongs here.
//
// Every parser takes the name of the argument it is validating so that errors
// name the flag or positional the user got wrong. Nothing here applies a
// silent default: a value that cannot be used is an error, not a guess.
package refactor

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"path"
	"reflect"
	"strings"
)

// Qualified is a dotted command line argument naming a member of a type, such
// as "User.Name" or "*User.Save".
type Qualified struct {
	// Owner is the struct or receiver type name.
	Owner string

	// Name is the field, method, or parameter name.
	Name string

	// Pointer reports whether the owner was written with a leading "*", as in
	// "*User.Save". Pointer and value receivers have separate method sets, so
	// the distinction is preserved rather than normalized away.
	Pointer bool
}

// String renders the argument in the form the user would have typed.
func (q Qualified) String() string {
	if q.Pointer {
		return "*" + q.Owner + "." + q.Name
	}
	return q.Owner + "." + q.Name
}

// ParseQualified parses an Owner.Name argument.
func ParseQualified(arg, s string) (Qualified, error) {
	var q Qualified

	rest := strings.TrimSpace(s)
	if strings.HasPrefix(rest, "*") {
		q.Pointer = true
		rest = rest[1:]
	}

	owner, name, ok := strings.Cut(rest, ".")
	if !ok || owner == "" || name == "" || strings.Contains(name, ".") {
		return q, fmt.Errorf("invalid %s %q: expected Type.Name", arg, s)
	}

	var err error
	if q.Owner, err = ParseIdent(arg, owner); err != nil {
		return q, err
	}
	if q.Name, err = ParseIdent(arg, name); err != nil {
		return q, err
	}
	return q, nil
}

// ParseIdent validates that s is a single Go identifier usable as a name.
func ParseIdent(arg, s string) (string, error) {
	// Keywords first: token.IsIdentifier already rejects them, but with a
	// message that does not explain why.
	switch {
	case token.Lookup(s).IsKeyword():
		return "", fmt.Errorf("invalid %s %q: %q is a Go keyword", arg, s, s)
	case s == "_":
		return "", fmt.Errorf("invalid %s %q: %q is not a usable name", arg, s, s)
	case !token.IsIdentifier(s):
		return "", fmt.Errorf("invalid %s %q: not a valid Go identifier", arg, s)
	}
	return s, nil
}

// ParseTypeExpr validates that s is a Go type expression and returns it as
// gofmt would print it, so that generated patches are well formatted no matter
// how the user spaced the flag value.
func ParseTypeExpr(arg, s string) (string, error) {
	expr, err := parser.ParseExpr(strings.TrimSpace(s))
	if err != nil {
		return "", fmt.Errorf("invalid %s %q: %v", arg, s, err)
	}

	var buf strings.Builder
	if err := printer.Fprint(&buf, token.NewFileSet(), expr); err != nil {
		return "", fmt.Errorf("invalid %s %q: %v", arg, s, err)
	}
	return buf.String(), nil
}

// ParseFieldList validates a parameter list such as
// "ctx context.Context, r *Request", with or without surrounding parentheses,
// and returns it as gofmt would print it, without parentheses.
func ParseFieldList(arg, s string) (string, error) {
	fields, _, _, err := parseSignatureFields(arg, s)
	if err != nil {
		return "", err
	}
	return fields, nil
}

// ParseResults validates a result list, reporting how many results it declares
// and whether they are named. Callers need both: a result list cannot be
// elided in a patch, so the shape has to be reproduced exactly.
//
// The rendered form keeps the parentheses whenever Go requires them — more
// than one result, or any named result.
func ParseResults(arg, s string) (list string, arity int, named bool, err error) {
	fields, arity, named, err := parseSignatureFields(arg, s)
	if err != nil {
		return "", 0, false, err
	}
	return ParenthesizeResults(fields, arity, named), arity, named, nil
}

// parseSignatureFields parses a parenthesized or bare field list by parsing it
// as the parameters of a function type, which is the only way to get go/parser
// to accept names, grouping, and "..." in one pass.
func parseSignatureFields(arg, s string) (rendered string, arity int, named bool, err error) {
	inner := strings.TrimSpace(s)

	// Strip the parentheses only when they are balanced, so that a stray one
	// ("ctx context.Context)") still reaches the parser and is rejected.
	if strings.HasPrefix(inner, "(") && strings.HasSuffix(inner, ")") {
		inner = inner[1 : len(inner)-1]
	}
	if strings.TrimSpace(inner) == "" {
		return "", 0, false, nil
	}

	expr, perr := parser.ParseExpr("func(" + inner + ")")
	if perr != nil {
		return "", 0, false, fmt.Errorf("invalid %s %q: %v", arg, s, perr)
	}
	fn, ok := expr.(*ast.FuncType)
	if !ok || fn.Params == nil {
		return "", 0, false, fmt.Errorf("invalid %s %q: expected a parameter list", arg, s)
	}

	rendered, arity, named, rerr := RenderFields(token.NewFileSet(), fn.Params)
	if rerr != nil {
		return "", 0, false, fmt.Errorf("invalid %s %q: %v", arg, s, rerr)
	}
	return rendered, arity, named, nil
}

// RenderFields renders a parameter or result list without its parentheses,
// reporting how many entries it declares and whether they are named.
//
// A grouped entry such as "a, b string" counts once per name, which is what
// callers reproducing a signature need.
func RenderFields(fset *token.FileSet, list *ast.FieldList) (rendered string, arity int, named bool, err error) {
	if list == nil || len(list.List) == 0 {
		return "", 0, false, nil
	}

	parts := make([]string, 0, len(list.List))
	for _, field := range list.List {
		var buf strings.Builder

		names := make([]string, 0, len(field.Names))
		for _, n := range field.Names {
			names = append(names, n.Name)
		}
		if len(names) > 0 {
			named = true
			buf.WriteString(strings.Join(names, ", "))
			buf.WriteString(" ")
			arity += len(names)
		} else {
			arity++
		}

		if err := printer.Fprint(&buf, fset, field.Type); err != nil {
			return "", 0, false, err
		}
		parts = append(parts, buf.String())
	}

	return strings.Join(parts, ", "), arity, named, nil
}

// ParenthesizeResults wraps a rendered result list in parentheses when Go
// requires them: more than one result, or any named result.
func ParenthesizeResults(rendered string, arity int, named bool) string {
	switch {
	case arity == 0:
		return ""
	case arity > 1 || named:
		return "(" + rendered + ")"
	default:
		return rendered
	}
}

// ParseTag validates a struct tag and returns it wrapped in backquotes, ready
// to place in a patch. An empty value clears the tag.
func ParseTag(arg, s string) (string, error) {
	raw := strings.TrimSpace(s)
	raw = strings.TrimPrefix(raw, "`")
	raw = strings.TrimSuffix(raw, "`")
	if raw == "" {
		return "", nil
	}
	if strings.Contains(raw, "`") {
		return "", fmt.Errorf("invalid %s %q: a struct tag cannot contain a backquote", arg, s)
	}

	// reflect is the authority on tag syntax: a tag it cannot read back is one
	// that encoding/json and friends would silently ignore.
	tag := reflect.StructTag(raw)
	for rest := raw; rest != ""; {
		key, remainder, err := nextTagKey(rest)
		if err != nil {
			return "", fmt.Errorf("invalid %s %q: %v", arg, s, err)
		}
		if _, ok := tag.Lookup(key); !ok {
			return "", fmt.Errorf("invalid %s %q: could not read back key %q", arg, s, key)
		}
		rest = remainder
	}

	return "`" + raw + "`", nil
}

// nextTagKey returns the first key in a struct tag and the unconsumed
// remainder, mirroring the grammar reflect.StructTag.Lookup accepts.
func nextTagKey(s string) (key, rest string, err error) {
	s = strings.TrimLeft(s, " ")
	name, remainder, ok := strings.Cut(s, ":")
	if !ok || name == "" {
		return "", "", fmt.Errorf("expected key:\"value\", found %q", s)
	}
	if !strings.HasPrefix(remainder, `"`) {
		return "", "", fmt.Errorf("value for key %q is not quoted", name)
	}
	end := strings.Index(remainder[1:], `"`)
	if end < 0 {
		return "", "", fmt.Errorf("value for key %q is not terminated", name)
	}
	return name, remainder[end+2:], nil
}

// ParseImportPath validates that s is usable as an import path.
//
// It says nothing about whether a package name can be derived from the path;
// callers that need one use DerivePackageName, which fails separately so that a
// caller holding an explicit package name is not blocked by an undeducible one.
func ParseImportPath(arg, s string) (string, error) {
	switch {
	case strings.TrimSpace(s) == "":
		return "", fmt.Errorf("invalid %s: an import path is required", arg)
	case s != strings.TrimSpace(s):
		return "", fmt.Errorf("invalid %s %q: leading or trailing whitespace", arg, s)
	case strings.ContainsAny(s, "\"' \t\n"):
		return "", fmt.Errorf("invalid %s %q: quotes and whitespace are not allowed", arg, s)
	case strings.HasPrefix(s, "/"):
		return "", fmt.Errorf("invalid %s %q: an import path cannot be absolute", arg, s)
	case strings.Contains(s, "..."):
		return "", fmt.Errorf("invalid %s %q: wildcards are not allowed", arg, s)
	}
	return s, nil
}

// DerivePackageName returns the package name for an import path, guessing it
// the way the patch engine does: the last path segment.
//
// gopatch cannot resolve a package name from an import path, so a path whose
// last segment is not usable as an identifier is an error rather than a guess —
// otherwise the generated patch would silently match nothing.
func DerivePackageName(arg, importPath string) (string, error) {
	name := path.Base(importPath)
	if _, err := ParseIdent(arg, name); err != nil {
		return "", fmt.Errorf(
			"cannot derive a package name from %s %q: %q is not usable as one; "+
				"pass --qualifier to give it explicitly",
			arg, importPath, name)
	}
	return name, nil
}
