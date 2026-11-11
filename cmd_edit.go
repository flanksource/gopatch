package main

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/uber-go/gopatch/internal/refactor"
	"github.com/uber-go/gopatch/internal/refactor/patchgen"
	"github.com/uber-go/gopatch/internal/refactor/typed"
)

func (cmd *mainCmd) newEditCommand() *cobra.Command {
	edit := &cobra.Command{
		Use:   "edit",
		Short: "Change a struct field's type or tag, or a function's signature or body.",
		Long: "Change a struct field's type or tag, or a function or method's signature " +
			"or body.\n\n" +
			"There is no `edit struct`: renaming one is `gopatch rename struct`, " +
			"and changing its shape is `gopatch add field`, `gopatch delete field`, " +
			"or `gopatch edit field`.\n\n" +
			"A signature change updates neither call sites nor bodies: there is no way " +
			"to decide what to pass at a call that gains an argument, or what to " +
			"return from a body that gains a result. --content is how you write the " +
			"body afterwards; `gopatch replace` changes part of one instead of all of " +
			"it." +
			syntaxNote(_synName, _synMember, _synPkgQual, _synDirQual, _synPattern, _synType,
				_synParams, _synResults, _synTag, _synContent),
		Example: examples(
			"# a struct field's type or tag",
			"gopatch edit field  User.ID    --type UUID                  ./...",
			"gopatch edit field  User.Email --tag 'json:\"email\"'         ./...",
			"",
			"# a signature; the half you do not give is kept as it is",
			"gopatch edit func   Send      --params '(ctx context.Context, r *Request)' --import context ./...",
			"gopatch edit method Client.Do --results '(*Response, error)' ./...",
			"",
			"# a whole body, given inline or read from a file",
			"gopatch edit func   Send      --content 'return nil' ./...",
			"gopatch edit method Client.Do --content @hack/do.go   ./...",
		),
	}

	edit.AddCommand(
		cmd.newEditFieldCommand(),
		cmd.newEditFuncCommand(),
		cmd.newEditMethodCommand(),
	)

	return edit
}

func (cmd *mainCmd) newEditFieldCommand() *cobra.Command {
	var (
		opts    opOptions
		newType string
		tag     string
	)

	c := &cobra.Command{
		Use:   "field <Struct>.<field> [pattern...]",
		Short: "Change a struct field's type or struct tag.",
		Long: "Change a struct field's type or struct tag.\n\n" +
			"Pass --type to retype the field, --tag to set its struct tag, or " +
			`--tag "" to clear it. The field's current type and tag are read from ` +
			"the target files, because a struct tag cannot be matched by a " +
			"wildcard and has to be reproduced exactly.",
		Example: examples(
			"# retype the field; any Go type expression works",
			"gopatch edit field User.ID    --type UUID          ./...",
			"gopatch edit field User.Tags  --type '[]string'    ./...",
			"",
			"# set the struct tag (without backquotes), or clear it",
			"gopatch edit field User.Email --tag 'json:\"email,omitempty\"' ./...",
			"gopatch edit field User.Email --tag '' ./...",
			"",
			"# qualify the struct when more than one package declares it",
			"gopatch edit field store.User.ID            --type UUID ./...",
			"gopatch edit field ./internal/store:User.ID --type UUID ./...",
			"",
			"# --type and --tag change the same line, so run them one at a time",
		),
		Args: cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			target, err := refactor.ParseTarget("field", args[0], 2)
			if err != nil {
				return err
			}
			patterns := args[1:]

			typeGiven := c.Flags().Changed("type")
			tagGiven := c.Flags().Changed("tag")
			switch {
			case !typeGiven && !tagGiven:
				return errors.New("nothing to change: pass --type or --tag")
			case typeGiven && tagGiven:
				// Both would need one change to match the field twice, which
				// the engine does not do; two runs are unambiguous.
				return errors.New("--type and --tag change the same declaration; run them one at a time")
			}

			// The field's current type and tag have to be exact: a struct tag is
			// a string literal in the grammar, so it cannot be matched by a
			// wildcard, and omitting it would silently match nothing.
			found, pkg, err := cmd.findField("field", target, patterns)
			if err != nil {
				return err
			}

			if typeGiven {
				op := patchgen.EditFieldType{
					NewType: newType,
					OldType: found.Type,
					Tag:     found.Tag,
				}
				op.Struct, op.Field, op.Package = target.Owner(), target.Name(), pkg
				return cmd.runOperation(op, &opts, patterns)
			}

			parsedTag, err := refactor.ParseTag("--tag", tag)
			if err != nil {
				return err
			}

			op := patchgen.EditFieldTag{Tag: parsedTag, OldTag: found.Tag, Type: found.Type}
			op.Struct, op.Field, op.Package = target.Owner(), target.Name(), pkg
			return cmd.runOperation(op, &opts, patterns)
		},
	}

	addOpFlags(c.Flags(), &opts)
	c.Flags().StringVar(&newType, "type", "", "New type for the field.")
	c.Flags().StringVar(&tag, "tag", "",
		`New struct tag for the field, such as 'json:"id"'. Pass "" to clear it.`)

	return c
}

