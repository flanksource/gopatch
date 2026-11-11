package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/uber-go/gopatch/internal/refactor"
	"github.com/uber-go/gopatch/internal/refactor/typed"
)

// Descriptions of the flags that describe a function's shape and body.
const (
	_paramsDesc  = `Parameter list, such as "ctx context.Context, r *Request".`
	_resultsDesc = `Result list, such as "(*Response, error)".`

	_importDesc = "Import path a type or expression in the new source needs, as " +
		"\"path\", or \"qualifier=path\" to add it under an explicit alias. Repeat " +
		"for more than one. Imports the file already has are used automatically."

	_bodyContentDesc = "New body for the function, as Go statements: " +
		"--content 'return nil'. Prefix with \"@\" to read it from a file: " +
		"--content @body.go. Pass \"\" for an empty body."

	_replaceContentDesc = "What each --match becomes. \"$1\" and \"${name}\" expand " +
		"capture groups. Prefix with \"@\" to read it from a file, and pass \"\" to " +
		"delete the matched text."

	_matchDesc = "Regular expression to find in the function's body, matched against " +
		"the source as written. It spans the whole body, so \"^\" and \"$\" anchor the " +
		"body rather than a line unless you write \"(?m)\"."
)

// funcFlags holds the flags that describe a function's signature, its body, and
// the imports either refers to.
//
// One struct, because a command that takes more than one of them takes them as a
// set; each command registers only the flags it accepts, so an unaccepted one is
// rejected by cobra rather than silently ignored.
type funcFlags struct {
	// Params and Results are parameter and result lists, without and with
	// parentheses respectively.
	Params  string
	Results string

	// Content is new source for the body, or "@<path>" naming a file holding it.
	// On the replace commands it is the replacement for each match instead.
	Content string

	// Match is a regular expression to find in the body.
	Match string

	// Imports are the packages the new source refers to.
	Imports []string
}

func addSignatureFlags(fs *pflag.FlagSet, f *funcFlags) {
	fs.StringVar(&f.Params, "params", "", _paramsDesc)
	fs.StringVar(&f.Results, "results", "", _resultsDesc)
}

func addImportFlag(fs *pflag.FlagSet, f *funcFlags) {
	fs.StringArrayVar(&f.Imports, "import", nil, _importDesc)
}

// addContentFlag registers --content.
//
// The description is the caller's, because the content means different things on
// the two verbs: on edit it is the whole body, on replace it is what each match
// becomes.
func addContentFlag(fs *pflag.FlagSet, f *funcFlags, desc string) {
	fs.StringVar(&f.Content, "content", "", desc)
}

func addMatchFlag(fs *pflag.FlagSet, f *funcFlags) {
	fs.StringVar(&f.Match, "match", "", _matchDesc)
}

// parseChanges validates the signature flags for a command that edits an
// existing declaration, recording which halves the user actually gave so that
// the other is left as it is.
func (f funcFlags) parseChanges(c *cobra.Command) (typed.Signature, error) {
	params, results, imports, err := f.parse()
	if err != nil {
		return typed.Signature{}, err
	}

	out := typed.Signature{
		Params:     params,
		SetParams:  c.Flags().Changed("params"),
		Results:    results,
		SetResults: c.Flags().Changed("results"),
		Imports:    imports,
	}
	if !out.SetParams && !out.SetResults {
		return typed.Signature{}, errors.New("nothing to change: pass --params or --results")
	}
	return out, nil
}

// parse validates the signature flags and resolves the imports.
func (f funcFlags) parse() (params, results string, imports []typed.ImportRef, err error) {
	if params, err = refactor.ParseFieldList("--params", f.Params); err != nil {
		return "", "", nil, err
	}
	if results, _, _, err = refactor.ParseResults("--results", f.Results); err != nil {
		return "", "", nil, err
	}
	if imports, err = parseImportFlags(f.Imports); err != nil {
		return "", "", nil, err
	}
	return params, results, imports, nil
}

// body resolves the flags that describe a replacement function body.
func (cmd *mainCmd) body(f funcFlags) (typed.Body, error) {
	content, err := cmd.readContent("--content", f.Content)
	if err != nil {
		return typed.Body{}, err
	}
	imports, err := parseImportFlags(f.Imports)
	if err != nil {
		return typed.Body{}, err
	}
	return typed.Body{Content: content, Imports: imports}, nil
}

