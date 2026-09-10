package refactor

import (
	"fmt"
	"strings"
)

// Target is a refactoring target and the scope it was named in.
//
// A command's positional may name a declaration on its own ("User.Name"),
// qualify it with the package that declares it ("store.User.Name", or
// "example.com/store.User.Name"), or with a file or directory it lives under
// ("./internal/store:User.Name"). All three reach the same declaration; which
// is convenient depends on whether you know the package's name, its import
// path, or just where the file is.
type Target struct {
	// Names are the dotted parts of the declaration itself: one for a top-level
	// declaration, two for a member of a type.
	Names []string

	// Pointer records a leading "*", which a receiver may carry. Pointer and
	// value receivers declare methods on the same named type, so it does not
	// change what is found; it is kept so that messages can echo what was typed.
	Pointer bool

	// Qualifier is the package the target was qualified with, exactly as
	// written: a package clause ("store") or an import path
	// ("example.com/store"). Empty when the target was not package-qualified.
	Qualifier string

	// IsImportPath reports whether Qualifier is an import path rather than a
	// package clause.
	IsImportPath bool

	// Dir is the file or directory the target was qualified with, empty when it
	// was not path-qualified.
	Dir string
}

// ParseTarget parses a target argument that ends in names dot-separated parts:
// one for a top-level declaration, two for a field or a method.
//
// The qualifier is whatever precedes those parts, which is why they are counted
// from the right — an import path carries dots of its own, and
// "gopkg.in/yaml.v3.Node" has to come out as the type Node in
// gopkg.in/yaml.v3 rather than as four segments.
func ParseTarget(arg, s string, names int) (Target, error) {
	if names < 1 || names > 2 {
		// Not reachable from the command line; a caller asked for a shape that
		// does not exist.
		return Target{}, fmt.Errorf("internal error: %v names requested for %v", names, arg)
	}

	var t Target
	rest := strings.TrimSpace(s)
	if rest == "" {
		return t, fmt.Errorf("invalid %s: expected %s", arg, targetShape(names))
	}

	// A path qualifier is separated by ":", which neither an identifier nor an
	// import path may contain.
	if dir, after, ok := strings.Cut(rest, ":"); ok {
		if !looksLikePath(dir) {
			return t, fmt.Errorf(
				"invalid %s %q: %q before the \":\" does not look like a file or directory; "+
					"write it as \"./%s:...\", or drop the \":\" to qualify by package name",
				arg, s, dir, dir)
		}
		if after == "" {
			return t, fmt.Errorf("invalid %s %q: expected %s after the \":\"", arg, s, targetShape(names))
		}
		t.Dir, rest = dir, after
	}

	if trimmed, ok := strings.CutPrefix(rest, "*"); ok {
		t.Pointer, rest = true, trimmed
	}

	qualifier, tail, qualified := splitQualifier(rest, names)
	if qualified && qualifier == "" {
		return t, fmt.Errorf("invalid %s %q: the qualifier before %q is empty", arg, s, tail)
	}

	parts := strings.Split(tail, ".")
	if len(parts) != names {
		return t, fmt.Errorf("invalid %s %q: expected %s", arg, s, targetShape(names))
	}
	for _, part := range parts {
		if part == "" {
			// Only reachable for a two-name target: ".Name" leaves an empty
			// first part rather than an empty qualifier, and ParseIdent would
			// then report `invalid field ""`, which says nothing about the
			// argument that produced it.
			return t, fmt.Errorf("invalid %s %q: expected %s", arg, s, targetShape(names))
		}

		name, err := ParseIdent(arg, part)
		if err != nil {
			return t, err
		}
		t.Names = append(t.Names, name)
	}

	if qualifier == "" {
		return t, nil
	}

	t.Qualifier = qualifier
	// A package clause is a single identifier, so anything with a dot or a
	// slash in it was meant as an import path.
	t.IsImportPath = strings.ContainsAny(qualifier, "./")
	if t.IsImportPath {
		if _, err := ParseImportPath(arg, qualifier); err != nil {
			return t, err
		}
		return t, nil
	}
	if _, err := ParseIdent(arg, qualifier); err != nil {
		return t, err
	}
	return t, nil
}

// splitQualifier splits off the qualifier that precedes the last names
// dot-separated segments of s, reporting whether there was one to split.
func splitQualifier(s string, names int) (qualifier, tail string, qualified bool) {
	seen := 0
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] != '.' {
			continue
		}
		if seen++; seen == names {
			return s[:i], s[i+1:], true
		}
	}
	return "", s, false
}

// looksLikePath reports whether s was meant as a file or directory rather than
// as a package name.
//
// Requiring a marker is what keeps "store:User.Name" from being read as a path
// when "store" is a package; such a directory has to be written "./store".
func looksLikePath(s string) bool {
	return s == "." || s == ".." ||
		strings.ContainsAny(s, `/\`) ||
		strings.HasSuffix(s, ".go")
}

func targetShape(names int) string {
	if names == 1 {
		return `<Name>, <pkg>.<Name>, or <./dir>:<Name>`
	}
	return `<Type>.<name>, <pkg>.<Type>.<name>, or <./dir>:<Type>.<name>`
}

// Name returns the declaration's own name.
func (t Target) Name() string {
	if len(t.Names) == 0 {
		return ""
	}
	return t.Names[len(t.Names)-1]
}

// Owner returns the type that declares the member, and is empty for a top-level
// target.
func (t Target) Owner() string {
	if len(t.Names) < 2 {
		return ""
	}
	return t.Names[len(t.Names)-2]
}

// PackageName returns the package clause the qualifier implies, or an empty
// string when the target was not package-qualified.
func (t Target) PackageName(arg string) (string, error) {
	switch {
	case t.Qualifier == "":
		return "", nil
	case t.IsImportPath:
		return DerivePackageName(arg, t.Qualifier)
	default:
		return t.Qualifier, nil
	}
}

// ImportPath returns the qualifier when it was written as an import path, and
// an empty string otherwise.
func (t Target) ImportPath() string {
	if t.IsImportPath {
		return t.Qualifier
	}
	return ""
}

// String renders the target the way it would have been typed.
func (t Target) String() string {
	var b strings.Builder
	if t.Dir != "" {
		b.WriteString(t.Dir)
		b.WriteString(":")
	}
	if t.Pointer {
		b.WriteString("*")
	}
	if t.Qualifier != "" {
		b.WriteString(t.Qualifier)
		b.WriteString(".")
	}
	b.WriteString(strings.Join(t.Names, "."))
	return b.String()
}
