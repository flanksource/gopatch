package main

import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/flanksource/clicky/api"
	"github.com/flanksource/clicky/api/icons"
	"github.com/flanksource/uir/query"
	"github.com/spf13/cobra"
	"github.com/uber-go/gopatch/internal/refactor"
	"github.com/uber-go/gopatch/internal/refactor/typed"
)

type refOptions struct {
	Package string
	consumerOptions
	referenceIndexOptions
}

type referencePackage struct {
	path      string
	directory string
	files     map[string][]typed.Reference
}

func (cmd *mainCmd) newRefCommand() *cobra.Command {
	var opts refOptions
	command := &cobra.Command{
		Use:   "ref <symbol>",
		Short: "Show the definitions and references for a Go symbol.",
		Long: "Show the definitions and references for one package-level Go symbol, grouped by package and file. " +
			"The symbol kind is inferred from type information, and method references include linked interface and concrete methods. " +
			"The owning module, active Go workspace, and configured consumers must type-check and have current UIR snapshots." +
			syntaxNote(_synName, _synMember, _synPkgQual, _synDirQual),
		Example: examples(
			"gopatch ref User",
			"gopatch ref User.Name --package model",
			"gopatch ref store.User.Name",
			"gopatch ref ./internal/store:User.Name --consumers ../services",
		),
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			return cmd.runRef(c, args[0], &opts)
		},
	}
	command.Flags().StringVar(&opts.Package, "package", "", _typedPackageDesc)
	addConsumerFlags(command.Flags(), &opts.consumerOptions)
	addReferenceIndexFlags(command.Flags(), &opts.referenceIndexOptions)
	return command
}

func (cmd *mainCmd) runRef(command *cobra.Command, target string, opts *refOptions) error {
	cwd, err := cmd.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	queries, err := cmd.refQueries(target, opts.Package)
	if err != nil {
		return err
	}
	producer, err := typed.Load(typed.Request{Dir: cwd})
	if err != nil {
		return err
	}
	symbol, err := producer.ResolveSymbol(typed.SymbolRequest{Target: target, Queries: queries})
	if err != nil {
		return err
	}
	modules, err := cmd.loadTypedModules(producer, &opts.consumerOptions)
	if err != nil {
		return err
	}
	refs := expandRenameRefs(modules, symbol.Kind, symbol)
	indexed, err := indexedReferences(command.Context(), opts.referenceIndexOptions, modules, refs, symbol.Kind)
	if err != nil {
		return err
	}
	locations := make([]typed.Reference, 0, len(indexed))
	for _, location := range indexed {
		reference, err := indexedReference(location)
		if err != nil {
			return err
		}
		locations = append(locations, reference)
	}
	sortReferences(locations)

	definitions, references := referenceCounts(locations)
	if definitions == 0 {
		return fmt.Errorf("found %s but could not locate its definition", symbol.Desc)
	}
	tree := buildReferenceTree(canonicalMovePath(cwd), symbol, locations, definitions, references)
	_, err = fmt.Fprintln(command.OutOrStdout(), renderSourceTree(tree, shouldUseColors(command.OutOrStdout())))
	return err
}

func indexedReference(location query.ReferenceLocation) (typed.Reference, error) {
	path, content, err := indexedSource(location)
	if err != nil {
		return typed.Reference{}, err
	}
	line, column, start := 1, 1, 0
	for offset := 0; offset < location.StartByte; offset++ {
		if content[offset] == '\n' {
			line++
			column = 1
			start = offset + 1
		} else {
			column++
		}
	}
	end := len(content)
	if index := bytes.IndexByte(content[location.StartByte:], '\n'); index >= 0 {
		end = location.StartByte + index
	}
	return typed.Reference{
		Package: location.PackagePath, File: path, Offset: location.StartByte,
		Line: line, Column: column, Definition: location.Role == "definition",
		SourceLine: strings.TrimSpace(string(content[start:end])),
	}, nil
}

