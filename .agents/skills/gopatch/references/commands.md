# gopatch subcommand reference

Every command takes trailing path patterns — files or directories, `./...` for everything below the current directory. Dot and underscore directories, `testdata/` and `vendor/` are skipped.

## Naming the target

The target grammar is `[dir ":"] ["*"] [qualifier "."] name{"." name}`:

| Form | Example |
|---|---|
| A top-level declaration | `HTTPClient`, `Duration`, `doWork` |
| A field or method | `User.Name`, `Client.Do`, `'*Client.Do'` (quote the star) |
| Qualified by package name or import path | `store.User.Name`, `example.com/store.User.Name` |
| Qualified by the file or directory it is in | `./internal/store:User.Name`, `user.go:User.Name` |

Names are counted from the **right**, because an import path carries dots of its own: `gopkg.in/yaml.v3.Node` resolves to `Node` in `gopkg.in/yaml.v3`. A `:` always marks a path qualifier.

`--package` does the same job as a `pkg.` prefix and cannot be combined with one — a qualified target plus `--package` is refused rather than one silently winning. Qualify whenever more than one package under your paths declares the name; the syntactic commands error on an ambiguous name instead of picking.

## Flags shared by everything that writes

| Flag | Effect |
|---|---|
| `-d`, `--diff` | Print a diff to stdout, write nothing. **Use first, always.** |
| `--print-only` | Print whole patched files to stdout, write nothing. |
| `-v`, `--verbose` | Report per file whether it was patched. |
| `--skip-generated` | Skip files with a generated-code header. |
| `--skip-import-processing` | Do not run `goimports` over the result. |
| `--print-patch` | *Syntactic commands only.* Print the generated gopatch program instead of applying it. |

`move` shares `--diff` but not the other transformation flags; it provides `--dry-run` for a compact move summary and documents its own options below.

## rename

### Syntactic — works on code that does not compile

```shell
gopatch rename package http            httpclient      ./http
gopatch rename import  example.com/old example.com/new ./...
gopatch rename struct  HTTPClient      Client          ./...
gopatch rename type    Duration        Interval        ./...
gopatch rename func    doWork          Work            ./...
```

`struct`, `type` and `func` rewrite the declaration and every reference **in the declaring package**. To reach importers:

```shell
gopatch rename func New NewClient --import example.com/http ./...
gopatch rename func New NewClient --import example.com/http --qualifier httpc ./...
```

`--qualifier` is the name importing files use when it is not the package name.

`rename import` rewrites the path and, when the package name changes with it, every `old.X` selector:

```shell
# every http.X becomes client.X as well
gopatch rename import example.com/http example.com/client ./...

# spell the names out when a path's last segment is not the package name
gopatch rename import example.com/go-yaml gopkg.in/yaml.v3 --name yaml --to-name yaml ./...
```

Because these match by name, a local variable that happens to share it is rewritten too. Review with `-d`, or read the program with `--print-patch`.

### Type-aware — needs the module to compile

```shell
gopatch rename field  User.Name    Title ./...
gopatch rename method '*Client.Do' Send  ./...
gopatch rename field  User.Name    Title --consumers ../services ./...
gopatch rename method Client.Do    Send  --consumers-from repos.txt ./...
```

`rename field` moves the declaration, every `x.Field` selector on that type, and every key in a keyed composite literal of it, together, in one pass over the whole module. A field of the same name on another type is left alone.

`rename method` renames the method, the interface method it implements, and every other implementation of that interface. It refuses when the interface is declared outside the module.

`--consumers <dir>` is repeatable and recursively discovers other modules whose `go.mod` requires the producer module. `--consumers-from <file>` reads the same search roots one per line. Both flags are limited to `rename field` and `rename method`; all selected modules must type-check, and no file is written until every edit has been prepared. Consumer code is resolved against the producer version it currently requires.

## move

```shell
gopatch move file   internal/old/user.go internal/model/ --dry-run
gopatch move struct old.User             internal/model/user.go --diff
gopatch move type   old.ID               internal/model/id.go
gopatch move func   file.go:Load          internal/config/load.go
gopatch move var    old.DefaultTimeout    internal/config/defaults.go
gopatch move const  old.DefaultPort       internal/config/defaults.go
gopatch move method file.go:Client.Close  internal/client/close.go
```

`move file` accepts an existing or new destination directory, preserving the source basename, or an explicit `.go` path. A same-package move only relocates source. A cross-package move rewrites the package clause, imports, and every typed reference in the module. It also rewrites modules in the active `go.work`; use repeatable `--consumers` or `--consumers-from` to add consumer search roots.

Moving a named type also moves all receiver methods. `move struct` additionally verifies that the target's underlying type is a struct. A method moved by itself must remain in its receiver package. A source file left with no declarations is deleted.

`move` prepares and type-checks the full overlay before the first filesystem change. It refuses destination collisions, import cycles, inaccessible private dependencies, incompatible build constraints or `_test.go` classification, cgo files, partial moves of files containing `init`, and grouped constants whose implicit values or `iota` semantics cannot be preserved.

