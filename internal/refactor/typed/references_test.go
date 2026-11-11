package typed

import (
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var _ = Describe("References", func() {
	It("resolves a symbol without a kind and returns exact source locations", func() {
		root := GinkgoT().TempDir()
		writeTypedMoveFile(root, "go.mod", "module example.com/ref\n\ngo 1.23\n")
		writeTypedMoveFile(root, "model/model.go", `package model

type User struct {
	Name string
}

func Read(user User) string {
	return user.Name
}

var Default = User{Name: "Ada"}
`)

		program, err := Load(Request{Dir: root, Env: append(os.Environ(), "GO111MODULE=on", "GOWORK=off")})
		require.NoError(GinkgoT(), err)
		symbol, err := program.ResolveSymbol(SymbolRequest{
			Target: "User.Name",
			Queries: []SymbolQuery{{
				Owner: "User",
				Name:  "Name",
			}},
		})
		require.NoError(GinkgoT(), err)
		assert.Equal(GinkgoT(), KindField, symbol.Kind)

		locations, err := program.References(symbol)
		require.NoError(GinkgoT(), err)
		require.Len(GinkgoT(), locations, 3)
		expectedFile, err := filepath.EvalSymlinks(filepath.Join(root, "model", "model.go"))
		require.NoError(GinkgoT(), err)
		assert.Equal(GinkgoT(), Reference{
			Package:    "example.com/ref/model",
			File:       expectedFile,
			Offset:     35,
			Line:       4,
			Column:     2,
			Definition: true,
			SourceLine: "Name string",
		}, locations[0])
		assert.Equal(GinkgoT(), "return user.Name", locations[1].SourceLine)
		assert.Equal(GinkgoT(), 8, locations[1].Line)
		assert.False(GinkgoT(), locations[1].Definition)
		assert.Equal(GinkgoT(), `var Default = User{Name: "Ada"}`, locations[2].SourceLine)
	})

	It("reports both interpretations of an ambiguous dotted target", func() {
		root := GinkgoT().TempDir()
		writeTypedMoveFile(root, "go.mod", "module example.com/ref\n\ngo 1.23\n")
		writeTypedMoveFile(root, "store/user.go", "package store\n\ntype User struct{}\n")
		writeTypedMoveFile(root, "app/app.go", "package app\n\ntype store struct {\n\tUser string\n}\n")

		program, err := Load(Request{Dir: root, Env: append(os.Environ(), "GO111MODULE=on", "GOWORK=off")})
		require.NoError(GinkgoT(), err)
		_, err = program.ResolveSymbol(SymbolRequest{
			Target: "store.User",
			Queries: []SymbolQuery{
				{Package: "store", Name: "User"},
				{Owner: "store", Name: "User"},
			},
		})

		require.Error(GinkgoT(), err)
		assert.Contains(GinkgoT(), err.Error(), `symbol "store.User" is ambiguous`)
		assert.Contains(GinkgoT(), err.Error(), "./store:User")
		assert.Contains(GinkgoT(), err.Error(), "./app:store.User")
	})

	It("ignores an invalid alternative interpretation when one symbol matches", func() {
		root := GinkgoT().TempDir()
		writeTypedMoveFile(root, "go.mod", "module example.com/ref\n\ngo 1.23\n")
		writeTypedMoveFile(root, "store/user.go", "package store\n\ntype User struct{}\n")
		writeTypedMoveFile(root, "app/app.go", "package app\n\nvar store int\n")

		program, err := Load(Request{Dir: root, Env: append(os.Environ(), "GO111MODULE=on", "GOWORK=off")})
		require.NoError(GinkgoT(), err)
		symbol, err := program.ResolveSymbol(SymbolRequest{
			Target: "store.User",
			Queries: []SymbolQuery{
				{Package: "store", Name: "User"},
				{Owner: "store", Name: "User"},
			},
		})

		require.NoError(GinkgoT(), err)
		assert.Equal(GinkgoT(), KindType, symbol.Kind)
		assert.Equal(GinkgoT(), "example.com/ref/store", symbol.Pkg)
	})

	It("infers package-level declaration kinds and deduplicates test variants", func() {
		root := GinkgoT().TempDir()
		writeTypedMoveFile(root, "go.mod", "module example.com/ref\n\ngo 1.23\n")
		writeTypedMoveFile(root, "model/model.go", "package model\n\ntype Item struct{}\nfunc Build() {}\nvar Current int\nconst Limit = 1\n")
		writeTypedMoveFile(root, "model/model_test.go", "package model\n\nvar _ Item\n")

		program, err := Load(Request{Dir: root, Env: append(os.Environ(), "GO111MODULE=on", "GOWORK=off")})
		require.NoError(GinkgoT(), err)
		for name, want := range map[string]Kind{
			"Item": KindType, "Build": KindFunc, "Current": KindVar, "Limit": KindConst,
		} {
			symbol, resolveErr := program.ResolveSymbol(SymbolRequest{
				Target: name, Queries: []SymbolQuery{{Name: name}},
			})
			require.NoError(GinkgoT(), resolveErr, name)
			assert.Equal(GinkgoT(), want, symbol.Kind, name)
		}

		symbol, err := program.ResolveSymbol(SymbolRequest{
			Target: "Item", Queries: []SymbolQuery{{Name: "Item"}},
		})
		require.NoError(GinkgoT(), err)
		locations, err := program.References(symbol)
		require.NoError(GinkgoT(), err)
		assert.Len(GinkgoT(), locations, 2, "one definition and one test-file use")
		assert.True(GinkgoT(), locations[0].Definition)
		assert.False(GinkgoT(), locations[1].Definition)
	})
})
