package typed

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMoveEngine(t *testing.T) {
	RunSpecs(t, "Typed Move Suite")
}

var _ = Describe("Move", func() {
	It("moves a type and its methods while rewriting typed references", func() {
		root := GinkgoT().TempDir()
		writeTypedMoveFile(root, "go.mod", "module example.com/move\n\ngo 1.23\n")
		writeTypedMoveFile(root, "old/user.go", "package old\n\ntype User struct{ Name string }\n\nfunc (u User) String() string { return u.Name }\n")
		writeTypedMoveFile(root, "old/use.go", "package old\n\nfunc Build() User { return User{} }\n")
		writeTypedMoveFile(root, "new/doc.go", "package new\n")

		program, err := Load(Request{Dir: root, Env: append(os.Environ(), "GO111MODULE=on", "GOWORK=off")})
		require.NoError(GinkgoT(), err)
		symbol, err := program.Resolve(Query{Kind: KindType, Package: "old", Name: "User"})
		require.NoError(GinkgoT(), err)
		result, err := program.Move(MoveRequest{Symbol: symbol, Destination: filepath.Join(root, "new", "user.go")})
		require.NoError(GinkgoT(), err)

		assert.Equal(GinkgoT(), "example.com/move/old", result.SourcePackage)
		assert.Equal(GinkgoT(), "example.com/move/new", result.TargetPackage)
		assert.ElementsMatch(GinkgoT(), []string{"method User.String", "type User"}, result.Declarations)
		assertMoveEdit(GinkgoT(), result.Edits, filepath.Join(root, "old", "user.go"), true, "")
		assertMoveEdit(GinkgoT(), result.Edits, filepath.Join(root, "new", "user.go"), false, "func (u User) String() string")
		assertMoveEdit(GinkgoT(), result.Edits, filepath.Join(root, "old", "use.go"), false, "new.User")
	})

	It("moves one spec of a grouped const block to another package and leaves its siblings qualified", func() {
		root := GinkgoT().TempDir()
		writeTypedMoveFile(root, "go.mod", "module example.com/move\n\ngo 1.23\n")
		writeTypedMoveFile(root, "sqltext/classify.go", "package sqltext\n\ntype StatementType string\n")
		writeTypedMoveFile(root, "xetrace/classify.go", "package xetrace\n\nimport \"example.com/move/sqltext\"\n\nconst (\n\tStmtSelect sqltext.StatementType = \"SELECT\"\n\tStmtInsert sqltext.StatementType = \"INSERT\"\n)\n")
		writeTypedMoveFile(root, "xetrace/use.go", "package xetrace\n\nimport \"example.com/move/sqltext\"\n\nfunc IsRead(t sqltext.StatementType) bool { return t == StmtSelect && t != StmtInsert }\n")

		result := moveTyped(root, Query{Kind: KindConst, Package: "xetrace", Name: "StmtSelect"}, filepath.Join(root, "sqltext", "statement.go"))

		assert.Equal(GinkgoT(), []string{"const StmtSelect"}, result.Declarations)
		source := moveEditContents(GinkgoT(), result.Edits, filepath.Join(root, "xetrace", "classify.go"))
		assert.Contains(GinkgoT(), source, "StmtInsert sqltext.StatementType = \"INSERT\"")
		assert.NotContains(GinkgoT(), source, "StmtSelect")
		assertMoveEdit(GinkgoT(), result.Edits, filepath.Join(root, "sqltext", "statement.go"), false, "const StmtSelect StatementType = \"SELECT\"")
		assertMoveEdit(GinkgoT(), result.Edits, filepath.Join(root, "xetrace", "use.go"), false, "t == sqltext.StmtSelect && t != StmtInsert")
		require.NoError(GinkgoT(), ValidateEdits(root, result.Edits))
	})

	It("moves an exported var within its package even when it depends on unexported declarations", func() {
		root := GinkgoT().TempDir()
		writeTypedMoveFile(root, "go.mod", "module example.com/move\n\ngo 1.23\n")
		writeTypedMoveFile(root, "xetrace/classify.go", "package xetrace\n\nvar FilterableTypes = []string{dmlGroup}\n\nfunc Classify() string { return dmlGroup }\n")
		writeTypedMoveFile(root, "xetrace/filter.go", "package xetrace\n\nconst dmlGroup = \"dml\"\n")

		result := moveTyped(root, Query{Kind: KindVar, Package: "xetrace", Name: "FilterableTypes"}, filepath.Join(root, "xetrace", "filter.go"))

		assert.Equal(GinkgoT(), result.SourcePackage, result.TargetPackage)
		assert.NotContains(GinkgoT(), moveEditContents(GinkgoT(), result.Edits, filepath.Join(root, "xetrace", "classify.go")), "FilterableTypes")
		assertMoveEdit(GinkgoT(), result.Edits, filepath.Join(root, "xetrace", "filter.go"), false, "var FilterableTypes = []string{dmlGroup}")
		require.NoError(GinkgoT(), ValidateEdits(root, result.Edits))
	})

	It("moves an unexported func within its package while a test file in that package still calls it", func() {
		root := GinkgoT().TempDir()
		writeTypedMoveFile(root, "go.mod", "module example.com/move\n\ngo 1.23\n")
		writeTypedMoveFile(root, "calc/calc.go", "package calc\n\nfunc double(n int) int { return n * scale }\n\nconst scale = 2\n\nfunc Quad(n int) int { return double(double(n)) }\n")
		writeTypedMoveFile(root, "calc/calc_test.go", "package calc\n\nimport \"testing\"\n\nfunc TestDouble(t *testing.T) {\n\tif double(2) != 4 {\n\t\tt.Fatal(\"double\")\n\t}\n}\n")

		result := moveTyped(root, Query{Kind: KindFunc, Package: "calc", Name: "double"}, filepath.Join(root, "calc", "double.go"))

		assertMoveEdit(GinkgoT(), result.Edits, filepath.Join(root, "calc", "double.go"), false, "func double(n int) int { return n * scale }")
		assert.NotContains(GinkgoT(), moveEditContents(GinkgoT(), result.Edits, filepath.Join(root, "calc", "calc.go")), "func double")
		require.NoError(GinkgoT(), ValidateEdits(root, result.Edits))
	})

	It("names the declaration site of an unexported dependency that blocks a cross-package move", func() {
		root := GinkgoT().TempDir()
		writeTypedMoveFile(root, "go.mod", "module example.com/move\n\ngo 1.23\n")
		writeTypedMoveFile(root, "old/user.go", "package old\n\ntype User struct{ value private }\n")
		writeTypedMoveFile(root, "old/private.go", "package old\n\ntype private struct{}\n")
		writeTypedMoveFile(root, "new/doc.go", "package new\n")

		program, err := Load(Request{Dir: root, Env: append(os.Environ(), "GO111MODULE=on", "GOWORK=off")})
		require.NoError(GinkgoT(), err)
		symbol, err := program.Resolve(Query{Kind: KindType, Package: "old", Name: "User"})
		require.NoError(GinkgoT(), err)
		_, err = program.Move(MoveRequest{Symbol: symbol, Destination: filepath.Join(root, "new", "user.go")})

		require.Error(GinkgoT(), err)
		assert.Equal(GinkgoT(), "moved code depends on unexported declarations that remain in example.com/move/old: old/private.go:3:6 (private)", err.Error())
	})
})

