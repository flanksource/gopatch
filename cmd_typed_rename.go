package main

import (
	"errors"
	"fmt"
	"path/filepath"
	"sort"

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
	consumerOptions
}

func addTypedRenameFlags(fs *pflag.FlagSet, opts *typedRenameOptions) {
	addTypedFlags(fs, &opts.typedOptions)
	addConsumerFlags(fs, &opts.consumerOptions)
}

func addConsumerFlags(fs *pflag.FlagSet, opts *consumerOptions) {
	fs.StringArrayVar(&opts.Consumers, "consumers", nil, _consumersDesc)
	fs.StringVar(&opts.ConsumersFrom, "consumers-from", "", _consumersFromDesc)
}

type typedRenameRequest struct {
	Options    *typedRenameOptions
	Patterns   []string
	Query      typed.Query
	To         string
	StructOnly bool
}

type renameModule struct {
	Root     string
	Program  *typed.Program
	Producer bool
}

type renameBatch struct {
	Root       string
	ModulePath string
	Producer   bool
	Edits      []typed.FileEdit
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
	if req.StructOnly && !target.IsStruct() {
		return fmt.Errorf("%s is not a struct type", target.Desc)
	}
	modules, err := cmd.loadTypedModules(producer, &req.Options.consumerOptions)
	if err != nil {
		return err
	}
	refs := expandRenameRefs(modules, req.Query.Kind, target)
	editable := editablePackages(modules)
	batches, err := prepareRenameBatches(renamePreparation{
		Modules: modules, Kind: req.Query.Kind, Target: target, Refs: refs,
		EditablePackages: editable, To: req.To, Cwd: cwd, ProducerFiles: allowed,
	}, func(batch renameBatch) error {
		return cmd.printPlannedEdits(batch.Edits, producer.Root())
	})
	if err != nil {
		return err
	}
	return cmd.emitRenameBatches(cwd, &req.Options.apply, batches)
}

func (cmd *mainCmd) loadTypedModules(producer *typed.Program, opts *consumerOptions) ([]renameModule, error) {
	modules := []renameModule{{Root: producer.Root(), Program: producer, Producer: true}}
	selection, err := cmd.consumerRoots(producer.Root(), opts)
	if err != nil {
		return modules, err
	}
	workspace, err := consumers.Workspace(producer.Root())
	if err != nil {
		return nil, err
	}
	found := workspace
	if len(selection.Roots) > 0 {
		discovered, findErr := consumers.Find(consumers.Request{ProducerRoot: producer.Root(), Roots: selection.Roots})
		if findErr != nil {
			return nil, findErr
		}
		if selection.Explicit && len(discovered) == 0 {
			return nil, errors.New("consumer discovery found no modules that require the producer module")
		}
		found = append(found, discovered...)
	}
	sort.Slice(found, func(i, j int) bool {
		return canonicalMovePath(found[i].Root) < canonicalMovePath(found[j].Root)
	})
	if len(found) == 0 {
		return modules, nil
	}
	seen := map[string]bool{canonicalMovePath(producer.Root()): true}
	for _, module := range found {
		root := canonicalMovePath(module.Root)
		if seen[root] {
			continue
		}
		seen[root] = true
		program, err := typed.Load(typed.Request{Dir: root})
		if err != nil {
			return nil, fmt.Errorf("load consumer module %v: %w", module.Root, err)
		}
		modules = append(modules, renameModule{Root: root, Program: program})
	}
	return modules, nil
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

func prepareRenameBatches(req renamePreparation, planned func(renameBatch) error) ([]renameBatch, error) {
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
		_, err := module.Program.Rename(sym, req.To)
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
		batch := renameBatch{
			Root: module.Root, ModulePath: module.Program.ModulePath(), Producer: module.Producer,
			Edits: edits,
		}
		batches = append(batches, batch)
		if err := planned(batch); err != nil {
			return nil, err
		}
		producerChanged = producerChanged || module.Producer
	}
	if !producerChanged {
		return nil, fmt.Errorf("found %s but no identifier to rewrite", req.Target.Desc)
	}
	return batches, nil
}

func (cmd *mainCmd) emitRenameBatches(cwd string, opts *options, batches []renameBatch) error {
	var changes []sourceChange
	var edits []typed.FileEdit
	for _, batch := range batches {
		edits = append(edits, batch.Edits...)
		for _, edit := range batch.Edits {
			path := sourcePath{Absolute: edit.Path, Provided: edit.Path}
			if rel, err := filepath.Rel(cwd, edit.Path); err == nil {
				path.Provided = rel
			}
			changes = append(changes, sourceChange{
				Path: path, Original: edit.Original, Modified: edit.Modified,
			})
		}
	}
	if err := validateFileEdits(edits); err != nil {
		return err
	}
	if err := validateRenderedEdits(edits); err != nil {
		return err
	}
	if err := cmd.emitChanges(opts, changes); err != nil {
		return err
	}
	return nil
}
