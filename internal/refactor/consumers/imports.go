package consumers

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
)

// ImportRequest describes roots to search for modules importing one exact path.
type ImportRequest struct {
	Roots      []string
	ImportPath string
}

// FindImport discovers modules containing a Go file that imports ImportPath.
func FindImport(req ImportRequest) ([]Module, error) {
	found := make(map[string]Module)
	for _, root := range req.Roots {
		root, err := canonicalPath(root)
		if err != nil {
			return nil, fmt.Errorf("resolve consumer root %q: %w", root, err)
		}
		if err := walkImportModules(root, req.ImportPath, found); err != nil {
			return nil, err
		}
	}
	modules := make([]Module, 0, len(found))
	for _, module := range found {
		modules = append(modules, module)
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Root < modules[j].Root })
	return modules, nil
}

func walkImportModules(root, importPath string, found map[string]Module) error {
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && path != root && skipDir(entry.Name()) {
			return filepath.SkipDir
		}
		if entry.IsDir() || entry.Name() != "go.mod" {
			return nil
		}
		module, err := readModule(filepath.Dir(path))
		if err != nil {
			return err
		}
		imports, err := moduleImports(module.Root, importPath)
		if err != nil {
			return err
		}
		if imports {
			found[module.Root] = module.Module
		}
		return nil
	})
}

func moduleImports(root, importPath string) (bool, error) {
	found := false
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && skipDir(entry.Name()) {
				return filepath.SkipDir
			}
			if path != root {
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if found || filepath.Ext(path) != ".go" {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parse imports in %v: %w", path, err)
		}
		for _, spec := range file.Imports {
			value, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				return fmt.Errorf("parse import in %v: %w", path, err)
			}
			if value == importPath {
				found = true
				break
			}
		}
		return nil
	})
	return found, err
}
