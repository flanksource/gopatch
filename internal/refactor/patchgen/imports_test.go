package patchgen

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uber-go/gopatch/internal/text"
)

func TestRenameImport(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc string
		give RenameImport
		want []byte
	}{
		{
			// testdata/rewrite_import_paths: the local name is preserved, so
			// references need no rewrite and the reference line is only there to
			// give the change a body.
			desc: "path only, name preserved",
			give: RenameImport{
				FromPath: "foo-go/client",
				ToPath:   "foo-client",
				FromName: "fooclient",
				ToName:   "fooclient",
			},
			want: text.Unlines(
				"@@",
				"var fooclient, X identifier",
				"@@",
				`-import fooclient "foo-go/client"`,
				`+import fooclient "foo-client"`,
				"",
				" fooclient.X",
			),
		},
		{
			desc: "derived names match, so references are untouched",
			give: RenameImport{
				FromPath: "example.com/a/client",
				ToPath:   "example.com/b/client",
			},
			want: text.Unlines(
				"@@",
				"var client, X identifier",
				"@@",
				`-import client "example.com/a/client"`,
				`+import client "example.com/b/client"`,
				"",
				" client.X",
			),
		},
		{
			// testdata/replace_any_import_with_unnamed, generalized: the new
			// import is unnamed because its name follows the path.
			desc: "qualifier changes with the path",
			give: RenameImport{
				FromPath: "path/filepath",
				ToPath:   "example.com/fp",
			},
			want: text.Unlines(
				"@@",
				"var filepath, X identifier",
				"@@",
				`-import filepath "path/filepath"`,
				`+import "example.com/fp"`,
				"",
				"-filepath.X",
				"+fp.X",
			),
		},
		{
			desc: "explicit new name that the path does not imply",
			give: RenameImport{
				FromPath: "path/filepath",
				ToPath:   "example.com/go-fp",
				ToName:   "fp",
			},
			want: text.Unlines(
				"@@",
				"var filepath, X identifier",
				"@@",
				`-import filepath "path/filepath"`,
				`+import fp "example.com/go-fp"`,
				"",
				"-filepath.X",
				"+fp.X",
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

func TestRenameImportErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc    string
		give    RenameImport
		wantErr string
	}{
		{
			desc:    "same path",
			give:    RenameImport{FromPath: "a/b", ToPath: "a/b"},
			wantErr: "already the new import path",
		},
		{
			desc:    "malformed path",
			give:    RenameImport{FromPath: "a/b", ToPath: `"a/c"`},
			wantErr: "new import path",
		},
		{
			desc:    "old name cannot be derived",
			give:    RenameImport{FromPath: "gopkg.in/yaml.v3", ToPath: "example.com/yaml"},
			wantErr: "--qualifier",
		},
		{
			desc:    "new name cannot be derived",
			give:    RenameImport{FromPath: "example.com/yaml", ToPath: "gopkg.in/yaml.v3"},
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

// Naming the old import is not optional: an import written verbatim in a patch
// matches only files that named it the same way, so it has to be a metavariable.
func TestRenameImportAlwaysDeclaresTheImportName(t *testing.T) {
	t.Parallel()

	got, err := RenameImport{FromPath: "net/http", ToPath: "example.com/http"}.Patch()
	require.NoError(t, err)
	assert.Contains(t, stripComments(got), "var http, X identifier")
}
