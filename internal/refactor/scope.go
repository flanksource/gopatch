package refactor

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
)

// DeclaringPackages returns the names of the packages that declare name at the
// top level, across the given Go files.
//
// Files that do not parse are skipped rather than reported: a refactoring may
// legitimately be run over a tree that does not build, and the caller has
// already decided which files matter.
func DeclaringPackages(files []string, name string) []string {
	seen := make(map[string]struct{})

	fset := token.NewFileSet()
	for _, path := range files {
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		if declares(f, name) {
			seen[f.Name.Name] = struct{}{}
		}
	}

	pkgs := make([]string, 0, len(seen))
	for p := range seen {
		pkgs = append(pkgs, p)
	}
	sort.Strings(pkgs)
	return pkgs
}

// declares reports whether f declares name at the top level, as a type, a
// function without a receiver, a variable, or a constant.
func declares(f *ast.File, name string) bool {
	for _, decl := range f.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Recv == nil && d.Name.Name == name {
				return true
			}
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				switch s := spec.(type) {
				case *ast.TypeSpec:
					if s.Name.Name == name {
						return true
					}
				case *ast.ValueSpec:
					for _, n := range s.Names {
						if n.Name == name {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

// InferPackage returns the single package that declares the top-level name t
// reaches: the owning type for a member, the declaration itself otherwise.
//
// It fails rather than picking one when the answer is not unambiguous, and the
// message suggests the whole target requalified rather than only the part that
// was ambiguous, so it can be pasted back onto the command line. Guessing here
// would silently change the wrong declaration.
func InferPackage(arg string, files []string, t Target) (string, error) {
	name := t.Owner()
	if name == "" {
		name = t.Name()
	}

	pkgs := DeclaringPackages(files, name)
	switch len(pkgs) {
	case 1:
		return pkgs[0], nil
	case 0:
		return "", fmt.Errorf(
			"could not find a declaration of %q in the given paths; qualify %s with the "+
				"package or directory that declares it", name, arg)
	default:
		return "", fmt.Errorf(
			"%q is declared in packages %v; qualify %s with the one you mean, as in %q",
			name, pkgs, arg, pkgs[0]+"."+strings.Join(t.Names, "."))
	}
}
