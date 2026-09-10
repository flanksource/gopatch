package patchgen

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uber-go/gopatch/internal/text"
)

func TestDeleteField(t *testing.T) {
	t.Parallel()

	t.Run("local only", func(t *testing.T) {
		t.Parallel()

		got, err := DeleteField{
			structTarget: structTarget{Struct: "User", Field: "Legacy"},
		}.Patch()
		require.NoError(t, err)

		want := text.Unlines(
			"@@",
			"var T expression",
			"@@",
			" type User struct {",
			"   ...",
			"-  Legacy T",
			"   ...",
			" }",
			"",
			"@@",
			"var v expression",
			"@@",
			" User{",
			"   ...,",
			"-  Legacy: v,",
			"   ...,",
			" }",
		)
		assert.Equal(t, string(want), stripComments(got))
		requireCompiles(t, got)
	})

	t.Run("pinned type", func(t *testing.T) {
		t.Parallel()

		got, err := DeleteField{
			structTarget: structTarget{Struct: "User", Field: "Legacy"},
			Type:         "*Deprecated",
		}.Patch()
		require.NoError(t, err)

		// With the type given there is no metavariable for it.
		assert.Contains(t, stripComments(got), "-  Legacy *Deprecated")
		assert.NotContains(t, stripComments(got), "var T expression")
		requireCompiles(t, got)
	})

	// A struct tag is part of the field declaration and cannot be matched by a
	// wildcard, so an operation on a tagged field has to reproduce the tag or it
	// matches nothing at all.
	t.Run("tagged field carries its tag", func(t *testing.T) {
		t.Parallel()

		got, err := DeleteField{
			structTarget: structTarget{Struct: "User", Field: "ID"},
			Type:         "string",
			Tag:          "`json:\"id\"`",
		}.Patch()
		require.NoError(t, err)

		assert.Contains(t, stripComments(got), "-  ID string `json:\"id\"`")
		requireCompiles(t, got)
	})

	t.Run("with importing packages", func(t *testing.T) {
		t.Parallel()

		got, err := DeleteField{
			structTarget: structTarget{Struct: "Params", Field: "Foo"},
			ImportPath:   "example.com/foo",
		}.Patch()
		require.NoError(t, err)

		src := stripComments(got)
		// Three changes: the declaration, unqualified literals, and qualified
		// literals in importing packages.
		assert.Equal(t, 3, countChanges(src))
		assert.Contains(t, src, ` import foo "example.com/foo"`)
		assert.Contains(t, src, " foo.Params{")
		requireCompiles(t, got)
	})
}

