This directory contains end-to-end tests for the refactoring subcommands —
`gopatch rename`, `add`, `delete` and `edit` — which generate a patch from
command line arguments instead of reading one from a file.

Each test is a [txtar] archive, the same format as the archives in the parent
directory, and is run by `TestRefactorIntegration`. `TestIntegration` skips
directories, so it never sees these.

  [txtar]: https://godoc.org/github.com/rogpeppe/go-internal/txtar

The archives are shaped differently from the ones in `testdata/`: a refactoring
command is invoked **once over the whole extracted tree**, not once per Go file.
That is how the commands are used, and a single command may legitimately change
several files.

-   `args` holds the command line, without the trailing file patterns. It is
    split the way a shell would, so a flag value containing spaces is quoted
-   Input files use the ".in.go" suffix and are renamed to ".go" before the run
-   Files created by a command use the ".new.go" suffix; files expected to be deleted use ".delete.go"
-   `patterns` overrides the trailing file patterns, which default to every
    `.go` file in the archive. An empty `patterns` appends none, which is what a
    command taking its target file as a positional needs
-   `want.patch` is the expected output of `--print-patch`, including the `#`
    description comments. Absent for the type-aware commands, which generate no
    patch; that mode is then skipped
-   `want.diff` and `want.stderr` are optional; when present they pin the
    stdout and stderr of `--diff`
-   `want.error` makes the archive a refusal case: the command must fail with an
    error containing that text and leave every file untouched. It replaces the
    other modes rather than adding to them
-   `go.mod` makes the extracted tree a module, which the type-aware commands
    need. It is written out but is not a refactoring target
-   nested `go.mod` members create separate consumer modules for cross-module typed rename tests
-   a `.txt` member is written out and is not a refactoring target either. It is
    for a file the command *reads*, which is what `--content @<path>` needs; it
    cannot be a `.go` file, because a body's statements are not a Go file and
    would break the module load

A member's name may contain directories — `store/user.in.go` — which is how an
archive gets more than one package. That is what the qualified-target archives
need: two packages declaring the same struct, so that a change scoped to one of
them can be seen not to touch the other.

Each input file may have an ".out.go" counterpart holding its expected contents
after the command runs. **An input with no ".out.go" must come out
byte-identical**, which is what catches a command that changes more than it
should. A ".new.go" member must not exist in preview mode and must be created in place; a ".delete.go" member must survive previews unchanged and disappear in place.

Every archive is run three ways: `--print-patch` against `want.patch` (when it
has one), `--diff` (which must also leave every file on disk untouched), and in
place against the ".out.go" files.

The type-aware commands run in-process against the extracted tree, so they need
no special handling here: the harness already injects the working directory, and
the engine forces module mode for the `go` command it runs — which matters,
because `TestIntegration` sets `GO111MODULE=off` for the whole test process.

A test file takes the form,

    <comments>

    -- args --
    rename struct HTTPClient Client --package http

    -- want.patch --
    # rename struct HTTPClient -> Client in package http ...
    @@
    @@
     package http

    -HTTPClient
    +Client

    -- client.in.go --
    package http

    // input source

    -- client.out.go --
    package http

    // output source

Some archives deliberately pin behaviour that is not ideal — the blank line
`delete_field` leaves behind, for instance. Those are noted in the archive's
comment, so that a future improvement shows up as a failing test rather than
going unnoticed.
