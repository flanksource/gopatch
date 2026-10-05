package typed

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func loadModule(t *testing.T, path ...string) *Program {
	t.Helper()

	dir, err := filepath.Abs(filepath.Join(append([]string{"testdata", "cross_module"}, path...)...))
	require.NoError(t, err)
	p, err := Load(Request{
		Dir: dir,
		Env: append(os.Environ(), "GO111MODULE=on", "GOWORK=off"),
	})
	require.NoError(t, err)
	return p
}

func TestResolveFieldRefsAcrossModules(t *testing.T) {
	t.Parallel()

	producer := loadModule(t, "producer")
	consumer := loadModule(t, "consumer")
	sym, err := producer.Resolve(Query{Kind: KindField, Owner: "User", Name: "Name"})
	require.NoError(t, err)
	refs := sym.Refs()
	assert.Equal(t, []SymbolRef{{ImportPath: "example.com/producer/model", Recv: "User", Name: "Name"}}, refs)

	consumerSym := consumer.ResolveRefs(RefQuery{Kind: KindField, Refs: refs, Desc: sym.Desc})
	require.NotNil(t, consumerSym)
	assert.True(t, consumer.HasSourceIdentifiers(consumerSym))

	n, err := renameUsingReferences(t, consumer, consumerSym, "Title")
	require.NoError(t, err)
	assert.Equal(t, 2, n)
	edits, err := consumer.Edits()
	require.NoError(t, err)
	got := modified(t, edits, "app.go")
	assert.Contains(t, got, "u.Title")
	assert.Contains(t, got, "model.User{Title:")
	assert.Contains(t, got, "return o.Name")
}

func TestMethodRefsExpandAcrossConsumerAssignments(t *testing.T) {
	t.Parallel()

	producer := loadModule(t, "producer")
	consumer := loadModule(t, "consumer")
	sym, err := producer.Resolve(Query{Kind: KindMethod, Owner: "File", Name: "Save"})
	require.NoError(t, err)

	consumerSym := consumer.ResolveRefs(RefQuery{Kind: KindMethod, Refs: sym.Refs(), Desc: sym.Desc})
	refs := consumer.ExpandRefs(consumerSym)
	assert.Equal(t, []SymbolRef{
		{ImportPath: "example.com/producer/model", Recv: "File", Name: "Save"},
		{ImportPath: "example.com/producer/model", Recv: "Saver", Name: "Save"},
	}, refs)

	owned := make(map[string]bool)
	for _, path := range producer.PackagePaths() {
		owned[path] = true
	}
	for _, path := range consumer.PackagePaths() {
		owned[path] = true
	}
	producerSym := producer.ResolveRefs(RefQuery{
		Kind: KindMethod, Refs: refs, Desc: sym.Desc, EditablePackages: owned,
	})
	consumerSym = consumer.ResolveRefs(RefQuery{
		Kind: KindMethod, Refs: refs, Desc: sym.Desc, EditablePackages: owned,
	})

	_, err = renameUsingReferences(t, producer, producerSym, "Store")
	require.NoError(t, err)
	_, err = renameUsingReferences(t, consumer, consumerSym, "Store")
	require.NoError(t, err)

	producerEdits, err := producer.Edits()
	require.NoError(t, err)
	assert.Contains(t, modified(t, producerEdits, "model.go"), "type Saver interface {\n\tStore() error")
	consumerEdits, err := consumer.Edits()
	require.NoError(t, err)
	assert.Contains(t, modified(t, consumerEdits, "app.go"), "return s.Store()")
}

func TestResolvedMethodRefsStillRefuseUnselectedPackages(t *testing.T) {
	t.Parallel()

	p := load(t, "methods")
	sym := p.ResolveRefs(RefQuery{
		Kind: KindMethod,
		Refs: []SymbolRef{{
			ImportPath: "example.com/methods/store", Recv: "Named", Name: "String",
		}},
		Desc:             "method Named.String",
		EditablePackages: map[string]bool{"example.com/methods/store": true},
	})

	_, err := renameUsingReferences(t, p, sym, "Text")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fmt.Stringer.String")
	assert.Contains(t, err.Error(), "outside this module")
}
