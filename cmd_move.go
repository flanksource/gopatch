package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/pkg/diff"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/uber-go/gopatch/internal/refactor"
	"github.com/uber-go/gopatch/internal/refactor/consumers"
	"github.com/uber-go/gopatch/internal/refactor/typed"
)

const (
	_dryRunDesc   = "Summarize the proposed move without modifying any files."
	_moveDiffDesc = "Print the full unified diff without modifying any files."
)

type moveOptions struct {
	typedRenameOptions
	DryRun bool
}

type stagedMoveEdit struct {
	edit              typed.FileEdit
	staged, backup    string
	installed, backed bool
}

func addMoveFlags(fs *pflag.FlagSet, opts *moveOptions, packageFlag bool) {
	fs.BoolVarP(&opts.apply.Diff, "diff", "d", false, _moveDiffDesc)
	fs.BoolVar(&opts.DryRun, "dry-run", false, _dryRunDesc)
	fs.StringArrayVar(&opts.Consumers, "consumers", nil, _consumersDesc)
	fs.StringVar(&opts.ConsumersFrom, "consumers-from", "", _consumersFromDesc)
	if packageFlag {
		fs.StringVar(&opts.Package, "package", "", _typedPackageDesc)
	}
}

func (cmd *mainCmd) newMoveCommand() *cobra.Command {
	move := &cobra.Command{
		Use:   "move",
		Short: "Move Go files and declarations.",
		Long: "Move a Go file or a package-level declaration while updating typed references.\n\n" +
			"A move within one package only relocates source. A move to another package " +
			"rewrites imports and consumers throughout the module. Named types carry all " +
			"of their receiver methods with them.",
		Example: examples(
			"gopatch move file internal/old/user.go internal/model/",
			"gopatch move struct old.User internal/model/user.go",
			"gopatch move func file.go:Load internal/config/load.go --dry-run",
			"gopatch move method file.go:Client.Close internal/client/close.go --diff",
		),
	}

	move.AddCommand(cmd.newMoveFileCommand())
	for _, spec := range []struct {
		noun       string
		kind       typed.Kind
		names      int
		structOnly bool
	}{
		{noun: "struct", kind: typed.KindType, names: 1, structOnly: true},
		{noun: "type", kind: typed.KindType, names: 1},
		{noun: "func", kind: typed.KindFunc, names: 1},
		{noun: "var", kind: typed.KindVar, names: 1},
		{noun: "const", kind: typed.KindConst, names: 1},
		{noun: "method", kind: typed.KindMethod, names: 2},
	} {
		move.AddCommand(cmd.newMoveSymbolCommand(spec.noun, spec.kind, spec.names, spec.structOnly))
	}
	return move
}

func (cmd *mainCmd) newMoveFileCommand() *cobra.Command {
	var opts moveOptions
	c := &cobra.Command{
		Use:   "file <source.go> <destination-dir|destination.go>",
		Short: "Move one Go file and update references when its package changes.",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			return cmd.runMoveFile(&opts, args[0], args[1])
		},
	}
	addMoveFlags(c.Flags(), &opts, false)
	return c
}

func (cmd *mainCmd) newMoveSymbolCommand(noun string, kind typed.Kind, names int, structOnly bool) *cobra.Command {
	var opts moveOptions
	c := &cobra.Command{
		Use:   noun + " <target> <destination.go>",
		Short: "Move one " + noun + " declaration to another file.",
		Args:  cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			target, err := refactor.ParseTarget(noun, args[0], names)
			if err != nil {
				return err
			}
			q, err := cmd.query(noun, kind, target, &opts.typedOptions)
			if err != nil {
				return err
			}
			return cmd.runMoveSymbol(&opts, q, args[1], structOnly)
		},
	}
	addMoveFlags(c.Flags(), &opts, true)
	return c
}

func (cmd *mainCmd) runMoveFile(opts *moveOptions, source, destination string) error {
	if err := validateMoveOutputMode(opts); err != nil {
		return err
	}
	cwd, err := cmd.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	source, err = cmd.absPath(source)
	if err != nil {
		return err
	}
	destination, err = cmd.absPath(destination)
	if err != nil {
		return err
	}
	if info, statErr := os.Stat(destination); statErr == nil && info.IsDir() {
		destination = filepath.Join(destination, filepath.Base(source))
	} else if os.IsNotExist(statErr) && filepath.Ext(destination) == "" {
		destination = filepath.Join(destination, filepath.Base(source))
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	if err := rejectCgoMove(source); err != nil {
		return err
	}

	program, err := typed.Load(typed.Request{Dir: cwd})
	if err != nil {
		return err
	}
	result, err := program.Move(typed.MoveRequest{SourceFile: source, Destination: destination})
	if err != nil {
		return err
	}
	if err := cmd.extendMoveToConsumers(opts, program, &result); err != nil {
		return err
	}
	return cmd.finishMove(opts, result)
}

func rejectCgoMove(path string) error {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return fmt.Errorf("parse move source %v: %w", path, err)
	}
	for _, spec := range file.Imports {
		if importPath, err := strconv.Unquote(spec.Path.Value); err == nil && importPath == "C" {
			return fmt.Errorf("cannot move cgo file %v safely", path)
		}
	}
	return nil
}

