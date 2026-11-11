package refactor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeGo writes one Go file into a fresh directory and returns its path.
func writeGo(t *testing.T, name, src string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(path, []byte(src), 0o600))
	return path
}

func TestFindFuncSignature(t *testing.T) {
	t.Parallel()

	src := `package p

import "context"

func Send(r *Request) (*Response, error) { return nil, nil }

func Log(msg string) {}

func Now() time.Time { return time.Time{} }

func Group(a, b string, c int) (ok bool, err error) { return }

func Variadic(format string, args ...any) {}

func Fetch(ctx context.Context) error { return nil }

type Request struct{}
type Response struct{}
`
	path := writeGo(t, "p.go", src)

	tests := []struct {
		name        string
		wantParams  string
		wantResults string
	}{
		{name: "Send", wantParams: "r *Request", wantResults: "(*Response, error)"},
		{name: "Log", wantParams: "msg string", wantResults: ""},
		{name: "Now", wantParams: "", wantResults: "time.Time"},
		{name: "Group", wantParams: "a, b string, c int", wantResults: "(ok bool, err error)"},
		{name: "Variadic", wantParams: "format string, args ...any", wantResults: ""},
		{name: "Fetch", wantParams: "ctx context.Context", wantResults: "error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := Search{Arg: "--package", Files: []string{path}}.FuncSignature(tt.name)
			require.NoError(t, err)
			assert.Equal(t, tt.wantParams, got.Params, "params")
			assert.Equal(t, tt.wantResults, got.Results, "results")
		})
	}
}

func TestFindFuncSignatureString(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "(r *Request) (*Response, error)",
		FuncSignature{Params: "r *Request", Results: "(*Response, error)"}.String())
	assert.Equal(t, "(msg string)", FuncSignature{Params: "msg string"}.String())
	assert.Equal(t, "()", FuncSignature{}.String())
}

func TestFindFuncSignatureErrors(t *testing.T) {
	t.Parallel()

	t.Run("not found", func(t *testing.T) {
		t.Parallel()

		path := writeGo(t, "p.go", "package p\n\nfunc Other() {}\n")

		_, err := Search{Arg: "--package", Files: []string{path}}.FuncSignature("Send")
		require.Error(t, err)
		assert.ErrorContains(t, err, `could not find a top-level func "Send"`)
	})

	t.Run("is a method", func(t *testing.T) {
		t.Parallel()

		// A method cannot be matched through a plain func pattern, and a
		// receiver plus a parameter list in one patch cross-connect, so this
		// has to be an error rather than a silent no-op.
		path := writeGo(t, "p.go", "package p\n\ntype T struct{}\n\nfunc (t *T) Send() {}\n")

		_, err := Search{Arg: "--package", Files: []string{path}}.FuncSignature("Send")
		require.Error(t, err)
		assert.ErrorContains(t, err, "is a method on t *T")
		assert.ErrorContains(t, err, "type-aware commands")
	})

	t.Run("conflicting signatures", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		a := filepath.Join(dir, "a.go")
		b := filepath.Join(dir, "b.go")
		require.NoError(t, os.WriteFile(a, []byte("package a\n\nfunc Send(x int) {}\n"), 0o600))
		require.NoError(t, os.WriteFile(b, []byte("package b\n\nfunc Send(s string) error { return nil }\n"), 0o600))

		_, err := Search{Arg: "--package", Files: []string{a, b}}.FuncSignature("Send")
		require.Error(t, err)
		assert.ErrorContains(t, err, "more than one signature")
		assert.ErrorContains(t, err, "--package")
	})

	t.Run("same signature twice is fine", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		a := filepath.Join(dir, "a.go")
		b := filepath.Join(dir, "b.go")
		require.NoError(t, os.WriteFile(a, []byte("package a\n\nfunc Send(x int) {}\n"), 0o600))
		require.NoError(t, os.WriteFile(b, []byte("package b\n\nfunc Send(x int) {}\n"), 0o600))

		got, err := Search{Arg: "--package", Files: []string{a, b}}.FuncSignature("Send")
		require.NoError(t, err)
		assert.Equal(t, "x int", got.Params)
	})

	t.Run("unparseable files are skipped", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		bad := filepath.Join(dir, "bad.go")
		good := filepath.Join(dir, "good.go")
		require.NoError(t, os.WriteFile(bad, []byte("package p\n\nfunc {{{\n"), 0o600))
		require.NoError(t, os.WriteFile(good, []byte("package p\n\nfunc Send(x int) {}\n"), 0o600))

		// A refactor may legitimately run over a tree that does not build.
		got, err := Search{Arg: "--package", Files: []string{bad, good}}.FuncSignature("Send")
		require.NoError(t, err)
		assert.Equal(t, "x int", got.Params)
	})
}

