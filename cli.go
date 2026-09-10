package main

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Flag and argument descriptions.
//
// These live as constants rather than inline so that TestRootCommandHelp can
// assert they are still reachable from the rendered help: nothing else in the
// test suite covers them, and a dropped usage string is otherwise invisible.
const (
	_versionDesc = "Display the version of gopatch."

	_verboseDesc = "Turn on verbose mode that prints whether or not the file was patched " +
		"for each file found."

	_patchDesc = "Path to a patch file specifying the code transformation. " +
		"Multiple patches may be provided to be applied in-order. " +
		"If the flag is omitted, a patch will be read from stdin."

	_patchesFileDesc = "File containing a list of paths to patch files. " +
		"Each file must be listed on its own line."

	_diffDesc = "Print a diff of the proposed changes to stdout but don't modify any files. " +
		"The description comment of each patch that matched is printed above its diff."

	_printOnlyDesc = "Print files to stdout without modifying them. " +
		"The description comment of each patch that matched is printed with them."

	_skipImportProcessingDesc = "Skips processing of imports."

	_skipGeneratedDesc = "Skips running on files with generated code."

	// Describes the positional arguments. cobra has no place to hang a
	// positional description, so this goes in the root command's Long.
	_patternDesc = "One or more files or directores containing Go code. " +
		"When directories are provided, all Go files in them and their " +
		"descendants will be transformed."
)

// The argument forms the refactoring subcommands share.
//
// A usage line can only show a shape like "<Struct>.<field>"; these say what
// goes in it, and which spellings a shell will mangle unless they are quoted.
// Each verb lists only the ones it accepts, via syntaxNote.
const (
	_synName   = `<Name>             a top-level declaration: HTTPClient, Duration, doWork`
	_synMember = `<Type>.<name>      a field or a method: User.Name, Client.Do, '*Client.Do'`

	_synPkgQual = `<pkg>.<target>     qualified by package name or import path:` + "\n" +
		`                     store.User.Name, example.com/store.User.Name`
	_synDirQual = `<./dir>:<target>   qualified by the file or directory it is in:` + "\n" +
		`                     ./internal/store:User.Name, user.go:User.Name`

	_synNewName = `<new>              the replacement name, a plain identifier: Title`
	_synPattern = `<pattern>...       files or directories to change: ./... or ./internal/http`
	_synFile    = `<file.go>          one Go file in this module: ./config/options.go`
	_synType    = `<type>             any Go type expression: string, *http.Client, 'map[string][]byte'`
	_synParams  = `--params '...'     a parameter list, parentheses optional: 'ctx context.Context, n int'`
	_synResults = `--results '...'    a result list, parenthesized where Go requires it: '(int, error)'`
	_synTag     = `--tag '...'        a struct tag without backquotes: 'json:"id,omitempty"'`

	_synContent = `--content '...'    a function body, as Go statements without the braces,` + "\n" +
		`                     or '@<file>': 'return nil', @hack/body.go`
	_synReplacement = `--content '...'    what each match becomes, or '@<file>'; '$1' expands a` + "\n" +
		`                     capture group: 'slog.Info($1)'`
	_synMatch = `--match '...'      a Go regular expression: 'log\.Printf\((.*)\)'`
)

// syntaxNote renders an "Argument syntax:" section for a command's Long.
func syntaxNote(lines ...string) string {
	return "\n\nArgument syntax:\n" + examples(lines...)
}

