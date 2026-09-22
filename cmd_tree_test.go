package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"

	"github.com/flanksource/clicky/api/icons"
	. "github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var _ = Describe("tree", func() {
	It("renders packages, files, and declarations with line numbers", func() {
		root := GinkgoT().TempDir()
		writeTreeFiles(root, map[string]string{
			"model/user.go": `package model

import "fmt"

const Version = "v1"

var Default = User{}

type User struct{}

type Validator interface {
	Validate(input string, retries int) error
}

type ID = string

func New(name string, count int) User {
	return User{}
}

func (*User) Validate(value string, retries int) error {
	return nil
}
`,
		})

		cmd, stdout := treeCommand(root)
		require.NoError(GinkgoT(), cmd.Run([]string{"tree", "model/user.go", "--variables=true", "--fields=true"}))

		assert.Equal(GinkgoT(), "📦\u00a0package\u00a0model\u00a0./model\n"+
			"╰── 🐹\u00a0user.go\n"+
			"    ├── π\u00a0const\u00a0Version\u00a0L5\n"+
			"    ├── 𝑣\u00a0var\u00a0Default\u00a0L7\n"+
			"    ├── 🏷️\u00a0struct\u00a0User\u00a0L9\n"+
			"    │   ╰── ƒ\u00a0method\u00a0L21-L23\u00a0User.Validate(value string, retries int)\n"+
			"    ├── 🔗\u00a0interface\u00a0Validator\u00a0L11\n"+
			"    │   ╰── ƒ\u00a0method\u00a0L12-L12\u00a0Validate(input string, retries int)\n"+
			"    ├── 🏷️\u00a0type\u00a0ID\u00a0L15\n"+
			"    ╰── λ\u00a0func\u00a0L17-L19\u00a0New(name string, count int)\n",
			stdout.String())
	})

	It("groups methods under their receiver when its type is declared in another file", func() {
		root := GinkgoT().TempDir()
		writeTreeFiles(root, map[string]string{
			"model/method.go": `package model

import "context"

func (User) Save(ctx context.Context, ids ...string) error {
	return nil
}
`,
			"model/user.go": "package model\n\ntype User struct{}\n",
		})

		cmd, stdout := treeCommand(root)
		require.NoError(GinkgoT(), cmd.Run([]string{"tree", "model/method.go"}))

		assert.Contains(GinkgoT(), stdout.String(), "🐹\u00a0method.go\n"+
			"    ╰── 🏷️\u00a0methods\u00a0User\n"+
			"        ╰── ƒ\u00a0method\u00a0L5-L7\u00a0User.Save(ctx context.Context, ids ...string)")
	})

	It("excludes test files by default and includes them when requested", func() {
		root := GinkgoT().TempDir()
		writeTreeFiles(root, map[string]string{
			"source.go":      "package sample\n\nfunc Source() {}\n",
			"source_test.go": "package sample\n\nfunc TestSource() {}\n",
		})

		cmd, stdout := treeCommand(root)
		require.NoError(GinkgoT(), cmd.Run([]string{"tree", "."}))
		assert.Contains(GinkgoT(), stdout.String(), "source.go")
		assert.NotContains(GinkgoT(), stdout.String(), "source_test.go")

		cmd, stdout = treeCommand(root)
		require.NoError(GinkgoT(), cmd.Run([]string{"tree", ".", "--tests=true"}))
		assert.Contains(GinkgoT(), stdout.String(), "source_test.go")
	})

	It("uses Clicky colors when color output is enabled", func() {
		node := sourceTreeNode{icon: icons.Lambda, kind: "func", name: "Run()", detail: "L1-L3"}

		assert.Contains(GinkgoT(), renderSourceTree(node, true), "\x1b[")
		assert.NotContains(GinkgoT(), renderSourceTree(node, false), "\x1b[")
	})

	It("styles function punctuation, parameter names, and parameter types", func() {
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, "sample.go", "package sample\nfunc Run(value string, ids ...int) {}", 0)
		require.NoError(GinkgoT(), err)
		decl := file.Decls[0].(*ast.FuncDecl)

		signature, err := functionSignature(fset, decl.Name.Name, decl.Type.Params)
		require.NoError(GinkgoT(), err)
		assert.Equal(GinkgoT(), "Run(value string, ids ...int)", signature.String())
		assert.Contains(GinkgoT(), signature.HTML(), "font-bold text-blue-600")
		assert.Contains(GinkgoT(), signature.HTML(), "text-muted")
		assert.Contains(GinkgoT(), signature.HTML(), "text-orange-500")
		assert.Contains(GinkgoT(), signature.HTML(), "text-cyan-500")
	})

	It("defaults to the current tree and sorts packages and files", func() {
		root := GinkgoT().TempDir()
		writeTreeFiles(root, map[string]string{
			"zeta/z.go":  "package zeta\n\nfunc Last() {}\n",
			"alpha/z.go": "package alpha\n\nfunc Zed() {}\n",
			"alpha/a.go": "package alpha\n\ntype First struct{}\n",
		})

		cmd, stdout := treeCommand(root)
		require.NoError(GinkgoT(), cmd.Run([]string{"tree"}))

		got := stdout.String()
		alpha := "📦\u00a0package\u00a0alpha\u00a0./alpha"
		zeta := "📦\u00a0package\u00a0zeta\u00a0./zeta"
		assert.Contains(GinkgoT(), got, alpha)
		assert.Contains(GinkgoT(), got, zeta)
		assert.Contains(GinkgoT(), got, "🐹\u00a0a.go")
		assert.Contains(GinkgoT(), got, "🐹\u00a0z.go")
		assert.Less(GinkgoT(), bytes.Index([]byte(got), []byte(alpha)), bytes.Index([]byte(got), []byte(zeta)))
		assert.Less(GinkgoT(), bytes.Index([]byte(got), []byte("🐹\u00a0a.go")), bytes.Index([]byte(got), []byte("🐹\u00a0z.go")))
	})

	It("fails on invalid Go without printing a partial tree", func() {
		root := GinkgoT().TempDir()
		writeTreeFiles(root, map[string]string{
			"valid.go":   "package sample\n\nfunc Valid() {}\n",
			"invalid.go": "package sample\n\nfunc Invalid(\n",
		})

		cmd, stdout := treeCommand(root)
		err := cmd.Run([]string{"tree", "."})

		require.Error(GinkgoT(), err)
		assert.Contains(GinkgoT(), err.Error(), "could not parse")
		assert.Empty(GinkgoT(), stdout.String())
	})

	It("does not require type checking", func() {
		root := GinkgoT().TempDir()
		writeTreeFiles(root, map[string]string{
			"broken.go": "package sample\n\nfunc Broken() { missing() }\n",
		})

		cmd, stdout := treeCommand(root)
		require.NoError(GinkgoT(), cmd.Run([]string{"tree", "broken.go"}))
		assert.Contains(GinkgoT(), stdout.String(), "func\u00a0L3-L3\u00a0Broken()")
	})

	It("filters private declarations and requested categories", func() {
		root := GinkgoT().TempDir()
		writeTreeFiles(root, map[string]string{
			"sample.go": `package sample

var PublicVar int

type Public struct {
		Field string
		privateField bool
}

func PublicFunc() {}

func privateFunc() {}

func (Public) PublicMethod() {}

func (Public) privateMethod() {}
`,
		})

		cmd, stdout := treeCommand(root)
		require.NoError(GinkgoT(), cmd.Run([]string{
			"tree", "sample.go", "--private=false", "--methods=false", "--functions=false", "--fields=true", "--variables=false",
		}))

		got := stdout.String()
		assert.Contains(GinkgoT(), got, "struct\u00a0Public")
		assert.Contains(GinkgoT(), got, "field\u00a0Field")
		assert.NotContains(GinkgoT(), got, "privateField")
		assert.NotContains(GinkgoT(), got, "PublicFunc")
		assert.NotContains(GinkgoT(), got, "PublicMethod")
		assert.NotContains(GinkgoT(), got, "PublicVar")
	})

	It("accepts the historical variables flag spelling", func() {
		root := GinkgoT().TempDir()
		writeTreeFiles(root, map[string]string{
			"sample.go": "package sample\n\nvar PublicVar int\n",
		})

		cmd, stdout := treeCommand(root)
		require.NoError(GinkgoT(), cmd.Run([]string{"tree", "sample.go", "--vsaariables=true"}))
		assert.Contains(GinkgoT(), stdout.String(), "var\u00a0PublicVar")
	})

	It("keeps the tree filters at their documented defaults", func() {
		cmd, _ := treeCommand(GinkgoT().TempDir())
		root, _ := cmd.newRootCommand()
		tree, _, err := root.Find([]string{"tree"})
		require.NoError(GinkgoT(), err)

		for name, expected := range map[string]string{
			"private": "false", "methods": "true", "functions": "true",
			"fields": "false", "variables": "false", "tests": "false",
		} {
			flag := tree.Flags().Lookup(name)
			require.NotNil(GinkgoT(), flag, name)
			assert.Equal(GinkgoT(), expected, flag.DefValue, name)
		}
	})

	It("reports a path that cannot be enumerated", func() {
		root := GinkgoT().TempDir()
		cmd, stdout := treeCommand(root)

		err := cmd.Run([]string{"tree", "missing"})

		require.Error(GinkgoT(), err)
		assert.Contains(GinkgoT(), err.Error(), "missing")
		assert.NotContains(GinkgoT(), err.Error(), "<nil>")
		assert.Empty(GinkgoT(), stdout.String())
	})

	It("describes its patterns as inspection targets", func() {
		cmd, _ := treeCommand(GinkgoT().TempDir())
		root, _ := cmd.newRootCommand()
		tree, _, err := root.Find([]string{"tree"})
		require.NoError(GinkgoT(), err)

		help := tree.UsageString() + tree.Long
		assert.Contains(GinkgoT(), help, "files or directories to inspect")
		assert.NotContains(GinkgoT(), help, "files or directories to change")
	})
})

func treeCommand(root string) (*mainCmd, *bytes.Buffer) {
	var stdout bytes.Buffer
	return &mainCmd{
		Stdin:  bytes.NewReader(nil),
		Stdout: &stdout,
		Stderr: &bytes.Buffer{},
		Getwd:  func() (string, error) { return root, nil },
	}, &stdout
}

func writeTreeFiles(root string, files map[string]string) {
	for name, content := range files {
		path := filepath.Join(root, name)
		require.NoError(GinkgoT(), os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(GinkgoT(), os.WriteFile(path, []byte(content), 0o644))
	}
}
