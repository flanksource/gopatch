package main

import (
	"github.com/spf13/cobra"
	"github.com/uber-go/gopatch/internal/refactor"
	"github.com/uber-go/gopatch/internal/refactor/typed"
)

func (cmd *mainCmd) newReplaceCommand() *cobra.Command {
	replace := &cobra.Command{
		Use:   "replace",
		Short: "Replace text inside a function or method body.",
		Long: "Replace every match of a regular expression inside one function's body.\n\n" +
			"This is the escape hatch for a change no pattern over the syntax tree " +
			"expresses well — a log call's arguments, a string literal, a comment. " +
			"It is a textual substitution, but a scoped one: the body is found with " +
			"type information, so the expression never reaches another function, " +
			"another type's method, or a same-named declaration in another package.\n\n" +
			"The result is parsed as Go before anything is written, so a substitution " +
			"that produces something that is not a body is refused rather than " +
			"leaving the file broken. A pattern that matches nothing is an error too.\n\n" +
			"To replace a whole body rather than part of one, use " +
			"`gopatch edit func --content` or `gopatch edit method --content`." +
			syntaxNote(_synName, _synMember, _synPkgQual, _synDirQual, _synPattern,
				_synMatch, _synReplacement) + _typedLongSuffix,
		Example: examples(
			"# rewrite a call, keeping its arguments",
			"gopatch replace func   Send      --match 'log\\.Printf\\((.*)\\)' --content 'slog.Info($1)' --import log/slog ./...",
			"",
			"# a plain string swap needs no capture group",
			"gopatch replace method Client.Do --match 'http://' --content 'https://' ./...",
			"",
			"# delete every match",
			"gopatch replace func   Send      --match '(?m)^\\s*// TODO.*$' --content '' ./...",
		),
	}

	replace.AddCommand(
		cmd.newReplaceFuncCommand(),
		cmd.newReplaceMethodCommand(),
	)

	return replace
}

func (cmd *mainCmd) newReplaceFuncCommand() *cobra.Command {
	var (
		opts typedOptions
		f    funcFlags
	)

	c := &cobra.Command{
		Use:   "func <name> [pattern...]",
		Short: "Replace text inside a function's body.",
		Long: "Replace every match of --match inside a top-level function's body with " +
			"--content.\n\n" +
			"The expression sees the body's source as written, comments included, " +
			"between the braces and not including them. Nothing outside that one " +
			"function is read or changed." + _typedLongSuffix,
		Example: examples(
			"# $1 is the first capture group",
			"gopatch replace func Send --match 'log\\.Printf\\((.*)\\)' --content 'slog.Info($1)' --import log/slog ./...",
			"",
			"# read the replacement from a file",
			"gopatch replace func Send --match 'panic\\(.*\\)' --content @hack/handle.go ./...",
			"",
			"# \"^\" and \"$\" anchor the whole body; (?m) makes them anchor a line",
			"gopatch replace func Send --match '(?m)^\\s*// TODO.*$' --content '' ./...",
			"",
			"# qualify the name when more than one package declares it",
			"gopatch replace func transport.Send --match 'v1' --content 'v2' ./...",
		),
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			target, err := refactor.ParseTarget("func", args[0], 1)
			if err != nil {
				return err
			}
			return cmd.runSubstitute("func", typed.KindFunc, target, f, &opts, args[1:])
		},
	}

	addTypedFlags(c.Flags(), &opts)
	addMatchFlag(c.Flags(), &f)
	addContentFlag(c.Flags(), &f, _replaceContentDesc)
	addImportFlag(c.Flags(), &f)

	return c
}

func (cmd *mainCmd) newReplaceMethodCommand() *cobra.Command {
	var (
		opts typedOptions
		f    funcFlags
	)

	c := &cobra.Command{
		Use:   "method <Recv>.<method> [pattern...]",
		Short: "Replace text inside a method's body.",
		Long: "Replace every match of --match inside a method's body with --content.\n\n" +
			"The method is resolved by type, so a same-named method on another type " +
			"is left alone — which is the whole reason this is not a `sed` " +
			"invocation." + _typedLongSuffix,
		Example: examples(
			"gopatch replace method Client.Do --match 'http://' --content 'https://' ./...",
			"",
			"# a pointer receiver names the same method; quote it for the shell",
			"gopatch replace method '*Client.Do' --match 'ctx' --content 'r.Context()' ./...",
			"",
			"# qualify the receiver when more than one package declares it",
			"gopatch replace method store.Client.Do --match 'v1' --content 'v2' ./...",
		),
		Args: cobra.MinimumNArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			target, err := refactor.ParseTarget("method", args[0], 2)
			if err != nil {
				return err
			}
			return cmd.runSubstitute("method", typed.KindMethod, target, f, &opts, args[1:])
		},
	}

	addTypedFlags(c.Flags(), &opts)
	addMatchFlag(c.Flags(), &f)
	addContentFlag(c.Flags(), &f, _replaceContentDesc)
	addImportFlag(c.Flags(), &f)

	return c
}