func (cmd *mainCmd) newEditFuncCommand() *cobra.Command {
	var (
		opts opOptions
		sig  funcFlags
	)

	c := &cobra.Command{
		Use:   "func <name> [pattern...]",
		Short: "Change a function's signature or body.",
		Long: "Change a top-level function's signature or replace its body.\n\n" +
			"Pass --params, --results, or both. The parts you do not give are " +
			"kept as they are, read from the target files, so the rewrite " +
			"reproduces the signature exactly.\n\n" +
			"Pass --content to replace the body instead. That needs type " +
			"information to find the function exactly, so it runs the type-aware " +
			"engine and requires the module to compile; --params and --results do " +
			"not, and generate a patch. The two therefore cannot be combined — run " +
			"them one at a time.\n\n" +
			"Call sites are NOT updated: gopatch cannot decide what to pass at " +
			"each one. Methods are not supported — use `gopatch edit method`.",
		Example: examples(
			"# replace the parameter list, keeping the results",
			"gopatch edit func Send --params 'ctx context.Context, r *Request' --import context ./...",
			"",
			"# replace the results, keeping the parameters",
			"gopatch edit func Send --results '(*Response, error)' ./...",
			"",
			"# parentheses are optional on --params, and an empty value drops them all",
			"gopatch edit func Send --params '(n int)' ./...",
			"gopatch edit func Send --params '' ./...",
			"",
			"# replace the whole body; --content takes Go statements",
			"gopatch edit func Send --content 'return errors.New(\"not implemented\")' --import errors ./...",
			"gopatch edit func Send --content @hack/send.go ./...",
			"gopatch edit func Send --content '' ./...",
			"",
			"# qualify the name when more than one package declares it",
			"gopatch edit func transport.Send            --params '' ./...",
			"gopatch edit func ./internal/transport:Send --params '' ./...",
		),
		Args: cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			target, err := refactor.ParseTarget("func", args[0], 1)
			if err != nil {
				return err
			}
			name, patterns := target.Name(), args[1:]

			contentGiven := c.Flags().Changed("content")
			signatureGiven := c.Flags().Changed("params") || c.Flags().Changed("results")
			switch {
			case !contentGiven && !signatureGiven:
				return errors.New("nothing to change: pass --params, --results, or --content")
			case contentGiven && signatureGiven:
				// One invocation, one engine: composing a patch and a typed
				// rewrite of the same declaration would have to order them, and
				// after either the other's view of the file is stale.
				return errors.New(
					"--content replaces the body with type information while --params and " +
						"--results generate a patch; run them one at a time")
			case contentGiven:
				if err := refusePatchFlags(c, "--content"); err != nil {
					return err
				}
				// The patch-only flags aside, a body change needs exactly what a
				// typed command needs; the target carries the package.
				return cmd.runSetBody("func", typed.KindFunc, target, sig,
					&typedOptions{apply: opts.apply}, patterns)
			}

			current, search, err := cmd.findSignature("func", target, patterns)
			if err != nil {
				return err
			}

			importPath, err := singleImport(sig.Imports)
			if err != nil {
				return err
			}
			if importPath != "" {
				if err := search.RequireImportBlock(name, importPath); err != nil {
					return err
				}
			}

			want := current
			if c.Flags().Changed("params") {
				if want.Params, err = refactor.ParseFieldList("--params", sig.Params); err != nil {
					return err
				}
			}
			if c.Flags().Changed("results") {
				if want.Results, _, _, err = refactor.ParseResults("--results", sig.Results); err != nil {
					return err
				}
			}

			return cmd.runOperation(patchgen.SetSignature{
				Func:    name,
				Package: search.Package,
				From:    current,
				To:      want,
				Import:  importPath,
				Summary: fmt.Sprintf("change the signature of func %v from %v to %v",
					name, current, want),
			}, &opts, patterns)
		},
	}

	addOpFlags(c.Flags(), &opts)
	addSignatureFlags(c.Flags(), &sig)
	addImportFlag(c.Flags(), &sig)
	addContentFlag(c.Flags(), &sig, _bodyContentDesc)

	return c
}

