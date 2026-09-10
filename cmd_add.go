package main

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/uber-go/gopatch/internal/refactor"
	"github.com/uber-go/gopatch/internal/refactor/patchgen"
	"github.com/uber-go/gopatch/internal/refactor/typed"
)

func (cmd *mainCmd) newAddCommand() *cobra.Command {
	add := &cobra.Command{
		Use:   "add",
		Short: "Add a struct field, a parameter, or a new declaration.",
		Long: "Add a struct field, a function parameter, or a whole new declaration.\n\n" +
			"`field` and `param` change an existing declaration and generate a patch to " +
			"do it. `struct`, `func` and `method` create a declaration, which a patch " +
			"cannot do — it has nothing to match — so they append to a named file using " +
			"type information. Their name is never qualified: the file already says " +
			"which package the declaration goes in." +
			syntaxNote(_synName, _synMember, _synPkgQual, _synDirQual, _synFile,
				_synPattern, _synType, _synParams, _synResults, _synTag),
		Example: examples(
			"# change an existing declaration, over a set of paths",
			"gopatch add field User.Nickname string --tag 'json:\"nickname\"' ./...",
			"gopatch add param Send ctx context.Context --import context ./...",
			"",
			"# create a declaration, appended to one named file",
			"gopatch add struct Options ./config/options.go",
			"gopatch add func   Load    ./config/options.go --params 'path string' --results '(*Options, error)'",
			"gopatch add method Options.Validate ./config/options.go --results error",
		),
	}

	add.AddCommand(
		cmd.newAddFieldCommand(),
		cmd.newAddParamCommand(),
		cmd.newAddStructCommand(),
		cmd.newAddFuncCommand(),
		cmd.newAddMethodCommand(),
	)

	return add
}

func (cmd *mainCmd) newAddFieldCommand() *cobra.Command {
	var (
		opts  opOptions
		tag   string
		first bool
	)

	c := &cobra.Command{
		Use:   "field <Struct>.<field> <type> [pattern...]",
		Short: "Add a field to a struct.",
		Long: "Add a field to a struct.\n\n" +
			"The type is any Go type expression. A qualified type such as time.Duration " +
			"needs the file to import that package already: this command changes the " +
			"struct, not the import block.",
		Example: examples(
			"# appended after the existing fields",
			"gopatch add field User.Nickname string ./...",
			"",
			"# quote the tag so the shell keeps its inner quotes",
			"gopatch add field User.Nickname string --tag 'json:\"nickname\"' ./...",
			"",
			"# any type expression; quote the ones a shell would mangle",
			"gopatch add field Client.Headers 'map[string][]string' ./...",
			"gopatch add field Client.Pool    '*sync.Pool'          ./...",
			"",
			"# insert before the existing fields instead",
			"gopatch add field User.ID string --first ./...",
			"",
			"# qualify the struct when more than one package declares it",
			"gopatch add field store.User.Nickname            string ./...",
			"gopatch add field ./internal/store:User.Nickname string ./...",
		),
		Args: cobra.MinimumNArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			target, err := refactor.ParseTarget("field", args[0], 2)
			if err != nil {
				return err
			}
			patterns := args[2:]

			parsedTag, err := refactor.ParseTag("--tag", tag)
			if err != nil {
				return err
			}

			// The struct itself has to be located even though nothing is read
			// from it, because the change is scoped to the declaring package.
			pkg, err := cmd.findStructPackage("field", target, patterns)
			if err != nil {
				return err
			}

			op := patchgen.AddField{Type: args[1], Tag: parsedTag}
			op.Struct, op.Field, op.Package = target.Owner(), target.Name(), pkg
			if first {
				op.Position = patchgen.PositionFirst
			}

			return cmd.runOperation(op, &opts, patterns)
		},
	}

	addOpFlags(c.Flags(), &opts)
	c.Flags().StringVar(&tag, "tag", "", `Struct tag for the new field, such as 'json:"id"'.`)
	c.Flags().BoolVar(&first, "first", false,
		"Insert the field before the existing ones instead of after them.")

	return c
}