`--dry-run` prints a compact summary of source and destination paths, package changes, declarations, references, and file create/update/delete counts. `--diff` prints the full unified diff. Both write nothing and cannot be combined. `move` does not accept `--print-only`.

## add

### Change an existing declaration (syntactic)

```shell
gopatch add field User.Nickname string --tag 'json:"nickname"' ./...
gopatch add param Send ctx context.Context --import context ./...
gopatch add param Send '' context.Context --import context ./...   # unnamed parameter
gopatch add param Send n int ./...                                 # type already in scope
```

`add param` adds a **leading** parameter — the `ctx context.Context` sweep. It changes the declaration only; call sites are yours.

`add field` appends the field to the struct, tag and all.

### Create a declaration (type-aware, appended to one named file)

```shell
gopatch add struct Options ./config/options.go
gopatch add func   Load    ./config/options.go --params 'path string' --results '(*Options, error)'
gopatch add method Options.Validate ./config/options.go --results error
```

A patch cannot create a declaration — it has nothing to match — so these take one file rather than path patterns, and the name is never qualified: the file already says which package it lands in.

## delete

```shell
gopatch delete field  User.Nickname    ./...   # syntactic
gopatch delete struct Options          ./...   # type-aware
gopatch delete type   Interval         ./...
gopatch delete func   unusedHelper     ./...
gopatch delete method Options.Validate ./...
```

`delete field` removes the field declaration and drops the key from composite literals; `--import` extends that to literals in importing packages. It does **not** remove reads of the field, so the package will not build until you fix them — that is the syntactic engine's price for working without type information.

A struct tag is part of a field declaration and cannot be matched by a wildcard, so `delete field` and `edit field` look the existing field up in source and reproduce its tag verbatim, failing loudly rather than guessing. `--print-patch` shows the result:

```diff
# delete field User.Nickname and drop it from User literals
@@
@@
 package spike

 type User struct {
   ...
-  Nickname string `json:"nickname,omitempty"`
   ...
 }
```

Note the ` package spike` context line. Without it the change would match `User` in *every* package your paths cover.

The rest remove a whole declaration, which a patch cannot express (its `+` section may not be empty), so they are type-aware — and they refuse while anything still refers to the target, naming the references:

```
func getInstance is still used at instance.go:24:6, instance.go:25:6; remove those references first
```

That refusal is the feature. A line-range `sed` delete removes the text and leaves the callers dangling.

## edit

There is no `edit struct`: renaming one is `rename struct`, and changing its shape is `add field` / `delete field` / `edit field`.

```shell
# a field's type or tag                       (syntactic)
gopatch edit field  User.ID    --type UUID              ./...
gopatch edit field  User.Email --tag 'json:"email"'     ./...

# a signature — the half you omit is kept     (syntactic)
gopatch edit func   Send      --params 'ctx context.Context, r *Request' --import context ./...
gopatch edit method Client.Do --results '(*Response, error)'  ./...
gopatch edit func   Send      --params ''               ./...   # drop all parameters

# a whole body                                (type-aware)
gopatch edit func   Send      --content 'return errors.New("not implemented")' --import errors ./...
gopatch edit method Client.Do --content @hack/do.go     ./...
gopatch edit func   Send      --content ''              ./...   # empty body
```

A signature change updates **neither call sites nor bodies**. There is no way to decide what to pass at a call that gains an argument, or what to return from a body that gains a result. `--content` is how you write the body afterwards.

`--params` takes parentheses optionally; `--results` needs them wherever Go does. `--content` takes statements without the braces, or `@file`.

`edit func` is the one command whose engine depends on its flags — `--params`/`--results` generate a patch, `--content` runs the type-aware path — so it refuses the two together rather than accepting and ignoring one.

## replace

```shell
gopatch replace func   Send      --match 'log\.Printf\((.*)\)' --content 'slog.Info($1)' --import log/slog ./...
gopatch replace method Client.Do --match 'http://'  --content 'https://' ./...
gopatch replace func   Send      --match '(?m)^\s*// TODO.*$' --content '' ./...
```

The escape hatch for what no tree pattern expresses well: a log call's arguments, a string literal, a comment. It is a textual substitution — but a **scoped** one. The body is located with type information, so the regular expression never reaches another function, another type's method, or a same-named declaration in another package.

`--match` is a Go regular expression; `$1` in `--content` expands a capture group. The result is parsed as Go before anything is written, so a substitution that would not produce a valid body is refused rather than leaving the file broken. A pattern that matches nothing is an error too.

To replace a whole body rather than part of one, use `edit … --content`.

## --import

`--import` means two different things depending on the command, and both are worth knowing:

- On the syntactic renames and `delete field`: the import path of the **declaring** package, which extends the rewrite to qualified references in packages that import it.
- On `add param`, `edit … --params/--results/--content`, and `replace`: an import the **new source** needs, as `path` or `qualifier=path`. Repeat for more than one. Imports the file already has are used automatically.

## Exit behavior

Errors go to stderr and set a non-zero exit code; usage is never dumped to stdout, so `--diff` and `--print-only` output stays clean and pipeable.
