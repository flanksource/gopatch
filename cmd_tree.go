package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/flanksource/clicky/api"
	"github.com/flanksource/clicky/api/icons"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

type treeOptions struct {
	private   bool
	methods   bool
	functions bool
	fields    bool
	variables bool
	tests     bool
}

type sourceTreeNode struct {
	icon        icons.Icon
	kind        string
	name        string
	label       api.Textable
	detail      string
	detailFirst bool
	children    []api.TreeNode
}

func (n sourceTreeNode) Pretty() api.Text {
	text := api.Text{}.Add(n.icon).Space()
	if n.kind != "" {
		text = text.Append(n.kind, "text-muted").Space()
	}
	if n.detailFirst && n.detail != "" {
		text = text.Append(n.detail, "text-muted").Space()
	}
	if n.label != nil {
		text = text.Add(n.label)
	} else {
		text = text.Append(n.name, "font-bold")
	}
	if !n.detailFirst && n.detail != "" {
		text = text.Space().Append(n.detail, "text-muted")
	}
	return text
}

func (n sourceTreeNode) GetChildren() []api.TreeNode {
	return n.children
}

type sourcePackage struct {
	directory string
	name      string
	files     []api.TreeNode
}

func (cmd *mainCmd) newTreeCommand() *cobra.Command {
	var opts treeOptions
	command := &cobra.Command{
		Use:   "tree [pattern...]",
		Short: "Show packages, files, and declarations as a source tree.",
		Long: "Show the Go source tree as packages, files, and declarations with line numbers. " +
			"Functions and methods include parameters and source ranges; methods are nested by receiver. " +
			"The command parses syntax only, so it also works while the code does not type-check." +
			syntaxNote(_synInspect),
		Example: examples(
			"gopatch tree",
			"gopatch tree ./internal/...",
			"gopatch tree ./model/user.go",
		),
		Args: cobra.ArbitraryArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if len(args) == 0 {
				args = []string{"./..."}
			}
			return cmd.runTree(c, args, opts)
		},
	}
	command.Flags().BoolVar(&opts.private, "private", false, "Include unexported declarations.")
	command.Flags().BoolVar(&opts.methods, "methods", true, "Include methods.")
	command.Flags().BoolVar(&opts.functions, "functions", true, "Include functions.")
	command.Flags().BoolVar(&opts.fields, "fields", false, "Include struct fields and interface methods.")
	command.Flags().BoolVar(&opts.variables, "variables", false, "Include variables and constants.")
	command.Flags().BoolVar(&opts.tests, "tests", false, "Include Go test files.")
	command.Flags().BoolVar(&opts.variables, "vsaariables", false, "Alias for --variables.")
	_ = command.Flags().MarkHidden("vsaariables")
	return command
}

func (cmd *mainCmd) runTree(c *cobra.Command, patterns []string, opts treeOptions) error {
	cwd, err := cmd.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	files, err := findFiles(cwd, patterns)
	if err != nil {
		return err
	}

	packages, err := buildSourcePackages(cwd, files, opts)
	if err != nil {
		return err
	}
	for i, pkg := range packages {
		if i > 0 {
			if _, err := fmt.Fprintln(c.OutOrStdout()); err != nil {
				return err
			}
		}
		node := sourceTreeNode{
			icon:     icons.Package,
			kind:     "package",
			name:     pkg.name,
			detail:   displayDirectory(cwd, pkg.directory),
			children: pkg.files,
		}
		if _, err := fmt.Fprintln(c.OutOrStdout(), renderSourceTree(node, shouldUseTreeColors(c.OutOrStdout()))); err != nil {
			return err
		}
	}
	return nil
}

func buildSourcePackages(cwd string, files []sourcePath, opts treeOptions) ([]sourcePackage, error) {
	fset := token.NewFileSet()
	packages := make(map[string]*sourcePackage)
	for _, path := range files {
		if !opts.tests && strings.HasSuffix(filepath.Base(path.Absolute), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, path.Absolute, nil, parser.AllErrors)
		if err != nil {
			return nil, fmt.Errorf("could not parse %q: %w", path.Provided, err)
		}

		directory := filepath.Dir(path.Absolute)
		key := directory + "\x00" + file.Name.Name
		pkg := packages[key]
		if pkg == nil {
			pkg = &sourcePackage{directory: directory, name: file.Name.Name}
			packages[key] = pkg
		}
		declarations, err := declarationNodes(fset, file, opts)
		if err != nil {
			return nil, fmt.Errorf("build tree for %q: %w", path.Provided, err)
		}
		pkg.files = append(pkg.files, sourceTreeNode{
			icon:     icons.Golang,
			name:     filepath.Base(path.Absolute),
			children: declarations,
		})
	}

	result := make([]sourcePackage, 0, len(packages))
	for _, pkg := range packages {
		result = append(result, *pkg)
	}
	sort.Slice(result, func(i, j int) bool {
		left := displayDirectory(cwd, result[i].directory) + "\x00" + result[i].name
		right := displayDirectory(cwd, result[j].directory) + "\x00" + result[j].name
		return left < right
	})
	return result, nil
}