// gopatch cannot add the first import to a file: with no import block to extend,
// the change lands as an inserted declaration and the import is dropped, leaving
// the function declared twice. The commands have to refuse that rather than
// silently corrupt the file.
func TestRequireImportBlock(t *testing.T) {
	t.Parallel()

	t.Run("file already imports something", func(t *testing.T) {
		t.Parallel()

		path := writeGo(t, "p.go", "package p\n\nimport \"errors\"\n\nfunc Send() error { return errors.New(\"x\") }\n")

		require.NoError(t, Search{Files: []string{path}}.RequireImportBlock("Send", "context"))
	})

	t.Run("file has no imports", func(t *testing.T) {
		t.Parallel()

		path := writeGo(t, "bare.go", "package p\n\nfunc Send() {}\n")

		err := Search{Files: []string{path}}.RequireImportBlock("Send", "context")
		require.Error(t, err)
		assert.ErrorContains(t, err, "bare.go")
		assert.ErrorContains(t, err, "cannot add the first import")
		assert.ErrorContains(t, err, "context")
	})

	t.Run("only files declaring the func matter", func(t *testing.T) {
		t.Parallel()

		dir := t.TempDir()
		bare := filepath.Join(dir, "bare.go")
		decl := filepath.Join(dir, "decl.go")
		// bare.go has no imports but does not declare Send, so it is irrelevant.
		require.NoError(t, os.WriteFile(bare, []byte("package p\n\nfunc Other() {}\n"), 0o600))
		require.NoError(t, os.WriteFile(decl,
			[]byte("package p\n\nimport \"errors\"\n\nfunc Send() error { return errors.New(\"x\") }\n"), 0o600))

		require.NoError(t, Search{Files: []string{bare, decl}}.RequireImportBlock("Send", "context"))
	})
}

func TestFindStructField(t *testing.T) {
	t.Parallel()

	src := "package p\n\n" +
		"type User struct {\n" +
		"\tID    string `json:\"id\"`\n" +
		"\tName  string\n" +
		"\tHost  *http.Client\n" +
		"\tStart, End int\n" +
		"}\n\n" +
		"type Other struct {\n" +
		"\tID int\n" +
		"}\n"
	path := writeGo(t, "p.go", src)

	t.Run("tagged field", func(t *testing.T) {
		t.Parallel()

		got, err := Search{Files: []string{path}}.StructField("User", "ID")
		require.NoError(t, err)
		assert.Equal(t, "string", got.Type)
		assert.Equal(t, "`json:\"id\"`", got.Tag)
	})

	t.Run("untagged field", func(t *testing.T) {
		t.Parallel()

		got, err := Search{Files: []string{path}}.StructField("User", "Name")
		require.NoError(t, err)
		assert.Equal(t, "string", got.Type)
		assert.Empty(t, got.Tag)
	})

	t.Run("qualified type", func(t *testing.T) {
		t.Parallel()

		got, err := Search{Files: []string{path}}.StructField("User", "Host")
		require.NoError(t, err)
		assert.Equal(t, "*http.Client", got.Type)
	})

	t.Run("scoped to the named struct", func(t *testing.T) {
		t.Parallel()

		got, err := Search{Files: []string{path}}.StructField("Other", "ID")
		require.NoError(t, err)
		assert.Equal(t, "int", got.Type)
	})

	t.Run("grouped declaration is refused", func(t *testing.T) {
		t.Parallel()

		// gopatch does not match a grouped field declaration, so a patch built
		// for one would silently do nothing.
		_, err := Search{Files: []string{path}}.StructField("User", "End")
		require.Error(t, err)
		assert.ErrorContains(t, err, "declared in a group")
		assert.ErrorContains(t, err, "split the declaration")
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()

		_, err := Search{Files: []string{path}}.StructField("User", "Missing")
		require.Error(t, err)
		assert.ErrorContains(t, err, "could not find field User.Missing")
	})
}

