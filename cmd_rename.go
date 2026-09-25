package main

import (
	"github.com/spf13/cobra"
	"github.com/uber-go/gopatch/internal/refactor"
	"github.com/uber-go/gopatch/internal/refactor/typed"
)

func (cmd *mainCmd) newRenameCommand() *cobra.Command {
	rename := &cobra.Command{
		Use:   "rename",
		Short: "Rename a package, import path, type, function, field, or method.",
		Long: "Rename a package, import path, type, function, field, or method.\n\n" +
			"Every rename uses Go type information, updates exact references across the " +
			"module and selected consumers, and requires the affected modules to compile." +
			syntaxNote(_synName, _synMember, _synPkgQual, _synDirQual, _synNewName, _synPattern),
		Example: examples(
			"# exact typed renames across the module and selected consumers",
			"gopatch rename struct  HTTPClient      Client              ./...",
			"gopatch rename type    Duration        Interval            ./...",
			"gopatch rename func    doWork          Work                ./...",
			"gopatch rename package http            httpclient          ./http",
			"gopatch rename import  example.com/old example.com/new     ./...",
			"",
			"# members are resolved against their declaring type",
			"gopatch rename field   User.Name       Title               ./...",
			"gopatch rename method  '*Client.Do'    Send                ./...",
		),
	}

	rename.AddCommand(
		cmd.newRenameIdentCommand("struct", typed.KindType, true,
			"Rename a struct and every reference to it.",
			examples(
				"# the declaration and every reference in the declaring package",
				"gopatch rename struct HTTPClient Client ./http",
				"",
				"# also rewrite consumers found under sibling repositories",
				"gopatch rename struct HTTPClient Client --consumers ../services ./...",
				"",
				"# when two packages declare the name, say which one you mean",
				"gopatch rename struct Config Options --package server ./...",
				"gopatch rename struct server.Config Options ./...",
				"gopatch rename struct ./internal/server:Config Options ./...",
				"",
				"# an import path qualifier selects the declaring package exactly",
				"gopatch rename struct example.com/http.HTTPClient Client ./...",
			)),
		cmd.newRenameIdentCommand("type", typed.KindType, false,
			"Rename a named type and every reference to it.",
			examples(
				"# any named type: an interface, an alias target, a defined type",
				"gopatch rename type Duration Interval ./...",
				"",
				"# preview declarations and consumers without writing",
				"gopatch rename type Duration Interval --diff ./...",
			)),
		cmd.newRenameIdentCommand("func", typed.KindFunc, false,
			"Rename a function and every reference to it.",
			examples(
				"# the declaration, every call, and every use as a value",
				"gopatch rename func doWork Work ./...",
				"",
				"# explicit import aliases remain unchanged in consumers",
				"gopatch rename func New NewClient --consumers ../services ./...",
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
func (cmd *mainCmd) newRenameIdentCommand(noun string, kind typed.Kind, structOnly bool, short, example string) *cobra.Command {
	var opts typedRenameOptions

	c := &cobra.Command{
		Use:   noun + " <old> <new> [pattern...]",
		Short: short,
		Long: short + "\n\n" +
			"Rewrites the exact declaration and every typed reference in the module, " +
			"active Go workspace, and configured consumer modules." + _typedLongSuffix,
		Example: example,
		Args:    cobra.MinimumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			return cmd.runRenameIdent(noun, kind, structOnly, &opts, args)
		},
	}

	addTypedRenameFlags(c.Flags(), &opts)

	return c
}

func (cmd *mainCmd) runRenameIdent(noun string, kind typed.Kind, structOnly bool, opts *typedRenameOptions, args []string) error {
	target, err := refactor.ParseTarget(noun, args[0], 1)
	if err != nil {
		return err
	}
	to, err := refactor.ParseIdent("new name", args[1])
	if err != nil {
		return err
	}
	query, err := cmd.query(noun, kind, target, &opts.typedOptions)
	if err != nil {
		return err
	}
	return cmd.runTypedRename(typedRenameRequest{
		Options: opts, Patterns: args[2:], Query: query, To: to, StructOnly: structOnly,
	})
}