func declarationNodes(fset *token.FileSet, file *ast.File, opts treeOptions) ([]api.TreeNode, error) {
	methods, receiverOrder, err := collectMethodNodes(fset, file, opts)
	if err != nil {
		return nil, err
	}

	var nodes []api.TreeNode
	for _, decl := range file.Decls {
		switch decl := decl.(type) {
		case *ast.GenDecl:
			if decl.Tok == token.IMPORT {
				continue
			}
			for _, spec := range decl.Specs {
				specNodes, err := declarationSpecNodes(fset, decl.Tok, spec, opts)
				if err != nil {
					return nil, err
				}
				if typeSpec, ok := spec.(*ast.TypeSpec); ok && len(specNodes) == 1 {
					if receiverMethods := methods[typeSpec.Name.Name]; len(receiverMethods) > 0 {
						specNodes[0].(*sourceTreeNode).children = append(specNodes[0].GetChildren(), receiverMethods...)
						delete(methods, typeSpec.Name.Name)
					}
				}
				nodes = append(nodes, specNodes...)
			}
		case *ast.FuncDecl:
			if decl.Recv != nil {
				continue
			}
			node, err := functionNode(fset, decl, opts)
			if err != nil {
				return nil, err
			}
			if node != nil {
				nodes = append(nodes, node)
			}
		}
	}
	for _, receiver := range receiverOrder {
		if receiverMethods := methods[receiver]; len(receiverMethods) > 0 {
			nodes = append(nodes, &sourceTreeNode{
				icon: icons.Type, kind: "methods", name: receiver, children: receiverMethods,
			})
		}
	}
	return nodes, nil
}

func collectMethodNodes(fset *token.FileSet, file *ast.File, opts treeOptions) (map[string][]api.TreeNode, []string, error) {
	methods := make(map[string][]api.TreeNode)
	var receiverOrder []string
	for _, item := range file.Decls {
		decl, ok := item.(*ast.FuncDecl)
		if !ok || decl.Recv == nil {
			continue
		}
		node, err := functionNode(fset, decl, opts)
		if err != nil {
			return nil, nil, err
		}
		if node == nil {
			continue
		}
		receiver := receiverName(decl.Recv.List[0].Type)
		if _, exists := methods[receiver]; !exists {
			receiverOrder = append(receiverOrder, receiver)
		}
		methods[receiver] = append(methods[receiver], node)
	}
	return methods, receiverOrder, nil
}

func declarationSpecNodes(fset *token.FileSet, tok token.Token, spec ast.Spec, opts treeOptions) ([]api.TreeNode, error) {
	switch spec := spec.(type) {
	case *ast.TypeSpec:
		if !includePrivate(spec.Name.Name, opts) {
			return nil, nil
		}
		kind, icon := "type", icons.Type
		if !spec.Assign.IsValid() {
			switch spec.Type.(type) {
			case *ast.StructType:
				kind = "struct"
			case *ast.InterfaceType:
				kind, icon = "interface", icons.Interface
			}
		}
		node := declarationNode(fset, icon, kind, spec.Name.Name, spec.Name.Pos())
		if opts.fields {
			switch typ := spec.Type.(type) {
			case *ast.StructType:
				node.children = structFieldNodes(fset, typ, opts)
			case *ast.InterfaceType:
				children, err := interfaceMethodNodes(fset, typ, opts)
				if err != nil {
					return nil, err
				}
				node.children = children
			}
		}
		return []api.TreeNode{node}, nil
	case *ast.ValueSpec:
		if !opts.variables {
			return nil, nil
		}
		icon := icons.Variable
		if tok == token.CONST {
			icon = icons.Constant
		}
		nodes := make([]api.TreeNode, 0, len(spec.Names))
		for _, name := range spec.Names {
			if !includePrivate(name.Name, opts) {
				continue
			}
			nodes = append(nodes, declarationNode(fset, icon, strings.ToLower(tok.String()), name.Name, name.Pos()))
		}
		return nodes, nil
	default:
		return nil, nil
	}
}

func functionNode(fset *token.FileSet, decl *ast.FuncDecl, opts treeOptions) (*sourceTreeNode, error) {
	if decl.Recv == nil {
		if !opts.functions || !includePrivate(decl.Name.Name, opts) {
			return nil, nil
		}
		signature, err := functionSignature(fset, decl.Name.Name, decl.Type.Params)
		if err != nil {
			return nil, err
		}
		return rangedDeclarationNode(fset, icons.Lambda, "func", signature, decl.Pos(), decl.End()), nil
	}
	receiver := receiverName(decl.Recv.List[0].Type)
	if receiver == "" {
		return nil, fmt.Errorf("determine receiver for method %s", decl.Name.Name)
	}
	if !opts.methods || !includePrivate(receiver, opts) || !includePrivate(decl.Name.Name, opts) {
		return nil, nil
	}
	signature, err := functionSignature(fset, receiver+"."+decl.Name.Name, decl.Type.Params)
	if err != nil {
		return nil, err
	}
	return rangedDeclarationNode(fset, icons.Method, "method", signature, decl.Pos(), decl.End()), nil
}