func (cmd *mainCmd) newAddParamCommand() *cobra.Command {
	var (
		opts       opOptions
		importPath string
	)

	c := &cobra.Command{
		Use:   "param <func> <name> <type> [pattern...]",
		Short: "Add a leading parameter to a function.",
		Long: "Add a leading parameter to a top-level function.\n\n" +
			"The function's current signature is read from the target files and " +
			"reproduced exactly, so the rewrite is unambiguous. Pass an empty " +
			"name to add an unnamed parameter.\n\n" +
			"Call sites are NOT updated: gopatch cannot decide what to pass at " +
			"each one. Methods are not supported.",
		Example: examples(
			"# func Send(r *Request) error  ->  func Send(ctx context.Context, r *Request) error",
			"gopatch add param Send ctx context.Context --import context ./...",
			"",
			"# an unnamed parameter: pass an empty name",
			"gopatch add param Send '' context.Context --import context ./...",
			"",
			"# a type already in scope needs no --import",
			"gopatch add param Send n int ./...",
			"",
			"# qualify the function when more than one package declares it",
			"gopatch add param transport.Send            n int ./...",
			"gopatch add param ./internal/transport:Send n int ./...",
		),
		Args: cobra.MinimumNArgs(3),
		RunE: func(_ *cobra.Command, args []string) error {
			target, err := refactor.ParseTarget("func", args[0], 1)
			if err != nil {
				return err
			}
			name, paramName, paramType := target.Name(), args[1], args[2]
			patterns := args[3:]

			current, search, err := cmd.findSignature("func", target, patterns)
			if err != nil {
				return err
			}

			if importPath != "" {
				if err := search.RequireImportBlock(name, importPath); err != nil {
					return err
				}
			}

			op, err := patchgen.AddParam(name, current, paramName, paramType, importPath)
			if err != nil {
				return err
			}
			op.Package = search.Package

			return cmd.runOperation(op, &opts, patterns)
		},
	}

	addOpFlags(c.Flags(), &opts)
	c.Flags().StringVar(&importPath, "import", "",
		"Import path to add for the new parameter's type, such as \"context\".")

	return c
}

func (cmd *mainCmd) newAddStructCommand() *cobra.Command {
	var opts typedOptions

	c := &cobra.Command{
		Use:   "struct <Name> <file.go>",
		Short: "Append an empty struct declaration to a file.",
		Long: "Append an empty struct declaration to a file.\n\n" +
			"Use `gopatch add field` to fill it in. The name must not already be " +
			"declared in the package." + _typedLongSuffix,
		Example: examples(
			"# appends \"type Options struct {\\n}\" to the file",
			"gopatch add struct Options ./config/options.go",
			"",
			"# then fill it in",
			"gopatch add field Options.Timeout time.Duration ./config/options.go",
		),
		Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			target, err := refactor.ParseTarget("struct name", args[0], 1)
			if err != nil {
				return err
			}
			if err := requireUnqualified("struct name", target); err != nil {
				return err
			}

			return cmd.runAddDecl(&opts, typed.Declaration{
				File: args[1],
				Kind: typed.KindType,
				Name: target.Name(),
			}, args[1])
		},
	}

	addTypedFlags(c.Flags(), &opts)

	return c
}

