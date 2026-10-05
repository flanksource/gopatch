package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/flanksource/uir/indexer"
	"github.com/flanksource/uir/storage"
	. "github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func indexedRenameDatabase(roots ...string) string {
	GinkgoHelper()
	dsn := filepath.Join(GinkgoT().TempDir(), "uir.db")
	database, err := storage.UirDB(context.Background(), storage.DBOptions{DSN: dsn})
	require.NoError(GinkgoT(), err)
	engine, err := indexer.New(database)
	require.NoError(GinkgoT(), err)
	for _, root := range roots {
		_, err = engine.IndexModules(context.Background(), indexer.ModuleOptions{Path: root, IncludeTests: true})
		require.NoError(GinkgoT(), err)
	}
	sqlDB, err := database.DB()
	require.NoError(GinkgoT(), err)
	require.NoError(GinkgoT(), sqlDB.Close())
	return dsn
}

var _ = Describe("typed top-level rename", func() {
	It("requires an existing UIR index before changing a symbol", func() {
		GinkgoT().Setenv("GOWORK", "off")
		root := moveFixture(map[string]string{
			"model/user.go": "package model\n\ntype User struct{}\nvar Current User\n",
		})
		cmd, _, _ := moveCommand(root)
		err := cmd.Run([]string{"rename", "struct", "User", "Person", "./..."})
		require.Error(GinkgoT(), err)
		assert.Contains(GinkgoT(), err.Error(), "--uir-dsn is required")
		assertFileContains(filepath.Join(root, "model", "user.go"), "type User struct{}")
	})

	It("uses the existing UIR index and refuses a stale snapshot before writing", func() {
		GinkgoT().Setenv("GOWORK", "off")
		root := moveFixture(map[string]string{
			"model/user.go": "package model\n\ntype User struct{}\nvar Current User\n",
		})
		dsn := indexedRenameDatabase(root)
		cmd, _, _ := moveCommand(root)
		require.NoError(GinkgoT(), cmd.Run([]string{"rename", "struct", "User", "Person", "--uir-dsn", dsn, "--diff", "./..."}))
		writeMoveFiles(root, map[string]string{"model/user.go": "package model\n\ntype User struct{}\nvar Current User\nvar Added User\n"})
		cmd, _, _ = moveCommand(root)
		err := cmd.Run([]string{"rename", "struct", "User", "Person", "--uir-dsn", dsn, "./..."})
		require.Error(GinkgoT(), err)
		assert.Contains(GinkgoT(), err.Error(), "stale")
		assertFileContains(filepath.Join(root, "model", "user.go"), "type User struct{}")
	})
	It("renames only references bound to the selected struct", func() {
		GinkgoT().Setenv("GOWORK", "off")
		root := moveFixture(map[string]string{
			"model/user.go": "package model\n\ntype User struct{}\n\nfunc Build() User { return User{} }\n\nfunc shadow() { User := 1; _ = User }\n",
		})
		dsn := indexedRenameDatabase(root)
		cmd, _, stderr := moveCommand(root)
		require.NoError(GinkgoT(), cmd.Run([]string{"rename", "struct", "User", "Person", "--uir-dsn", dsn, "./..."}))

		contents, err := os.ReadFile(filepath.Join(root, "model", "user.go"))
		require.NoError(GinkgoT(), err)
		assert.Contains(GinkgoT(), string(contents), "type Person struct")
		assert.Contains(GinkgoT(), string(contents), "func Build() Person { return Person{} }")
		assert.Contains(GinkgoT(), string(contents), "User := 1")
		assert.Contains(GinkgoT(), stderr.String(), "✏️ target update model/user.go")
	})

	It("rejects a non-struct target for rename struct", func() {
		GinkgoT().Setenv("GOWORK", "off")
		root := moveFixture(map[string]string{
			"model/value.go": "package model\n\ntype Value interface{ String() string }\n",
		})
		cmd, _, _ := moveCommand(root)
		err := cmd.Run([]string{"rename", "struct", "Value", "Result", "./..."})
		require.Error(GinkgoT(), err)
		assert.Contains(GinkgoT(), err.Error(), "not a struct type")
	})

	It("prints planned edits to stderr in print-only mode", func() {
		GinkgoT().Setenv("GOWORK", "off")
		root := moveFixture(map[string]string{
			"model/user.go": "package model\n\ntype User struct{}\n",
		})
		dsn := indexedRenameDatabase(root)
		cmd, stdout, stderr := moveCommand(root)
		require.NoError(GinkgoT(), cmd.Run([]string{
			"rename", "struct", "User", "Person", "--print-only", "--uir-dsn", dsn, "./...",
		}))
		assert.Contains(GinkgoT(), stdout.String(), "type Person struct")
		assert.NotContains(GinkgoT(), stdout.String(), "target update")
		assert.Contains(GinkgoT(), stderr.String(), "✏️ target update model/user.go")
		assertFileContains(filepath.Join(root, "model", "user.go"), "type User struct")
	})

	It("updates requiring consumers from the active Go workspace", func() {
		workspace := GinkgoT().TempDir()
		writeMoveFiles(workspace, map[string]string{
			"go.work":                "go 1.23\n\nuse (\n\t./producer\n\t./consumer\n)\n",
			"producer/go.mod":        "module example.com/producer\n\ngo 1.23\n",
			"producer/model/user.go": "package model\n\ntype User struct{}\n",
			"consumer/go.mod":        "module example.com/consumer\n\ngo 1.23\n\nrequire example.com/producer v0.0.0\n",
			"consumer/app/app.go":    "package app\n\nimport \"example.com/producer/model\"\n\nvar User model.User\n",
		})
		producer := filepath.Join(workspace, "producer")
		canonicalWorkspace, err := filepath.EvalSymlinks(workspace)
		require.NoError(GinkgoT(), err)
		GinkgoT().Setenv("GOWORK", filepath.Join(canonicalWorkspace, "go.work"))
		dsn := indexedRenameDatabase(producer, filepath.Join(workspace, "consumer"))
		cmd, _, _ := moveCommand(producer)
		require.NoError(GinkgoT(), cmd.Run([]string{"rename", "struct", "User", "Person", "--uir-dsn", dsn, "./..."}))

		assertFileContains(filepath.Join(workspace, "consumer", "app", "app.go"), "model.Person")
	})
})

