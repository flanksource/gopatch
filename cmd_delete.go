package main

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/uber-go/gopatch/internal/refactor"
	"github.com/uber-go/gopatch/internal/refactor/patchgen"
	"github.com/uber-go/gopatch/internal/refactor/typed"
)

func (cmd *mainCmd) newDeleteCommand() *cobra.Command {
	del := &cobra.Command{
		Use:   "delete",
		Short: "Delete a struct field, a type, a function, or a method.",
		Long: "Delete a struct field, a type, a function, or a method.\n\n" +
			"`field` generates a patch and works on code that does not compile. The " +
			"others remove a whole declaration, which a patch cannot express — its " +
			"replacement section may not be empty — so they use type information and " +
			"refuse when anything still refers to what you asked to delete." +
			syntaxNote(_synName, _synMember, _synPkgQual, _synDirQual, _synPattern),
		Example: examples(
			"# syntactic: works on code that does not compile",
			"gopatch delete field  User.Nickname     ./...",
			"",
			"# type-aware: refuses while anything still refers to the declaration",
			"gopatch delete struct Options           ./...",
			"gopatch delete type   Interval          ./...",
			"gopatch delete func   unusedHelper      ./...",
			"gopatch delete method Options.Validate  ./...",
		),
	}

	del.AddCommand(
		cmd.newDeleteFieldCommand(),
		cmd.newDeleteDeclCommand("struct", typed.KindType,
			"Delete a struct declaration that nothing refers to.",
			examples(
				"# removes the type and its doc comment",
				"gopatch delete struct Options ./...",
				"",
				"# one spec out of a \"type ( ... )\" block leaves the block behind",
				"gopatch delete struct Options --package config ./...",
				"",
				"# qualify the name instead of passing --package",
				"gopatch delete struct config.Options ./...",
				"gopatch delete struct ./internal/config:Options ./...",
			)),
		cmd.newDeleteDeclCommand("type", typed.KindType,
			"Delete a named type declaration that nothing refers to.",
			examples(
				"# any named type: an interface, an alias, a defined type",
				"gopatch delete type Interval ./...",
				"",
				"# see what it would remove before it does",
				"gopatch delete type Interval -d ./...",
			)),
		cmd.newDeleteDeclCommand("func", typed.KindFunc,
			"Delete a function that nothing calls.",
			examples(
				"# a function that calls only itself still counts as unused",
				"gopatch delete func unusedHelper ./...",
				"",
				"# an import left with no other user goes with it",
				"gopatch delete func unusedHelper -d ./...",
			)),
		cmd.newDeleteMethodCommand(),
	)

	return del
}

func (cmd *mainCmd) newDeleteFieldCommand() *cobra.Command {
	var (
		opts  opOptions
		scope literalScopeOptions
	)

	c := &cobra.Command{
		Use:   "field <Struct>.<field> [pattern...]",
		Short: "Delete a struct field and drop it from composite literals.",
		Long: "Delete a struct field.\n\n" +
			"Removes the field's declaration and its key from keyed composite " +
			"literals of that struct. Both are scoped to the named struct, so a " +
			"same-named field on another type is left alone.\n\n" +
			"Code that reads the field is not changed and will stop compiling; " +
			"that is deliberate, so the remaining uses are easy to find.",
		Example: examples(
			"# drops the declaration and the key from User{...} literals",
			"gopatch delete field User.Nickname ./...",
			"",
			"# also drop the key in packages that import the struct",
			"gopatch delete field User.Nickname --import example.com/store ./...",
			"",
			"# see the generated patch first",
			"gopatch delete field User.Nickname --print-patch ./...",
			"",
			"# qualify the struct when more than one package declares it",
			"gopatch delete field store.User.Nickname ./...",
			"gopatch delete field ./internal/store:User.Nickname ./...",
		),
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			target, err := refactor.ParseTarget("field", args[0], 2)
			if err != nil {
				return err
			}
			patterns := args[1:]

			// The declaration is reproduced exactly, tag included: a struct tag
			// cannot be matched by a wildcard, so omitting it would produce a
			// patch that silently matches nothing.
			found, pkg, err := cmd.findField("field", target, patterns)
			if err != nil {
				return err
			}

			op := patchgen.DeleteField{
				Type:       found.Type,
				Tag:        found.Tag,
				ImportPath: scope.ImportPath,
				Qualifier:  scope.Qualifier,
			}
			op.Struct, op.Field, op.Package = target.Owner(), target.Name(), pkg

			return cmd.runOperation(op, &opts, patterns)
		},
	}

	addOpFlags(c.Flags(), &opts)
	addLiteralScopeFlags(c.Flags(), &scope)

	return c
}

// newDeleteDeclCommand builds `gopatch delete <noun> <name> [paths...]`.
//
// A struct, an interface and any other named type are one declaration to
// go/types, so the nouns share an implementation; the noun only sharpens the
// help text.
func (cmd *mainCmd) newDeleteDeclCommand(noun string, kind typed.Kind, short, example string) *cobra.Command {
	var opts typedOptions

	c := &cobra.Command{
		Use:   noun + " <name> [pattern...]",
		Short: short,
		Long: short + "\n\n" +
			"The declaration goes with its doc comment, and an import left with no " +
			"other user goes with it. Anything that still refers to the declaration " +
			"is reported instead, with the positions, and nothing is written." +
			_typedLongSuffix,
		Example: example,
		Args:    cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return cmd.runDeleteSymbol(noun, kind, 1, &opts, args)
		},
	}

	addTypedFlags(c.Flags(), &opts)

	return c
}

func (cmd *mainCmd) newDeleteMethodCommand() *cobra.Command {
	var opts typedOptions

	c := &cobra.Command{
		Use:   "method <Recv>.<method> [pattern...]",
		Short: "Delete a method that nothing calls.",
		Long: "Delete a method.\n\n" +
			"The declaration goes with its doc comment. A method that is still called, " +
			"including through an interface it implements, is reported instead." +
			_typedLongSuffix,
		Example: examples(
			"gopatch delete method Options.Validate ./...",
			"",
			"# a pointer receiver names the same method; quote it for the shell",
			"gopatch delete method '*Client.close' ./...",
			"",
			"# qualify the receiver when more than one package declares it",
			"gopatch delete method config.Options.Validate ./...",
			"gopatch delete method ./internal/config:Options.Validate ./...",
		),
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return cmd.runDeleteSymbol("method", typed.KindMethod, 2, &opts, args)
		},
	}

	addTypedFlags(c.Flags(), &opts)

	return c
}

// runDeleteSymbol resolves a declaration and removes it.
func (cmd *mainCmd) runDeleteSymbol(
	arg string, kind typed.Kind, names int, opts *typedOptions, args []string,
) error {
	target, err := refactor.ParseTarget(arg, args[0], names)
	if err != nil {
		return err
	}

	q, err := cmd.query(arg, kind, target, opts)
	if err != nil {
		return err
	}

	return cmd.runTyped(typedRequest{
		Options:  opts,
		Patterns: args[1:],
		Run: func(p *typed.Program) (string, error) {
			sym, err := p.Resolve(q)
			if err != nil {
				return "", err
			}

			files, err := p.Delete(sym)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("delete %v from %v (%d file(s))", sym.Desc, sym.Pkg, files), nil
		},
	})
}