// examples renders a command's Example block.
//
// cobra prints the field verbatim under an "Examples:" heading, so the
// indentation belongs here rather than in every caller. An empty string is a
// blank line between groups.
func examples(lines ...string) string {
	var b strings.Builder
	for _, l := range lines {
		if l != "" {
			b.WriteString("  ")
			b.WriteString(l)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// options holds the values of the command line flags.
type options struct {
	Patches              []string // -p, --patch
	PatchesFile          string   // -P, --patches-file
	Diff                 bool     // -d, --diff
	Print                bool     // --print-only
	SkipImportProcessing bool     // --skip-import-processing
	SkipGenerated        bool     // --skip-generated
	Verbose              bool     // -v, --verbose
}

// addApplyFlags registers the flags shared by every command that rewrites Go
// files, so that refactoring subcommands accept the same output modes as the
// root command.
func addApplyFlags(fs *pflag.FlagSet, opts *options) {
	fs.BoolVarP(&opts.Diff, "diff", "d", false, _diffDesc)
	fs.BoolVar(&opts.Print, "print-only", false, _printOnlyDesc)
	fs.BoolVar(&opts.SkipImportProcessing, "skip-import-processing", false, _skipImportProcessingDesc)
	fs.BoolVar(&opts.SkipGenerated, "skip-generated", false, _skipGeneratedDesc)
	fs.BoolVarP(&opts.Verbose, "verbose", "v", false, _verboseDesc)
}

// addPatchFlags registers the flags that name patch files. Only the root
// command takes these; refactoring subcommands synthesize their own patches.
func addPatchFlags(fs *pflag.FlagSet, opts *options) {
	// StringArray rather than StringSlice: StringSlice splits on commas, which
	// would quietly turn a patch path containing a comma into two paths.
	fs.StringArrayVarP(&opts.Patches, "patch", "p", nil, _patchDesc)
	fs.StringVarP(&opts.PatchesFile, "patches-file", "P", "", _patchesFileDesc)
}

// newRootCommand builds the gopatch command tree.
//
// The returned command is single-use: pflag accumulates repeated --patch
// values, so a command reused across runs would carry patches over from the
// previous one.
func (cmd *mainCmd) newRootCommand() (*cobra.Command, *options) {
	var opts options

	root := &cobra.Command{
		Use:   "gopatch [options] <pattern>...",
		Short: "Apply patch files to Go source code.",
		Long: "gopatch matches and transforms Go code.\n\n" +
			"`gopatch patch` applies a patch file. The other commands take the change " +
			"as arguments instead, so the common mechanical refactorings need no patch " +
			"at all: see `gopatch rename --help`, `add`, `delete`, `edit` and " +
			"`replace`. `gopatch install` writes the agent skills this binary carries.\n\n" +
			"Patches may still be applied by the root command, but that is deprecated " +
			"and will be removed in " + _rootPatchRemovalVersion + "; use `gopatch patch`.\n\n" +
			_patternDesc,
		Example: examples(
			"# apply a patch file to every Go file under the current directory",
			"gopatch patch -p rules.patch ./...",
			"",
			"# show what would change without writing anything",
			"gopatch patch -d -p rules.patch ./...",
			"",
			"# refactor without writing a patch at all",
			"gopatch rename struct HTTPClient Client ./...",
			"gopatch rename field  User.Name   Title  ./...",
			"",
			"# teach your AI coding agents to use it",
			"gopatch install",
		),

		// Without this, cobra's default validator rejects the first
		// positional as an unknown command as soon as the tree has any
		// subcommand, which would break every existing invocation.
		Args: cobra.ArbitraryArgs,

		// runMain reports errors and owns the exit code. Left to itself,
		// cobra would print the error a second time, and its usage dump goes
		// to stdout, which corrupts --diff and --print-only output.
		SilenceUsage:  true,
		SilenceErrors: true,

		DisableFlagsInUseLine: true,
		Version:               _version,

		RunE: func(c *cobra.Command, args []string) error {
			return cmd.runRoot(c, &opts, args)
		},
	}

	root.SetIn(cmd.Stdin)
	root.SetOut(cmd.Stdout)
	root.SetErr(cmd.Stderr)
	root.SetVersionTemplate("gopatch {{.Version}}\n")

	addApplyFlags(root.Flags(), &opts)
	addPatchFlags(root.Flags(), &opts)

	// Registering --version up front keeps our own description, and stops
	// cobra from claiming -v, which belongs to --verbose.
	root.Flags().Bool("version", false, _versionDesc)

	root.AddCommand(cmd.subcommands()...)

	return root, &opts
}

// subcommands returns the root command's children.
func (cmd *mainCmd) subcommands() []*cobra.Command {
	return append([]*cobra.Command{
		cmd.newPatchCommand(),
		cmd.newRenameCommand(),
		cmd.newAddCommand(),
		cmd.newDeleteCommand(),
		cmd.newEditCommand(),
		cmd.newReplaceCommand(),
		cmd.newInstallCommand(),
		cmd.newVersionCommand(),
	}, cmd.newDocTopics()...)
}

func (cmd *mainCmd) newVersionCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: _versionDesc,
		Args:  cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			_, err := fmt.Fprintln(c.OutOrStdout(), "gopatch "+_version)
			return err
		},
	}
}

// runRoot is the deprecated spelling of `gopatch patch`.
//
// It still applies patches, because every existing invocation, script and CI
// job spells it this way, but it says on stderr where the command has moved.
// The warning is skipped when there are no patterns: that is a user asking for
// help, not one using the old form.
func (cmd *mainCmd) runRoot(c *cobra.Command, opts *options, patterns []string) error {
	if len(patterns) > 0 {
		warnRootPatchDeprecated(c.ErrOrStderr())
	}

	return cmd.runPatch(c, opts, patterns)
}
