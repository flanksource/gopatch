package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uber-go/gopatch/internal/refactor/typed"
)

func TestTypedRenameConsumerFlags(t *testing.T) {
	t.Parallel()

	root := mustRootCommand(t)
	for _, args := range [][]string{{"rename", "field"}, {"rename", "method"}} {
		command, _, err := root.Find(args)
		require.NoError(t, err)
		assert.NotNil(t, command.Flags().Lookup("consumers"))
		assert.NotNil(t, command.Flags().Lookup("consumers-from"))
	}

	command, _, err := root.Find([]string{"delete", "method"})
	require.NoError(t, err)
	assert.Nil(t, command.Flags().Lookup("consumers"))
	assert.Nil(t, command.Flags().Lookup("consumers-from"))
}

func TestCheckEditScopeRefusesUncoveredReference(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	inside := filepath.Join(root, "inside.go")
	outside := filepath.Join(root, "outside.go")
	require.NoError(t, os.WriteFile(inside, []byte("package p\n"), 0o644))
	require.NoError(t, os.WriteFile(outside, []byte("package p\n"), 0o644))

	err := checkEditScope(root, []typed.FileEdit{{Path: outside}}, []string{inside})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside.go")
	assert.Contains(t, err.Error(), "nothing was written")
}

func TestEmitChangesValidatesTheWholeBatchBeforeWriting(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	first := filepath.Join(root, "first.go")
	second := filepath.Join(root, "second.go")
	require.NoError(t, os.WriteFile(first, []byte("first before"), 0o644))
	require.NoError(t, os.WriteFile(second, []byte("second changed"), 0o644))

	cmd := mainCmd{Stdout: bytes.NewBuffer(nil), Stderr: bytes.NewBuffer(nil)}
	err := cmd.emitChanges(&options{}, []sourceChange{
		{Path: sourcePath{Absolute: first}, Original: []byte("first before"), Modified: []byte("first after")},
		{Path: sourcePath{Absolute: second}, Original: []byte("second before"), Modified: []byte("second after")},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "changed after it was loaded")
	got, readErr := os.ReadFile(first)
	require.NoError(t, readErr)
	assert.Equal(t, "first before", string(got))
}

func TestConsumerRootsRejectsAnEmptyList(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	list := filepath.Join(root, "consumers.txt")
	require.NoError(t, os.WriteFile(list, []byte("\n  \n"), 0o644))
	cmd := mainCmd{Getwd: func() (string, error) { return root, nil }}

	_, err := cmd.consumerRoots(&typedRenameOptions{ConsumersFrom: list})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "at least one non-empty search directory")
}
