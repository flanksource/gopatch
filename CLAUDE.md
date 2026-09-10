# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Repository identity

This checkout lives at `flanksource/gopatch` but is a fork of `uber-go/gopatch`: the `go.mod` module path and the `origin` remote are both still `github.com/uber-go/gopatch`. **All internal imports use `github.com/uber-go/gopatch/...`** — do not "fix" them to `flanksource`, that would break the module.

The fork's own addition is the `patch/` package (`Parse` + `File.Apply`), which exposes gopatch as a library. Everything else is under `internal/`, so `patch` is the only importable surface for downstream consumers — treat it as public API.

## Commands

```shell
make build              # go build ./...   (G-3: never call go build directly)
make install            # go install into $GOBIN, or GOPATH/bin; stamps -X main._version
make test               # go test -v -race ./...
make cover              # coverage profile + cover.html (this is what CI runs)
make lint               # gofmt + golint + staticcheck  (see caveat below)
```

Single tests:

```shell
go test ./internal/engine/ -run TestMatch           # one unit test
go test . -run 'TestIntegration/foo_call_to_bar'    # one testdata/ txtar file
go test . -run 'TestIntegration/dedupe_args/simple' # one Go file pair inside it
```

Caveats:

- **The module is `go 1.23` and CI pins Go 1.23.x. Verify with that toolchain, not the machine default.** `~/sdk/go1.23.4/bin` holds one; prefix commands with `PATH=$HOME/sdk/go1.23.4/bin:$PATH`. (`~/sdk/go1.22.5` also exists but its linker omits `LC_UUID`, so macOS 15+ refuses to run the test binaries it builds.)
- On go1.26 `make build`, `go vet` and `make lint` pass, but `go test ./...` fails in `internal/goast` (`TestOffsetPos/file_with_import`) and `internal/pgo` (`TestParse/statement_list_with_dots`). That is toolchain drift, not a regression: `go/ast.BasicLit` gained a `ValueEnd` field the hand-built expected ASTs do not set. Both pass on go1.23.
- `staticcheck` flags `ast.Object`/`ast.Scope` as deprecated, and it is right that they cannot be computed without type information — which is gopatch's premise, not a bug. `internal/goast` and `internal/engine/replacer.go` carry `//lint:file-ignore SA1019` with that reasoning. Do not extend the ignore to new code.
- **The Makefile exports `GOBIN=./bin`**, so the lint tools stay out of the user's path. Any target that installs something *for the user* has to undo that: `make install` uses `INSTALL_BIN`, captured from the environment with `:=` **before** the export, because a recursively-expanded `?=` would read the redirected value and silently install into `./bin`.
- The integration tests set `GO111MODULE=off` for their duration so patched fixture code is never resolved against the network. `internal/refactor/typed` forces module mode for the `go` command it runs, which is what lets its archives run in the same process.

## Architecture

A `.patch` file is compiled through four stages, then run against Go files. `docs/HACKING.md` is the authoritative deep-dive; this is the map.

1. **`internal/parse/section`** — splits the program into changes by `@@` headers, without parsing contents. Produces `Section`s of raw bytes plus positions.
2. **`internal/parse`** — parses each change's metavariable section (Go `var` decls, via `go/scanner`) and its patch section. The unified diff is split into two independent sources, *minus* and *plus*, each of which must parse on its own.
3. **`internal/pgo`** (+ `internal/pgo/augment`) — parses "Patch Go": a superset of Go where the package clause is optional, statements/expressions may appear at top level, and `...` ("dots") is accepted anywhere. `augment` rewrites invalid pgo into valid Go (`foo(...)` → `foo(dts)`, wrapping in a fake `package _`/`func _()`), records `Augmentation`s and `PosAdjustment`s, runs `go/parser`, then swaps the placeholder nodes back out for `pgo.Dots` and maps positions back to the original file.
4. **`internal/engine`** — `Compile` turns the parsed program into a `Program` of `Change`s. Each change compiles its minus section into a **`Matcher`** and its plus section into a **`Replacer`**. Matchers/Replacers are built by *reflection* over `go/ast` node types (`reflect_match.go`, `reflect_replace.go`) rather than hand-written per node.

At runtime, per change: `Change.Match(*ast.File) (data.Data, bool)` then `Change.Replace(data.Data, Changelog) (*ast.File, error)`.