func (cmd *mainCmd) newAddFuncCommand() *cobra.Command {
	var (
		opts typedOptions
		sig  funcFlags
	)

	c := &cobra.Command{
		Use:   "func <name> <file.go>",
		Short: "Append a function stub to a file.",
		Long: "Append a function stub to a file.\n\n" +
			`The body is panic("not implemented"), so the file still compiles whatever ` +
			"the signature declares." + _typedLongSuffix,
		Example: examples(
			"# func Load() { panic(\"not implemented\") }",
			"gopatch add func Load ./config/options.go",
			"",
			"# func Load(path string) (*Options, error)",
			"gopatch add func Load ./config/options.go --params 'path string' --results '(*Options, error)'",
			"",
			"# a type from another package needs its import path",
			"gopatch add func Load ./config/options.go --params 'ctx context.Context' --import context",
			"",
			"# name=path adds the import under that name, for a package whose name",
			"# is not the last segment of its path",
			"gopatch add func Load ./config/options.go --params 'y *yaml.Node' --import yaml=gopkg.in/yaml.v3",
		),
		Args: cobra.ExactArgs(2),
		RunE: func(_ *cobra.Command, args []string) error {
			target, err := refactor.ParseTarget("func name", args[0], 1)
			if err != nil {
				return err
			}
			if err := requireUnqualified("func name", target); err != nil {
				return err
			}
			params, results, imports, err := sig.parse()
			if err != nil {
				return err
			}

			return cmd.runAddDecl(&opts, typed.Declaration{
				File:    args[1],
				Kind:    typed.KindFunc,
				Name:    target.Name(),
				Params:  params,
				Results: results,
				Imports: imports,
			}, args[1])
		},
	}

	addTypedFlags(c.Flags(), &opts)
	addSignatureFlags(c.Flags(), &sig)
	addImportFlag(c.Flags(), &sig)

	return c
}

func (cmd *mainCmd) newAddMethodCommand() *cobra.Command {
	var (
		opts typedOptions
		sig  funcFlags
		recv string
	)

	c := &cobra.Command{
		Use:   "method <Recv>.<name> <file.go>",
		Short: "Append a method stub to a file.",
		Long: "Append a method stub to a file.\n\n" +
			`The body is panic("not implemented"). The receiver is "r *Recv" by ` +
			"default; pass --recv to name it or to take the receiver by value." +
			_typedLongSuffix,
		Example: examples(
			"# func (r *Options) Validate() error",
			"gopatch add method Options.Validate ./config/options.go --results error",
			"",
			"# name the receiver, or take it by value",
			"gopatch add method Options.String ./config/options.go --recv 'o Options' --results string",
			"",
			"# repeat --import for each package the signature refers to",
			"gopatch add method Options.Fetch ./config/options.go --params 'ctx context.Context, u *url.URL' --import context --import net/url",
		),
		Args: cobra.ExactArgs(2),
		RunE: func(c *cobra.Command, args []string) error {
			target, err := refactor.ParseTarget("method", args[0], 2)
			if err != nil {
				return err
			}
			if err := requireUnqualified("method", target); err != nil {
				return err
			}
			owner, name := target.Owner(), target.Name()

			receiver := recv
			if !c.Flags().Changed("recv") {
				receiver = "r *" + owner
			}
			params, results, imports, err := sig.parse()
			if err != nil {
				return err
			}

			return cmd.runAddDecl(&opts, typed.Declaration{
				File:    args[1],
				Kind:    typed.KindMethod,
				Name:    name,
				Recv:    receiver,
				Params:  params,
				Results: results,
				Imports: imports,
			}, args[1])
		},
	}

	addTypedFlags(c.Flags(), &opts)
	addSignatureFlags(c.Flags(), &sig)
	addImportFlag(c.Flags(), &sig)
	c.Flags().StringVar(&recv, "recv", "",
		`Receiver declaration, such as "u User" for a value receiver.`)

	return c
}

// runAddDecl appends one declaration to a file.
//
// The file is resolved against the command's working directory rather than the
// process's, because that is what the rest of the CLI uses and what the tests
// inject.
func (cmd *mainCmd) runAddDecl(opts *typedOptions, decl typed.Declaration, file string) error {
	cwd, err := cmd.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	if !filepath.IsAbs(decl.File) {
		decl.File = filepath.Join(cwd, decl.File)
	}

	return cmd.runTyped(typedRequest{
		Options:  opts,
		Patterns: []string{file},
		Run: func(p *typed.Program) (string, error) {
			if _, err := p.Add(decl); err != nil {
				return "", err
			}
			return fmt.Sprintf("add %v %v to %v", decl.Kind, decl.Name, file), nil
		},
	})
}
