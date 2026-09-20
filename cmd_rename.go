package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/uber-go/gopatch/internal/refactor"
	"github.com/uber-go/gopatch/internal/refactor/patchgen"
	"github.com/uber-go/gopatch/internal/refactor/typed"
)

// renameOptions holds the flags the identifier rename subcommands accept.
type renameOptions struct {
	opOptions

	// Package is the declaring package's package clause. Inferred from the
	// target files when unset.
	Package string

	// ImportPath is the declaring package's import path. When unset, only the
	// declaring package is updated.
	ImportPath string

	// Qualifier is the name importing packages use for ImportPath. Defaults to
	// the package name.
	Qualifier string
}

func addRenameScopeFlags(fs *pflag.FlagSet, opts *renameOptions) {
	fs.StringVar(&opts.Package, "package", "",
		"Package clause of the package that declares the symbol. "+
			"Inferred from the given paths when omitted.")
	fs.StringVar(&opts.ImportPath, "import", "",
		"Import path of the declaring package. When given, qualified references "+
			"in importing packages are rewritten too.")
	fs.StringVar(&opts.Qualifier, "qualifier", "",
		"Name importing packages use for --import. Defaults to the package name.")
}

func (cmd *mainCmd) newRenameCommand() *cobra.Command {
	rename := &cobra.Command{
		Use:   "rename",
		Short: "Rename a package, import path, type, function, field, or method.",
		Long: "Rename a package, import path, type, function, field, or method.\n\n" +
			"`package`, `import`, `struct`, `type` and `func` are syntactic: gopatch " +
			"matches by name, so they work on code that does not compile, but a local " +
			"variable that happens to share the name is rewritten too. Review with " +
			"--diff, or inspect the generated program with --print-patch.\n\n" +
			"`field` and `method` use type information instead, because a name match " +
			"cannot tell one type's Name from another's. They need the module to " +
			"compile, and they rewrite the whole module rather than one package." +
			syntaxNote(_synName, _synMember, _synPkgQual, _synDirQual, _synNewName, _synPattern),
		Example: examples(
			"# syntactic: name-matched, works on code that does not compile",
			"gopatch rename struct  HTTPClient      Client              ./...",
			"gopatch rename type    Duration        Interval            ./...",
			"gopatch rename func    doWork          Work                ./...",
			"gopatch rename package http            httpclient          ./http",
			"gopatch rename import  example.com/old example.com/new     ./...",
			"",
			"# type-aware: exact, needs the module to compile",
			"gopatch rename field   User.Name       Title               ./...",
			"gopatch rename method  '*Client.Do'    Send                ./...",
		),
	}

	rename.AddCommand(
		cmd.newRenameIdentCommand("struct", patchgen.KindStruct,
			"Rename a struct and every reference to it.",
			examples(
				"# the declaration and every reference in the declaring package",
				"gopatch rename struct HTTPClient Client ./http",
				"",
				"# also rewrite http.HTTPClient wherever the package is imported",
				"gopatch rename struct HTTPClient Client --import example.com/http ./...",
				"",
				"# when two packages declare the name, say which one you mean",
				"gopatch rename struct Config Options --package server ./...",
				"gopatch rename struct server.Config Options ./...",
				"gopatch rename struct ./internal/server:Config Options ./...",
				"",
				"# an import path qualifier does the work of both --package and",
				"# --import: it names the package everywhere, so consumers move too",
				"gopatch rename struct example.com/http.HTTPClient Client ./...",
				"",
				"# read the generated patch instead of applying it; --package",
				"# is required here, because there are no paths to infer it from",
				"gopatch rename struct HTTPClient Client --package http --print-patch",
			)),
		cmd.newRenameIdentCommand("type", patchgen.KindType,
			"Rename a named type and every reference to it.",
			examples(
				"# any named type: an interface, an alias target, a defined type",
				"gopatch rename type Duration Interval ./...",
				"",
				"# also rewrite timeutil.Duration in importing packages",
				"gopatch rename type Duration Interval --import example.com/timeutil ./...",
			)),
		cmd.newRenameIdentCommand("func", patchgen.KindFunc,
			"Rename a function and every reference to it.",
			examples(
				"# the declaration, every call, and every use as a value",
				"gopatch rename func doWork Work ./...",
				"",
				"# consumers refer to it as httpc.New, not http.New",
				"gopatch rename func New NewClient --import example.com/http --qualifier httpc ./...",
			)),
		cmd.newRenamePackageCommand(),
		cmd.newRenameImportCommand(),
		cmd.newRenameFieldCommand(),
		cmd.newRenameMethodCommand(),
	)

	return rename
}

