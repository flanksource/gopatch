package main

import (
	"errors"
	"fmt"
	"sort"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/uber-go/gopatch/internal/refactor"
	"github.com/uber-go/gopatch/internal/refactor/consumers"
	"github.com/uber-go/gopatch/internal/refactor/typed"
)

type pathRenameOptions struct {
	apply options
	consumerOptions
}

type pathRenameBatchRequest struct {
	Module       renameModule
	Count        int
	ProducerRoot string
	Cwd          string
	Allowed      []string
	CheckScope   bool
}

func addPathRenameFlags(flags *pflag.FlagSet, opts *pathRenameOptions) {
	flags.BoolVarP(&opts.apply.Diff, "diff", "d", false, _diffDesc)
	flags.BoolVar(&opts.apply.Print, "print-only", false, _printOnlyDesc)
	flags.BoolVarP(&opts.apply.Verbose, "verbose", "v", false, _verboseDesc)
	addConsumerFlags(flags, &opts.consumerOptions)
}

func (cmd *mainCmd) newRenamePackageCommand() *cobra.Command {
	var opts pathRenameOptions
	command := &cobra.Command{
		Use:   "package <old> <new> [pattern...]",
		Short: "Rename a package clause and its consumers.",
		Long: "Rename a package clause and every bound unaliased qualifier in the module, " +
			"active Go workspace, and configured consumers. Explicit import aliases stay unchanged.",
		Example: examples(
			"gopatch rename package http httpclient ./http",
			"gopatch rename package http httpclient --consumers ../services ./http",
		),
		Args: cobra.MinimumNArgs(3),
		RunE: func(_ *cobra.Command, args []string) error {
			return cmd.runRenamePackage(&opts, args[0], args[1], args[2:])
		},
	}
	addPathRenameFlags(command.Flags(), &opts)
	return command
}

func (cmd *mainCmd) runRenamePackage(opts *pathRenameOptions, from, to string, patterns []string) error {
	from, err := refactor.ParseIdent("old package name", from)
	if err != nil {
		return err
	}
	to, err = refactor.ParseIdent("new package name", to)
	if err != nil {
		return err
	}
	if from == to {
		return errors.New("old and new package names are the same")
	}
	cwd, allowed, producer, err := cmd.loadRenameProducer(patterns)
	if err != nil {
		return err
	}
	importPath, err := producer.ResolvePackage(from, allowed)
	if err != nil {
		return err
	}
	modules, err := cmd.loadTypedModules(producer, &opts.consumerOptions)
	if err != nil {
		return err
	}
	var batches []renameBatch
	for _, module := range modules {
		var count int
		var renameErr error
		if module.Producer {
			count, renameErr = module.Program.RenamePackageClause(importPath, from, to)
		} else {
			count, renameErr = module.Program.RenamePackageUses(importPath, to)
		}
		if renameErr != nil {
			return renameErr
		}
		batch, batchErr := cmd.pathRenameBatch(pathRenameBatchRequest{
			Module: module, Count: count, ProducerRoot: producer.Root(), Cwd: cwd, Allowed: allowed,
		})
		if batchErr != nil {
			return batchErr
		}
		if batch != nil {
			batches = append(batches, *batch)
		}
	}
	return cmd.emitRenameBatches(cwd, &opts.apply, batches)
}

func (cmd *mainCmd) pathRenameBatch(req pathRenameBatchRequest) (*renameBatch, error) {
	if req.Count == 0 {
		return nil, nil
	}
	edits, err := req.Module.Program.Edits()
	if err != nil {
		return nil, err
	}
	if req.Module.Producer && req.CheckScope {
		if err := checkEditScope(req.Cwd, edits, req.Allowed); err != nil {
			return nil, err
		}
	}
	batch := &renameBatch{
		Root: req.Module.Root, ModulePath: req.Module.Program.ModulePath(), Producer: req.Module.Producer,
		Edits: edits,
	}
	if err := cmd.printPlannedEdits(edits, req.ProducerRoot); err != nil {
		return nil, err
	}
	return batch, nil
}

func (cmd *mainCmd) newRenameImportCommand() *cobra.Command {
	var opts pathRenameOptions
	var fromName, toName string
	command := &cobra.Command{
		Use:     "import <old-path> <new-path> [pattern...]",
		Aliases: []string{"import-path"},
		Short:   "Rewrite an exact import path and its bound qualifier.",
		Long: "Rewrite an exact import path across the module, active Go workspace, and configured " +
			"consumers. Explicit aliases are preserved unless --to-name overrides them.",
		Example: examples(
			"gopatch rename import example.com/old/http example.com/new/http ./...",
			"gopatch rename import example.com/http example.com/client --consumers ../services ./...",
			"gopatch rename import example.com/go-yaml gopkg.in/yaml.v3 --name yaml --to-name yaml ./...",
		),
		Args: cobra.MinimumNArgs(3),
		RunE: func(_ *cobra.Command, args []string) error {
			return cmd.runRenameImport(&opts, fromName, toName, args[0], args[1], args[2:])
		},
	}
	addPathRenameFlags(command.Flags(), &opts)
	command.Flags().StringVar(&fromName, "name", "", "Expected current package name for the old path.")
	command.Flags().StringVar(&toName, "to-name", "", "Package name to use after the rewrite.")
	return command
}

