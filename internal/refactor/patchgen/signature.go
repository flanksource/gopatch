package patchgen

import (
	"fmt"
	"strings"

	"github.com/uber-go/gopatch/internal/refactor"
	"go.uber.org/multierr"
)

// SetSignature rewrites a top-level function's signature.
//
// Both `add param` and `edit func --signature` render as this: the current
// signature is resolved from the target files and reproduced exactly, with no
// elision anywhere in it. That is deliberate — "..." in a result list makes
// go/format fail outright, and two "..." in one signature cross-connect and
// corrupt the output, so the only safe shape is an exact rewrite.
//
// Call sites are not updated. gopatch cannot do that: the call-site half of a
// signature change is a known engine bug, and choosing what to pass for a new
// parameter is a decision the tool cannot make.
type SetSignature struct {
	// Func is the function's name.
	Func string

	// From is the function's current signature, as resolved from the source.
	From refactor.FuncSignature

	// To is the replacement signature.
	To refactor.FuncSignature

	// Package is the package clause of the package that declares the function.
	// When empty the change matches the name in any package, which is wrong as
	// soon as two packages declare it with the same signature.
	Package string

	// Import is added by the change when non-empty, for a type the new
	// signature introduces.
	Import string

	// Summary describes the change for the patch's description comment. It lets
	// `add param` report what it did rather than dumping both signatures.
	Summary string
}

var _ Operation = SetSignature{}

// Validate reports every problem with the operation's arguments.
func (o SetSignature) Validate() error {
	var errs error
	if _, err := refactor.ParseIdent("func name", o.Func); err != nil {
		errs = multierr.Append(errs, err)
	}
	if o.Package != "" {
		if _, err := refactor.ParseIdent("package name", o.Package); err != nil {
			errs = multierr.Append(errs, err)
		}
	}
	if o.From.String() == o.To.String() {
		errs = multierr.Append(errs, fmt.Errorf(
			"func %v already has the signature %v", o.Func, o.To))
	}
	if o.Import != "" {
		if _, err := refactor.ParseImportPath("--import", o.Import); err != nil {
			errs = multierr.Append(errs, err)
		}
	}

	// A "..." anywhere in a signature is either a variadic parameter, which is
	// fine, or an elision, which is not. Reject the elision.
	for _, sig := range []string{o.To.Params, o.To.Results} {
		if hasElision(sig) {
			errs = multierr.Append(errs, fmt.Errorf(
				"invalid signature %q: gopatch cannot elide a signature it is rewriting", sig))
		}
	}

	return errs
}

// hasElision reports whether s contains a bare "..." rather than a variadic
// parameter type such as "args ...string".
func hasElision(s string) bool {
	for i := strings.Index(s, "..."); i >= 0; {
		rest := strings.TrimSpace(s[i+3:])
		if rest == "" || strings.HasPrefix(rest, ",") || strings.HasPrefix(rest, ")") {
			return true
		}
		next := strings.Index(s[i+3:], "...")
		if next < 0 {
			return false
		}
		i += 3 + next
	}
	return false
}

// Describe summarizes the change.
func (o SetSignature) Describe() string {
	if o.Summary != "" {
		return o.Summary + " (call sites are not updated)"
	}
	return fmt.Sprintf("change func %v%v to %v%v (call sites are not updated)",
		o.Func, o.From, o.Func, o.To)
}

// Patch renders the change.
func (o SetSignature) Patch() ([]byte, error) {
	if err := o.Validate(); err != nil {
		return nil, err
	}

	p := newProgram(o.Describe())
	c := p.change()

	// Without the package clause the change matches the name wherever it is
	// declared with this signature, not only in the package that owns it.
	if o.Package != "" {
		c.pkg(o.Package)
	}

	if o.Import != "" {
		c.addImport("", o.Import)
	}

	c.diff(
		del(0, "func "+o.Func+o.From.String()+" {"),
		ins(0, "func "+o.Func+o.To.String()+" {"),
		dots(1),
		ctx(0, "}"),
	)

	return p.Bytes()
}

// AddParam builds the SetSignature that prepends a parameter to a function.
//
// The parameter goes first because that is where a context, a logger, or a
// clock belongs, and it is the order the existing add_ctx_param example uses.
func AddParam(name string, current refactor.FuncSignature, paramName, paramType, importPath string) (SetSignature, error) {
	if _, err := refactor.ParseTypeExpr("param type", paramType); err != nil {
		return SetSignature{}, err
	}

	param := paramType
	if paramName != "" {
		if _, err := refactor.ParseIdent("param name", paramName); err != nil {
			return SetSignature{}, err
		}
		param = paramName + " " + paramType
	}

	// A named parameter cannot be mixed with unnamed ones, so adding a named
	// parameter to an unnamed list would not compile.
	if paramName != "" && current.Params != "" && !allNamed(current.Params) {
		return SetSignature{}, fmt.Errorf(
			"func %v has unnamed parameters (%v), so %q cannot be given a name; "+
				"pass just the type", name, current.Params, paramName)
	}

	params := param
	if current.Params != "" {
		params = param + ", " + current.Params
	}

	return SetSignature{
		Func:    name,
		From:    current,
		To:      refactor.FuncSignature{Params: params, Results: current.Results},
		Import:  importPath,
		Summary: fmt.Sprintf("add parameter %v to func %v", param, name),
	}, nil
}

// allNamed reports whether every entry in a rendered parameter list has a name.
func allNamed(params string) bool {
	for _, part := range strings.Split(params, ",") {
		if len(strings.Fields(strings.TrimSpace(part))) < 2 {
			return false
		}
	}
	return true
}
