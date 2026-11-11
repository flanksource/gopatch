package main

import (
	"path/filepath"

	"github.com/flanksource/clicky/api"
	. "github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uber-go/gopatch/internal/refactor/typed"
)

var _ = Describe("ref", func() {
	It("pretty prints a definition and exact references", func() {
		GinkgoT().Setenv("GOWORK", "off")
		root := moveFixture(map[string]string{
			"model/model.go": `package model

type User struct {
	Name string
}

func Read(user User) string {
	return user.Name
}

var Default = User{Name: "Ada"}
`,
		})

		dsn := indexedRenameDatabase(root)
		cmd, stdout, _ := moveCommand(root)
		require.NoError(GinkgoT(), cmd.Run([]string{"ref", "User.Name", "--package", "model", "--uir-dsn", dsn}))

		assert.Equal(GinkgoT(), "🔍\u00a0field\u00a0User.Name\u00a01 definition, 2 references\n"+
			"╰── 📦\u00a0package\u00a0example.com/move/model\u00a0./model\n"+
			"    ╰── 🐹\u00a0model.go\n"+
			"        ├── 🎯\u00a0definition\u00a0L4:2\u00a0Name string\n"+
			"        ├── 🔗\u00a0reference\u00a0L8:14\u00a0return user.Name\n"+
			"        ╰── 🔗\u00a0reference\u00a0L11:20\u00a0var Default = User{Name: \"Ada\"}\n",
			stdout.String())
	})

	It("includes requiring modules from the active Go workspace", func() {
		workspace := GinkgoT().TempDir()
		writeMoveFiles(workspace, map[string]string{
			"go.work":                "go 1.23\n\nuse (\n\t./producer\n\t./consumer\n)\n",
			"producer/go.mod":        "module example.com/producer\n\ngo 1.23\n",
			"producer/model/user.go": "package model\n\ntype User struct{}\n",
			"consumer/go.mod":        "module example.com/consumer\n\ngo 1.23\n\nrequire example.com/producer v0.0.0\n",
			"consumer/app/app.go":    "package app\n\nimport \"example.com/producer/model\"\n\nvar Current model.User\n",
		})

		canonicalWorkspace, err := filepath.EvalSymlinks(workspace)
		require.NoError(GinkgoT(), err)
		GinkgoT().Setenv("GOWORK", filepath.Join(canonicalWorkspace, "go.work"))
		dsn := indexedRenameDatabase(filepath.Join(workspace, "producer"), filepath.Join(workspace, "consumer"))
		cmd, stdout, _ := moveCommand(filepath.Join(workspace, "producer"))
		require.NoError(GinkgoT(), cmd.Run([]string{"ref", "model.User", "--uir-dsn", dsn}))

		assert.Contains(GinkgoT(), stdout.String(), "example.com/producer/model")
		assert.Contains(GinkgoT(), stdout.String(), "example.com/consumer/app")
		assert.Contains(GinkgoT(), stdout.String(), "var Current model.User")
	})

	It("includes explicitly selected consumer modules", func() {
		GinkgoT().Setenv("GOWORK", "off")
		workspace := GinkgoT().TempDir()
		writeMoveFiles(workspace, map[string]string{
			"producer/go.mod":        "module example.com/producer\n\ngo 1.23\n",
			"producer/model/user.go": "package model\n\ntype User struct{}\n",
			"consumer/go.mod":        "module example.com/consumer\n\ngo 1.23\n\nrequire example.com/producer v0.0.0\nreplace example.com/producer => ../producer\n",
			"consumer/app/app.go":    "package app\n\nimport \"example.com/producer/model\"\n\nvar Current model.User\n",
		})

		dsn := indexedRenameDatabase(filepath.Join(workspace, "producer"), filepath.Join(workspace, "consumer"))
		cmd, stdout, _ := moveCommand(filepath.Join(workspace, "producer"))
		require.NoError(GinkgoT(), cmd.Run([]string{"ref", "model.User", "--consumers", workspace, "--uir-dsn", dsn}))

		assert.Contains(GinkgoT(), stdout.String(), "example.com/consumer/app")
		assert.Contains(GinkgoT(), stdout.String(), "var Current model.User")
	})

	It("skips requiring modules that do not reference the symbol", func() {
		workspace := GinkgoT().TempDir()
		writeMoveFiles(workspace, map[string]string{
			"go.work":                "go 1.23\n\nuse (\n\t./producer\n\t./consumer\n)\n",
			"producer/go.mod":        "module example.com/producer\n\ngo 1.23\n",
			"producer/model/user.go": "package model\n\ntype User struct{}\n",
			"consumer/go.mod":        "module example.com/consumer\n\ngo 1.23\n\nrequire example.com/producer v0.0.0\n",
			"consumer/app/app.go":    "package app\n\nconst Name = \"consumer\"\n",
		})

		canonicalWorkspace, err := filepath.EvalSymlinks(workspace)
		require.NoError(GinkgoT(), err)
		GinkgoT().Setenv("GOWORK", filepath.Join(canonicalWorkspace, "go.work"))
		dsn := indexedRenameDatabase(filepath.Join(workspace, "producer"), filepath.Join(workspace, "consumer"))
		cmd, stdout, _ := moveCommand(filepath.Join(workspace, "producer"))
		require.NoError(GinkgoT(), cmd.Run([]string{"ref", "model.User", "--uir-dsn", dsn}))

		assert.Contains(GinkgoT(), stdout.String(), "1 definition, 0 references")
		assert.NotContains(GinkgoT(), stdout.String(), "example.com/consumer/app")
	})

	It("includes linked interface and concrete method references", func() {
		GinkgoT().Setenv("GOWORK", "off")
		root := moveFixture(map[string]string{
			"runner/runner.go": `package runner

type Runner interface { Run() }
type Worker struct{}
func (Worker) Run() {}
func Call(r Runner) { r.Run() }
var _ Runner = Worker{}
`,
		})

		dsn := indexedRenameDatabase(root)
		cmd, stdout, _ := moveCommand(root)
		require.NoError(GinkgoT(), cmd.Run([]string{"ref", "Worker.Run", "--package", "runner", "--uir-dsn", dsn}))

		assert.Contains(GinkgoT(), stdout.String(), "2 definitions, 1 reference")
		assert.Contains(GinkgoT(), stdout.String(), "type Runner interface { Run() }")
		assert.Contains(GinkgoT(), stdout.String(), "func (Worker) Run() {}")
		assert.Contains(GinkgoT(), stdout.String(), "func Call(r Runner) { r.Run() }")
	})

	It("fails before printing when a symbol is ambiguous", func() {
		GinkgoT().Setenv("GOWORK", "off")
		root := moveFixture(map[string]string{
			"first/value.go":  "package first\n\ntype Value struct{}\n",
			"second/value.go": "package second\n\ntype Value struct{}\n",
		})
		cmd, stdout, _ := moveCommand(root)

		err := cmd.Run([]string{"ref", "Value"})

		require.Error(GinkgoT(), err)
		assert.Contains(GinkgoT(), err.Error(), "ambiguous")
		assert.Empty(GinkgoT(), stdout.String())
	})

	It("fails before printing when a selected module does not type-check", func() {
		root := moveFixture(map[string]string{
			"model/user.go": "package model\n\ntype User struct{}\nvar _ = missingName\n",
		})
		cmd, stdout, _ := moveCommand(root)

		err := cmd.Run([]string{"ref", "model.User"})

		require.Error(GinkgoT(), err)
		assert.Contains(GinkgoT(), err.Error(), "does not type-check")
		assert.Empty(GinkgoT(), stdout.String())
	})

	It("registers only read-only reference flags", func() {
		root, _ := discardCmd().newRootCommand()
		ref, _, err := root.Find([]string{"ref"})
		require.NoError(GinkgoT(), err)

		assert.NotNil(GinkgoT(), ref.Flags().Lookup("package"))
		assert.NotNil(GinkgoT(), ref.Flags().Lookup("consumers"))
		assert.NotNil(GinkgoT(), ref.Flags().Lookup("consumers-from"))
		assert.Nil(GinkgoT(), ref.Flags().Lookup("diff"))
		assert.Nil(GinkgoT(), ref.Flags().Lookup("print-only"))
	})

	It("preserves source-line styling across ANSI and HTML renderers", func() {
		location := typed.Reference{Line: 7, Column: 3, SourceLine: "return user.Name"}
		node := referenceLocationNodes([]typed.Reference{location})[0].(sourceTreeNode)

		assert.Contains(GinkgoT(), node.Pretty().HTML(), "font-mono")
		assert.Contains(GinkgoT(), renderSourceTree(sourceTreeNode{children: []api.TreeNode{node}}, true), "\x1b[")
	})
})
