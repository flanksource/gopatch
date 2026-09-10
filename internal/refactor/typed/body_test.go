package typed

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// clientSend resolves Client.Send from the bodies fixture.
func clientSend(t *testing.T, p *Program) *Symbol {
	t.Helper()

	sym, err := p.Resolve(Query{Kind: KindMethod, Owner: "Client", Name: "Send"})
	require.NoError(t, err)
	return sym
}

func TestSetBodyReplacesOneDeclaration(t *testing.T) {
	t.Parallel()

	p := load(t, "bodies")

	n, err := p.SetBody(clientSend(t, p), Body{Content: "return nil"})
	require.NoError(t, err)
	assert.Equal(t, 1, n, "declarations changed")

	edits, err := p.Edits()
	require.NoError(t, err)

	// errors was used only by the body that was replaced, so the restorer drops
	// it; log survives because Other.Send and Deliver still call it.
	assert.Equal(t, `package send

import "log"

// Sender has no body of its own, so a body operation on it must be refused.
type Sender interface {
	Send(id string) error
}

// Client sends things.
type Client struct {
	Host string
}

// Send logs and fails.
func (c *Client) Send(id string) error {
	return nil
}

// Other has a method of the same name, which no body operation may touch.
type Other struct{}

// Send does nothing.
func (o Other) Send(id string) error {
	log.Printf("sending %v", id)
	return nil
}

// Deliver is a top-level function, and the only other user of log. It calls it
// twice, so a substitution's count can be seen to be matches and not
// declarations.
func Deliver(id string) error {
	log.Printf("delivering %v", id)
	log.Printf("delivered %v", id)
	return nil
}

func use() error {
	var s Sender = &Client{Host: "h"}
	if err := s.Send("1"); err != nil {
		return err
	}
	if err := (Other{}).Send("2"); err != nil {
		return err
	}
	return Deliver("3")
}
`, modified(t, edits, "send.go"))
}

func TestSetBodyOnAFunc(t *testing.T) {
	t.Parallel()

	p := load(t, "bodies")

	sym, err := p.Resolve(Query{Kind: KindFunc, Name: "Deliver"})
	require.NoError(t, err)

	// A body may bring an import with it, exactly as a new declaration may.
	_, err = p.SetBody(sym, Body{
		Content: `return fmt.Errorf("cannot deliver %v", id)`,
		Imports: []ImportRef{{Path: "fmt", Qualifier: "fmt"}},
	})
	require.NoError(t, err)

	edits, err := p.Edits()
	require.NoError(t, err)
	assert.Contains(t, modified(t, edits, "send.go"), `func Deliver(id string) error {
	return fmt.Errorf("cannot deliver %v", id)
}`)
	assert.Contains(t, modified(t, edits, "send.go"), `"fmt"`)
}

func TestSetBodyAcceptsAnEmptyBody(t *testing.T) {
	t.Parallel()

	p := load(t, "bodies")

	sym, err := p.Resolve(Query{Kind: KindFunc, Name: "use"})
	require.NoError(t, err)

	// An empty body is legal Go, and refusing it would make "clear this out"
	// impossible to express. It does not have to compile: use returns an error.
	_, err = p.SetBody(sym, Body{Content: ""})
	require.NoError(t, err)

	edits, err := p.Edits()
	require.NoError(t, err)
	assert.Contains(t, modified(t, edits, "send.go"), "func use() error {\n}\n")
}

