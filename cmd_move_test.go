package main

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uber-go/gopatch/internal/refactor/typed"
)

func TestMove(t *testing.T) {
	RunSpecs(t, "Move Command Suite")
}

var _ = Describe("move", func() {
	It("moves a file within its package", func() {
		root := GinkgoT().TempDir()
		require.NoError(GinkgoT(), os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/move\n\ngo 1.23\n"), 0o644))
		require.NoError(GinkgoT(), os.Mkdir(filepath.Join(root, "source"), 0o755))
		require.NoError(GinkgoT(), os.Mkdir(filepath.Join(root, "dest"), 0o755))
		require.NoError(GinkgoT(), os.WriteFile(filepath.Join(root, "source", "value.go"), []byte("package source\n\nconst Value = 1\n"), 0o644))
		require.NoError(GinkgoT(), os.WriteFile(filepath.Join(root, "dest", "doc.go"), []byte("package dest\n"), 0o644))

		var stdout, stderr bytes.Buffer
		cmd := mainCmd{
			Stdin:  bytes.NewReader(nil),
			Stdout: &stdout,
			Stderr: &stderr,
			Getwd:  func() (string, error) { return root, nil },
		}
		require.NoError(GinkgoT(), cmd.Run([]string{"move", "file", "source/value.go", "source/moved.go"}))

		_, err := os.Stat(filepath.Join(root, "source", "value.go"))
		assert.True(GinkgoT(), os.IsNotExist(err))
		contents, err := os.ReadFile(filepath.Join(root, "source", "moved.go"))
		require.NoError(GinkgoT(), err)
		assert.Equal(GinkgoT(), "package source\n\nconst Value = 1\n", string(contents))
	})

	It("moves a file to another package and updates module consumers", func() {
		root := moveFixture(map[string]string{
			"old/value.go": "package old\n\nconst Value = 1\n",
			"new/doc.go":   "package new\n",
			"app/app.go":   "package app\n\nimport \"example.com/move/old\"\n\nconst Value = old.Value\n",
		})

		cmd, _, _ := moveCommand(root)
		require.NoError(GinkgoT(), cmd.Run([]string{"move", "file", "old/value.go", "new"}))

		assertFileMissing(filepath.Join(root, "old", "value.go"))
		assertFileContains(filepath.Join(root, "new", "value.go"), "package new", "const Value = 1")
		assertFileContains(filepath.Join(root, "app", "app.go"), `"example.com/move/new"`, "new.Value")
	})

	It("rewrites blank imports when the source package is fully relocated", func() {
		root := moveFixture(map[string]string{
			"old/register.go": "package old\n\nfunc init() {}\n",
			"app/app.go":      "package app\n\nimport _ \"example.com/move/old\"\n",
		})
		cmd, _, _ := moveCommand(root)
		require.NoError(GinkgoT(), cmd.Run([]string{"move", "file", "old/register.go", "new"}))

		assertFileContains(filepath.Join(root, "app", "app.go"), `_ "example.com/move/new"`)
	})

	It("moves a struct with all receiver methods", func() {
		root := moveFixture(map[string]string{
			"old/user.go":  "package old\n\ntype User struct{ Name string }\n\nfunc (u User) String() string { return u.Name }\n",
			"old/build.go": "package old\n\nfunc Build() User { return User{Name: \"a\"} }\n",
			"new/doc.go":   "package new\n",
			"app/app.go":   "package app\n\nimport \"example.com/move/old\"\n\nfunc Name(u old.User) string { return u.String() }\n",
		})

		cmd, _, _ := moveCommand(root)
		require.NoError(GinkgoT(), cmd.Run([]string{"move", "struct", "old.User", "new/user.go"}))

		assertFileMissing(filepath.Join(root, "old", "user.go"))
		assertFileContains(filepath.Join(root, "new", "user.go"), "type User struct", "func (u User) String() string")
		assertFileContains(filepath.Join(root, "old", "build.go"), `"example.com/move/new"`, "new.User")
		assertFileContains(filepath.Join(root, "app", "app.go"), `"example.com/move/new"`, "new.User")
	})

	It("summarizes a dry run without touching files", func() {
		root := moveFixture(map[string]string{
			"old/value.go": "package old\n\nconst Value = 1\n",
			"new/doc.go":   "package new\n",
		})
		cmd, stdout, stderr := moveCommand(root)
		require.NoError(GinkgoT(), cmd.Run([]string{"move", "file", "old/value.go", "new", "--dry-run"}))

		assertFileContains(filepath.Join(root, "old", "value.go"), "package old")
		assertFileMissing(filepath.Join(root, "new", "value.go"))
		assert.Contains(GinkgoT(), stdout.String(), "package example.com/move/old -> example.com/move/new")
		assert.Contains(GinkgoT(), stdout.String(), "1 create")
		assert.Contains(GinkgoT(), stdout.String(), "1 delete")
		assert.NotContains(GinkgoT(), stdout.String(), "const Value = 1")
		assert.Contains(GinkgoT(), stderr.String(), "target delete old/value.go")
		assert.Contains(GinkgoT(), stderr.String(), "target create new/value.go")
	})

	It("prints Clicky-style target changes to stderr in preview modes", func() {
		root := moveFixture(map[string]string{
			"old/value.go": "package old\n\nconst Value = 1\n",
			"new/doc.go":   "package new\n",
		})
		cmd, stdout, stderr := moveCommand(root)
		require.NoError(GinkgoT(), cmd.Run([]string{"move", "file", "old/value.go", "new", "--diff"}))

		assert.Contains(GinkgoT(), stderr.String(), "➖ target delete old/value.go")
		assert.Contains(GinkgoT(), stderr.String(), "➕ target create new/value.go")
		assert.Contains(GinkgoT(), stdout.String(), "--- ")
		assert.NotContains(GinkgoT(), stdout.String(), "target create")
	})

	It("refuses combining dry-run with a full diff", func() {
		root := moveFixture(map[string]string{
			"old/value.go": "package old\n\nconst Value = 1\n",
		})
		cmd, _, _ := moveCommand(root)
		err := cmd.Run([]string{"move", "file", "old/value.go", "new", "--dry-run", "--diff"})

		require.Error(GinkgoT(), err)
		assert.Contains(GinkgoT(), err.Error(), "mutually exclusive")
		assertFileContains(filepath.Join(root, "old", "value.go"), "const Value = 1")
		assertFileMissing(filepath.Join(root, "new", "value.go"))
	})

	It("refuses a cross-package move with a private dependency", func() {
		root := moveFixture(map[string]string{
			"old/user.go": "package old\n\ntype private struct{}\n\ntype User struct{ value private }\n",
			"new/doc.go":  "package new\n",
		})
		cmd, _, _ := moveCommand(root)
		err := cmd.Run([]string{"move", "struct", "old.User", "new/user.go"})
		require.Error(GinkgoT(), err)
		assert.Contains(GinkgoT(), err.Error(), "unexported declarations")
		assertFileContains(filepath.Join(root, "old", "user.go"), "type User")
		assertFileMissing(filepath.Join(root, "new", "user.go"))
	})

	It("updates consumers from the active Go workspace", func() {
		workspace := GinkgoT().TempDir()
		writeMoveFiles(workspace, map[string]string{
			"go.work":               "go 1.23\n\nuse (\n\t./producer\n\t./consumer\n\t./unrelated\n)\n",
			"producer/go.mod":       "module example.com/producer\n\ngo 1.23\n",
			"producer/old/value.go": "package old\n\nconst Value = 1\n",
			"consumer/go.mod":       "module example.com/consumer\n\ngo 1.23\n\nrequire example.com/producer v0.0.0\n",
			"consumer/app/app.go":   "package app\n\nimport \"example.com/producer/old\"\n\nconst Value = old.Value\n",
			"unrelated/go.mod":      "module example.com/unrelated\n\ngo 1.23\n",
			"unrelated/broken.go":   "package unrelated\n\nvar Broken = missing\n",
		})

		cmd, _, _ := moveCommand(filepath.Join(workspace, "producer"))
		require.NoError(GinkgoT(), cmd.Run([]string{"move", "file", "old/value.go", "new/value.go"}))

		assertFileContains(filepath.Join(workspace, "consumer", "app", "app.go"), `"example.com/producer/new"`, "new.Value")
	})

	It("updates explicitly discovered consumer modules", func() {
		workspace := GinkgoT().TempDir()
		writeMoveFiles(workspace, map[string]string{
			"producer/go.mod":       "module example.com/producer\n\ngo 1.23\n",
			"producer/old/value.go": "package old\n\nconst Value = 1\n",
			"consumer/go.mod":       "module example.com/consumer\n\ngo 1.23\n\nrequire example.com/producer v0.0.0\n\nreplace example.com/producer => ../producer\n",
			"consumer/app/app.go":   "package app\n\nimport \"example.com/producer/old\"\n\nconst Value = old.Value\n",
		})

		producer := filepath.Join(workspace, "producer")
		cmd, _, _ := moveCommand(producer)
		require.NoError(GinkgoT(), cmd.Run([]string{"move", "file", "old/value.go", "new/value.go", "--consumers", workspace}))

		assertFileContains(filepath.Join(workspace, "consumer", "app", "app.go"), `"example.com/producer/new"`, "new.Value")
	})

	It("updates consumer modules discovered from project config", func() {
		workspace := GinkgoT().TempDir()
		writeMoveFiles(workspace, map[string]string{
			"producer/go.mod":       "module example.com/producer\n\ngo 1.23\n",
			"producer/.gopatch.yml": "consumers:\n  - ../consumer\n",
			"producer/old/value.go": "package old\n\nconst Value = 1\n",
			"consumer/go.mod":       "module example.com/consumer\n\ngo 1.23\n\nrequire example.com/producer v0.0.0\n\nreplace example.com/producer => ../producer\n",
			"consumer/app/app.go":   "package app\n\nimport \"example.com/producer/old\"\n\nconst Value = old.Value\n",
		})

		producer := filepath.Join(workspace, "producer")
		cmd, _, stderr := moveCommand(producer)
		require.NoError(GinkgoT(), cmd.Run([]string{"move", "file", "old/value.go", "new/value.go"}))

		assertFileContains(filepath.Join(workspace, "consumer", "app", "app.go"), `"example.com/producer/new"`, "new.Value")
		assert.Contains(GinkgoT(), stderr.String(), "consumer example.com/consumer update app/app.go")
	})

	It("moves independent variables and constants", func() {
		root := moveFixture(map[string]string{
			"old/values.go": "package old\n\nvar Current = 3\n\nconst Limit = 5\n",
			"old/use.go":    "package old\n\nfunc Value() int { return Current + Limit }\n",
			"new/doc.go":    "package new\n",
		})
		cmd, _, _ := moveCommand(root)
		require.NoError(GinkgoT(), cmd.Run([]string{"move", "var", "old.Current", "new/current.go"}))

		cmd, _, _ = moveCommand(root)
		require.NoError(GinkgoT(), cmd.Run([]string{"move", "const", "old.Limit", "new/limit.go"}))
		assertFileContains(filepath.Join(root, "new", "current.go"), "var Current = 3")
		assertFileContains(filepath.Join(root, "new", "limit.go"), "const Limit = 5")
		assertFileContains(filepath.Join(root, "old", "use.go"), "new.Current", "new.Limit")
	})

	It("preserves the source mode when a declaration creates a file", func() {
		root := moveFixture(map[string]string{
			"old/value.go": "package old\n\nfunc Value() int { return 1 }\n",
			"old/use.go":   "package old\n\nfunc Use() int { return Value() }\n",
			"new/doc.go":   "package new\n",
		})
		require.NoError(GinkgoT(), os.Chmod(filepath.Join(root, "old", "value.go"), 0o600))
		require.NoError(GinkgoT(), os.Chmod(filepath.Join(root, "old", "use.go"), 0o600))
		cmd, _, _ := moveCommand(root)
		require.NoError(GinkgoT(), cmd.Run([]string{"move", "func", "old.Value", "new/value.go"}))

		info, err := os.Stat(filepath.Join(root, "new", "value.go"))
		require.NoError(GinkgoT(), err)
		assert.Equal(GinkgoT(), os.FileMode(0o600), info.Mode().Perm())
		info, err = os.Stat(filepath.Join(root, "old", "use.go"))
		require.NoError(GinkgoT(), err)
		assert.Equal(GinkgoT(), os.FileMode(0o600), info.Mode().Perm())
	})

	It("refuses unsafe grouped constants", func() {
		root := moveFixture(map[string]string{
			"old/values.go": "package old\n\nconst (\n\tFirst = iota\n\tSecond\n)\n",
			"new/doc.go":    "package new\n",
		})
		cmd, _, _ := moveCommand(root)
		err := cmd.Run([]string{"move", "const", "old.Second", "new/second.go"})
		require.Error(GinkgoT(), err)
		assert.Contains(GinkgoT(), err.Error(), "implicit value or iota")
	})

	It("refuses a name collision in the destination package", func() {
		root := moveFixture(map[string]string{
			"old/user.go": "package old\n\ntype User struct{}\n",
			"new/user.go": "package new\n\ntype User struct{}\n",
		})
		cmd, _, _ := moveCommand(root)
		err := cmd.Run([]string{"move", "struct", "old.User", "new/moved.go"})

		require.Error(GinkgoT(), err)
		assert.Contains(GinkgoT(), err.Error(), `already declares "User"`)
		assertFileContains(filepath.Join(root, "old", "user.go"), "type User")
		assertFileMissing(filepath.Join(root, "new", "moved.go"))
	})

	It("refuses one name from a multi-name declaration", func() {
		root := moveFixture(map[string]string{
			"old/values.go": "package old\n\nvar First, Second = 1, 2\n",
			"new/doc.go":    "package new\n",
		})
		cmd, _, _ := moveCommand(root)
		err := cmd.Run([]string{"move", "var", "old.First", "new/first.go"})

		require.Error(GinkgoT(), err)
		assert.Contains(GinkgoT(), err.Error(), "shares one declaration with other names")
		assertFileContains(filepath.Join(root, "old", "values.go"), "First, Second")
		assertFileMissing(filepath.Join(root, "new", "first.go"))
	})

	It("refuses a stale edit set before staging", func() {
		root := GinkgoT().TempDir()
		path := filepath.Join(root, "value.go")
		require.NoError(GinkgoT(), os.WriteFile(path, []byte("package old\n"), 0o644))

		err := validateFileEdits([]typed.FileEdit{{Path: path, Original: []byte("package before\n"), Modified: []byte("package after\n")}})

		require.Error(GinkgoT(), err)
		assert.Contains(GinkgoT(), err.Error(), "changed after it was loaded")
		assertFileContains(path, "package old")
	})

	It("moves a method within its receiver package and refuses a package change", func() {
		root := moveFixture(map[string]string{
			"old/client.go": "package old\n\ntype Client struct{}\n\nfunc (Client) Close() error { return nil }\n",
			"new/doc.go":    "package new\n",
		})
		cmd, _, _ := moveCommand(root)
		err := cmd.Run([]string{"move", "method", "old.Client.Close", "new/close.go"})
		require.Error(GinkgoT(), err)
		assert.Contains(GinkgoT(), err.Error(), "must remain in the package")

		cmd, _, _ = moveCommand(root)
		require.NoError(GinkgoT(), cmd.Run([]string{"move", "method", "old.Client.Close", "old/close.go"}))
		assertFileContains(filepath.Join(root, "old", "close.go"), "func (Client) Close() error")
		contents, err := os.ReadFile(filepath.Join(root, "old", "client.go"))
		require.NoError(GinkgoT(), err)
		assert.NotContains(GinkgoT(), string(contents), "Close")
	})

	It("prints a complete diff without writing", func() {
		root := moveFixture(map[string]string{
			"old/value.go": "package old\n\nconst Value = 1\n",
			"new/doc.go":   "package new\n",
		})
		cmd, stdout, _ := moveCommand(root)
		require.NoError(GinkgoT(), cmd.Run([]string{"move", "file", "old/value.go", "new", "--diff"}))
		assert.Contains(GinkgoT(), stdout.String(), "/dev/null")
		assert.Contains(GinkgoT(), stdout.String(), "const Value = 1")
		assertFileContains(filepath.Join(root, "old", "value.go"), "package old")
		assertFileMissing(filepath.Join(root, "new", "value.go"))
	})

	It("preserves a whole file build constraint when creating a package directory", func() {
		constraint := "//go:build " + runtime.GOOS
		root := moveFixture(map[string]string{
			"old/value.go": constraint + "\n\npackage old\n\nconst Value = 1\n",
		})
		cmd, _, _ := moveCommand(root)
		require.NoError(GinkgoT(), cmd.Run([]string{"move", "file", "old/value.go", "new"}))

		assertFileMissing(filepath.Join(root, "old", "value.go"))
		assertFileContains(filepath.Join(root, "new", "value.go"), constraint, "package new")
	})

	It("refuses a symbol move across incompatible build constraints", func() {
		constraint := "//go:build " + runtime.GOOS
		root := moveFixture(map[string]string{
			"old/value.go": constraint + "\n\npackage old\n\nfunc Value() int { return 1 }\n",
			"new/doc.go":   "package new\n",
		})
		cmd, _, _ := moveCommand(root)
		err := cmd.Run([]string{"move", "func", "old.Value", "new/value.go"})

		require.Error(GinkgoT(), err)
		assert.Contains(GinkgoT(), err.Error(), "incompatible build constraints")
		assertFileContains(filepath.Join(root, "old", "value.go"), "func Value")
		assertFileMissing(filepath.Join(root, "new", "value.go"))
	})

	It("refuses a move that would introduce an import cycle", func() {
		root := moveFixture(map[string]string{
			"old/user.go":   "package old\n\ntype User struct{ Helper Helper }\n",
			"old/helper.go": "package old\n\ntype Helper struct{}\n",
			"old/use.go":    "package old\n\nfunc Build() User { return User{} }\n",
			"new/doc.go":    "package new\n",
		})
		cmd, _, _ := moveCommand(root)
		err := cmd.Run([]string{"move", "struct", "old.User", "new/user.go"})

		require.Error(GinkgoT(), err)
		assert.Contains(GinkgoT(), err.Error(), "import cycle")
		assertFileContains(filepath.Join(root, "old", "user.go"), "type User")
		assertFileMissing(filepath.Join(root, "new", "user.go"))
	})

	It("refuses moving an init file away from a package that remains", func() {
		root := moveFixture(map[string]string{
			"old/init.go":  "package old\n\nfunc init() {}\n",
			"old/value.go": "package old\n\nconst Value = 1\n",
			"new/doc.go":   "package new\n",
		})
		cmd, _, _ := moveCommand(root)
		err := cmd.Run([]string{"move", "file", "old/init.go", "new"})

		require.Error(GinkgoT(), err)
		assert.Contains(GinkgoT(), err.Error(), "initialization ownership")
		assertFileContains(filepath.Join(root, "old", "init.go"), "func init")
		assertFileMissing(filepath.Join(root, "new", "init.go"))
	})

	It("refuses cross-package cgo moves", func() {
		root := moveFixture(map[string]string{
			"old/value.go": "package old\n\n/* #include <stdlib.h> */\nimport \"C\"\n\nfunc Value() {}\n",
			"new/doc.go":   "package new\n",
		})
		cmd, _, _ := moveCommand(root)
		err := cmd.Run([]string{"move", "file", "old/value.go", "new"})

		require.Error(GinkgoT(), err)
		assert.Contains(GinkgoT(), err.Error(), "cgo file")
		assertFileContains(filepath.Join(root, "old", "value.go"), "func Value")
		assertFileMissing(filepath.Join(root, "new", "value.go"))
	})
})

func moveFixture(files map[string]string) string {
	root := GinkgoT().TempDir()
	files["go.mod"] = "module example.com/move\n\ngo 1.23\n"
	writeMoveFiles(root, files)
	return root
}

func writeMoveFiles(root string, files map[string]string) {
	for name, contents := range files {
		path := filepath.Join(root, name)
		require.NoError(GinkgoT(), os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(GinkgoT(), os.WriteFile(path, []byte(contents), 0o644))
	}
}

func moveCommand(root string) (*mainCmd, *bytes.Buffer, *bytes.Buffer) {
	var stdout, stderr bytes.Buffer
	return &mainCmd{
		Stdin:       bytes.NewReader(nil),
		Stdout:      &stdout,
		Stderr:      &stderr,
		Getwd:       func() (string, error) { return root, nil },
		UserHomeDir: func() (string, error) { return root, nil },
	}, &stdout, &stderr
}

func assertFileContains(path string, fragments ...string) {
	contents, err := os.ReadFile(path)
	require.NoError(GinkgoT(), err)
	for _, fragment := range fragments {
		assert.Contains(GinkgoT(), string(contents), fragment)
	}
}

func assertFileMissing(path string) {
	_, err := os.Stat(path)
	assert.True(GinkgoT(), os.IsNotExist(err), "%s still exists: %v", path, err)
}