func (cmd *mainCmd) newRenameFieldCommand() *cobra.Command {
	var opts typedRenameOptions

	c := &cobra.Command{
		Use:   "field <Struct>.<field> <new> [pattern...]",
		Short: "Rename a struct field and every reference to it.",
		Long: "Rename a struct field.\n\n" +
			"The declaration, every x.Field selector on that type, and every key in a " +
			"keyed composite literal of it move together. A field of the same name on " +
			"another type is left alone. Use --consumers or --consumers-from to include " +
			"separate modules that require this one." + _typedLongSuffix,
		Example: examples(
			"# the declaration, every u.Name, and every User{Name: ...} key",
			"gopatch rename field User.Name Title ./...",
			"",
			"# review it first; --diff writes nothing",
			"gopatch rename field User.Name Title -d ./...",
			"",
			"# when two packages declare User, say which one you mean —",
			"# by package name, by import path, by directory, or with --package",
			"gopatch rename field store.User.Name             Title ./...",
			"gopatch rename field example.com/store.User.Name Title ./...",
			"gopatch rename field ./internal/store:User.Name  Title ./...",
			"gopatch rename field User.Name Title --package store ./...",
			"",
			"# include every requiring module found below sibling repositories",
			"gopatch rename field User.Name Title --consumers ../services ./...",
		),
		Args: cobra.MinimumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			return cmd.runRenameSymbol("field", typed.KindField, &opts, args)
		},
	}

	addTypedRenameFlags(c.Flags(), &opts)

	return c
}

func (cmd *mainCmd) newRenameMethodCommand() *cobra.Command {
	var opts typedRenameOptions

	c := &cobra.Command{
		Use:   "method <Recv>.<method> <new> [pattern...]",
		Short: "Rename a method, its interface, and every implementation.",
		Long: "Rename a method.\n\n" +
			"Every call through the method's own type is renamed. When the method " +
			"implements an interface that the module actually assigns to, the interface " +
			"method and its other implementations are renamed with it — renaming only " +
			"one side of that relationship compiles the declaring package and breaks " +
			"the assignment somewhere else. The same applies in reverse: renaming an " +
			"interface method renames every implementation.\n\n" +
			"An implementation outside this module cannot be renamed, so a method that " +
			"satisfies one — fmt.Stringer, for instance — is refused. Selected consumer " +
			"modules participate in the same interface closure." + _typedLongSuffix,
		Example: examples(
			"# Client.Do, the interface it implements, and every implementation",
			"gopatch rename method Client.Do Send ./...",
			"",
			"# a pointer receiver names the same method; quote it for the shell",
			"gopatch rename method '*Client.Do' Send ./...",
			"",
			"# renaming from the interface end does the same thing",
			"gopatch rename method Doer.Do Send ./...",
			"",
			"# qualify the receiver when more than one package declares it",
			"gopatch rename method store.Client.Do               Send ./...",
			"gopatch rename method './internal/store:*Client.Do' Send ./...",
			"",
			"# load requiring modules listed one search directory per line",
			"gopatch rename method Client.Do Send --consumers-from repos.txt ./...",
		),
		Args: cobra.MinimumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			return cmd.runRenameSymbol("method", typed.KindMethod, &opts, args)
		},
	}

	addTypedRenameFlags(c.Flags(), &opts)

	return c
}

// runRenameSymbol resolves a type-aware rename target and applies it.
func (cmd *mainCmd) runRenameSymbol(arg string, kind typed.Kind, opts *typedRenameOptions, args []string) error {
	target, err := refactor.ParseTarget(arg, args[0], 2)
	if err != nil {
		return err
	}
	to, err := refactor.ParseIdent("new name", args[1])
	if err != nil {
		return err
	}

	q, err := cmd.query(arg, kind, target, &opts.typedOptions)
	if err != nil {
		return err
	}

	return cmd.runTypedRename(typedRenameRequest{
		Options:  opts,
		Patterns: args[2:],
		Query:    q,
		To:       to,
	})
}

// newRenameIdentCommand builds `gopatch rename <noun> <old> <new> [paths...]`.
//
// gopatch has no type information, so renaming a struct, a named type and a
// function are the same identifier rewrite; the noun only sharpens the help text
// and the reported description.
func (cmd *mainCmd) newRenameIdentCommand(noun string, kind patchgen.DeclKind, short, example string) *cobra.Command {
	var opts renameOptions

	c := &cobra.Command{
		Use:   noun + " <old> <new> [pattern...]",
		Short: short,
		Long: short + "\n\n" +
			"Rewrites the declaration and every reference in the declaring package. " +
			"Pass --import to also rewrite qualified references in packages that " +
			"import it.",
		Example: example,
		Args:    cobra.MinimumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			return cmd.runRenameIdent(noun, kind, &opts, args)
		},
	}

	addOpFlags(c.Flags(), &opts.opOptions)
	addRenameScopeFlags(c.Flags(), &opts)

	return c
}

