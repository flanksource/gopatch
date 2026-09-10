package refactor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTarget(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc  string
		arg   string
		names int
		want  Target
	}{
		{
			desc:  "bare name",
			arg:   "HTTPClient",
			names: 1,
			want:  Target{Names: []string{"HTTPClient"}},
		},
		{
			desc:  "qualified by package clause",
			arg:   "http.HTTPClient",
			names: 1,
			want:  Target{Names: []string{"HTTPClient"}, Qualifier: "http"},
		},
		{
			desc:  "qualified by import path",
			arg:   "example.com/http.HTTPClient",
			names: 1,
			want:  Target{Names: []string{"HTTPClient"}, Qualifier: "example.com/http", IsImportPath: true},
		},
		{
			desc:  "import path whose last segment carries a dot",
			arg:   "gopkg.in/yaml.v3.Node",
			names: 1,
			want:  Target{Names: []string{"Node"}, Qualifier: "gopkg.in/yaml.v3", IsImportPath: true},
		},
		{
			desc:  "bare member",
			arg:   "User.Name",
			names: 2,
			want:  Target{Names: []string{"User", "Name"}},
		},
		{
			desc:  "member qualified by package clause",
			arg:   "store.User.Name",
			names: 2,
			want:  Target{Names: []string{"User", "Name"}, Qualifier: "store"},
		},
		{
			desc:  "member qualified by import path",
			arg:   "example.com/store.User.Name",
			names: 2,
			want:  Target{Names: []string{"User", "Name"}, Qualifier: "example.com/store", IsImportPath: true},
		},
		{
			desc:  "pointer receiver",
			arg:   "*Client.Do",
			names: 2,
			want:  Target{Names: []string{"Client", "Do"}, Pointer: true},
		},
		{
			desc:  "pointer receiver with a package",
			arg:   "*store.Client.Do",
			names: 2,
			want:  Target{Names: []string{"Client", "Do"}, Pointer: true, Qualifier: "store"},
		},
		{
			desc:  "qualified by directory",
			arg:   "./internal/store:User.Name",
			names: 2,
			want:  Target{Names: []string{"User", "Name"}, Dir: "./internal/store"},
		},
		{
			desc:  "qualified by file",
			arg:   "user.go:User.Name",
			names: 2,
			want:  Target{Names: []string{"User", "Name"}, Dir: "user.go"},
		},
		{
			desc:  "qualified by the current directory",
			arg:   ".:HTTPClient",
			names: 1,
			want:  Target{Names: []string{"HTTPClient"}, Dir: "."},
		},
		{
			desc:  "directory and package together",
			arg:   "./internal:store.User.Name",
			names: 2,
			want:  Target{Names: []string{"User", "Name"}, Dir: "./internal", Qualifier: "store"},
		},
		{
			desc:  "surrounding space",
			arg:   "  http.HTTPClient  ",
			names: 1,
			want:  Target{Names: []string{"HTTPClient"}, Qualifier: "http"},
		},
		{
			// A dotted, slashless qualifier is an import path, not two package
			// names: "example.com" is a legal module path.
			desc:  "dotted qualifier with no slash",
			arg:   "a.b.c",
			names: 1,
			want:  Target{Names: []string{"c"}, Qualifier: "a.b", IsImportPath: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()

			got, err := ParseTarget("target", tt.arg, tt.names)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseTargetErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc  string
		arg   string
		names int
		want  string
	}{
		{
			desc:  "empty",
			arg:   "",
			names: 1,
			want:  "expected <Name>, <pkg>.<Name>, or <./dir>:<Name>",
		},
		{
			desc:  "member given as one name",
			arg:   "Name",
			names: 2,
			want:  "expected <Type>.<name>, <pkg>.<Type>.<name>, or <./dir>:<Type>.<name>",
		},
		{
			desc:  "leading dot",
			arg:   ".HTTPClient",
			names: 1,
			want:  "the qualifier before \"HTTPClient\" is empty",
		},
		{
			// The dot is consumed as the member separator, so this is an empty
			// owner rather than an empty qualifier. The message has to name the
			// whole argument: "invalid field \"\"" would name nothing.
			desc:  "leading dot on a member",
			arg:   ".HTTPClient",
			names: 2,
			want:  `invalid target ".HTTPClient": expected <Type>.<name>`,
		},
		{
			desc:  "empty name after the owner",
			arg:   "User.",
			names: 2,
			want:  `invalid target "User.": expected <Type>.<name>`,
		},
		{
			desc:  "package-looking path qualifier",
			arg:   "store:User.Name",
			names: 2,
			want:  `does not look like a file or directory`,
		},
		{
			desc:  "nothing after the colon",
			arg:   "./store:",
			names: 2,
			want:  `expected <Type>.<name>`,
		},
		{
			desc:  "keyword as a name",
			arg:   "User.func",
			names: 2,
			want:  `"func" is a Go keyword`,
		},
		{
			desc:  "blank as a name",
			arg:   "User._",
			names: 2,
			want:  `"_" is not a usable name`,
		},
		{
			desc:  "empty segment",
			arg:   "User..Name",
			names: 2,
			want:  `invalid target "User..Name": expected <Type>.<name>`,
		},
		{
			desc:  "absolute import path",
			arg:   "/example.com/store.User",
			names: 1,
			want:  "an import path cannot be absolute",
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()

			_, err := ParseTarget("target", tt.arg, tt.names)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestTargetAccessors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc     string
		arg      string
		names    int
		owner    string
		name     string
		pkgName  string
		imported string
		str      string
	}{
		{
			desc:    "bare name",
			arg:     "HTTPClient",
			names:   1,
			name:    "HTTPClient",
			pkgName: "",
			str:     "HTTPClient",
		},
		{
			desc:    "package clause",
			arg:     "http.HTTPClient",
			names:   1,
			name:    "HTTPClient",
			pkgName: "http",
			str:     "http.HTTPClient",
		},
		{
			desc:     "import path implies the clause",
			arg:      "example.com/http.HTTPClient",
			names:    1,
			name:     "HTTPClient",
			pkgName:  "http",
			imported: "example.com/http",
			str:      "example.com/http.HTTPClient",
		},
		{
			desc:    "member",
			arg:     "*store.Client.Do",
			names:   2,
			owner:   "Client",
			name:    "Do",
			pkgName: "store",
			str:     "*store.Client.Do",
		},
		{
			desc:  "directory qualified",
			arg:   "./internal/store:User.Name",
			names: 2,
			owner: "User",
			name:  "Name",
			str:   "./internal/store:User.Name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()

			target, err := ParseTarget("target", tt.arg, tt.names)
			require.NoError(t, err)

			assert.Equal(t, tt.owner, target.Owner(), "owner")
			assert.Equal(t, tt.name, target.Name(), "name")
			assert.Equal(t, tt.imported, target.ImportPath(), "import path")
			assert.Equal(t, tt.str, target.String(), "round trip")

			pkg, err := target.PackageName("target")
			require.NoError(t, err)
			assert.Equal(t, tt.pkgName, pkg, "package name")
		})
	}
}

func TestTargetPackageNameUndeducible(t *testing.T) {
	t.Parallel()

	// The last segment of the path is not an identifier, so no package clause
	// can be derived from it and the caller has to be told rather than guessed
	// at.
	target, err := ParseTarget("target", "example.com/go-http.Client", 1)
	require.NoError(t, err)

	_, err = target.PackageName("target")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "cannot derive a package name")
}