// singleImport narrows repeated --import values to the one a generated patch can
// add.
//
// gopatch adds imports from a change's plus section, and a signature change is
// one change, so it can introduce one import. Refusing the second is better than
// dropping it silently and emitting code that does not compile.
func singleImport(values []string) (string, error) {
	if len(values) > 1 {
		return "", fmt.Errorf(
			"a generated patch can add one import, but %d were given (%v); "+
				"add the rest with a second run, or use --content, which is type-aware",
			len(values), strings.Join(values, ", "))
	}
	if len(values) == 0 {
		return "", nil
	}

	if _, alias, ok := strings.Cut(values[0], "="); ok {
		return "", fmt.Errorf(
			"--import %q names an alias, which a generated patch cannot add; "+
				"pass the path alone (--import %v)", values[0], alias)
	}
	return refactor.ParseImportPath("--import", values[0])
}

func (cmd *mainCmd) newEditMethodCommand() *cobra.Command {
	var (
		opts typedOptions
		sig  funcFlags
	)

	c := &cobra.Command{
		Use:   "method <Recv>.<method> [pattern...]",
		Short: "Change a method's signature or body.",
		Long: "Change a method's signature, or replace its body.\n\n" +
			"Pass --params, --results, or both; the part you do not give is kept. " +
			"A patch cannot do this — its receiver and parameter elisions " +
			"cross-connect and produce nonsense — so this command resolves the " +
			"method with type information instead.\n\n" +
			"Pass --content to replace the body. A signature change leaves the body " +
			"alone, so adding a result leaves the method without a return statement " +
			"for you to write; --content is how you write it. The two are separate " +
			"runs, because each has to see the other's result.\n\n" +
			"Call sites are never updated." + _typedLongSuffix,
		Example: examples(
			"# add a parameter, keeping the results",
			"gopatch edit method Client.Do --params 'ctx context.Context, r *Request' --import context ./...",
			"",
			"# change the results, keeping the parameters",
			"gopatch edit method Client.Do --results '(*Response, error)' ./...",
			"",
			"# replace the whole body; --content takes Go statements",
			"gopatch edit method Client.Do --content 'return c.transport.RoundTrip(r)' ./...",
			"gopatch edit method Client.Do --content @hack/do.go --import net/http ./...",
			"",
			"# a pointer receiver names the same method; quote it for the shell",
			"gopatch edit method '*Client.Do' --params '' ./...",
			"",
			"# qualify the receiver when more than one package declares it",
			"gopatch edit method store.Client.Do               --params '' ./...",
			"gopatch edit method './internal/store:*Client.Do' --params '' ./...",
		),
		Args: cobra.MinimumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			target, err := refactor.ParseTarget("method", args[0], 2)
			if err != nil {
				return err
			}

			contentGiven := c.Flags().Changed("content")
			signatureGiven := c.Flags().Changed("params") || c.Flags().Changed("results")
			switch {
			case !contentGiven && !signatureGiven:
				return errors.New("nothing to change: pass --params, --results, or --content")
			case contentGiven && signatureGiven:
				// Both are the typed engine, so this could be done in one pass;
				// it is refused because the body a caller writes has to be the
				// one that matches the new signature, and they cannot see it
				// until the signature has changed.
				return errors.New(
					"--params and --results change the signature, --content changes the " +
						"body; run them one at a time, so the body you write is the one " +
						"the new signature needs")
			case contentGiven:
				return cmd.runSetBody("method", typed.KindMethod, target, sig, &opts, args[1:])
			}

			q, err := cmd.query("method", typed.KindMethod, target, &opts)
			if err != nil {
				return err
			}

			want, err := sig.parseChanges(c)
			if err != nil {
				return err
			}

			return cmd.runTyped(typedRequest{
				Options:  &opts,
				Patterns: args[1:],
				Run: func(p *typed.Program) (string, error) {
					sym, err := p.Resolve(q)
					if err != nil {
						return "", err
					}

					n, err := p.SetSignature(sym, want)
					if err != nil {
						return "", err
					}
					return fmt.Sprintf(
						"change the signature of %v in %v (%d declaration(s); "+
							"call sites and bodies are not updated)",
						sym.Desc, sym.Pkg, n), nil
				},
			})
		},
	}

	addTypedFlags(c.Flags(), &opts)
	addSignatureFlags(c.Flags(), &sig)
	addImportFlag(c.Flags(), &sig)
	addContentFlag(c.Flags(), &sig, _bodyContentDesc)

	return c
}
