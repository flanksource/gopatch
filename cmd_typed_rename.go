package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/pflag"
	"github.com/uber-go/gopatch/internal/refactor/consumers"
	"github.com/uber-go/gopatch/internal/refactor/typed"
)

const (
	_consumersDesc     = "Directory to search recursively for Go modules that require the producer module. Repeat for more than one root."
	_consumersFromDesc = "File containing consumer search directories, one per line. Relative paths are resolved from the working directory."
)

type typedRenameOptions struct {
	typedOptions
	Consumers     []string
	ConsumersFrom string
}

func addTypedRenameFlags(fs *pflag.FlagSet, opts *typedRenameOptions) {
	addTypedFlags(fs, &opts.typedOptions)
	fs.StringArrayVar(&opts.Consumers, "consumers", nil, _consumersDesc)
	fs.StringVar(&opts.ConsumersFrom, "consumers-from", "", _consumersFromDesc)
}

type typedRenameRequest struct {
	Options  *typedRenameOptions
	Patterns []string
	Query    typed.Query
	To       string
}

type renameModule struct {
	Root     string
	Program  *typed.Program
	Producer bool
}

type renameBatch struct {
	Root        string
	Summary     string
	Identifiers int
	Edits       []typed.FileEdit
}

func (cmd *mainCmd) runTypedRename(req typedRenameRequest) error {
	if len(req.Patterns) == 0 {
		return errors.New("please provide at least one pattern")
	}
	cwd, err := cmd.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	allowed, err := cmd.resolveFiles(req.Patterns)
	if err != nil {
		return err
	}

	producer, err := typed.Load(typed.Request{Dir: cwd})
	if err != nil {
		return err
	}
	target, err := producer.Resolve(req.Query)
	if err != nil {
		return err
	}
	modules, err := cmd.loadRenameModules(producer, req.Options)
	if err != nil {
		return err
	}
	refs := expandRenameRefs(modules, req.Query.Kind, target)
	editable := editablePackages(modules)
	batches, err := prepareRenameBatches(renamePreparation{
		Modules: modules, Kind: req.Query.Kind, Target: target, Refs: refs,
		EditablePackages: editable, To: req.To, Cwd: cwd, ProducerFiles: allowed,
	})
	if err != nil {
		return err
	}
	return cmd.emitRenameBatches(cwd, &req.Options.apply, batches)
}

func (cmd *mainCmd) loadRenameModules(producer *typed.Program, opts *typedRenameOptions) ([]renameModule, error) {
	modules := []renameModule{{Root: producer.Root(), Program: producer, Producer: true}}
	roots, err := cmd.consumerRoots(opts)
	if err != nil {
		return modules, err
	}
	if len(roots) == 0 {
		return modules, nil
	}
	found, err := consumers.Find(consumers.Request{ProducerRoot: producer.Root(), Roots: roots})
	if err != nil {
		return nil, err
	}
	if len(found) == 0 {
		return nil, errors.New("consumer discovery found no modules that require the producer module")
	}
	for _, module := range found {
		program, err := typed.Load(typed.Request{Dir: module.Root})
		if err != nil {
			return nil, fmt.Errorf("load consumer module %v: %w", module.Root, err)
		}
		modules = append(modules, renameModule{Root: module.Root, Program: program})
	}
	return modules, nil
}

func (cmd *mainCmd) consumerRoots(opts *typedRenameOptions) ([]string, error) {
	roots := append([]string(nil), opts.Consumers...)
	requested := len(roots) > 0 || opts.ConsumersFrom != ""
	if opts.ConsumersFrom != "" {
		path, err := cmd.absPath(opts.ConsumersFrom)
		if err != nil {
			return nil, err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read --consumers-from %v: %w", path, err)
		}
		for _, line := range strings.Split(string(contents), "\n") {
			if root := strings.TrimSpace(line); root != "" {
				roots = append(roots, root)
			}
		}
	}
	if requested && len(roots) == 0 {
		return nil, errors.New("consumer discovery needs at least one non-empty search directory")
	}
	for i, root := range roots {
		if root == "" {
			return nil, errors.New("--consumers cannot be empty")
		}
		abs, err := cmd.absPath(root)
		if err != nil {
			return nil, err
		}
		roots[i] = abs
	}
	return roots, nil
}

