package refactor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseQualified(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc string
		give string
		want Qualified
	}{
		{
			desc: "struct field",
			give: "User.Name",
			want: Qualified{Owner: "User", Name: "Name"},
		},
		{
			desc: "unexported field",
			give: "user.name",
			want: Qualified{Owner: "user", Name: "name"},
		},
		{
			desc: "pointer receiver",
			give: "*User.Save",
			want: Qualified{Owner: "User", Name: "Save", Pointer: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()

			got, err := ParseQualified("field", tt.give)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseQualifiedErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc    string
		give    string
		wantErr string
	}{
		{desc: "empty", give: "", wantErr: "expected Type.Name"},
		{desc: "no dot", give: "Name", wantErr: "expected Type.Name"},
		{desc: "trailing dot", give: "User.", wantErr: "expected Type.Name"},
		{desc: "leading dot", give: ".Name", wantErr: "expected Type.Name"},
		{desc: "two dots", give: "pkg.User.Name", wantErr: "expected Type.Name"},
		{desc: "keyword owner", give: "range.Name", wantErr: `"range" is a Go keyword`},
		{desc: "keyword name", give: "User.range", wantErr: `"range" is a Go keyword`},
		{desc: "not an identifier", give: "User.my-field", wantErr: "not a valid Go identifier"},
		{desc: "blank owner", give: "_.Name", wantErr: `"_" is not a usable name`},
		{desc: "lone pointer", give: "*.Save", wantErr: "expected Type.Name"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()

			_, err := ParseQualified("field", tt.give)
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.wantErr)
			// The argument name must appear so the user knows which one is bad.
			assert.ErrorContains(t, err, "field")
		})
	}
}

func TestParseIdent(t *testing.T) {
	t.Parallel()

	for _, give := range []string{"Name", "name", "n", "N1", "_name", "Ünïcode"} {
		t.Run(give, func(t *testing.T) {
			t.Parallel()

			got, err := ParseIdent("name", give)
			require.NoError(t, err)
			assert.Equal(t, give, got)
		})
	}
}

func TestParseIdentErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc    string
		give    string
		wantErr string
	}{
		{desc: "empty", give: "", wantErr: "not a valid Go identifier"},
		{desc: "keyword", give: "func", wantErr: `"func" is a Go keyword`},
		{desc: "blank", give: "_", wantErr: `"_" is not a usable name`},
		{desc: "dotted", give: "a.b", wantErr: "not a valid Go identifier"},
		{desc: "leading digit", give: "1n", wantErr: "not a valid Go identifier"},
		{desc: "spaced", give: "a b", wantErr: "not a valid Go identifier"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()

			_, err := ParseIdent("name", tt.give)
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.wantErr)
		})
	}
}

func TestParseTypeExpr(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc string
		give string
		want string
	}{
		{desc: "builtin", give: "string", want: "string"},
		{desc: "pointer", give: "*Request", want: "*Request"},
		{desc: "qualified", give: "context.Context", want: "context.Context"},
		{desc: "slice", give: "[]byte", want: "[]byte"},
		{desc: "map", give: "map[string]int", want: "map[string]int"},
		{desc: "func", give: "func(int) error", want: "func(int) error"},
		{desc: "generic", give: "Result[T]", want: "Result[T]"},
		// Whatever spacing the user typed, the emitted patch is gofmt-clean.
		{desc: "respaced", give: "map[string ]  int", want: "map[string]int"},
		{desc: "trimmed", give: "  *Request  ", want: "*Request"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()

			got, err := ParseTypeExpr("type", tt.give)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseTypeExprErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc string
		give string
	}{
		{desc: "empty", give: ""},
		{desc: "blank", give: "   "},
		{desc: "unbalanced", give: "map[string"},
		{desc: "statement", give: "x := 1"},
		{desc: "two expressions", give: "int, string"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()

			_, err := ParseTypeExpr("type", tt.give)
			require.Error(t, err)
			assert.ErrorContains(t, err, "type")
		})
	}
}

