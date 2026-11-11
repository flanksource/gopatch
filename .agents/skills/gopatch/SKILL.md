---
name: gopatch
description: Rewrite Go source across files with gopatch instead of sed, perl, awk, or a repeated hand-edit — moves and renames (file, type, struct, func, var, const, field, method, import, package), signature changes, call-site sweeps, struct field changes, deleting declarations, and scoped edits inside one function body. Load this whenever a Go change is the same change in more than one place, or before running `sed -i`, `perl -pi`, `gofmt -r`, or an `xargs` sweep over `*.go`.
---

# gopatch

**If a Go edit is the same change in more than one place, it is a `gopatch` command — not `sed -i`, not `perl -pi`, not N hand-edits.**

gopatch matches Go *syntax*. A pattern cannot leak into a string literal or a comment, cannot hit a same-named member of a different type, and does not care what line anything is on. The output is formatted, so no `gofmt -w` chaser is needed.

## Install

```shell
command -v gopatch || go install github.com/uber-go/gopatch@latest
```

That gets the patch engine, which has been in releases since 0.4.0. On a released binary it is spelled `gopatch -p rules.patch ./...`, without the `patch` subcommand.

The `patch`, `tree`, `ref`, `rename`, `move`, `add`, `delete`, `edit`, `replace` and `install` subcommands used below are **not in a release yet**; they live in the [flanksource fork](https://github.com/flanksource/gopatch). Build them from a checkout:

```shell
git clone https://github.com/flanksource/gopatch && cd gopatch && make install
```

`gopatch rename --help` tells you which build you have. If it errors, everything below has to be written as a patch file instead — see [references/patches.md](references/patches.md).

## Pick the command

| The change you want | Command |
|---|---|
| Inspect packages, files, and declarations with line numbers | `gopatch tree ./...` |
| Find the typed definitions and uses of a symbol | `gopatch ref User.Name --package model` |
| Rename a struct or any named type | `gopatch rename struct HTTPClient Client ./...`<br>`gopatch rename type Duration Interval ./...` |
| Rename a function, with its calls | `gopatch rename func doWork Work ./...` |
| …and in other modules | add repeatable `--consumers ../repos`, use `--consumers-from repos.txt`, or configure `.gopatch.yml` |
| Rename a struct field: declaration, every `x.Field`, every `T{Field: …}` key | `gopatch rename field User.Name Title ./...` |
| Rename a method, its interface, every implementation | `gopatch rename method '*Client.Do' Send ./...` |
| …and in other Go modules that require this module | add repeatable `--consumers ../repos`, or `--consumers-from repos.txt` |
| Move a Go file, changing its package and consumers when needed | `gopatch move file internal/old/user.go internal/model/ --dry-run` |
| Move a declaration to another file or package | `gopatch move struct old.User internal/model/user.go --diff` (also `type`, `func`, `var`, `const`, `method`) |
| Swap an import path, and the qualifier with it | `gopatch rename import encoding/json github.com/segmentio/encoding/json ./...` |
| Rewrite a package clause after a move | `gopatch rename package clientcmd mccontext ./clientcmd/mccontext` |
| Add a struct field | `gopatch add field User.Nickname string --tag 'json:"nickname"' ./...` |
| Add a leading parameter (the `ctx` sweep) | `gopatch add param Send ctx context.Context --import context ./...` |
| Change a signature | `gopatch edit func Send --params 'ctx context.Context, r *Request' --import context ./...` |
| Change a field's type or tag | `gopatch edit field User.ID --type UUID ./...` |
| Replace a whole body | `gopatch edit func Send --content 'return nil' ./...`<br>`gopatch edit method Client.Do --content @hack/do.go ./...` |
| Delete a struct field, and drop it from composite literals | `gopatch delete field User.Nickname ./...` |
| Delete a declaration | `gopatch delete func unusedHelper ./...` (also `struct`, `type`, `method`) |
| Create a declaration stub | `gopatch add struct Options ./config/options.go` (also `func`, `method`) |
| Text substitution scoped to one function body | `gopatch replace func report --match 'log\.Printf\((.*)\)' --content 'slog.Info($1)' --import log/slog ./...` |
| Anything shaped like a pattern: drop an argument, restructure a statement, `errors.New(fmt.Sprintf(...))` | a patch file → `gopatch patch -p rules.patch ./...` |

`gopatch <verb> --help` is accurate and worth reading; the full reference is [references/commands.md](references/commands.md).

## The one thing to understand: two engines

Subcommands are served by two engines, and which one you get decides what the command can do and what it needs from you.

**Syntactic** — `add field|param`, `delete field`, `edit field|func|method` with `--type`/`--tag`/`--params`/`--results`. These render a gopatch program and run it. They match by name, so they work on **code that does not compile** — mid-refactor, mid-merge, generated-but-broken.

**Type-aware** — `ref`, every `rename`, `move`, `delete struct|type|func|method`, `add struct|func|method`, `edit … --content`, `replace`. These load the whole module with `go/packages` and resolve exactly the references that mean the same symbol. `ref` prints those locations without changing files; transformation commands rewrite them. `User.Name` never touches `Account.Name`. The cost is that **the module must compile**; if it does not, the command reports the build errors and changes nothing:

```
the code does not type-check, so a type-aware refactoring cannot be trusted:
  broken.go:4:9: undefined: helperThatDoesNotExist
Fix the build first, or use one of the patch-generating commands, which work on code that does not compile
```

Type-aware commands also refuse rather than degrade: `delete func` fails while anything still calls it, naming the call sites.

Never mix the two in one logical change. A half-applied refactor that exits 0 is the failure mode both engines are built to avoid.

## Workflow

1. **Preview.** `-d` / `--diff` writes nothing. Do this first, every time, especially in a dirty worktree.
2. **Read the generated program** when a syntactic add/delete/edit command surprises you: `--print-patch` prints the gopatch program instead of applying it. Typed renames intentionally do not accept `--print-patch`, `--import`, or `--qualifier`; use `--diff`.
3. **Apply**, then build and run the focused tests. The output is gofmt'd, so no `gofmt -w` chaser. Imports are handled for you by the type-aware commands (that is what `--import` feeds); in a **patch file** you state import changes yourself, as a `-import "old"` / `+import "new"` pair.
4. **Scope by path.** Positional patterns are files and directories (`./...`, `./internal/http`, `user.go`), not package patterns. Dot/underscore directories, `testdata/` and `vendor/` are skipped. Type-aware commands load the whole module but only write files your paths cover, and refuse when a required edit falls outside them.

Every ref, rename, and move includes requiring modules from the active `go.work`. `--consumers <dir>` recursively discovers other modules whose `go.mod` requires the producer module; repeat it for multiple search roots, or use `--consumers-from <file>` with one root per line. `rename import` discovers modules containing the exact old import path instead, even for unrelated third-party paths. Every selected module must type-check, and every edit is prepared before the first write.

Consumer roots may also live in `<module>/.gopatch.yml` or `~/.gopatch.yml` as `consumers: [...]`. The project file replaces the home file, CLI consumer flags replace configured roots, and active workspace consumers are always added. Config paths are relative to the YAML file and may start with `~/`.

For `move`, use `--dry-run` for a compact paths/counts summary or `--diff` for the complete source diff. Named types carry their receiver methods; method-only moves stay within the receiver package. Rename and move print each planned target or consumer file change to stderr in every mode, validate the complete overlay, and install the change set transactionally.

Useful flags on transformation commands include `--print-only` (to stdout), `-v` (per-file report), `--skip-generated`, `--package` / a qualified target (`store.User.Name`, `./internal/store:User.Name`) when two packages declare the same name. `move` has its own preview contract described above. Flags and paths may come in any order; `gopatch -- <path>` forces a path that shares its name with a subcommand (`./add`, `./replace`) to be read as a path.

## Patch files

For anything the subcommands do not name — dropping an argument, folding two statements into one, a call whose arguments must be carried through — write a patch. Two `@@` lines separate the metavariable section from the diff:

```diff
# Drop the Mode argument from getInstance calls, whatever mode it names.
@@
var key, p, m, opts expression
@@
-getInstance(key, p, m, opts)
+getInstance(key, p, opts)
```

```shell
gopatch patch -d -p drop-mode.patch ./...   # preview
gopatch patch -p drop-mode.patch ./...      # apply
```

A metavariable (`identifier`, `expression`) is a hole that matches once and is reproduced in the `+` section; `...` elides the rest of an argument list, a body, or a statement block. Import lines in the patch both constrain the match and rewrite the import. See [references/patches.md](references/patches.md).

## Limits — know these before you promise a sweep

- **Elision stops after the first match in a scope.** Once `...` is used, gopatch replaces only the first instance in the given scope ([#10]); re-run until the diff is empty, or write the pattern without elision.
- **Matching elisions between the `-` and `+` sections is imperfect** ([#9]).
- The patch engine has **no type information** by design — that is what lets it run on code that does not compile. Precision on `x.Field` and `x.Method()` is exactly why those are type-aware commands.
- `edit … --params/--results` changes the declaration only. Call sites and bodies are yours: nothing can decide what to pass at a call that gained an argument.
- Error messages for malformed patch files are terse; `--print-patch` on a subcommand is a good way to see a program that is known to parse.

  [#9]: https://github.com/uber-go/gopatch/issues/9
  [#10]: https://github.com/uber-go/gopatch/issues/10

## References

- [references/commands.md](references/commands.md) — every subcommand, its flags, and what it refuses.
- [references/patches.md](references/patches.md) — the patch language: metavariables, elision, imports, statements, declarations.
- [references/from-sed.md](references/from-sed.md) — a cookbook built from real `sed -i` / `perl -pi` invocations found in agent session history, each with the gopatch command that replaces it and the bug the text edit was risking.
