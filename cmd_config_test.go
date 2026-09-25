package main

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uber-go/gopatch/internal/refactor/typed"
)

var _ = Describe("consumer configuration", func() {
	It("uses project config instead of home config, including when project consumers are empty", func() {
		root := GinkgoT().TempDir()
		home := GinkgoT().TempDir()
		homeConsumers := filepath.Join(home, "repos")
		require.NoError(GinkgoT(), os.Mkdir(homeConsumers, 0o755))
		require.NoError(GinkgoT(), os.WriteFile(filepath.Join(home, configName), []byte("consumers:\n  - repos\n"), 0o644))
		require.NoError(GinkgoT(), os.WriteFile(filepath.Join(root, configName), nil, 0o644))

		cmd := configCommand(root, home)
		selection, err := cmd.consumerRoots(root, &consumerOptions{})
		require.NoError(GinkgoT(), err)
		assert.Empty(GinkgoT(), selection.Roots)
		assert.False(GinkgoT(), selection.Explicit)
	})

	It("uses home config when the project has none", func() {
		root := GinkgoT().TempDir()
		home := GinkgoT().TempDir()
		repos := filepath.Join(home, "repos")
		require.NoError(GinkgoT(), os.Mkdir(repos, 0o755))
		require.NoError(GinkgoT(), os.WriteFile(filepath.Join(home, configName), []byte("consumers:\n  - ~/repos\n"), 0o644))

		selection, err := configCommand(root, home).consumerRoots(root, &consumerOptions{})
		require.NoError(GinkgoT(), err)
		assert.Equal(GinkgoT(), []string{canonicalTestPath(repos)}, selection.Roots)
	})

	It("lets CLI roots replace config roots and marks them explicit", func() {
		root := GinkgoT().TempDir()
		home := GinkgoT().TempDir()
		configured := filepath.Join(root, "configured")
		selected := filepath.Join(root, "selected")
		require.NoError(GinkgoT(), os.Mkdir(configured, 0o755))
		require.NoError(GinkgoT(), os.Mkdir(selected, 0o755))
		require.NoError(GinkgoT(), os.WriteFile(filepath.Join(root, configName), []byte("consumers:\n  - configured\n"), 0o644))

		selection, err := configCommand(root, home).consumerRoots(root, &consumerOptions{Consumers: []string{"selected"}})
		require.NoError(GinkgoT(), err)
		assert.Equal(GinkgoT(), []string{canonicalTestPath(selected)}, selection.Roots)
		assert.True(GinkgoT(), selection.Explicit)
	})

	It("rejects unknown keys and missing roots", func() {
		root := GinkgoT().TempDir()
		home := GinkgoT().TempDir()
		cmd := configCommand(root, home)
		require.NoError(GinkgoT(), os.WriteFile(filepath.Join(root, configName), []byte("consumerz: []\n"), 0o644))
		_, err := cmd.consumerRoots(root, &consumerOptions{})
		require.Error(GinkgoT(), err)
		assert.Contains(GinkgoT(), err.Error(), "field consumerz not found")

		require.NoError(GinkgoT(), os.WriteFile(filepath.Join(root, configName), []byte("consumers:\n  - missing\n"), 0o644))
		_, err = cmd.consumerRoots(root, &consumerOptions{})
		require.Error(GinkgoT(), err)
		assert.Contains(GinkgoT(), err.Error(), "missing")
	})
})

var _ = Describe("transactional file edits", func() {
	It("preserves the mode of an updated file", func() {
		root := GinkgoT().TempDir()
		path := filepath.Join(root, "value.go")
		require.NoError(GinkgoT(), os.WriteFile(path, []byte("before"), 0o600))
		require.NoError(GinkgoT(), applyFileEdits([]typed.FileEdit{
			{Path: path, Original: []byte("before"), Modified: []byte("after")},
		}))
		info, err := os.Stat(path)
		require.NoError(GinkgoT(), err)
		assert.Equal(GinkgoT(), os.FileMode(0o600), info.Mode().Perm())
	})
})

func configCommand(root, home string) *mainCmd {
	return &mainCmd{
		Getwd:       func() (string, error) { return root, nil },
		UserHomeDir: func() (string, error) { return home, nil },
	}
}

func canonicalTestPath(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	require.NoError(GinkgoT(), err)
	return resolved
}