func moveTyped(root string, query Query, destination string) MoveResult {
	program, err := Load(Request{Dir: root, Env: append(os.Environ(), "GO111MODULE=on", "GOWORK=off")})
	require.NoError(GinkgoT(), err)
	symbol, err := program.Resolve(query)
	require.NoError(GinkgoT(), err)
	result, err := program.Move(MoveRequest{Symbol: symbol, Destination: destination})
	require.NoError(GinkgoT(), err)
	return result
}

func moveEditContents(t GinkgoTInterface, edits []FileEdit, path string) string {
	t.Helper()
	path = realFuturePath(path)
	for _, edit := range edits {
		if edit.Path == path {
			return string(edit.Modified)
		}
	}
	t.Errorf("no move edit for %s", path)
	return ""
}

func writeTypedMoveFile(root, name, contents string) {
	path := filepath.Join(root, name)
	require.NoError(GinkgoT(), os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(GinkgoT(), os.WriteFile(path, []byte(contents), 0o644))
}

func assertMoveEdit(t GinkgoTInterface, edits []FileEdit, path string, deleted bool, contains string) {
	t.Helper()
	path = realFuturePath(path)
	for _, edit := range edits {
		if edit.Path != path {
			continue
		}
		assert.Equal(t, deleted, edit.Delete)
		if contains != "" {
			assert.Contains(t, string(edit.Modified), contains)
		}
		return
	}
	t.Errorf("no move edit for %s", path)
}
