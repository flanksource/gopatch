package patchgen

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uber-go/gopatch/internal/text"
)

// TestRenameIdentMatchesDestutterExample pins the generator to the one patch
// the end-to-end suite already proves: testdata/destutter runs
// examples/destutter.patch and asserts its output.
func TestRenameIdentMatchesDestutterExample(t *testing.T) {
	t.Parallel()

	want, err := os.ReadFile("../../../examples/destutter.patch")
	require.NoError(t, err, "read the checked-in example")

	got, err := RenameIdent{
		Kind: KindStruct,
		From: "HTTPClient",
		To:   "Client",
		Scope: Scope{
			Package:    "http",
			ImportPath: "example.com/http",
		},
	}.Patch()
	require.NoError(t, err)

	assert.Equal(t, string(want), stripComments(got))
	requireCompiles(t, got)
}

func TestRenameIdent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc string
		give RenameIdent
		want []byte
	}{
		{
			desc: "declaring package only",
			give: RenameIdent{
				Kind:  KindStruct,
				From:  "HTTPClient",
				To:    "Client",
				Scope: Scope{Package: "http"},
			},
			want: text.Unlines(
				"@@",
				"@@",
				" package http",
				"",
				"-HTTPClient",
				"+Client",
			),
		},
		{
			desc: "func rename with consumers",
			give: RenameIdent{
				Kind: KindFunc,
				From: "Dial",
				To:   "Connect",
				Scope: Scope{
					Package:    "client",
					ImportPath: "example.com/client",
				},
			},
			want: text.Unlines(
				"@@",
				"@@",
				" package client",
				"",
				"-Dial",
				"+Connect",
				"",
				"@@",
				"var client identifier",
				"@@",
				` import client "example.com/client"`,
				"",
				"-client.Dial",
				"+client.Connect",
			),
		},
		{
			desc: "qualifier differs from the package name",
			give: RenameIdent{
				Kind: KindType,
				From: "UUID",
				To:   "ID",
				Scope: Scope{
					Package:    "types",
					ImportPath: "example.com/go-types",
					Qualifier:  "types",
				},
			},
			want: text.Unlines(
				"@@",
				"@@",
				" package types",
				"",
				"-UUID",
				"+ID",
				"",
				"@@",
				"var types identifier",
				"@@",
				` import types "example.com/go-types"`,
				"",
				"-types.UUID",
				"+types.ID",
			),
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()

			got, err := tt.give.Patch()
			require.NoError(t, err)
			assert.Equal(t, string(tt.want), stripComments(got))
			requireCompiles(t, got)
		})
	}
}

func TestRenameIdentDescribe(t *testing.T) {
	t.Parallel()

	withConsumers := RenameIdent{
		Kind:  KindStruct,
		From:  "A",
		To:    "B",
		Scope: Scope{Package: "p", ImportPath: "example.com/p"},
	}
	assert.Equal(t,
		"rename struct A -> B in package p and in consumers of example.com/p",
		withConsumers.Describe())

	// When the import path is unknown the narrowing has to be visible: the
	// description is what --diff and --print-only report on stderr.
	localOnly := RenameIdent{Kind: KindFunc, From: "A", To: "B", Scope: Scope{Package: "p"}}
	assert.Equal(t,
		"rename func A -> B in package p (declaring package only; pass --import to update consumers)",
		localOnly.Describe())
}

func TestRenameIdentErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc    string
		give    RenameIdent
		wantErr string
	}{
		{
			desc:    "missing package scope",
			give:    RenameIdent{Kind: KindStruct, From: "A", To: "B"},
			wantErr: "--package",
		},
		{
			desc:    "same name",
			give:    RenameIdent{Kind: KindStruct, From: "A", To: "A", Scope: Scope{Package: "p"}},
			wantErr: "already the new name",
		},
		{
			desc:    "keyword target",
			give:    RenameIdent{Kind: KindStruct, From: "A", To: "range", Scope: Scope{Package: "p"}},
			wantErr: "Go keyword",
		},
		{
			desc: "malformed import path",
			give: RenameIdent{
				Kind: KindStruct, From: "A", To: "B",
				Scope: Scope{Package: "p", ImportPath: "example.com/p/..."},
			},
			wantErr: "--import",
		},
		{
			desc: "keyword qualifier",
			give: RenameIdent{
				Kind: KindStruct, From: "A", To: "B",
				Scope: Scope{Package: "p", ImportPath: "example.com/p", Qualifier: "range"},
			},
			wantErr: "--qualifier",
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()

			_, err := tt.give.Patch()
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestRenamePackage(t *testing.T) {
	t.Parallel()

	got, err := RenamePackage{From: "foo", To: "bar"}.Patch()
	require.NoError(t, err)

	want := text.Unlines(
		"@@",
		"var x identifier",
		"@@",
		"-package foo",
		"+package bar",
		"",
		" x",
	)
	assert.Equal(t, string(want), stripComments(got))
	requireCompiles(t, got)
}

func TestRenamePackageErrors(t *testing.T) {
	t.Parallel()

	_, err := RenamePackage{From: "foo", To: "foo"}.Patch()
	require.Error(t, err)
	assert.ErrorContains(t, err, "already the new name")

	_, err = RenamePackage{From: "foo", To: "go/bar"}.Patch()
	require.Error(t, err)
	assert.ErrorContains(t, err, "not a valid Go identifier")
}
