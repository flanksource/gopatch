// Package consumers discovers Go modules that require a producer module.
package consumers

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
)

// Module is a Go module that requires the producer module.
type Module struct {
	Root string
	Path string
}

// Request describes the producer and roots to search for its consumers.
type Request struct {
	ProducerRoot string
	Roots        []string
}

// Find discovers modules under Roots that require ProducerRoot's module.
func Find(req Request) ([]Module, error) {
	producer, err := readModule(req.ProducerRoot)
	if err != nil {
		return nil, fmt.Errorf("read producer module: %w", err)
	}

	found := make(map[string]Module)
	for _, root := range req.Roots {
		root, err := canonicalPath(root)
		if err != nil {
			return nil, fmt.Errorf("resolve consumer root %q: %w", root, err)
		}
		if err := walk(root, producer.Path, found); err != nil {
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

func walk(root, producerPath string, found map[string]Module) error {
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
		if module.Path != producerPath && moduleRequires(module.file, producerPath) {
			found[module.Root] = module.Module
		}
		return nil
	})
}

func skipDir(name string) bool {
	return name == "vendor" || name == "testdata" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")
}

type parsedModule struct {
	Module
	file *modfile.File
}

func readModule(root string) (parsedModule, error) {
	root, err := canonicalPath(root)
	if err != nil {
		return parsedModule{}, err
	}
	path := filepath.Join(root, "go.mod")
	contents, err := os.ReadFile(path)
	if err != nil {
		return parsedModule{}, fmt.Errorf("read %v: %w", path, err)
	}
	file, err := modfile.Parse(path, contents, nil)
	if err != nil {
		return parsedModule{}, fmt.Errorf("parse %v: %w", path, err)
	}
	if file.Module == nil {
		return parsedModule{}, fmt.Errorf("parse %v: missing module directive", path)
	}
	return parsedModule{
		Module: Module{Root: root, Path: file.Module.Mod.Path},
		file:   file,
	}, nil
}

func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	return resolved, nil
}

func moduleRequires(file *modfile.File, path string) bool {
	for _, require := range file.Require {
		if require.Mod.Path == path {
			return true
		}
	}
	return false
}
