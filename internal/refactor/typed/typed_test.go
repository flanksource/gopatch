package typed

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// load loads one of the fixture modules under testdata.
//
// The environment is given explicitly rather than inherited: the CLI's own
// end-to-end suite sets GO111MODULE=off for the whole process, and a workspace
// in an ancestor directory would exclude these modules from the build list.
func load(t *testing.T, fixture string) *Program {
	t.Helper()

	dir, err := filepath.Abs(filepath.Join("testdata", fixture))
	require.NoError(t, err)

	p, err := Load(Request{
		Dir: dir,
		Env: append(os.Environ(), "GO111MODULE=on", "GOWORK=off"),
	})
	require.NoError(t, err)
	return p
}

// modified returns the new contents of one file from a set of edits.
func modified(t *testing.T, edits []FileEdit, base string) string {
	t.Helper()

	for _, e := range edits {
		if filepath.Base(e.Path) == base {
			return string(e.Modified)
		}
	}
	t.Fatalf("no edit for %v; got %v", base, paths(edits))
	return ""
}

func paths(edits []FileEdit) []string {
	out := make([]string, 0, len(edits))
	for _, e := range edits {
		out = append(out, filepath.Base(e.Path))
	}
	return out
}

func TestRenameField(t *testing.T) {
	t.Parallel()

	p := load(t, "rename_field")

	sym, err := p.Resolve(Query{Kind: KindField, Owner: "User", Name: "Name"})
	require.NoError(t, err)
	assert.Equal(t, "example.com/renamefield/user", sym.Pkg)

	count, err := p.Rename(sym, "Title")
	require.NoError(t, err)

	// The declaration, the method body, the constructor's literal, and the two
	// references in the importing package.
	assert.Equal(t, 5, count, "renamed positions")

	edits, err := p.Edits()
	require.NoError(t, err)
	assert.Equal(t, []string{"consumer.go", "user.go"}, paths(edits))

	assert.Equal(t, `package user

// User is a person.
type User struct {
	Title string `+"`json:\"name\"`"+`
	Age   int
}

// Other has a field with the same name, which must not be touched.
type Other struct {
	Name string
}

func (u *User) Greet() string {
	return "hello " + u.Title
}

func NewUser(name string) *User {
	return &User{Title: name}
}

func unrelated(o Other) string {
	return o.Name
}
`, modified(t, edits, "user.go"))

	assert.Equal(t, `package consumer

import "example.com/renamefield/user"

func Describe(u *user.User) string {
	return u.Title
}

func Build() *user.User {
	return &user.User{Title: "ann", Age: 3}
}

type local struct {
	Name string
}

func (l local) get() string { return l.Name }
`, modified(t, edits, "consumer.go"))
}

func TestRenameFieldRejectsCollision(t *testing.T) {
	t.Parallel()

	p := load(t, "rename_field")

	sym, err := p.Resolve(Query{Kind: KindField, Owner: "User", Name: "Name"})
	require.NoError(t, err)

	_, err = p.Rename(sym, "Age")
	require.Error(t, err)
	assert.Contains(t, err.Error(), `already has a field called "Age"`)
}

