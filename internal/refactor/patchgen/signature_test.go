package patchgen

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/uber-go/gopatch/internal/refactor"
	"github.com/uber-go/gopatch/internal/text"
)

func sig(params, results string) refactor.FuncSignature {
	return refactor.FuncSignature{Params: params, Results: results}
}

func TestSetSignature(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc string
		give SetSignature
		want []byte
	}{
		{
			desc: "add a parameter, results preserved",
			give: SetSignature{
				Func: "Send",
				From: sig("r *Request", "(*Response, error)"),
				To:   sig("ctx context.Context, r *Request", "(*Response, error)"),
			},
			want: text.Unlines(
				"@@",
				"@@",
				"-func Send(r *Request) (*Response, error) {",
				"+func Send(ctx context.Context, r *Request) (*Response, error) {",
				"   ...",
				" }",
			),
		},
		{
			desc: "no results",
			give: SetSignature{
				Func: "Log",
				From: sig("msg string", ""),
				To:   sig("ctx context.Context, msg string", ""),
			},
			want: text.Unlines(
				"@@",
				"@@",
				"-func Log(msg string) {",
				"+func Log(ctx context.Context, msg string) {",
				"   ...",
				" }",
			),
		},
		{
			desc: "no parameters",
			give: SetSignature{
				Func: "Now",
				From: sig("", "time.Time"),
				To:   sig("c Clock", "time.Time"),
			},
			want: text.Unlines(
				"@@",
				"@@",
				"-func Now() time.Time {",
				"+func Now(c Clock) time.Time {",
				"   ...",
				" }",
			),
		},
		{
			desc: "adds an import for the new type",
			give: SetSignature{
				Func:   "Send",
				From:   sig("r *Request", "error"),
				To:     sig("ctx context.Context, r *Request", "error"),
				Import: "context",
			},
			want: text.Unlines(
				"@@",
				"@@",
				`+import "context"`,
				"",
				"-func Send(r *Request) error {",
				"+func Send(ctx context.Context, r *Request) error {",
				"   ...",
				" }",
			),
		},
		{
			desc: "changes the results too",
			give: SetSignature{
				Func: "Do",
				From: sig("", ""),
				To:   sig("", "error"),
			},
			want: text.Unlines(
				"@@",
				"@@",
				"-func Do() {",
				"+func Do() error {",
				"   ...",
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

// A generated signature must never contain an elision. "..." in a result list
// makes go/format fail outright, and two of them in one signature cross-connect.
func TestSetSignatureRejectsElision(t *testing.T) {
	t.Parallel()

	tests := []struct {
		desc string
		to   refactor.FuncSignature
	}{
		{desc: "in the results", to: sig("r *Request", "(..., error)")},
		{desc: "in the parameters", to: sig("ctx context.Context, ...", "error")},
		{desc: "alone in the parameters", to: sig("...", "error")},
	}

	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			t.Parallel()

			_, err := SetSignature{
				Func: "Send",
				From: sig("r *Request", "error"),
				To:   tt.to,
			}.Patch()
			require.Error(t, err)
			assert.ErrorContains(t, err, "cannot elide a signature")
		})
	}
}

// A variadic parameter is not an elision and must be accepted.
func TestSetSignatureAllowsVariadic(t *testing.T) {
	t.Parallel()

	got, err := SetSignature{
		Func: "Printf",
		From: sig("format string", ""),
		To:   sig("w io.Writer, format string, args ...any", ""),
	}.Patch()
	require.NoError(t, err)
	assert.Contains(t, stripComments(got), "+func Printf(w io.Writer, format string, args ...any) {")
	requireCompiles(t, got)
}

func TestSetSignatureDescribeWarnsAboutCallSites(t *testing.T) {
	t.Parallel()

	op := SetSignature{
		Func: "Send",
		From: sig("r *Request", "error"),
		To:   sig("ctx context.Context, r *Request", "error"),
	}
	assert.Equal(t,
		"change func Send(r *Request) error to Send(ctx context.Context, r *Request) error"+
			" (call sites are not updated)",
		op.Describe())
}

func TestAddParam(t *testing.T) {
	t.Parallel()

	t.Run("named parameter onto a named list", func(t *testing.T) {
		t.Parallel()

		op, err := AddParam("Send", sig("r *Request", "error"), "ctx", "context.Context", "context")
		require.NoError(t, err)

		assert.Equal(t, "ctx context.Context, r *Request", op.To.Params)
		assert.Equal(t, "error", op.To.Results)
		assert.Equal(t, "add parameter ctx context.Context to func Send (call sites are not updated)",
			op.Describe())

		got, err := op.Patch()
		require.NoError(t, err)
		requireCompiles(t, got)
	})

	t.Run("anonymous parameter onto an unnamed list", func(t *testing.T) {
		t.Parallel()

		op, err := AddParam("Send", sig("string", "(*Response, error)"), "", "context.Context", "")
		require.NoError(t, err)
		assert.Equal(t, "context.Context, string", op.To.Params)

		got, err := op.Patch()
		require.NoError(t, err)
		requireCompiles(t, got)
	})

	t.Run("onto an empty parameter list", func(t *testing.T) {
		t.Parallel()

		op, err := AddParam("Now", sig("", "time.Time"), "c", "Clock", "")
		require.NoError(t, err)
		assert.Equal(t, "c Clock", op.To.Params)
	})

	t.Run("naming a parameter beside unnamed ones is refused", func(t *testing.T) {
		t.Parallel()

		// Go does not allow mixing named and unnamed parameters, so this would
		// produce code that does not compile.
		_, err := AddParam("Send", sig("string, int", ""), "ctx", "context.Context", "")
		require.Error(t, err)
		assert.ErrorContains(t, err, "unnamed parameters")
		assert.ErrorContains(t, err, "pass just the type")
	})

	t.Run("invalid type", func(t *testing.T) {
		t.Parallel()

		_, err := AddParam("Send", sig("", ""), "ctx", "map[string", "")
		require.Error(t, err)
		assert.ErrorContains(t, err, "param type")
	})
}