- **`internal/data`** is the immutable `context.Context`-shaped key/value store that carries captured state from Matcher to Replacer (metavariable bindings, elided slice sections, matched positions, import info). `data.Index` makes a read-optimized copy.
- **`internal/astdiff`** snapshots the AST before patching and diffs it after each change (using `internal/diff`'s edit-script algorithm) to feed the `Changelog` — the set of position ranges that actually changed. `cleanupFilePos` then merges the now-empty lines in the `token.File` and drops comments that fell inside changed regions, which is what keeps output formatting sane.
- **`internal/goast`** holds the reflected `go/ast` type table and `TransformPos`/`OffsetPos`, which walk a node and rewrite every `token.Pos` in place. `ast.File` and `*ast.Object` are special-cased to avoid cyclic/duplicated references, and invalid (zero) positions are deliberately left alone because a valid-vs-invalid `Pos` is semantically meaningful in the Go AST (e.g. `type Foo = Bar` vs `type Foo Bar`). **A new positional field in `go/ast` needs handling here** — that is the class of breakage behind the current go1.26 test failures.

Feature-specific engine files worth knowing before touching matching behavior: `metavar.go` (first occurrence captures, later ones must match the capture), `slice_dots.go` (`...` inside argument/field/statement slices), `for_dots.go` (`for ...` matching both `ForStmt` and `RangeStmt`), `stmt_list.go` (statement lists inside `BlockStmt`/`CaseClause`/`CommClause`), `import.go` (named vs unnamed imports, import metavariables), `search.go` (descends the AST to apply a matcher to nested nodes).

### Two drivers, one apply loop

`main.go` (`patchRunner.Apply`) and `patch/gopatch.go` (`File.Apply`) each implement the match → replace → `astdiff` → `cleanupFilePos` → `format.Node` → `imports.Process` sequence, and `cleanupFilePos` is currently **duplicated verbatim** in both. Any change to patch application semantics must land in both, or the CLI and the library API diverge — the `api` subtest in `e2e_test.go` is what catches that.

The CLI's other responsibilities: `loader.go` (`patchLoader` reads patches from stdin, `-p` files, or a `-P` list file, and compiles each into a `Program`), `findGoFiles` (walks patterns, trims a trailing `...`, skips dot/underscore dirs plus `testdata` and `vendor`), and the output modes `--diff` / `--print-only` / in-place write.

### Two refactoring engines

The `rename`/`add`/`delete`/`edit`/`replace` subcommands are served by two engines that meet at `emit` in `apply.go`, so their output is indistinguishable. Never mix them within one invocation: a half-applied change that exits 0 is the failure mode both designs exist to avoid. `edit func` is the one command whose engine depends on its flags — `--params`/`--results` generate a patch, `--content` runs the typed engine — so it refuses the two together, and `refusePatchFlags` refuses the patch-only flags on the typed path rather than accepting and ignoring them.

- **`internal/refactor/patchgen`** renders a gopatch program from CLI arguments and runs it through the normal pipeline. It inherits the engine's abilities and its limits, which `program.go`'s package doc enumerates — in particular that the plus section may not be empty (so no declaration can be deleted) and that a struct tag is part of a field declaration and cannot be matched by a wildcard (so field operations must reproduce it exactly, which is why the commands resolve it with `refactor.Search.StructField` first and fail loudly if they cannot).
- **`internal/refactor/typed`** loads the whole module with `go/packages`, resolves the target to a `types.Object`, and rewrites with `dave/dst`. Used for what a patch cannot express: `x.Field`/`x.Method()` renames, and deleting or creating a declaration. It refuses rather than degrades — on a package that does not type-check, on a rename that would change a file the given paths do not cover, on a method whose interface lives outside the module, on a delete with surviving references.

`internal/refactor` holds only what both need: argument parsing (`refs.go`), the qualified-target grammar (`target.go`), package inference (`scope.go`), and the source lookups patchgen needs to reproduce a signature or a field exactly (`lookup.go`).

`target.go` parses `[dir ":"] ["*"] [qualifier "."] name{"." name}` — the one non-obvious rule is that the names are counted from the **right**, because an import path carries dots of its own and `gopkg.in/yaml.v3.Node` has to come out as `Node` in `gopkg.in/yaml.v3`. A `:` marks a path qualifier, since neither an identifier nor an import path may contain one. `cmd_ops.go` and `cmd_typed.go` hold the shared plumbing that turns a `Target` into a `refactor.Search` or a `typed.Query`, and both refuse a qualified target given alongside `--package` rather than picking one.

**A patchgen change must always carry the ` package X` context line.** Without it a struct-field or signature change matches the name in *every* package the given paths cover, so a same-named struct elsewhere is silently rewritten — verified by spike, and pinned by `testdata/refactor/edit_field_qualified`. That is why `findField`/`findSignature`/`findStructPackage` infer the package when the target did not name one, and why `refactor.InferPackage` errors on an ambiguous name instead of choosing.

Two details in `typed` are load-bearing and easy to undo: a file compiled into both a package and its test variant is decorated into two dst trees, so `ownedFile` skips edits made through the tree that is not rendered; and every path is compared after `EvalSymlinks`, because the go command resolves symlinks and `findFiles` does not.

**There are two qualifier resolvers, and using the wrong one breaks things quietly.** New source parsed from a flag carries no import paths, and the dst restorer rebuilds the import block from the paths its identifiers carry, so a `pkg.Name` selector has to be rewritten into a single path-carrying `dst.Ident`. In a *type expression* every such selector is a package qualifier, so `resolveTypeQualifiers` errors on one it cannot resolve. In a *body* it usually is not — `c.Host` selects a field — so `resolveBodyQualifiers` leaves an unresolved name as written; the file's own imports and `--import` are the only two things that make a name a package there. `body.go` also reads the body's **source text** (`bodySource`, via `Decorator.Ast.Nodes` and the `Fset` offsets) rather than re-rendering it, which is what carries comments through a `replace` substitution.

## Tests

Unit tests are colocated (`*_test.go`) and use testify with table-driven cases.

End-to-end tests are the main safety net: each file directly under `testdata/` is a [txtar] archive run by `TestIntegration` in `e2e_test.go`. File suffixes inside an archive:

| Suffix | Meaning |
|---|---|
| `.patch` | patch to apply (or a single line `=> examples/foo.patch` to reference a real patch file) |
| `.in.go` | input, renamed to `.go` before the run |
| `.out.go` | expected file contents after patching |
| `.diff` | expected stdout of `--diff` |
| `.diff.stderr` | expected stderr (description comments) |
| `.groupimports` | expected `--skip-import-processing` diff; only run for entries listed in `groupImportTestFiles` |

Each Go file pair runs four ways: `--skip-import-processing`, `--diff`, `--print-only`, and through the `patch` package API. Every input needs a matching output and vice versa, or archive loading errors.

`testsToSkip` in `e2e_test.go` is the known-bugs list — a to-do, not a dumping ground. When fixing a matching/elision bug, remove its entry rather than adding one. Per the repo rules, never add a skip to make a suite green.

`testdata/refactor/` is a second archive format, run by `TestRefactorIntegration`, covering the subcommands. One command runs over the whole extracted tree, and a file with no `.out.go` must come out byte-identical. `testdata/refactor/README.md` documents the members; `go.mod` and `want.error` are what the type-aware archives use.

`internal/refactor/typed/testdata/` holds small fixture modules, each with its own `go.mod`. The go tool ignores `testdata`, so they never join `./...`.

  [txtar]: https://godoc.org/github.com/rogpeppe/go-internal/txtar

## Patch-authoring notes

`examples/*.patch` are real, runnable patches (staticcheck fixes, gomock cleanup, destuttering) and are exercised by testdata archives that link to them with `=>`. `docs/PatchesInDepth.md` is the reference for patch semantics; `docs/Appendix.md` covers identifier vs expression vs statement metavariable types.

Known behavioral limits documented in the README, relevant when a patch "doesn't work": elision matching between minus and plus sections is imperfect, and once elision is used gopatch stops replacing after the first instance in a given scope. gopatch also deliberately operates on code that does not compile, so no type information is available to matching.

## Release

`docs/RELEASE.md`: update `CHANGELOG.md` (user-facing changes only), rename `## Unreleased` to the version + date, bump `VERSION` in the README install snippet, land a "Preparing release vX.Y.Z" PR, then push an annotated tag. goreleaser builds from `.goreleaser.yml` and injects `main._version`; the changelog body comes from `tools/cmd/extract-changelog`.