func (cmd *mainCmd) runRenameImport(opts *pathRenameOptions, fromName, toName, fromPath, toPath string, patterns []string) error {
	overrideAliases := toName != ""
	fromPath, err := refactor.ParseImportPath("old import path", fromPath)
	if err != nil {
		return err
	}
	toPath, err = refactor.ParseImportPath("new import path", toPath)
	if err != nil {
		return err
	}
	if fromPath == toPath {
		return errors.New("old and new import paths are the same")
	}
	if toName == "" {
		toName, err = refactor.DerivePackageName("new import path", toPath)
	} else {
		toName, err = refactor.ParseIdent("new package name", toName)
	}
	if err != nil {
		return err
	}
	if fromName != "" {
		if fromName, err = refactor.ParseIdent("old package name", fromName); err != nil {
			return err
		}
	}
	cwd, allowed, producer, err := cmd.loadRenameProducer(patterns)
	if err != nil {
		return err
	}
	modules, selection, err := cmd.loadImportRenameModules(producer, &opts.consumerOptions, fromPath)
	if err != nil {
		return err
	}
	request := typed.RenameImportRequest{
		FromPath: fromPath, ToPath: toPath, FromName: fromName, ToName: toName, OverrideAliases: overrideAliases,
	}
	var batches []renameBatch
	for _, module := range modules {
		count, renameErr := module.Program.RenameImport(request)
		if renameErr != nil {
			return renameErr
		}
		batch, batchErr := cmd.pathRenameBatch(pathRenameBatchRequest{
			Module: module, Count: count, ProducerRoot: producer.Root(), Cwd: cwd,
			Allowed: allowed, CheckScope: true,
		})
		if batchErr != nil {
			return batchErr
		}
		if batch != nil {
			batches = append(batches, *batch)
		}
	}
	if len(batches) == 0 {
		if selection.Configured {
			return nil
		}
		return fmt.Errorf("no Go file imports %q", fromPath)
	}
	return cmd.emitRenameBatches(cwd, &opts.apply, batches)
}

func (cmd *mainCmd) loadRenameProducer(patterns []string) (string, []string, *typed.Program, error) {
	if len(patterns) == 0 {
		return "", nil, nil, errors.New("please provide at least one pattern")
	}
	cwd, err := cmd.Getwd()
	if err != nil {
		return "", nil, nil, fmt.Errorf("getwd: %w", err)
	}
	allowed, err := cmd.resolveFiles(patterns)
	if err != nil {
		return "", nil, nil, err
	}
	producer, err := typed.Load(typed.Request{Dir: cwd})
	return cwd, allowed, producer, err
}

func (cmd *mainCmd) loadImportRenameModules(producer *typed.Program, opts *consumerOptions, importPath string) ([]renameModule, consumerSelection, error) {
	selection, err := cmd.consumerRoots(producer.Root(), opts)
	if err != nil {
		return nil, selection, err
	}
	found, err := consumers.WorkspaceImport(producer.Root(), importPath)
	if err != nil {
		return nil, selection, err
	}
	if len(selection.Roots) > 0 {
		discovered, findErr := consumers.FindImport(consumers.ImportRequest{Roots: selection.Roots, ImportPath: importPath})
		if findErr != nil {
			return nil, selection, findErr
		}
		if selection.Explicit && len(discovered) == 0 {
			return nil, selection, fmt.Errorf("consumer discovery found no modules importing %q", importPath)
		}
		found = append(found, discovered...)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Root < found[j].Root })
	modules := []renameModule{{Root: producer.Root(), Program: producer, Producer: true}}
	seen := map[string]bool{canonicalMovePath(producer.Root()): true}
	for _, module := range found {
		root := canonicalMovePath(module.Root)
		if seen[root] {
			continue
		}
		seen[root] = true
		program, loadErr := typed.Load(typed.Request{Dir: root})
		if loadErr != nil {
			return nil, selection, fmt.Errorf("load import consumer %v: %w", root, loadErr)
		}
		modules = append(modules, renameModule{Root: root, Program: program})
	}
	return modules, selection, nil
}