func TestAddField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc string
		give AddField
		want []byte
	}{
		{
			desc: "last",
			give: AddField{
				structTarget: structTarget{Struct: "User", Field: "Nickname"},
				Type:         "string",
			},
			want: text.Unlines(
				"@@",
				"@@",
				" type User struct {",
				"   ...",
				"+  Nickname string",
				" }",
			),
		},
		{
			desc: "first",
			give: AddField{
				structTarget: structTarget{Struct: "User", Field: "ID"},
				Type:         "UUID",
				Position:     PositionFirst,
			},
			want: text.Unlines(
				"@@",
				"@@",
				" type User struct {",
				"+  ID UUID",
				"   ...",
				" }",
			),
		},
		{
			desc: "with a tag",
			give: AddField{
				structTarget: structTarget{Struct: "User", Field: "Nickname"},
				Type:         "string",
				Tag:          "`json:\"nickname\"`",
			},
			want: text.Unlines(
				"@@",
				"@@",
				" type User struct {",
				"   ...",
				"+  Nickname string `json:\"nickname\"`",
				" }",
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

func TestEditFieldType(t *testing.T) {
	t.Parallel()

	t.Run("inferred old type", func(t *testing.T) {
		t.Parallel()

		got, err := EditFieldType{
			structTarget: structTarget{Struct: "User", Field: "ID"},
			NewType:      "UUID",
		}.Patch()
		require.NoError(t, err)

		want := text.Unlines(
			"@@",
			"var T expression",
			"@@",
			" type User struct {",
			"   ...",
			"-  ID T",
			"+  ID UUID",
			"   ...",
			" }",
		)
		assert.Equal(t, string(want), stripComments(got))
		requireCompiles(t, got)
	})

	t.Run("pinned old type", func(t *testing.T) {
		t.Parallel()

		got, err := EditFieldType{
			structTarget: structTarget{Struct: "User", Field: "ID"},
			NewType:      "UUID",
			OldType:      "string",
		}.Patch()
		require.NoError(t, err)

		assert.Contains(t, stripComments(got), "-  ID string")
		assert.Contains(t, stripComments(got), "+  ID UUID")
		requireCompiles(t, got)
	})

	// Retyping a tagged field has to keep the tag on both sides: on the minus
	// side so the declaration matches at all, and on the plus side so the tag
	// survives the change.
	t.Run("tagged field keeps its tag through the change", func(t *testing.T) {
		t.Parallel()

		got, err := EditFieldType{
			structTarget: structTarget{Struct: "User", Field: "ID"},
			NewType:      "UUID",
			OldType:      "string",
			Tag:          "`json:\"id\"`",
		}.Patch()
		require.NoError(t, err)

		src := stripComments(got)
		assert.Contains(t, src, "-  ID string `json:\"id\"`")
		assert.Contains(t, src, "+  ID UUID `json:\"id\"`")
		requireCompiles(t, got)
	})

	t.Run("already that type", func(t *testing.T) {
		t.Parallel()

		_, err := EditFieldType{
			structTarget: structTarget{Struct: "User", Field: "ID"},
			NewType:      "string",
			OldType:      "string",
		}.Patch()
		require.Error(t, err)
		assert.ErrorContains(t, err, "already")
	})
}

func TestEditFieldTag(t *testing.T) {
	t.Parallel()

	t.Run("set on an untagged field", func(t *testing.T) {
		t.Parallel()

		got, err := EditFieldTag{
			structTarget: structTarget{Struct: "User", Field: "Name"},
			Tag:          "`json:\"name\"`",
			Type:         "string",
		}.Patch()
		require.NoError(t, err)

		want := text.Unlines(
			"@@",
			"@@",
			" type User struct {",
			"   ...",
			"-  Name string",
			"+  Name string `json:\"name\"`",
			"   ...",
			" }",
		)
		assert.Equal(t, string(want), stripComments(got))
		requireCompiles(t, got)
	})

	t.Run("replace an existing tag", func(t *testing.T) {
		t.Parallel()

		// A tag cannot be a metavariable, so the current one is pinned exactly.
		got, err := EditFieldTag{
			structTarget: structTarget{Struct: "User", Field: "ID"},
			Tag:          "`json:\"user_id\"`",
			OldTag:       "`json:\"id\"`",
			Type:         "string",
		}.Patch()
		require.NoError(t, err)

		src := stripComments(got)
		assert.Contains(t, src, "-  ID string `json:\"id\"`")
		assert.Contains(t, src, "+  ID string `json:\"user_id\"`")
		requireCompiles(t, got)
	})

	t.Run("clear a tag", func(t *testing.T) {
		t.Parallel()

		got, err := EditFieldTag{
			structTarget: structTarget{Struct: "User", Field: "ID"},
			OldTag:       "`json:\"id\"`",
			Type:         "string",
		}.Patch()
		require.NoError(t, err)

		src := stripComments(got)
		assert.Contains(t, src, "-  ID string `json:\"id\"`")
		assert.Contains(t, src, "+  ID string\n")
		requireCompiles(t, got)
	})

	t.Run("nothing to clear", func(t *testing.T) {
		t.Parallel()

		_, err := EditFieldTag{
			structTarget: structTarget{Struct: "User", Field: "ID"},
		}.Patch()
		require.Error(t, err)
		assert.ErrorContains(t, err, "no tag to clear")
	})
}

func TestFieldOperationErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc    string
		give    Operation
		wantErr string
	}{
		{
			desc:    "keyword struct name",
			give:    DeleteField{structTarget: structTarget{Struct: "range", Field: "A"}},
			wantErr: "Go keyword",
		},
		{
			desc:    "unparseable field type",
			give:    AddField{structTarget: structTarget{Struct: "U", Field: "A"}, Type: "map[string"},
			wantErr: "field type",
		},
		{
			desc: "qualifier without an import path",
			give: DeleteField{
				structTarget: structTarget{Struct: "U", Field: "A"},
				Qualifier:    "foo",
			},
			wantErr: "--qualifier needs --import",
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
