package consumers

import (
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImportConsumers(t *testing.T) {
	RunSpecs(t, "Import Consumer Suite")
}

var _ = Describe("FindImport", func() {
	It("matches an exact imported path without requiring a module dependency", func() {
		root := GinkgoT().TempDir()
		matched := writeImportModule(root, "repos/matched", "example.com/matched")
		other := writeImportModule(root, "repos/other", "example.com/other")
		require.NoError(GinkgoT(), os.WriteFile(filepath.Join(matched, "matched.go"), []byte("package matched\n\nimport _ \"third.party/exact\"\n"), 0o644))
		require.NoError(GinkgoT(), os.WriteFile(filepath.Join(other, "other.go"), []byte("package other\n\nimport _ \"third.party/exact/sub\"\n"), 0o644))

		modules, err := FindImport(ImportRequest{
			Roots: []string{filepath.Join(root, "repos")}, ImportPath: "third.party/exact",
		})
		require.NoError(GinkgoT(), err)
		assert.Equal(GinkgoT(), []Module{{Root: matched, Path: "example.com/matched"}}, modules)
	})
})

func writeImportModule(root, name, modulePath string) string {
	dir := filepath.Join(root, name)
	require.NoError(GinkgoT(), os.MkdirAll(dir, 0o755))
	require.NoError(GinkgoT(), os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module "+modulePath+"\n\ngo 1.23\n"), 0o644))
	resolved, err := filepath.EvalSymlinks(dir)
	require.NoError(GinkgoT(), err)
	return resolved
}
