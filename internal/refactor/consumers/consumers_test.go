package consumers

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFind(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	producer := writeModule(t, root, "producer", "module example.com/producer\n\ngo 1.23\n")
	direct := writeModule(t, root, "repos/direct", "module example.com/direct\n\ngo 1.23\n\nrequire example.com/producer v1.0.0\n")
	indirect := writeModule(t, root, "repos/nested/indirect", "module example.com/indirect\n\ngo 1.23\n\nrequire example.com/producer v1.0.0 // indirect\n")
	writeModule(t, root, "repos/unrelated", "module example.com/unrelated\n\ngo 1.23\n")
	writeModule(t, root, "repos/testdata/fixture", "module example.com/fixture\n\ngo 1.23\n\nrequire example.com/producer v1.0.0\n")

	got, err := Find(Request{
		ProducerRoot: producer,
		Roots:        []string{filepath.Join(root, "repos"), direct},
	})
	require.NoError(t, err)
	assert.Equal(t, []Module{
		{Root: direct, Path: "example.com/direct"},
		{Root: indirect, Path: "example.com/indirect"},
	}, got)
}

func TestFindRejectsMalformedModule(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	producer := writeModule(t, root, "producer", "module example.com/producer\n\ngo 1.23\n")
	writeModule(t, root, "repos/broken", "not a module\n")

	_, err := Find(Request{ProducerRoot: producer, Roots: []string{filepath.Join(root, "repos")}})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "parse")
}

func writeModule(t *testing.T, root, name, contents string) string {
	t.Helper()

	dir := filepath.Join(root, name)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(contents), 0o644))
	resolved, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	return resolved
}