func expandRenameRefs(modules []renameModule, kind typed.Kind, target *typed.Symbol) []typed.SymbolRef {
	refs := refSet(target.Refs())
	for grew := true; grew; {
		grew = false
		current := sortedRefs(refs)
		for _, module := range modules {
			sym := module.Program.ResolveRefs(typed.RefQuery{Kind: kind, Refs: current, Desc: target.Desc})
			for _, ref := range module.Program.ExpandRefs(sym) {
				if !refs[ref] {
					refs[ref] = true
					grew = true
				}
			}
		}
	}
	return sortedRefs(refs)
}

func refSet(refs []typed.SymbolRef) map[typed.SymbolRef]bool {
	out := make(map[typed.SymbolRef]bool, len(refs))
	for _, ref := range refs {
		out[ref] = true
	}
	return out
}

func sortedRefs(refs map[typed.SymbolRef]bool) []typed.SymbolRef {
	out := make([]typed.SymbolRef, 0, len(refs))
	for ref := range refs {
		out = append(out, ref)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ImportPath != out[j].ImportPath {
			return out[i].ImportPath < out[j].ImportPath
		}
		if out[i].Recv != out[j].Recv {
			return out[i].Recv < out[j].Recv
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func editablePackages(modules []renameModule) map[string]bool {
	paths := make(map[string]bool)
	for _, module := range modules {
		for _, path := range module.Program.PackagePaths() {
			paths[path] = true
		}
	}
	return paths
}

type renamePreparation struct {
	Modules          []renameModule
	Kind             typed.Kind
	Target           *typed.Symbol
	Refs             []typed.SymbolRef
	EditablePackages map[string]bool
	To, Cwd          string
	ProducerFiles    []string
}

func prepareRenameBatches(req renamePreparation) ([]renameBatch, error) {
	var batches []renameBatch
	var producerChanged bool
	for _, module := range req.Modules {
		sym := module.Program.ResolveRefs(typed.RefQuery{
			Kind: req.Kind, Refs: req.Refs, Desc: req.Target.Desc,
			EditablePackages: req.EditablePackages,
		})
		if !module.Program.HasSourceIdentifiers(sym) {
			continue
		}
		identifiers, err := module.Program.Rename(sym, req.To)
		if err != nil {
			return nil, fmt.Errorf("prepare rename in %v: %w", module.Root, err)
		}
		edits, err := module.Program.Edits()
		if err != nil {
			return nil, err
		}
		allowed := module.Program.FilePaths()
		if module.Producer {
			allowed = req.ProducerFiles
		}
		if err := checkEditScope(req.Cwd, edits, allowed); err != nil {
			return nil, err
		}
		batches = append(batches, renameBatch{
			Root: module.Root, Identifiers: identifiers, Edits: edits,
			Summary: fmt.Sprintf("rename %v to %v in %v (%d identifiers)",
				req.Target.Desc, req.To, module.Root, identifiers),
		})
		producerChanged = producerChanged || module.Producer
	}
	if !producerChanged {
		return nil, fmt.Errorf("found %s but no identifier to rewrite", req.Target.Desc)
	}
	return batches, nil
}

func (cmd *mainCmd) emitRenameBatches(cwd string, opts *options, batches []renameBatch) error {
	var changes []sourceChange
	for _, batch := range batches {
		for _, edit := range batch.Edits {
			path := sourcePath{Absolute: edit.Path, Provided: edit.Path}
			if rel, err := filepath.Rel(cwd, edit.Path); err == nil {
				path.Provided = rel
			}
			changes = append(changes, sourceChange{
				Path: path, Original: edit.Original, Modified: edit.Modified,
				Comments: []string{batch.Summary},
			})
		}
	}
	if err := cmd.emitChanges(opts, changes); err != nil {
		return err
	}
	logOut := io.Discard
	if opts.Verbose {
		logOut = cmd.Stdout
	}
	logger := log.New(logOut, "", 0)
	for _, batch := range batches {
		logger.Printf("%s: patched %d identifier(s) in %d file(s)",
			batch.Root, batch.Identifiers, len(batch.Edits))
	}
	return nil
}
