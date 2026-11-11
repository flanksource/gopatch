package main

import (
	"errors"
	"fmt"
	"go/token"
	"io"

	"github.com/spf13/cobra"
)

// _rootPatchRemovalVersion is the release that drops patch application from the
// root command. It is named in the deprecation warning and asserted by the
// tests, so the two cannot drift.
const _rootPatchRemovalVersion = "v0.6.0"

// _patchDescLong is a var rather than a const because it renders its pointer
// block with examples(), which keeps the indentation in one place.
var _patchDescLong = "gopatch applies patch files to Go source code.\n\n" +
	"A patch is a unified diff over Go syntax rather than over text: lines " +
	"prefixed with `-` are matched and removed, lines prefixed with `+` are " +
	"added, an unprefixed line is context that must match and is left alone, " +
	"and the `@@` section above them declares metavariables that match any " +
	"identifier or expression. A metavariable used twice must match the same " +
	"code both times; `...` elides the rest of an argument list, a body or a " +
	"statement block.\n\n" +
	"With no `-p` or `-P`, the patch is read from stdin.\n\n" + _patternDesc +
	"\n\nThe patch language is documented in this binary:\n" +
	examples(
		"gopatch help patches         the full reference",
		"gopatch help metavariables   identifiers vs expressions vs statements",
		"gopatch help examples        five worked patches",
	) +
	"\n\nTwo limits are worth knowing before you rely on a sweep. Once `...` is " +
	"used, only the first instance in a given scope is replaced (issue #10), " +
	"and matching elisions between the `-` and `+` sections does not always " +
	"work as expected (issue #9); a patch that puts `...` on its own line with " +
	"a leading space avoids the second.\n\n" +
	"For the common mechanical refactorings you do not need a patch file at " +
	"all: see `gopatch rename --help`, `add`, `delete`, `edit` and `replace`. " +
	"Their `--print-patch` flag prints a working patch for the change at hand, " +
	"which is the quickest way to start one."

func (cmd *mainCmd) newPatchCommand() *cobra.Command {
	var opts options

	c := &cobra.Command{
		Use:   "patch [options] <pattern>...",
		Short: "Apply patch files to Go source code.",
		Long:  _patchDescLong,
		Example: examples(
			"# apply a patch file to every Go file under the current directory",
			"gopatch patch -p rules.patch ./...",
			"",
			"# read the patch from stdin",
			"gopatch patch ./... < rules.patch",
			"",
			"# show what would change without writing anything",
			"gopatch patch -d -p rules.patch ./...",
			"",
			"# apply several patches, in the order given",
			"gopatch patch -p first.patch -p second.patch ./...",
			"",
			"# take the list of patches from a file, one path per line",
			"gopatch patch -P patches.txt ./...",
		),

		Args: cobra.ArbitraryArgs,

		// runMain reports errors and owns the exit code, and cobra's usage dump
		// goes to stdout, which would corrupt --diff and --print-only output.
		SilenceUsage:  true,
		SilenceErrors: true,

		DisableFlagsInUseLine: true,

		RunE: func(c *cobra.Command, args []string) error {
			return cmd.runPatch(c, &opts, args)
		},
	}

	addApplyFlags(c.Flags(), &opts)
	addPatchFlags(c.Flags(), &opts)

	return c
}

// runPatch applies the patches named by the command line options.
func (cmd *mainCmd) runPatch(c *cobra.Command, opts *options, patterns []string) error {
	if len(patterns) == 0 {
		fmt.Fprint(c.ErrOrStderr(), c.UsageString())
		fmt.Fprintln(c.ErrOrStderr())

		return errors.New("please provide at least one pattern")
	}

	fset := token.NewFileSet()
	progs, err := loadPatches(fset, opts, cmd.Stdin)
	if err != nil {
		return err
	}

	return cmd.runApply(applyRequest{
		FileSet:  fset,
		Applier:  newPatchRunner(fset, progs),
		Patterns: patterns,
		Options:  opts,
	})
}

// warnRootPatchDeprecated tells a caller that reached patch application through
// the root command where it is moving to.
//
// It writes to stderr: stdout carries --diff and --print-only output that gets
// piped into `patch`, an editor, or a review tool, and a warning mixed into it
// would corrupt all three.
func warnRootPatchDeprecated(w io.Writer) {
	fmt.Fprintf(w,
		"warning: applying patches from the root command is deprecated\n"+
			"         and will be removed in %s; use:\n"+
			"           gopatch patch -p rules.patch ./...\n",
		_rootPatchRemovalVersion)
}
