package consumers

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
)

// Workspace finds every module listed by the active go.work file.
func Workspace(dir string) ([]Module, error) {
	producer, err := readModule(dir)
	if err != nil {
		return nil, fmt.Errorf("read workspace producer module: %w", err)
	}
	modules, err := workspaceModules(dir)
	if err != nil {
		return nil, err
	}
	var consumers []Module
	for _, module := range modules {
		parsed, err := readModule(module.Root)
		if err != nil {
			return nil, err
		}
		if parsed.Path != producer.Path && moduleRequires(parsed.file, producer.Path) {
			consumers = append(consumers, module)
		}
	}
	return consumers, nil
}

// WorkspaceImport finds active workspace modules importing one exact path.
func WorkspaceImport(dir, importPath string) ([]Module, error) {
	modules, err := workspaceModules(dir)
	if err != nil {
		return nil, err
	}
	var found []Module
	for _, module := range modules {
		imports, err := moduleImports(module.Root, importPath)
		if err != nil {
			return nil, err
		}
		if imports {
			found = append(found, module)
		}
	}
	return found, nil
}

func workspaceModules(dir string) ([]Module, error) {
	command := exec.Command("go", "env", "GOWORK")
	command.Dir = dir
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("discover active Go workspace: %w", err)
	}
	path := strings.TrimSpace(string(output))
	if path == "" || path == "off" {
		return nil, nil
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read active Go workspace %v: %w", path, err)
	}
	work, err := modfile.ParseWork(path, contents, nil)
	if err != nil {
		return nil, fmt.Errorf("parse active Go workspace %v: %w", path, err)
	}

	seen := make(map[string]bool)
	modules := make([]Module, 0, len(work.Use))
	for _, use := range work.Use {
		root := use.Path
		if !filepath.IsAbs(root) {
			root = filepath.Join(filepath.Dir(path), root)
		}
		module, err := readModule(root)
		if err != nil {
			return nil, err
		}
		if seen[module.Root] {
			continue
		}
		seen[module.Root] = true
		modules = append(modules, module.Module)
	}
	sort.Slice(modules, func(i, j int) bool { return modules[i].Root < modules[j].Root })
	return modules, nil
}