func functionSignature(fset *token.FileSet, name string, params *ast.FieldList) (api.Textable, error) {
	signature := api.Text{}.
		Append(name, "font-bold text-blue-600").
		Append("(", "text-muted")
	for index, field := range params.List {
		if index > 0 {
			signature = signature.Append(", ", "text-muted")
		}
		for nameIndex, parameter := range field.Names {
			if nameIndex > 0 {
				signature = signature.Append(", ", "text-muted")
			}
			signature = signature.Append(parameter.Name, "text-orange-500")
		}
		if len(field.Names) > 0 {
			signature = signature.Append(" ")
		}
		var parameterType bytes.Buffer
		if err := format.Node(&parameterType, fset, field.Type); err != nil {
			return nil, fmt.Errorf("format parameter type for %s: %w", name, err)
		}
		signature = signature.Append(parameterType.String(), "text-cyan-500")
	}
	return signature.Append(")", "text-muted"), nil
}

func declarationNode(fset *token.FileSet, icon icons.Icon, kind, name string, pos token.Pos) *sourceTreeNode {
	return &sourceTreeNode{
		icon:   icon,
		kind:   kind,
		name:   name,
		detail: fmt.Sprintf("L%d", fset.Position(pos).Line),
	}
}

func rangedDeclarationNode(fset *token.FileSet, icon icons.Icon, kind string, label api.Textable, start, end token.Pos) *sourceTreeNode {
	return &sourceTreeNode{
		icon:        icon,
		kind:        kind,
		label:       label,
		detail:      fmt.Sprintf("L%d-L%d", fset.Position(start).Line, fset.Position(end).Line),
		detailFirst: true,
	}
}

func renderSourceTree(node sourceTreeNode, colors bool) string {
	tree := api.NewTree(node)
	if colors {
		return tree.ANSI()
	}
	return tree.String()
}

func shouldUseTreeColors(out io.Writer) bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb" {
		return false
	}
	if color := strings.ToLower(os.Getenv("COLOR")); color == "no" || color == "false" {
		return false
	}
	file, ok := out.(*os.File)
	return ok && term.IsTerminal(int(file.Fd()))
}

func structFieldNodes(fset *token.FileSet, typ *ast.StructType, opts treeOptions) []api.TreeNode {
	var nodes []api.TreeNode
	for _, field := range typ.Fields.List {
		if len(field.Names) == 0 {
			if name := embeddedFieldName(field.Type); name != "" && includePrivate(name, opts) {
				nodes = append(nodes, declarationNode(fset, icons.Code, "field", name, field.Type.Pos()))
			}
			continue
		}
		for _, name := range field.Names {
			if includePrivate(name.Name, opts) {
				nodes = append(nodes, declarationNode(fset, icons.Code, "field", name.Name, name.Pos()))
			}
		}
	}
	return nodes
}

func interfaceMethodNodes(fset *token.FileSet, typ *ast.InterfaceType, opts treeOptions) ([]api.TreeNode, error) {
	if !opts.methods {
		return nil, nil
	}
	var nodes []api.TreeNode
	for _, field := range typ.Methods.List {
		for _, name := range field.Names {
			if !includePrivate(name.Name, opts) {
				continue
			}
			method, ok := field.Type.(*ast.FuncType)
			if !ok {
				return nil, fmt.Errorf("method %s has unexpected type %T", name.Name, field.Type)
			}
			signature, err := functionSignature(fset, name.Name, method.Params)
			if err != nil {
				return nil, err
			}
			nodes = append(nodes, rangedDeclarationNode(fset, icons.Method, "method", signature, field.Pos(), field.End()))
		}
	}
	return nodes, nil
}

func embeddedFieldName(expr ast.Expr) string {
	switch expr := expr.(type) {
	case *ast.Ident:
		return expr.Name
	case *ast.StarExpr:
		return embeddedFieldName(expr.X)
	case *ast.SelectorExpr:
		return expr.Sel.Name
	case *ast.IndexExpr:
		return embeddedFieldName(expr.X)
	case *ast.IndexListExpr:
		return embeddedFieldName(expr.X)
	case *ast.ParenExpr:
		return embeddedFieldName(expr.X)
	default:
		return ""
	}
}

func includePrivate(name string, opts treeOptions) bool {
	return opts.private || ast.IsExported(name)
}

func receiverName(expr ast.Expr) string {
	switch expr := expr.(type) {
	case *ast.Ident:
		return expr.Name
	case *ast.StarExpr:
		return receiverName(expr.X)
	case *ast.IndexExpr:
		return receiverName(expr.X)
	case *ast.IndexListExpr:
		return receiverName(expr.X)
	case *ast.ParenExpr:
		return receiverName(expr.X)
	default:
		return ""
	}
}

func displayDirectory(cwd, directory string) string {
	relative, err := filepath.Rel(cwd, directory)
	if err != nil {
		return directory
	}
	if relative == "." || strings.HasPrefix(relative, ".") {
		return relative
	}
	return "." + string(filepath.Separator) + relative
}