func TestSetBodyRejectsContentThatIsNotGo(t *testing.T) {
	t.Parallel()

	p := load(t, "bodies")

	_, err := p.SetBody(clientSend(t, p), Body{Content: "return nil }} oops"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "the content is not a valid function body")

	// Nothing may be written when the content is rejected.
	edits, err := p.Edits()
	require.NoError(t, err)
	assert.Empty(t, edits, "a rejected body must leave the program unmodified")
}

func TestSetBodyRefusesAnInterfaceMethod(t *testing.T) {
	t.Parallel()

	p := load(t, "bodies")

	sym, err := p.Resolve(Query{Kind: KindMethod, Owner: "Sender", Name: "Send"})
	require.NoError(t, err)

	_, err = p.SetBody(sym, Body{Content: "return nil"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "is declared in an interface")
}

func TestSetBodyRefusesAField(t *testing.T) {
	t.Parallel()

	p := load(t, "bodies")

	sym, err := p.Resolve(Query{Kind: KindField, Owner: "Client", Name: "Host"})
	require.NoError(t, err)

	_, err = p.SetBody(sym, Body{Content: "return nil"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "is not a func or a method")
}

func TestSubstituteInBodyRewritesOnlyThatBody(t *testing.T) {
	t.Parallel()

	p := load(t, "bodies")

	decls, matches, err := p.SubstituteInBody(clientSend(t, p), Substitution{
		Match:   regexp.MustCompile(`log\.Printf\((.*)\)`),
		Replace: "slog.Info($1)",
		Imports: []ImportRef{{Path: "log/slog", Qualifier: "slog"}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, decls, "declarations changed")
	assert.Equal(t, 1, matches, "matches replaced")

	got := modified(t, edits(t, p), "send.go")

	// The comment inside the body survives, because the pattern is applied to
	// the source text and carries it through.
	assert.Contains(t, got, `func (c *Client) Send(id string) error {
	slog.Info("sending %v to %v", id, c.Host)
	// TODO: retry
	return errors.New("not implemented")
}`)

	// The same call in the same-named method on another type is left alone,
	// which is the whole reason this is not a sed invocation.
	assert.Contains(t, got, `func (o Other) Send(id string) error {
	log.Printf("sending %v", id)`)
	assert.Contains(t, got, `func Deliver(id string) error {
	log.Printf("delivering %v", id)`)
	assert.Contains(t, got, `"log/slog"`)
}

func TestSubstituteInBodyOnAFunc(t *testing.T) {
	t.Parallel()

	p := load(t, "bodies")

	sym, err := p.Resolve(Query{Kind: KindFunc, Name: "Deliver"})
	require.NoError(t, err)

	// Two matches in one body, to prove the count is matches and not
	// declarations.
	decls, matches, err := p.SubstituteInBody(sym, Substitution{
		Match:   regexp.MustCompile(`log\.Printf`),
		Replace: "slog.Info",
		Imports: []ImportRef{{Path: "log/slog", Qualifier: "slog"}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, decls, "declarations changed")
	assert.Equal(t, 2, matches, "matches replaced")

	got := modified(t, edits(t, p), "send.go")
	assert.Contains(t, got, `func Deliver(id string) error {
	slog.Info("delivering %v", id)
	slog.Info("delivered %v", id)
	return nil
}`)

	// log is still imported, because the two methods above still call it.
	assert.Contains(t, got, `"log"`)
	assert.Contains(t, got, `"log/slog"`)
}

func TestSubstituteInBodyReportsAPatternThatMatchesNothing(t *testing.T) {
	t.Parallel()

	p := load(t, "bodies")

	_, _, err := p.SubstituteInBody(clientSend(t, p), Substitution{
		Match:   regexp.MustCompile(`fmt\.Println`),
		Replace: "x",
	})
	require.Error(t, err)
	assert.ErrorContains(t, err, `nothing in the body of method Client.Send matches fmt\.Println`)

	got, err := p.Edits()
	require.NoError(t, err)
	assert.Empty(t, got, "a pattern that matched nothing must change nothing")
}

func TestSubstituteInBodyRefusesAResultThatIsNotGo(t *testing.T) {
	t.Parallel()

	p := load(t, "bodies")

	_, _, err := p.SubstituteInBody(clientSend(t, p), Substitution{
		Match:   regexp.MustCompile(`return`),
		Replace: "return return",
	})
	require.Error(t, err)
	assert.ErrorContains(t, err, "the content is not a valid function body")

	// The message shows what the substitution produced, because a regular
	// expression that composes badly is otherwise very hard to debug.
	assert.ErrorContains(t, err, "The substitution produced:")
}

func TestSubstituteInBodyRequiresAPattern(t *testing.T) {
	t.Parallel()

	p := load(t, "bodies")

	_, _, err := p.SubstituteInBody(clientSend(t, p), Substitution{Replace: "x"})
	require.Error(t, err)
	assert.ErrorContains(t, err, "no pattern to match against")
}

// edits renders the program, failing the test if it cannot.
func edits(t *testing.T, p *Program) []FileEdit {
	t.Helper()

	out, err := p.Edits()
	require.NoError(t, err)
	return out
}
