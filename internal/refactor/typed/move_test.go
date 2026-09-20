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
})

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