func TestResolveErrors(t *testing.T) {
	t.Parallel()

	p := load(t, "rename_field")

	tests := []struct {
		desc  string
		query Query
		want  string
	}{
		{
			desc:  "unknown field",
			query: Query{Kind: KindField, Owner: "User", Name: "Nope"},
			want:  `User has no field "Nope"`,
		},
		{
			desc:  "unknown type",
			query: Query{Kind: KindType, Name: "Missing"},
			want:  "could not find type Missing",
		},
		{
			desc:  "unknown package",
			query: Query{Kind: KindField, Owner: "User", Name: "Name", Package: "nope"},
			want:  `nothing in this module matches package "nope"`,
		},
		{
			desc:  "directory outside the module",
			query: Query{Kind: KindField, Owner: "User", Name: "Name", Dir: "/nope"},
			want:  "nothing in this module matches /nope",
		},
		{
			desc:  "field on a name that is a func",
			query: Query{Kind: KindField, Owner: "NewUser", Name: "Name"},
			want:  `"NewUser" is not a named type`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			_, err := p.Resolve(tt.query)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestResolveNarrowsByDirectory(t *testing.T) {
	t.Parallel()

	p := load(t, "decls")

	dir, err := filepath.Abs(filepath.Join("testdata", "decls", "other"))
	require.NoError(t, err)

	// Naming a directory is often easier than naming a package: the same Widget
	// is declared in both, and here the path picks one out.
	sym, err := p.Resolve(Query{Kind: KindType, Name: "Widget", Dir: dir})
	require.NoError(t, err)
	assert.Equal(t, "example.com/decls/other", sym.Pkg)

	file, err := filepath.Abs(filepath.Join("testdata", "decls", "app", "app.go"))
	require.NoError(t, err)

	sym, err = p.Resolve(Query{Kind: KindType, Name: "Widget", Dir: file})
	require.NoError(t, err)
	assert.Equal(t, "example.com/decls/app", sym.Pkg, "a file names its package too")
}

func TestResolveRefusesAnAmbiguousName(t *testing.T) {
	t.Parallel()

	p := load(t, "decls")

	_, err := p.Resolve(Query{Kind: KindType, Name: "Widget"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "declared in more than one package")
	assert.Contains(t, err.Error(), "example.com/decls/app, example.com/decls/other")
	assert.Contains(t, err.Error(), `as in "example.com/decls/app.Widget"`)

	sym, err := p.Resolve(Query{Kind: KindType, Name: "Widget", Package: "other"})
	require.NoError(t, err)
	assert.Equal(t, "example.com/decls/other", sym.Pkg)
}

func TestRenameMethodFollowsInterface(t *testing.T) {
	t.Parallel()

	p := load(t, "methods")

	sym, err := p.Resolve(Query{Kind: KindMethod, Owner: "File", Name: "Save"})
	require.NoError(t, err)

	count, err := p.Rename(sym, "Store")
	require.NoError(t, err)

	// The interface method, both implementations, and the s.Save() call.
	assert.Equal(t, 4, count, "renamed positions")

	edits, err := p.Edits()
	require.NoError(t, err)

	got := modified(t, edits, "store.go")
	assert.Contains(t, got, "Store() error\n}")
	assert.Contains(t, got, "func (f *File) Store() error")
	assert.Contains(t, got, "func (m Memory) Store() error")
	assert.Contains(t, got, "if err := s.Store(); err != nil")
	assert.NotContains(t, got, "Save() error", "no declaration keeps the old name")

	// Prose is left alone: a rename rewrites identifiers, and guessing which
	// words in a comment named the method would corrupt the ones that did not.
	assert.Contains(t, got, "// Save writes the file.")
	assert.Contains(t, got, "func SaveAll(savers ...Saver) error", "same-prefixed names are untouched")
}

func TestRenameInterfaceMethodFollowsImplementations(t *testing.T) {
	t.Parallel()

	p := load(t, "methods")

	sym, err := p.Resolve(Query{Kind: KindMethod, Owner: "Saver", Name: "Save"})
	require.NoError(t, err)

	_, err = p.Rename(sym, "Store")
	require.NoError(t, err)

	edits, err := p.Edits()
	require.NoError(t, err)

	got := modified(t, edits, "store.go")
	assert.Contains(t, got, "func (f *File) Store() error")
	assert.Contains(t, got, "func (m Memory) Store() error")
}

func TestRenameMethodRefusesOutsideModule(t *testing.T) {
	t.Parallel()

	p := load(t, "methods")

	sym, err := p.Resolve(Query{Kind: KindMethod, Owner: "Named", Name: "String"})
	require.NoError(t, err)

	_, err = p.Rename(sym, "Text")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fmt.Stringer.String")
	assert.Contains(t, err.Error(), "outside this module")
}

func TestDeleteUnreferencedType(t *testing.T) {
	t.Parallel()

	p := load(t, "decls")

	sym, err := p.Resolve(Query{Kind: KindType, Name: "Unused"})
	require.NoError(t, err)

	files, err := p.Delete(sym)
	require.NoError(t, err)
	assert.Equal(t, 1, files)

	edits, err := p.Edits()
	require.NoError(t, err)

	got := modified(t, edits, "app.go")
	assert.NotContains(t, got, "Unused")
	assert.NotContains(t, got, "referred to by nothing", "the doc comment goes with the type")
	assert.Contains(t, got, "type Used struct {")
}

func TestDeleteRefusesReferencedType(t *testing.T) {
	t.Parallel()

	p := load(t, "decls")

	sym, err := p.Resolve(Query{Kind: KindType, Name: "Used"})
	require.NoError(t, err)

	_, err = p.Delete(sym)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "type Used is still used at")
	assert.Contains(t, err.Error(), "app.go:16")
}

func TestDeleteFuncDropsNowUnusedImport(t *testing.T) {
	t.Parallel()

	p := load(t, "decls")

	sym, err := p.Resolve(Query{Kind: KindFunc, Name: "orphan"})
	require.NoError(t, err)

	_, err = p.Delete(sym)
	require.NoError(t, err)

	edits, err := p.Edits()
	require.NoError(t, err)

	got := modified(t, edits, "app.go")
	assert.NotContains(t, got, "func orphan")
	assert.NotContains(t, got, `"strings"`, "the import goes with its only user")
}

func TestDeleteRecursiveFuncIgnoresSelfReference(t *testing.T) {
	t.Parallel()

	p := load(t, "decls")

	sym, err := p.Resolve(Query{Kind: KindFunc, Name: "recursive"})
	require.NoError(t, err)

	_, err = p.Delete(sym)
	require.NoError(t, err)

	edits, err := p.Edits()
	require.NoError(t, err)
	assert.NotContains(t, modified(t, edits, "app.go"), "recursive")
}

func TestDeleteMethod(t *testing.T) {
	t.Parallel()

	p := load(t, "decls")

	sym, err := p.Resolve(Query{Kind: KindMethod, Owner: "Widget", Name: "Idle"})
	require.NoError(t, err)

	_, err = p.Delete(sym)
	require.NoError(t, err)

	edits, err := p.Edits()
	require.NoError(t, err)

	got := modified(t, edits, "app.go")
	assert.NotContains(t, got, "Idle")
	assert.Contains(t, got, "type Widget struct{}")
}

func TestDeleteOneSpecOfAGroup(t *testing.T) {
	t.Parallel()

	p := load(t, "decls")

	sym, err := p.Resolve(Query{Kind: KindType, Name: "Grouped"})
	require.NoError(t, err)

	_, err = p.Delete(sym)
	require.NoError(t, err)

	edits, err := p.Edits()
	require.NoError(t, err)

	got := modified(t, edits, "app.go")
	assert.NotContains(t, got, "Grouped struct{}")
	assert.Contains(t, got, "Sibling struct{}", "the sibling spec survives")
	assert.Contains(t, got, "type (", "the block survives")
}

func TestAddStruct(t *testing.T) {
	t.Parallel()

	p := load(t, "decls")

	file := filepath.Join("testdata", "decls", "app", "app.go")
	path, err := p.Add(Declaration{File: file, Kind: KindType, Name: "Fresh"})
	require.NoError(t, err)
	assert.Equal(t, "app.go", filepath.Base(path))

	edits, err := p.Edits()
	require.NoError(t, err)
	assert.Contains(t, modified(t, edits, "app.go"), "type Fresh struct {\n}")
}

func TestAddFuncResolvesQualifiedTypes(t *testing.T) {
	t.Parallel()

	p := load(t, "decls")

	_, err := p.Add(Declaration{
		File:    filepath.Join("testdata", "decls", "app", "app.go"),
		Kind:    KindFunc,
		Name:    "Join",
		Params:  "parts []string, sep string",
		Results: "(strings.Builder, error)",
	})
	require.NoError(t, err)

	edits, err := p.Edits()
	require.NoError(t, err)

	got := modified(t, edits, "app.go")
	assert.Contains(t, got, "func Join(parts []string, sep string) (strings.Builder, error) {")
	assert.Contains(t, got, `panic("not implemented")`)
	assert.Contains(t, got, `"strings"`)
}

func TestAddMethodAddsTheImportItNeeds(t *testing.T) {
	t.Parallel()

	p := load(t, "decls")

	_, err := p.Add(Declaration{
		File:    filepath.Join("testdata", "decls", "app", "app.go"),
		Kind:    KindMethod,
		Name:    "Wait",
		Recv:    "w Widget",
		Params:  "ctx context.Context",
		Results: "error",
		Imports: []ImportRef{{Path: "context", Qualifier: "context"}},
	})
	require.NoError(t, err)

	edits, err := p.Edits()
	require.NoError(t, err)

	got := modified(t, edits, "app.go")
	assert.Contains(t, got, "func (w Widget) Wait(ctx context.Context) error {")
	assert.Contains(t, got, "\t\"context\"\n", "the import is added unaliased")
}

func TestAddResolvesAnAliasedImport(t *testing.T) {
	t.Parallel()

	p := load(t, "decls")

	// An explicit qualifier has to reach the import declaration as an alias. The
	// restorer trusts its resolver for a package's name, so without one it would
	// emit `import "net/url"` beside a `ur.URL` that nothing declares.
	_, err := p.Add(Declaration{
		File:    filepath.Join("testdata", "decls", "app", "app.go"),
		Kind:    KindFunc,
		Name:    "Fetch",
		Params:  "u *ur.URL",
		Imports: []ImportRef{{Path: "net/url", Qualifier: "ur", Alias: true}},
	})
	require.NoError(t, err)

	edits, err := p.Edits()
	require.NoError(t, err)

	got := modified(t, edits, "app.go")
	assert.Contains(t, got, `ur "net/url"`)
	assert.Contains(t, got, "func Fetch(u *ur.URL) {")
}

func TestSetSignature(t *testing.T) {
	t.Parallel()

	p := load(t, "decls")

	sym, err := p.Resolve(Query{Kind: KindMethod, Owner: "Widget", Name: "Idle"})
	require.NoError(t, err)

	n, err := p.SetSignature(sym, Signature{
		Params:    "ctx context.Context",
		SetParams: true,
		Imports:   []ImportRef{{Path: "context", Qualifier: "context"}},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	edits, err := p.Edits()
	require.NoError(t, err)

	got := modified(t, edits, "app.go")
	assert.Contains(t, got, "func (w Widget) Idle(ctx context.Context) {}")
	assert.Contains(t, got, `"context"`)
	assert.Contains(t, got, "// Idle is called by nothing.", "the doc comment stays put")
}

func TestSetSignatureKeepsTheHalfNotGiven(t *testing.T) {
	t.Parallel()

	p := load(t, "decls")

	sym, err := p.Resolve(Query{Kind: KindFunc, Name: "recursive"})
	require.NoError(t, err)

	// Only the results change, so the parameter list must survive untouched.
	_, err = p.SetSignature(sym, Signature{Results: "(int, error)", SetResults: true})
	require.NoError(t, err)

	edits, err := p.Edits()
	require.NoError(t, err)
	assert.Contains(t, modified(t, edits, "app.go"), "func recursive(n int) (int, error) {")
}

func TestSetSignatureErrors(t *testing.T) {
	t.Parallel()

	t.Run("nothing to change", func(t *testing.T) {
		t.Parallel()

		p := load(t, "decls")
		sym, err := p.Resolve(Query{Kind: KindFunc, Name: "orphan"})
		require.NoError(t, err)

		_, err = p.SetSignature(sym, Signature{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "nothing to change")
	})

	t.Run("not a function", func(t *testing.T) {
		t.Parallel()

		p := load(t, "decls")
		sym, err := p.Resolve(Query{Kind: KindType, Name: "Used"})
		require.NoError(t, err)

		_, err = p.SetSignature(sym, Signature{Params: "", SetParams: true})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is not a func or a method")
	})

	t.Run("interface method", func(t *testing.T) {
		t.Parallel()

		p := load(t, "methods")
		sym, err := p.Resolve(Query{Kind: KindMethod, Owner: "Saver", Name: "Save"})
		require.NoError(t, err)

		_, err = p.SetSignature(sym, Signature{Params: "n int", SetParams: true})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "is declared in an interface")
	})
}

func TestAddErrors(t *testing.T) {
	t.Parallel()

	file := filepath.Join("testdata", "decls", "app", "app.go")

	tests := []struct {
		desc string
		decl Declaration
		want string
	}{
		{
			desc: "name already declared",
			decl: Declaration{File: file, Kind: KindType, Name: "Used"},
			want: `already declares "Used"`,
		},
		{
			desc: "method already declared",
			decl: Declaration{File: file, Kind: KindMethod, Name: "Idle", Recv: "w Widget"},
			want: "Widget already has a method called Idle",
		},
		{
			desc: "receiver type does not exist",
			decl: Declaration{File: file, Kind: KindMethod, Name: "Do", Recv: "n *Nope"},
			want: "does not declare a type called Nope",
		},
		{
			desc: "unresolvable qualifier",
			decl: Declaration{File: file, Kind: KindFunc, Name: "Fresh", Params: "t nope.Thing"},
			want: `cannot resolve the package qualifier nope`,
		},
		{
			desc: "file outside the module",
			decl: Declaration{File: filepath.Join("testdata", "decls", "nope.go"), Kind: KindType, Name: "Fresh"},
			want: "is not a Go file in this module",
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			p := load(t, "decls")
			_, err := p.Add(tt.decl)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestLoadRefusesCodeThatDoesNotCompile(t *testing.T) {
	t.Parallel()

	dir, err := filepath.Abs(filepath.Join("testdata", "broken"))
	require.NoError(t, err)

	_, err = Load(Request{
		Dir: dir,
		Env: append(os.Environ(), "GO111MODULE=on", "GOWORK=off"),
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "does not type-check")
	assert.Contains(t, err.Error(), "cannot use \"not a number\"")
}

// TestLoadIgnoresGopathMode pins the guarantee that makes this engine usable
// from gopatch's own test binary: TestIntegration sets GO111MODULE=off for the
// whole process, and in GOPATH mode packages get synthetic "_/abs/path" import
// paths and cannot refer to one another, so no cross-package reference would
// ever be found.
//
// Not parallel, because it changes the process environment.
func TestLoadIgnoresGopathMode(t *testing.T) {
	t.Setenv("GO111MODULE", "off")

	dir, err := filepath.Abs(filepath.Join("testdata", "rename_field"))
	require.NoError(t, err)

	p, err := Load(Request{Dir: dir})
	require.NoError(t, err)

	sym, err := p.Resolve(Query{Kind: KindField, Owner: "User", Name: "Name"})
	require.NoError(t, err)

	count, err := p.Rename(sym, "Title")
	require.NoError(t, err)
	assert.Equal(t, 5, count, "the reference in the importing package was found")
}

func TestLoadFindsTheModuleRootFromASubdirectory(t *testing.T) {
	t.Parallel()

	dir, err := filepath.Abs(filepath.Join("testdata", "rename_field", "user"))
	require.NoError(t, err)

	p, err := Load(Request{
		Dir: dir,
		Env: append(os.Environ(), "GO111MODULE=on", "GOWORK=off"),
	})
	require.NoError(t, err)

	sym, err := p.Resolve(Query{Kind: KindField, Owner: "User", Name: "Name"})
	require.NoError(t, err)

	// Standing in user/ must not hide the reference in consumer/.
	count, err := p.Rename(sym, "Title")
	require.NoError(t, err)
	assert.Equal(t, 5, count)
}

func TestModuleRoot(t *testing.T) {
	t.Parallel()

	t.Run("found above the given directory", func(t *testing.T) {
		t.Parallel()

		root := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"),
			[]byte("module example.com/x\n\ngo 1.22\n"), 0o644))

		nested := filepath.Join(root, "a", "b")
		require.NoError(t, os.MkdirAll(nested, 0o755))

		got, err := moduleRoot(nested)
		require.NoError(t, err)
		assert.Equal(t, root, got)
	})

	t.Run("absent", func(t *testing.T) {
		t.Parallel()

		// The filesystem root is the one directory that reliably has no module
		// above it; a temporary directory does not, because the go command and
		// other tools leave go.mod files there.
		_, err := moduleRoot(string(filepath.Separator))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no go.mod at or above")
	})
}

func TestEditsAreEmptyWithoutChanges(t *testing.T) {
	t.Parallel()

	p := load(t, "decls")

	edits, err := p.Edits()
	require.NoError(t, err)
	assert.Empty(t, edits)
}