var _ = Describe("typed package and import rename", func() {
	It("renames package clauses and only unaliased bound qualifiers", func() {
		root := moveFixture(map[string]string{
			"library/value.go": "package old\n\nconst Value = 1\n",
			"app/plain.go":     "package app\n\nimport \"example.com/move/library\"\n\nconst Plain = old.Value\n",
			"app/alias.go":     "package app\n\nimport legacy \"example.com/move/library\"\n\nconst Alias = legacy.Value\n",
		})
		cmd, _, _ := moveCommand(root)
		require.NoError(GinkgoT(), cmd.Run([]string{"rename", "package", "old", "newname", "./library"}))

		assertFileContains(filepath.Join(root, "library", "value.go"), "package newname")
		assertFileContains(filepath.Join(root, "app", "plain.go"), "newname.Value")
		assertFileContains(filepath.Join(root, "app", "alias.go"), "legacy.Value")
	})

	It("renames an exact import path and preserves explicit aliases", func() {
		root := moveFixture(map[string]string{
			"old/value.go":    "package old\n\nconst Value = 1\n",
			"newpkg/value.go": "package newpkg\n\nconst Value = 1\n",
			"app/plain.go":    "package app\n\nimport \"example.com/move/old\"\n\nconst Plain = old.Value\n",
			"app/alias.go":    "package app\n\nimport legacy \"example.com/move/old\"\n\nconst Alias = legacy.Value\n",
		})
		cmd, _, _ := moveCommand(root)
		require.NoError(GinkgoT(), cmd.Run([]string{
			"rename", "import", "example.com/move/old", "example.com/move/newpkg", "./app",
		}))

		assertFileContains(filepath.Join(root, "app", "plain.go"), `"example.com/move/newpkg"`, "newpkg.Value")
		assertFileContains(filepath.Join(root, "app", "alias.go"), `legacy "example.com/move/newpkg"`, "legacy.Value")
	})

	It("discovers configured consumers by exact import path without a module requirement", func() {
		workspace := GinkgoT().TempDir()
		writeMoveFiles(workspace, map[string]string{
			"producer/go.mod":       "module example.com/producer\n\ngo 1.23\n",
			"producer/producer.go":  "package producer\n",
			"producer/.gopatch.yml": "consumers:\n  - ../consumer\n",
			"consumer/go.mod":       "module example.com/consumer\n\ngo 1.23\n",
			"consumer/old/value.go": "package old\n\nconst Value = 1\n",
			"consumer/new/value.go": "package new\n\nconst Value = 1\n",
			"consumer/app/app.go":   "package app\n\nimport \"example.com/consumer/old\"\n\nconst Value = old.Value\n",
		})
		producer := filepath.Join(workspace, "producer")
		cmd, _, stderr := moveCommand(producer)
		require.NoError(GinkgoT(), cmd.Run([]string{
			"rename", "import", "example.com/consumer/old", "example.com/consumer/new", "./...",
		}))

		assertFileContains(filepath.Join(workspace, "consumer", "app", "app.go"), `"example.com/consumer/new"`, "new.Value")
		assert.Contains(GinkgoT(), stderr.String(), "consumer example.com/consumer update app/app.go")
	})

	It("prints planned edits before a later overlay failure without writing", func() {
		root := moveFixture(map[string]string{
			"old/value.go": "package old\n\nconst Value = 1\n",
			"new/value.go": "package client\n\nconst Value = 1\n",
			"app/app.go":   "package app\n\nimport \"example.com/move/old\"\n\nconst Value = old.Value\n",
		})
		cmd, _, stderr := moveCommand(root)
		err := cmd.Run([]string{"rename", "import", "example.com/move/old", "example.com/move/new", "./app"})
		require.Error(GinkgoT(), err)
		assert.Contains(GinkgoT(), stderr.String(), "✏️ target update app/app.go")
		assertFileContains(filepath.Join(root, "app", "app.go"), `"example.com/move/old"`, "old.Value")
	})

	It("treats configured import consumers with no match as a successful no-op", func() {
		root := moveFixture(map[string]string{
			"producer.go": "package move\n",
			"repos/.keep": "",
			configName:    "consumers:\n  - repos\n",
		})
		cmd, _, stderr := moveCommand(root)
		require.NoError(GinkgoT(), cmd.Run([]string{
			"rename", "import", "third.party/old", "third.party/new", "./...",
		}))
		assert.Empty(GinkgoT(), stderr.String())
	})
})