func (cmd *mainCmd) runMoveSymbol(opts *moveOptions, query typed.Query, destination string, structOnly bool) error {
	if err := validateMoveOutputMode(opts); err != nil {
		return err
	}
	cwd, err := cmd.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	destination, err = cmd.absPath(destination)
	if err != nil {
		return err
	}
	program, err := typed.Load(typed.Request{Dir: cwd})
	if err != nil {
		return err
	}
	symbol, err := program.Resolve(query)
	if err != nil {
		return err
	}
	result, err := program.Move(typed.MoveRequest{Symbol: symbol, Destination: destination, StructOnly: structOnly})
	if err != nil {
		return err
	}
	if err := cmd.extendMoveToConsumers(opts, program, &result); err != nil {
		return err
	}
	return cmd.finishMove(opts, result)
}

func (cmd *mainCmd) extendMoveToConsumers(opts *moveOptions, producer *typed.Program, result *typed.MoveResult) error {
	if result.SourcePackage == result.TargetPackage {
		return nil
	}
	workspace, err := consumers.Workspace(producer.Root())
	if err != nil {
		return err
	}
	roots, err := cmd.consumerRoots(&opts.typedRenameOptions)
	if err != nil {
		return err
	}
	if len(roots) > 0 {
		found, err := consumers.Find(consumers.Request{ProducerRoot: producer.Root(), Roots: roots})
		if err != nil {
			return err
		}
		if len(found) == 0 {
			return errors.New("consumer discovery found no modules that require the producer module")
		}
		workspace = append(workspace, found...)
	}

	seen := map[string]bool{canonicalMovePath(producer.Root()): true}
	for _, module := range workspace {
		root := canonicalMovePath(module.Root)
		if seen[root] {
			continue
		}
		seen[root] = true
		program, err := typed.Load(typed.Request{Dir: root})
		if err != nil {
			return fmt.Errorf("load move consumer %v: %w", root, err)
		}
		program.RegisterPackage(result.TargetPackage, result.TargetPackageName)
		count, err := program.RedirectMoves(result.Refs, result.TargetPackage, result.SourcePackageEntirelyRelocated)
		if err != nil {
			return fmt.Errorf("rewrite move consumer %v: %w", root, err)
		}
		edits, err := program.Edits()
		if err != nil {
			return err
		}
		result.References += count
		result.Edits = append(result.Edits, edits...)
	}
	return nil
}

func canonicalMovePath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return filepath.Clean(path)
}

func validateMoveOutputMode(opts *moveOptions) error {
	if opts.DryRun && opts.apply.Diff {
		return errors.New("--dry-run and --diff are mutually exclusive")
	}
	if opts.apply.Print {
		return errors.New("move does not support --print-only; use --dry-run or --diff")
	}
	return nil
}

func (cmd *mainCmd) finishMove(opts *moveOptions, result typed.MoveResult) error {
	if len(result.Edits) == 0 {
		return errors.New("move changed nothing")
	}
	sort.Slice(result.Edits, func(i, j int) bool { return result.Edits[i].Path < result.Edits[j].Path })
	if err := validateMoveEdits(result.Edits); err != nil {
		return err
	}
	if err := validateRenderedMove(result.Edits); err != nil {
		return err
	}
	if opts.DryRun {
		return cmd.printMoveSummary(result)
	}
	if opts.apply.Diff {
		return cmd.printMoveDiff(result.Edits)
	}
	return applyMoveEdits(result.Edits)
}

func validateRenderedMove(edits []typed.FileEdit) error {
	roots := make(map[string]bool)
	for _, edit := range edits {
		root, err := moveModuleRoot(filepath.Dir(edit.Path))
		if err != nil {
			return err
		}
		roots[root] = true
	}
	ordered := make([]string, 0, len(roots))
	for root := range roots {
		ordered = append(ordered, root)
	}
	sort.Strings(ordered)
	for _, root := range ordered {
		if err := typed.ValidateMove(root, edits); err != nil {
			return err
		}
	}
	return nil
}

func moveModuleRoot(dir string) (string, error) {
	for current := dir; ; current = filepath.Dir(current) {
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			return canonicalMovePath(current), nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no go.mod above move path %v", dir)
		}
	}
}

func validateMoveEdits(edits []typed.FileEdit) error {
	seen := make(map[string]bool, len(edits))
	for _, edit := range edits {
		if seen[edit.Path] {
			return fmt.Errorf("duplicate edit for %v", edit.Path)
		}
		seen[edit.Path] = true
		switch {
		case edit.Create:
			if _, err := os.Lstat(edit.Path); err == nil {
				return fmt.Errorf("destination %v appeared after the move was planned; nothing was written", edit.Path)
			} else if !os.IsNotExist(err) {
				return err
			}
		case edit.Delete || edit.Original != nil:
			current, err := os.ReadFile(edit.Path)
			if err != nil {
				return fmt.Errorf("re-read %v before moving: %w", edit.Path, err)
			}
			if !bytes.Equal(current, edit.Original) {
				return fmt.Errorf("%v changed after it was loaded; nothing was written", edit.Path)
			}
		}
	}
	return nil
}