func (cmd *mainCmd) runRenameIdent(noun string, kind patchgen.DeclKind, opts *renameOptions, args []string) error {
	target, err := refactor.ParseTarget(noun, args[0], 1)
	if err != nil {
		return err
	}
	to, patterns := args[1], args[2:]

	pkg, importPath, err := cmd.identScope(noun, target, opts)
	if err != nil {
		return err
	}

	if pkg == "" {
		// Nothing named the declaring package, so read it out of the target
		// files, narrowed to the path the target named if it named one.
		search, err := cmd.targetSearch(noun, target, patterns)
		if err != nil {
			return err
		}
		if pkg, err = refactor.InferPackage(noun, search.Files, target); err != nil {
			return err
		}
	}

	return cmd.runOperation(patchgen.RenameIdent{
		Kind: kind,
		From: target.Name(),
		To:   to,
		Scope: patchgen.Scope{
			Package:    pkg,
			ImportPath: importPath,
			Qualifier:  opts.Qualifier,
		},
	}, &opts.opOptions, patterns)
}

// identScope merges a qualified target with the flags that name the same scope.
//
// An import-path qualifier does the work of both --package and --import: naming
// a package by path says that is the package's identity everywhere, so consumers
// move with the declaration. A bare package name scopes the change to the
// declaring package only, as --package does.
func (cmd *mainCmd) identScope(arg string, t refactor.Target, opts *renameOptions) (pkg, importPath string, err error) {
	switch {
	case t.Qualifier != "" && opts.Package != "":
		return "", "", fmt.Errorf(
			"%s is already qualified with %q, so --package %q is ambiguous; drop one",
			arg, t.Qualifier, opts.Package)
	case t.ImportPath() != "" && opts.ImportPath != "":
		return "", "", fmt.Errorf(
			"%s already names the import path %q, so --import %q is ambiguous; drop one",
			arg, t.ImportPath(), opts.ImportPath)
	}

	if pkg, err = t.PackageName(arg); err != nil {
		return "", "", err
	}
	if pkg == "" {
		pkg = opts.Package
	}

	importPath = t.ImportPath()
	if importPath == "" {
		importPath = opts.ImportPath
	}
	return pkg, importPath, nil
}

func (cmd *mainCmd) newRenamePackageCommand() *cobra.Command {
	var opts opOptions

	c := &cobra.Command{
		Use:   "package <old> <new> [pattern...]",
		Short: "Rename a package clause.",
		Long: "Rename a package clause.\n\n" +
			"This changes the `package` line only. Packages that import this one " +
			"keep working, because their qualifier follows the import path; use " +
			"`gopatch rename import` to change that.",
		Example: examples(
			"# rewrite \"package http\" to \"package httpclient\" in every file there",
			"gopatch rename package http httpclient ./http",
			"",
			"# the directory and import path are separate; change them too",
			"git mv http httpclient",
			"gopatch rename import example.com/http example.com/httpclient ./...",
		),
		Args: cobra.MinimumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			op := patchgen.RenamePackage{From: args[0], To: args[1]}
			return cmd.runOperation(op, &opts, args[2:])
		},
	}

	addOpFlags(c.Flags(), &opts)

	return c
}

func (cmd *mainCmd) newRenameImportCommand() *cobra.Command {
	var (
		opts     opOptions
		fromName string
		toName   string
	)

	c := &cobra.Command{
		Use:     "import <old-path> <new-path> [pattern...]",
		Aliases: []string{"import-path"},
		Short:   "Rewrite an import path, and the qualifier when it changes with it.",
		Long: "Rewrite an import path.\n\n" +
			"When the package name implied by the new path differs from the old " +
			"one, every qualified reference moves with it. When the name is " +
			"unchanged, only the import line changes.\n\n" +
			"Imports left unused by the rewrite are removed automatically.",
		Example: examples(
			"# same package name at both paths: only the import line changes",
			"gopatch rename import example.com/old/http example.com/new/http ./...",
			"",
			"# different name: every http.X becomes client.X as well",
			"gopatch rename import example.com/http example.com/client ./...",
			"",
			"# spell the names out when a path's last segment is not the package name",
			"gopatch rename import example.com/go-yaml gopkg.in/yaml.v3 --name yaml --to-name yaml ./...",
		),
		Args: cobra.MinimumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			op := patchgen.RenameImport{
				FromPath: args[0],
				ToPath:   args[1],
				FromName: fromName,
				ToName:   toName,
			}
			return cmd.runOperation(op, &opts, args[2:])
		},
	}

	addOpFlags(c.Flags(), &opts)
	c.Flags().StringVar(&fromName, "name", "",
		"Package name the importing files currently use. "+
			"Derived from the old path when omitted.")
	c.Flags().StringVar(&toName, "to-name", "",
		"Package name to use after the rewrite. Derived from the new path when omitted.")

	return c
}
