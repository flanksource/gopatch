package patchgen

import (
	"go/token"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uber-go/gopatch/internal/engine"
	"github.com/uber-go/gopatch/internal/parse"
	"github.com/uber-go/gopatch/internal/text"
)

// requireCompiles asserts that src is a program the engine accepts. Every
// golden test runs it: a hand-written expectation can otherwise encode a
// program that gopatch rejects, or one that silently matches nothing.
func requireCompiles(t *testing.T, src []byte) {
	t.Helper()

	fset := token.NewFileSet()
	prog, err := parse.Parse(fset, "generated.patch", src)
	require.NoError(t, err, "generated patch does not parse:\n%s", src)

	_, err = engine.Compile(fset, prog)
	require.NoError(t, err, "generated patch does not compile:\n%s", src)
}

// countChanges returns the number of "@@" blocks in a program, which is two
// lines per change.
func countChanges(src string) int {
	n := 0
	for _, l := range strings.Split(src, "\n") {
		if l == "@@" {
			n++
		}
	}
	return n / 2
}

// stripComments removes the '#' description lines so a generated program can be
// compared against a checked-in patch file that has none.
func stripComments(src []byte) string {
	var kept []string
	for _, l := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(l, "#") {
			continue
		}
		kept = append(kept, l)
	}
	return strings.Join(kept, "\n")
}

func TestProgramRendersMetavariablesAndSections(t *testing.T) {
	t.Parallel()

	p := newProgram("rename a field")
	c := p.change()
	c.expr("T")
	c.pkg("things")
	c.diff(
		ctx(0, "type User struct {"),
		dots(1),
		del(1, "UserName T"),
		ins(1, "Name T"),
		dots(1),
		ctx(0, "}"),
	)

	got, err := p.Bytes()
	require.NoError(t, err)

	want := text.Unlines(
		"# rename a field",
		"@@",
		"var T expression",
		"@@",
		" package things",
		"",
		" type User struct {",
		"   ...",
		"-  UserName T",
		"+  Name T",
		"   ...",
		" }",
	)
	assert.Equal(t, string(want), string(got))
	requireCompiles(t, got)
}

func TestProgramGroupsMetavariablesByType(t *testing.T) {
	t.Parallel()

	p := newProgram("")
	c := p.change()
	c.ident("A", "B")
	c.expr("T")
	c.diff(
		ctx(0, "type Config struct {"),
		del(1, "A T"),
		del(1, "B T"),
		ins(1, "A, B T"),
		ctx(0, "}"),
	)

	got, err := p.Bytes()
	require.NoError(t, err)

	// One var line per metavariable type, names in declaration order, and no
	// description comment when the program has none.
	want := text.Unlines(
		"@@",
		"var A, B identifier",
		"var T expression",
		"@@",
		" type Config struct {",
		"-  A T",
		"-  B T",
		"+  A, B T",
		" }",
	)
	assert.Equal(t, string(want), string(got))
	requireCompiles(t, got)
}

func TestProgramRepeatsTheDescriptionOnEveryChange(t *testing.T) {
	t.Parallel()

	p := newProgram("do the thing")
	p.change().diff(del(0, "foo"), ins(0, "bar"))
	p.change().diff(del(0, "baz"), ins(0, "qux"))

	got, err := p.Bytes()
	require.NoError(t, err)

	// The CLI keeps only the last matching change's comments, so the
	// description has to appear on each one.
	assert.Equal(t, 2, strings.Count(string(got), "# do the thing"))
	requireCompiles(t, got)
}

func TestProgramFillerMakesAHeaderOnlyChangeLegal(t *testing.T) {
	t.Parallel()

	p := newProgram("")
	c := p.change()
	c.renamePkg("foo", "bar")
	c.filler()

	got, err := p.Bytes()
	require.NoError(t, err)

	want := text.Unlines(
		"@@",
		"var x identifier",
		"@@",
		"-package foo",
		"+package bar",
		"",
		" x",
	)
	assert.Equal(t, string(want), string(got))
	requireCompiles(t, got)
}

func TestProgramFillerAvoidsCollidingWithExistingNames(t *testing.T) {
	t.Parallel()

	p := newProgram("")
	c := p.change()
	c.deleteImport(c.ident("x"), "example.com/x")
	c.filler()

	got, err := p.Bytes()
	require.NoError(t, err)

	// "x" is taken by the import name, so the filler picks another name.
	assert.Contains(t, string(got), "var x, x2 identifier")
	assert.Contains(t, string(got), "\n x2\n")
	requireCompiles(t, got)
}

func TestProgramRejectsShapesTheEngineCannotCompile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc    string
		build   func() *program
		wantErr string
	}{
		{
			desc:    "no changes",
			build:   func() *program { return newProgram("") },
			wantErr: "no changes",
		},
		{
			desc: "empty change",
			build: func() *program {
				p := newProgram("")
				p.change()
				return p
			},
			wantErr: "is empty",
		},
		{
			desc: "package clause only",
			build: func() *program {
				p := newProgram("")
				p.change().renamePkg("foo", "bar")
				return p
			},
			wantErr: "matches only a package clause or imports",
		},
		{
			desc: "import only",
			build: func() *program {
				p := newProgram("")
				c := p.change()
				c.deleteImport("foo", "example.com/foo")
				return p
			},
			wantErr: "matches only a package clause or imports",
		},
		{
			desc: "empty plus section",
			build: func() *program {
				p := newProgram("")
				p.change().diff(del(0, "type Foo struct{}"))
				return p
			},
			wantErr: "empty plus section",
		},
		{
			desc: "empty minus section",
			build: func() *program {
				p := newProgram("")
				p.change().diff(ins(0, "type Foo struct{}"))
				return p
			},
			wantErr: "empty minus section",
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()

			_, err := tt.build().Bytes()
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

// The engine really does reject a change with an empty plus section, which is
// the constraint that makes whole-declaration deletion impossible in the DSL.
// Pinning it here means a future engine change that lifts the restriction shows
// up as a failing test rather than going unnoticed.
func TestEngineRejectsAnEmptyPlusSection(t *testing.T) {
	t.Parallel()

	src := text.Unlines("@@", "@@", "-type importKey string")

	_, err := parse.Parse(token.NewFileSet(), "generated.patch", src)
	require.Error(t, err)
	assert.ErrorContains(t, err, "expected a declaration or an expression, found EOF")
}