func (cmd *mainCmd) refQueries(target, packageName string) ([]typed.SymbolQuery, error) {
	seen := make(map[typed.SymbolQuery]bool)
	var queries []typed.SymbolQuery
	var conflict error
	for _, names := range []int{1, 2} {
		parsed, err := refactor.ParseTarget("symbol", target, names)
		if err != nil {
			continue
		}
		if packageName != "" && parsed.Qualifier != "" {
			conflict = fmt.Errorf("symbol is already qualified with %q, so --package %q is ambiguous; drop one",
				parsed.Qualifier, packageName)
			continue
		}
		query := typed.SymbolQuery{
			Owner: parsed.Owner(), Name: parsed.Name(), Package: parsed.Qualifier,
		}
		if packageName != "" {
			query.Package = packageName
		}
		if parsed.Dir != "" {
			query.Dir, err = cmd.absPath(parsed.Dir)
			if err != nil {
				return nil, err
			}
		}
		if !seen[query] {
			seen[query] = true
			queries = append(queries, query)
		}
	}
	if len(queries) > 0 {
		return queries, nil
	}
	if conflict != nil {
		return nil, conflict
	}
	return nil, fmt.Errorf("invalid symbol %q: expected <Name>, <Type>.<name>, <pkg>.<target>, or <./dir>:<target>", target)
}

func sortReferences(references []typed.Reference) {
	sort.Slice(references, func(i, j int) bool {
		left, right := references[i], references[j]
		if left.Package != right.Package {
			return left.Package < right.Package
		}
		if left.File != right.File {
			return left.File < right.File
		}
		if left.Line != right.Line {
			return left.Line < right.Line
		}
		if left.Column != right.Column {
			return left.Column < right.Column
		}
		return left.Definition && !right.Definition
	})
}

func referenceCounts(references []typed.Reference) (definitions, uses int) {
	for _, reference := range references {
		if reference.Definition {
			definitions++
		} else {
			uses++
		}
	}
	return definitions, uses
}

func buildReferenceTree(
	cwd string,
	symbol *typed.Symbol,
	references []typed.Reference,
	definitions int,
	uses int,
) sourceTreeNode {
	children := referencePackageNodes(cwd, groupReferences(references))
	name := strings.TrimPrefix(symbol.Desc, symbol.Kind.String()+" ")
	return sourceTreeNode{
		icon: icons.Search, kind: symbol.Kind.String(), name: name,
		detail: fmt.Sprintf("%d %s, %d %s",
			definitions, plural(definitions, "definition", "definitions"),
			uses, plural(uses, "reference", "references")),
		children: children,
	}
}

func groupReferences(references []typed.Reference) map[string]*referencePackage {
	packages := make(map[string]*referencePackage)
	for _, reference := range references {
		directory := filepath.Dir(reference.File)
		key := reference.Package + "\x00" + directory
		pkg := packages[key]
		if pkg == nil {
			pkg = &referencePackage{
				path: reference.Package, directory: directory, files: make(map[string][]typed.Reference),
			}
			packages[key] = pkg
		}
		pkg.files[reference.File] = append(pkg.files[reference.File], reference)
	}
	return packages
}

func referencePackageNodes(cwd string, packages map[string]*referencePackage) []api.TreeNode {
	packageKeys := make([]string, 0, len(packages))
	for key := range packages {
		packageKeys = append(packageKeys, key)
	}
	sort.Strings(packageKeys)
	children := make([]api.TreeNode, 0, len(packageKeys))
	for _, key := range packageKeys {
		pkg := packages[key]
		children = append(children, sourceTreeNode{
			icon: icons.Package, kind: "package", name: pkg.path,
			detail: displayDirectory(cwd, pkg.directory), children: referenceFileNodes(pkg.files),
		})
	}
	return children
}

func referenceFileNodes(files map[string][]typed.Reference) []api.TreeNode {
	paths := make([]string, 0, len(files))
	for path := range files {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	nodes := make([]api.TreeNode, 0, len(paths))
	for _, path := range paths {
		nodes = append(nodes, sourceTreeNode{
			icon: icons.Golang, name: filepath.Base(path), children: referenceLocationNodes(files[path]),
		})
	}
	return nodes
}

func referenceLocationNodes(references []typed.Reference) []api.TreeNode {
	nodes := make([]api.TreeNode, 0, len(references))
	for _, reference := range references {
		kind, icon := "reference", icons.Link
		if reference.Definition {
			kind, icon = "definition", icons.Target
		}
		nodes = append(nodes, sourceTreeNode{
			icon: icon, kind: kind,
			label:  api.Text{}.Append(reference.SourceLine, "font-mono"),
			detail: fmt.Sprintf("L%d:%d", reference.Line, reference.Column), detailFirst: true,
		})
	}
	return nodes
}

func plural(count int, singular, multiple string) string {
	if count == 1 {
		return singular
	}
	return multiple
}