func TestParseFieldList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc string
		give string
		want string
	}{
		{desc: "empty", give: "", want: ""},
		{desc: "parens only", give: "()", want: ""},
		{desc: "named", give: "ctx context.Context", want: "ctx context.Context"},
		{desc: "parenthesized", give: "(ctx context.Context)", want: "ctx context.Context"},
		{desc: "anonymous", give: "string, int", want: "string, int"},
		{desc: "grouped", give: "a, b string", want: "a, b string"},
		{desc: "variadic", give: "args ...string", want: "args ...string"},
		{desc: "respaced", give: "( ctx   context.Context , r *Request )", want: "ctx context.Context, r *Request"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()

			got, err := ParseFieldList("signature", tt.give)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseFieldListErrors(t *testing.T) {
	t.Parallel()

	for _, give := range []string{"ctx context.Context)", "(", "x :=", "ctx context.Context,,"} {
		t.Run(give, func(t *testing.T) {
			t.Parallel()

			_, err := ParseFieldList("signature", give)
			require.Error(t, err)
			assert.ErrorContains(t, err, "signature")
		})
	}
}

func TestParseResults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc      string
		give      string
		want      string
		wantArity int
		wantNamed bool
	}{
		{desc: "none", give: "", want: "", wantArity: 0},
		{desc: "single", give: "error", want: "error", wantArity: 1},
		{desc: "single parenthesized", give: "(error)", want: "error", wantArity: 1},
		{desc: "pair", give: "(*Response, error)", want: "(*Response, error)", wantArity: 2},
		{desc: "named pair", give: "(resp *Response, err error)", want: "(resp *Response, err error)", wantArity: 2, wantNamed: true},
		{desc: "triple", give: "(int, string, error)", want: "(int, string, error)", wantArity: 3},
		// A single named result still needs its parentheses to stay valid Go.
		{desc: "single named", give: "(err error)", want: "(err error)", wantArity: 1, wantNamed: true},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()

			got, arity, named, err := ParseResults("results", tt.give)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got, "rendered")
			assert.Equal(t, tt.wantArity, arity, "arity")
			assert.Equal(t, tt.wantNamed, named, "named")
		})
	}
}

func TestParseTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc string
		give string
		want string
	}{
		{desc: "json", give: `json:"id"`, want: "`json:\"id\"`"},
		{desc: "two keys", give: `json:"id" yaml:"id"`, want: "`json:\"id\" yaml:\"id\"`"},
		{desc: "with options", give: `json:"id,omitempty"`, want: "`json:\"id,omitempty\"`"},
		{desc: "already quoted", give: "`json:\"id\"`", want: "`json:\"id\"`"},
		{desc: "empty clears the tag", give: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()

			got, err := ParseTag("tag", tt.give)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseTagErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc string
		give string
	}{
		{desc: "no value", give: "json"},
		{desc: "unquoted value", give: "json:id"},
		{desc: "contains a backquote", give: "json:\"i`d\""},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()

			_, err := ParseTag("tag", tt.give)
			require.Error(t, err)
			assert.ErrorContains(t, err, "tag")
		})
	}
}

func TestParseImportPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc     string
		give     string
		wantName string
	}{
		{desc: "stdlib", give: "context", wantName: "context"},
		{desc: "nested stdlib", give: "net/http", wantName: "http"},
		{desc: "domain", give: "example.com/foo/client", wantName: "client"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()

			got, err := ParseImportPath("import", tt.give)
			require.NoError(t, err)
			assert.Equal(t, tt.give, got)

			name, err := DerivePackageName("import", tt.give)
			require.NoError(t, err)
			assert.Equal(t, tt.wantName, name)
		})
	}
}

func TestParseImportPathErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc string
		give string
	}{
		{desc: "empty", give: ""},
		{desc: "quoted", give: `"context"`},
		{desc: "spaced", give: "net/ http"},
		{desc: "absolute", give: "/net/http"},
		{desc: "wildcard", give: "net/http/..."},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()

			_, err := ParseImportPath("import", tt.give)
			require.Error(t, err)
			assert.ErrorContains(t, err, "import")
		})
	}
}

// A package name is guessed from the import path's last segment, so a path
// whose last segment is not an identifier must be reported rather than
// producing a patch that silently matches nothing. The path itself is still
// valid: a caller that supplies the name explicitly is not blocked.
func TestDerivePackageNameErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc     string
		give     string
		wantName string
	}{
		{desc: "versioned suffix", give: "gopkg.in/yaml.v3", wantName: "yaml.v3"},
		{desc: "bare domain", give: "example.com", wantName: "example.com"},
		{desc: "hyphenated", give: "example.com/go-types", wantName: "go-types"},
		{desc: "keyword segment", give: "example.com/go/range", wantName: "range"},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()

			// The path is well formed...
			_, err := ParseImportPath("import", tt.give)
			require.NoError(t, err)

			// ...but no package name follows from it.
			_, err = DerivePackageName("import", tt.give)
			require.Error(t, err)
			assert.ErrorContains(t, err, tt.wantName)
			assert.ErrorContains(t, err, "--qualifier")
		})
	}
}