func TestSearchNarrowsByPackage(t *testing.T) {
	t.Parallel()

	store := writeGo(t, "store.go", "package store\n\n"+
		"type User struct {\n\tID string\n}\n\n"+
		"func Send(u User) error { return nil }\n")
	api := writeGo(t, "api.go", "package api\n\n"+
		"type User struct {\n\tID int\n}\n\n"+
		"func Send(u User, n int) {}\n")
	files := []string{store, api}

	t.Run("field is ambiguous across packages", func(t *testing.T) {
		t.Parallel()

		// One of the two would be picked silently otherwise, and the patch built
		// from it would be applied to both.
		_, err := Search{Arg: "target", Files: files}.StructField("User", "ID")
		require.Error(t, err)
		assert.ErrorContains(t, err, "declared differently")
		assert.ErrorContains(t, err, "int and string")
		assert.ErrorContains(t, err, "qualify target")
	})

	t.Run("field narrowed by package", func(t *testing.T) {
		t.Parallel()

		got, err := Search{Arg: "target", Files: files, Package: "api"}.StructField("User", "ID")
		require.NoError(t, err)
		assert.Equal(t, "int", got.Type)
	})

	t.Run("signature is ambiguous across packages", func(t *testing.T) {
		t.Parallel()

		_, err := Search{Arg: "target", Files: files}.FuncSignature("Send")
		require.Error(t, err)
		assert.ErrorContains(t, err, "more than one signature")
		assert.ErrorContains(t, err, "qualify target")
	})

	t.Run("signature narrowed by package", func(t *testing.T) {
		t.Parallel()

		got, err := Search{Arg: "target", Files: files, Package: "store"}.FuncSignature("Send")
		require.NoError(t, err)
		assert.Equal(t, "(u User) error", got.String())
	})

	t.Run("package with no such declaration", func(t *testing.T) {
		t.Parallel()

		_, err := Search{Arg: "target", Files: files, Package: "api"}.FuncSignature("Missing")
		require.Error(t, err)
		assert.ErrorContains(t, err, "could not find a top-level func \"Missing\" in package api")
	})
}

func TestInferPackage(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	a := filepath.Join(dir, "a.go")
	b := filepath.Join(dir, "b.go")
	require.NoError(t, os.WriteFile(a, []byte("package alpha\n\ntype Client struct{}\n"), 0o600))
	require.NoError(t, os.WriteFile(b, []byte("package beta\n\ntype Client struct{}\n\nfunc Only() {}\n"), 0o600))

	t.Run("unique", func(t *testing.T) {
		t.Parallel()

		got, err := InferPackage("func", []string{a, b}, Target{Names: []string{"Only"}})
		require.NoError(t, err)
		assert.Equal(t, "beta", got)
	})

	t.Run("ambiguous", func(t *testing.T) {
		t.Parallel()

		_, err := InferPackage("struct", []string{a, b}, Target{Names: []string{"Client"}})
		require.Error(t, err)
		assert.ErrorContains(t, err, "[alpha beta]")
		assert.ErrorContains(t, err, `as in "alpha.Client"`)
	})

	// The declaration inferred for a member is its owning type, and the
	// suggestion requalifies the whole target rather than only that half —
	// "alpha.Client" alone would not be a usable field argument.
	t.Run("ambiguous member", func(t *testing.T) {
		t.Parallel()

		_, err := InferPackage("field", []string{a, b}, Target{Names: []string{"Client", "Timeout"}})
		require.Error(t, err)
		assert.ErrorContains(t, err, `"Client" is declared in packages [alpha beta]`)
		assert.ErrorContains(t, err, `qualify field with the one you mean, as in "alpha.Client.Timeout"`)
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()

		_, err := InferPackage("struct", []string{a, b}, Target{Names: []string{"Nope"}})
		require.Error(t, err)
		assert.ErrorContains(t, err, `could not find a declaration of "Nope"`)
	})
}