// substitution resolves the flags that describe a regular-expression
// replacement inside a function body.
func (cmd *mainCmd) substitution(f funcFlags) (typed.Substitution, error) {
	if f.Match == "" {
		return typed.Substitution{}, errors.New(
			"nothing to look for: pass --match with the expression to replace")
	}

	// Compiled up front so a bad expression is reported before the module is
	// loaded, which takes seconds on a large one.
	re, err := regexp.Compile(f.Match)
	if err != nil {
		return typed.Substitution{}, fmt.Errorf("invalid --match: %w", err)
	}

	content, err := cmd.readContent("--content", f.Content)
	if err != nil {
		return typed.Substitution{}, err
	}
	imports, err := parseImportFlags(f.Imports)
	if err != nil {
		return typed.Substitution{}, err
	}
	return typed.Substitution{Match: re, Replace: content, Imports: imports}, nil
}

// runSetBody replaces the whole body of a func or method.
func (cmd *mainCmd) runSetBody(
	arg string, kind typed.Kind, target refactor.Target, f funcFlags,
	opts *typedOptions, patterns []string,
) error {
	q, err := cmd.query(arg, kind, target, opts)
	if err != nil {
		return err
	}
	body, err := cmd.body(f)
	if err != nil {
		return err
	}

	return cmd.runTyped(typedRequest{
		Options:  opts,
		Patterns: patterns,
		Run: func(p *typed.Program) (string, error) {
			sym, err := p.Resolve(q)
			if err != nil {
				return "", err
			}

			n, err := p.SetBody(sym, body)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf(
				"replace the body of %v in %v (%d declaration(s); call sites are not updated)",
				sym.Desc, sym.Pkg, n), nil
		},
	})
}

// runSubstitute replaces every match of a pattern inside a func or method's body.
func (cmd *mainCmd) runSubstitute(
	arg string, kind typed.Kind, target refactor.Target, f funcFlags,
	opts *typedOptions, patterns []string,
) error {
	q, err := cmd.query(arg, kind, target, opts)
	if err != nil {
		return err
	}
	sub, err := cmd.substitution(f)
	if err != nil {
		return err
	}

	return cmd.runTyped(typedRequest{
		Options:  opts,
		Patterns: patterns,
		Run: func(p *typed.Program) (string, error) {
			sym, err := p.Resolve(q)
			if err != nil {
				return "", err
			}

			decls, matches, err := p.SubstituteInBody(sym, sub)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf(
				"replace %v match(es) of %v in the body of %v in %v (%d declaration(s))",
				matches, sub.Match, sym.Desc, sym.Pkg, decls), nil
		},
	})
}

// refusePatchFlags reports any patch-only flag that a type-aware run cannot
// honor.
//
// `edit func` chooses its engine from its flags, so the flags that mean something
// only to a generated patch are registered whether or not they apply. Accepting
// one and ignoring it would misreport what the command did.
func refusePatchFlags(c *cobra.Command, because string) error {
	var given []string
	for _, name := range []string{"print-patch", "skip-import-processing", "skip-generated"} {
		if c.Flags().Changed(name) {
			given = append(given, "--"+name)
		}
	}
	if len(given) == 0 {
		return nil
	}

	return fmt.Errorf(
		"%v runs the type-aware engine, which generates no patch and derives the import "+
			"block from the result, so %v cannot be honored; drop it",
		because, strings.Join(given, " and "))
}

// readContent resolves a --content value: literal Go source, or "@<path>" naming
// a file to read it from, relative to the working directory.
//
// The marker needs no escape, because no Go token begins with "@": content that
// would be mistaken for a file reference could not have compiled anyway.
func (cmd *mainCmd) readContent(flag, value string) (string, error) {
	path, ok := strings.CutPrefix(value, "@")
	if !ok {
		return value, nil
	}
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("%v: expected a file path after \"@\"", flag)
	}

	abs, err := cmd.absPath(path)
	if err != nil {
		return "", err
	}
	content, err := os.ReadFile(abs)
	if err != nil {
		return "", fmt.Errorf("%v: %w", flag, err)
	}
	return string(content), nil
}

// parseImportFlags reads repeated --import values.
//
// A bare path takes its qualifier from the last path segment, which is the
// package's name in all but a handful of cases. The "<qualifier>=<path>" form
// says the name explicitly, and is written into the file as an import alias:
// gopatch cannot look a package's real name up without building it, so aliasing
// is the only way to guarantee the qualifier in the code and the one the import
// introduces are the same.
func parseImportFlags(values []string) ([]typed.ImportRef, error) {
	out := make([]typed.ImportRef, 0, len(values))
	for _, v := range values {
		qualifier, path, explicit := strings.Cut(v, "=")
		if !explicit {
			path = qualifier
		}
		if _, err := refactor.ParseImportPath("--import", path); err != nil {
			return nil, err
		}

		var err error
		if explicit {
			qualifier, err = refactor.ParseIdent("--import", qualifier)
		} else {
			qualifier, err = refactor.DerivePackageName("--import", path)
		}
		if err != nil {
			return nil, err
		}
		out = append(out, typed.ImportRef{Path: path, Qualifier: qualifier, Alias: explicit})
	}
	return out, nil
}