func (cmd *mainCmd) printMoveSummary(result typed.MoveResult) error {
	if _, err := fmt.Fprintf(cmd.Stdout, "move %s -> %s\n", result.SourceFile, result.DestinationFile); err != nil {
		return err
	}
	if result.SourcePackage != result.TargetPackage {
		if _, err := fmt.Fprintf(cmd.Stdout, "package %s -> %s\n", result.SourcePackage, result.TargetPackage); err != nil {
			return err
		}
	}
	if len(result.Declarations) > 0 {
		if _, err := fmt.Fprintf(cmd.Stdout, "declarations: %s\n", strings.Join(result.Declarations, ", ")); err != nil {
			return err
		}
	}
	created, updated, deleted := 0, 0, 0
	for _, edit := range result.Edits {
		switch {
		case edit.Create:
			created++
		case edit.Delete:
			deleted++
		default:
			updated++
		}
	}
	_, err := fmt.Fprintf(cmd.Stdout, "references: %d; files: %d create, %d update, %d delete\n", result.References, created, updated, deleted)
	return err
}

func (cmd *mainCmd) printMoveDiff(edits []typed.FileEdit) error {
	for _, edit := range edits {
		before, after := edit.Path, edit.Path
		if edit.Create {
			before = "/dev/null"
		}
		if edit.Delete {
			after = "/dev/null"
		}
		if err := diff.Text(before, after, edit.Original, edit.Modified, cmd.Stdout); err != nil {
			return err
		}
	}
	return nil
}

func applyMoveEdits(edits []typed.FileEdit) error {
	staged := make([]stagedMoveEdit, len(edits))
	var createdDirs []string
	for _, edit := range edits {
		if edit.Delete {
			continue
		}
		dirs, err := createMoveDirectories(filepath.Dir(edit.Path))
		if err != nil {
			cleanupMoveDirectories(createdDirs)
			return err
		}
		createdDirs = append(createdDirs, dirs...)
	}
	for i, edit := range edits {
		staged[i].edit = edit
		if edit.Delete {
			continue
		}
		file, err := os.CreateTemp(filepath.Dir(edit.Path), ".gopatch-stage-*")
		if err != nil {
			rollbackMove(staged, createdDirs)
			return err
		}
		staged[i].staged = file.Name()
		mode := edit.Mode
		if mode == 0 {
			mode = 0o644
		}
		if err := file.Chmod(mode); err != nil {
			file.Close()
			rollbackMove(staged, createdDirs)
			return err
		}
		if _, err := file.Write(edit.Modified); err != nil {
			file.Close()
			rollbackMove(staged, createdDirs)
			return err
		}
		if err := file.Close(); err != nil {
			rollbackMove(staged, createdDirs)
			return err
		}
	}
	for i := range staged {
		if staged[i].edit.Create {
			continue
		}
		backup, err := reserveMovePath(filepath.Dir(staged[i].edit.Path))
		if err != nil {
			rollbackMove(staged, createdDirs)
			return err
		}
		staged[i].backup = backup
		if err := os.Rename(staged[i].edit.Path, backup); err != nil {
			rollbackMove(staged, createdDirs)
			return err
		}
		staged[i].backed = true
	}
	for i := range staged {
		if staged[i].edit.Delete {
			continue
		}
		if err := os.Rename(staged[i].staged, staged[i].edit.Path); err != nil {
			rollbackMove(staged, createdDirs)
			return err
		}
		staged[i].staged = ""
		staged[i].installed = true
	}
	for _, item := range staged {
		if item.backed {
			if err := os.Remove(item.backup); err != nil {
				return fmt.Errorf("remove move backup %v: %w", item.backup, err)
			}
		}
	}
	return nil
}

func createMoveDirectories(dir string) ([]string, error) {
	var created []string
	for current := dir; ; current = filepath.Dir(current) {
		if _, err := os.Stat(current); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return nil, err
		}
		created = append(created, current)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return created, nil
}

func reserveMovePath(dir string) (string, error) {
	file, err := os.CreateTemp(dir, ".gopatch-backup-*")
	if err != nil {
		return "", err
	}
	path := file.Name()
	if err := file.Close(); err != nil {
		return "", err
	}
	if err := os.Remove(path); err != nil {
		return "", err
	}
	return path, nil
}

func rollbackMove(staged []stagedMoveEdit, createdDirs []string) {
	for i := len(staged) - 1; i >= 0; i-- {
		if staged[i].installed {
			_ = os.Remove(staged[i].edit.Path)
		}
		if staged[i].backed {
			_ = os.Rename(staged[i].backup, staged[i].edit.Path)
		}
		if staged[i].staged != "" {
			_ = os.Remove(staged[i].staged)
		}
	}
	cleanupMoveDirectories(createdDirs)
}

func cleanupMoveDirectories(dirs []string) {
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i]) > len(dirs[j]) })
	for _, dir := range dirs {
		_ = os.Remove(dir)
	}
}
