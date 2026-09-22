# gopatch [![Go](https://github.com/uber-go/gopatch/actions/workflows/go.yml/badge.svg)](https://github.com/uber-go/gopatch/actions/workflows/go.yml) [![codecov](https://codecov.io/gh/uber-go/gopatch/branch/main/graph/badge.svg?token=tFsx23GSTB)](https://codecov.io/gh/uber-go/gopatch)

gopatch is a tool to match and transform Go code. It is meant to aid in
refactoring and restyling.

# Table of contents

- [Introduction](#introduction)
- [Getting started](#getting-started)
  - [Installation](#installation)
  - [Your first patch](#your-first-patch)
  - [Apply the patch](#apply-the-patch)
  - [Next steps](#next-steps)
- [Usage](#usage)
  - [Commands](#commands)
    - [Inspecting the source tree](#inspecting-the-source-tree)
    - [Finding symbol references](#finding-symbol-references)
    - [Commands that generate a patch](#commands-that-generate-a-patch)
    - [Commands that use type information](#commands-that-use-type-information)
  - [Options](#options)
  - [Agent skills](#agent-skills)
- [Patches](#patches)
  - [Metavariables](#metavariables)
  - [Statements](#statements)
  - [Elision](#elision)
  - [Comments](#comments)
    - [Description comments](#description-comments)
    - [Usage in diff mode](#usage-during-diff-mode)
- [Examples](#examples)
- [Project status](#project-status)
  - [Goals](#goals)
  - [Known issues](#known-issues)
  - [Upcoming](#upcoming)
- [Similar Projects](#similar-projects)
- [Credits](#credits)

# Introduction

gopatch operates like the Unix `patch` tool: given a patch file and another
file as input, it applies the changes specified in the patch to the provided
file.

```
 .-------.                      .-------.
/_|      |.                    /_|      |.
|        ||.    +---------+    |        ||.
|   .go  |||>-->| gopatch |>-->|   .go  |||
|        |||    +---------+    |        |||
'--------'||      ^            '--------'||
 '--------'|      |             '--------'|
  '--------'      |              '--------'
     .-------.    |
    /_|      |    |
    |        +----'
    | .patch |
    |        |
    '--------'
```

What specifically differentiates it from `patch` is that unlike plain text
transformations, it can be smarter because it understands Go syntax.

# Getting started

## Installation

Download a **pre-built binary** of gopatch from the [Releases page] or by
running the following command in your terminal and place it on your `$PATH`.

  [Releases page]: https://github.com/uber-go/gopatch/releases

```bash
VERSION=0.4.0
URL="https://github.com/uber-go/gopatch/releases/download/v$VERSION/gopatch_${VERSION}_$(uname -s)_$(uname -m).tar.gz"
curl -L "$URL" | tar xzv gopatch
```

Alternatively, if you have Go installed, **build it from source** and install
it with the following command.

```bash
go install github.com/uber-go/gopatch@latest
```

From a checkout, `make install` does the same thing and additionally stamps the
version from `git describe`, so `gopatch version` identifies the commit you
built rather than reporting `unknown`. It installs into `$GOBIN`, or
`$(go env GOPATH)/bin` when that is unset; pass `INSTALL_BIN=<dir>` to put it
somewhere else.

Building from source requires Go 1.26.1 or newer.

## Your first patch

Write your first patch.

```shell
$ cat > ~/s1028.patch
# Replace redundant fmt.Sprintf with fmt.Errorf
@@
@@
-import "errors"

-errors.New(fmt.Sprintf(...))
+fmt.Errorf(...)
```

This patch is a fix for staticcheck [S1028]. It searches for uses of
[`fmt.Sprintf`] with [`errors.New`], and simplifies them by replacing them
with [`fmt.Errorf`].

  [S1028]: https://staticcheck.io/docs/checks#S1028
  [`fmt.Sprintf`]: https://golang.org/pkg/fmt/#Sprintf
  [`errors.New`]: https://golang.org/pkg/errors/#New
  [`fmt.Errorf`]: https://golang.org/pkg/fmt/#Errorf

For example,

```go
return errors.New(fmt.Sprintf("invalid port: %v", err))
// becomes
return fmt.Errorf("invalid port: %v", err)
```

## Apply the patch

-  `cd` to your Go project's directory.

  ```shell
  $ cd ~/go/src/example.com/myproject
  ```

  Run `gopatch patch` on the project, supplying the previously written patch
  with the `-p` flag.

  ```shell
  $ gopatch patch -p ~/s1028.patch ./...
  ```

  This will apply the patch on all Go code in your project.

  Check if there were any instances of this issue in your code by running
  `git diff`.
- Instead, `cd` to your Go project's directory.

  ```shell
  $ cd ~/go/src/example.com/myproject
  ```

  Run `gopatch patch` on the project, supplying the previously written patch
  with the `-p` flag along with '-d' flag.

  ```shell
  $ gopatch patch -d -p ~/s1028.patch ./...
  ```

  This will turn on diff mode and will write the diff to stdout instead of modifying all
  the Go code in your project. To provide more context on what the patch does, if
  there were description comments in the patch, they will also get displayed at 
  the top. To learn more about description comments jump to section [here](#description-comments)
  
  For example if we applied patch ~/s1028 to our testfile error.go
  ```shell
  $ gopatch patch -d -p ~/s1028.patch ./testdata/test_files/diff_example/
  ```
  Output would be : 
  ```
  gopatch/testdata/test_files/diff_example/error.go:Replace redundant fmt.Sprintf with fmt.Errorf
  --- gopatch/testdata/test_files/diff_example/error.go
  +++ gopatch/testdata/test_files/diff_example/error.go
  @@ -7,7 +7,7 @@
 
  func foo() error {
          err := errors.New("test")
  -       return errors.New(fmt.Sprintf("error: %v", err))
  +       return fmt.Errorf("error: %v", err)
  }
 
   func main() {

  ```
  Note: Only the description comments of patches that actually **apply** are displayed.

## Next steps

To learn how to write your own patches, move on to the [Patches] section. To
dive deeper into patches, check out [Patches in depth].

  [Patches in depth]: docs/PatchesInDepth.md

To experiment with other sample patches, check out the [Examples] section.

  [Patches]: #patches
  [Examples]: #examples

# Usage

To apply a patch file, provide the following arguments.

```
gopatch patch [options] pattern ...
```

Where pattern specifies one or more Go files, or directories containing Go
files. For directories, all Go code inside them and their descendants will be
considered by gopatch.

Flags and patterns may be given in any order, and `gopatch -- <path>` forces a
path to be treated as a path even when it shares a name with a subcommand.

> **Deprecated:** patches may still be applied by the root command, as
> `gopatch [options] pattern ...`. That spelling now prints a warning to stderr
> and will be removed in v0.6.0. Use `gopatch patch`. It also resolves a bare
> `examples` or `patches` argument to the help topic of that name, so a
> directory called either needs `gopatch -- examples`; `gopatch patch` does not
> have that problem.

The reference documentation ships inside the binary, so it is available from a
`go install` build with no checkout:

```shell
gopatch help patches         # the patch language, in depth
gopatch help metavariables   # identifiers vs expressions vs statements
gopatch help examples        # the five patches in examples/, ready to run
```

## Commands

For the common mechanical refactorings you do not need to write a patch at all.
These subcommands take the change as arguments instead.

```
gopatch patch          [options]                   [pattern...]
gopatch tree                                       [pattern...]
gopatch ref            <symbol>

gopatch rename struct  <Old> <New>                 [pattern...]
gopatch rename type    <Old> <New>                 [pattern...]
gopatch rename func    <old> <new>                 [pattern...]
gopatch rename package <old> <new>                 [pattern...]
gopatch rename import  <old-path> <new-path>       [pattern...]
gopatch rename field   <Struct>.<field> <new>      [pattern...]
gopatch rename method  <Recv>.<method> <new>       [pattern...]

gopatch move   file    <source.go> <destination-dir|destination.go>
gopatch move   struct  <target> <destination.go>
gopatch move   type    <target> <destination.go>
gopatch move   func    <target> <destination.go>
gopatch move   var     <target> <destination.go>
gopatch move   const   <target> <destination.go>
gopatch move   method  <Type>.<method> <destination.go>

gopatch add    field   <Struct>.<field> <type>     [pattern...]
gopatch add    param   <func> <name> <type>        [pattern...]
gopatch add    struct  <Name> <file.go>
gopatch add    func    <name> <file.go>            --params '...' --results '...'
gopatch add    method  <Recv>.<name> <file.go>     --params '...' --results '...'

gopatch delete field   <Struct>.<field>            [pattern...]
gopatch delete struct  <Name>                      [pattern...]
gopatch delete type    <Name>                      [pattern...]
gopatch delete func    <name>                      [pattern...]
gopatch delete method  <Recv>.<method>             [pattern...]

gopatch edit   field   <Struct>.<field> --type T | --tag '...'
gopatch edit   func    <name>                      --params '...' --results '...'
gopatch edit   func    <name>                      --content '...' | --content @file
gopatch edit   method  <Recv>.<method>             --params '...' --results '...'
gopatch edit   method  <Recv>.<method>             --content '...' | --content @file

gopatch replace func   <name>            --match <re> --content '...'
gopatch replace method <Recv>.<method>   --match <re> --content '...'

gopatch install        [skill...]        [--agent <id>] [--dir <path>]
```

They are served by two engines, and each command's `--help` says which one it
uses. Both support `--diff` and in-place rewriting. Commands that transform
existing files also support `--print-only`; `move` uses `--dry-run` for a
compact relocation summary instead.

### Inspecting the source tree

`gopatch tree` prints a Clicky tree of package nodes, their Go files, and each package-level declaration with its source line, using ANSI colors on terminal output. Functions and methods include parameter names and types plus their starting and ending lines; callable names, punctuation, parameter names, and parameter types use distinct syntax colors. Methods are nested below their receiver type; when the receiver is declared outside the inspected file, they are nested below a receiver-specific `methods` node. It parses syntax without loading types, so it also works when a module is mid-refactor and does not compile. Paths accept the same files, directories, and `./...` suffix as transformation commands; with no path it inspects `./...`. Test files are excluded unless `--tests=true` is set.

```shell
gopatch tree
gopatch tree ./internal/...
gopatch tree ./model/user.go
gopatch tree --private=false --methods=true --functions=true --fields=false --variables=false --tests=false ./...
```

### Finding symbol references

`gopatch ref` resolves a package-level declaration, field, or method with Go type information and prints its definitions and uses as a Clicky tree grouped by package and file. Each location includes its line, column, and trimmed source line. Method results include linked interface methods and their concrete implementations.

```shell
gopatch ref User
gopatch ref User.Name --package model
gopatch ref store.User.Name
gopatch ref ./internal/store:User.Name --consumers ../services
```

The command searches the owning module, requiring modules in the active `go.work`, and consumer modules selected through `.gopatch.yml`, `--consumers`, or `--consumers-from`. All searched modules must type-check. A dotted target such as `store.User` can mean either `User` in package `store` or field/method `User` on type `store`; when both exist, gopatch reports both candidates and asks for a package or path-qualified target.

### Naming the target

Every command above takes its target as a dotted name, and every one of them
accepts that name qualified with the package or the directory that declares it:

```
User.Name                     the field, in whichever package declares User
store.User.Name               ... in the package whose clause is "store"
example.com/store.User.Name   ... in the package at that import path
./internal/store:User.Name    ... in the package under that directory
user.go:User.Name             ... in the package that file belongs to
```

A path qualifier is separated with `:`, because an import path may contain dots
and slashes but never a colon. It has to be recognizable as a path, so a bare
directory is written `./store`, not `store`. The name is split off from the
right, which is what makes `gopkg.in/yaml.v3.Node` the type `Node` in
`gopkg.in/yaml.v3` rather than four segments of something.

A qualifier is only needed when the name is ambiguous, and gopatch says so when
it is instead of choosing a package for you:

```shell
$ gopatch edit field User.ID --type UUID ./...
"User" is declared in packages [api store]; qualify field with the one you mean, as in "api.User.ID"
```

`--package <clause>` is the older spelling of a package-name qualifier and still
works; giving both is refused rather than resolved by precedence.

For commands that generate a patch, the qualifier selects and scopes the syntactic match. For typed renames, it identifies the exact declaration; references in the module, active Go workspace, and selected consumer modules move with it.

`add struct|func|method` are the exception — they never take a qualified name,
because their `<file.go>` argument already says which package the new
declaration goes into.

### Commands that generate a patch

`add field|param`, `delete field` and `edit field|func` build a gopatch program from their arguments and run it through the engine described in the rest of this document. `--print-patch` writes that program to stdout instead of applying it.

```shell
$ gopatch add field User.Enabled bool --package model --print-patch
@@
@@
 type User struct {

+Enabled bool
```

These syntactic commands work on code that does not compile, but match by name rather than type. Review them with `--diff` when scope is not obvious.

### Commands that use type information

`ref`, every `rename` command, `move`, `delete struct|type|func|method`,
`add struct|func|method`, `edit method`, `edit func --content` and `replace`
cannot be expressed as a patch. A patch matching `x.Name` would rewrite every
type's `Name`; a patch cannot delete a declaration at all, because its
replacement section may not be empty; it cannot create one, because it would
have nothing to match; a receiver and a parameter list in one change need two
elisions, which cross-connect; and a whole function body is not something a
minus section can match.

These resolve the target with `go/types` and rewrite the source with
[dave/dst], so they change exactly the references that mean the same symbol:

```shell
$ gopatch rename field User.Name Title -d ./...
```

renames the declaration, every `u.Name` on a `User`, and every `User{Name: ...}`
key, in every package of the module — and leaves another type's `Name` alone.

Every rename and move automatically includes requiring modules from the active `go.work`. Pass `--consumers <dir>` more than once, list search directories one per line in `--consumers-from <file>`, or configure search roots in `.gopatch.yml`. Gopatch recursively finds modules whose `go.mod` requires the producer module; `rename import` instead finds modules containing the exact old import path, including unrelated third-party paths.

```shell
gopatch rename field User.Name Title --consumers ../services --consumers ../tools ./...
gopatch rename method Client.Do Send --consumers-from repos.txt --diff ./...
```

Project configuration at the producer module root overrides user configuration, including an empty consumer list. When the project file is absent, gopatch reads `~/.gopatch.yml`:

```yaml
consumers:
  - ../consumer-repos
  - ~/go/src/github.com/example
```

Configured paths are relative to the configuration file. `~/` is expanded, symlinks are canonicalized, duplicates are removed, and missing roots or unknown YAML keys fail before refactoring. Supplying either consumer CLI flag replaces configured roots; an explicit CLI search that finds no matching module is an error, while configured roots with no match are a successful no-op. Active `go.work` consumers are always unioned in.

Consumer source is resolved against the producer version its module currently requires. Updating that requirement is a separate dependency-management step; local `replace` directives and `go.work` files continue to control which producer source `go/packages` sees.

### Moving files and declarations

`gopatch move` relocates a complete Go file or one package-level declaration. Moving within a package only changes file ownership. Moving to another package updates the package clause, imports, and typed references throughout the producer module, every module in the active `go.work`, and any additional consumers selected with `--consumers` or `--consumers-from`.

```shell
gopatch move file internal/old/user.go internal/model/ --dry-run
gopatch move struct old.User internal/model/user.go --diff
gopatch move func file.go:Load internal/config/load.go
gopatch move var old.DefaultTimeout internal/config/defaults.go
gopatch move method file.go:Client.Close internal/client/close.go
```

A named type carries all of its receiver methods. A method moved alone must remain in its receiver's package. Empty source files are removed, a missing destination directory becomes a package named after that directory, and an existing destination file must declare the target package.

Before writing, gopatch renders and type-checks the complete producer-and-consumer overlay. It refuses destination name collisions, import cycles, private dependencies that would become inaccessible, incompatible test/build contexts, partial `init` ownership changes, cgo files, and unsafe grouped constants. `--dry-run` prints paths, package changes, declaration and reference counts, and file create/update/delete counts without source content; `--diff` prints the full patch. The two modes are mutually exclusive.

As edits are planned, rename and move commands print Clicky-formatted lines to stderr in every output mode, keeping stdout clean for diffs and source output:

```text
✏️ target update old/user.go
➕ target create new/user.go
✏️ consumer example.com/app update app/main.go
```

These are planned changes and can appear before a later validation failure. Default-mode writes remain transactional: after the full overlay validates, either every target and consumer file is installed or the original set is restored.

In exchange they need the module to compile. If it does not, they report the
build errors and change nothing. A few more consequences:

-   The whole module is loaded, whatever paths you give, because a reference can
    live in any package. The paths bound what may be *written*: if the change
    reaches a file they do not cover, the command says so and writes nothing,
    rather than applying half a rename.
-   Consumer search roots include normal nested modules and skip hidden, underscore-prefixed, `vendor` and `testdata` directories. A selected consumer that does not reference the symbol is simply unchanged, but it must still type-check.
-   `rename method` moves the interface methods a type is required to have, and
    their other implementations, along with the method. If one of them is
    outside the module — `fmt.Stringer`, say — the rename is refused.
-   `delete` refuses while anything still refers to the declaration, and lists
    the positions. An import left with no other user goes with it.
-   `add struct|func|method` append to a named file. A generated function's body
    is `panic("not implemented")`, so the file still compiles. A signature that
    names a package the file does not import yet needs `--import <path>`, or
    `--import <qualifier>=<path>` to add it under an explicit alias.
-   `edit method --params/--results` changes the signature and nothing else. Like
    `edit func`, it updates neither call sites nor bodies, so adding a result
    leaves the method without a return statement for you to write — which is
    what `--content` is for, in a second run.
-   `--print-patch` and `--skip-import-processing` are not accepted, because
    there is no patch and the engine owns the import block.

  [dave/dst]: https://github.com/dave/dst

### Replacing a body

`--content` on `edit func` and `edit method` replaces a function's whole body.
It takes Go statements — not a whole function, and not the braces — or
`@<path>` to read them from a file:

```shell
$ gopatch edit method Client.Do --content 'return c.transport.RoundTrip(r)' ./...
$ gopatch edit func  Send       --content @hack/send.go --import log/slog ./...
$ gopatch edit func  Send       --content '' ./...
```

`gopatch replace` changes part of a body instead, by regular expression:

```shell
$ gopatch replace func Send --match 'log\.Printf\((.*)\)' --content 'slog.Info($1)' --import log/slog ./...
```

This is the escape hatch for a change no pattern over the syntax tree expresses
well — a log call's arguments, a string literal, a comment. It is a textual
substitution, but a scoped one: the body is located with type information, so
the expression cannot reach another function, another type's same-named method,
or another package's same-named declaration. The pattern sees the body's source
as written, comments included, between the braces and not including them; it
spans the whole body, so `^` and `$` anchor the body rather than a line unless
you write `(?m)`.

Three things are refused rather than guessed at:

-   A pattern that **matches nothing** is an error. Exiting 0 having changed
    nothing is what a mistyped expression looks like otherwise.
-   Content that **is not valid Go** is rejected before anything is written, and
    the message shows what the substitution produced.
-   `--content` cannot be combined with `--params`/`--results`. On `edit func`
    they are different engines; on `edit method` they are the same engine but
    still separate runs, because the body you write has to be the one the new
    signature needs, and you cannot see it until the signature has changed.

Neither is type-checked, though — only parsed. A body that refers to a name that
is not in scope, or that names a package you did not pass `--import` for, is
written out and fails at build time. A qualifier is only resolved to an import
when the file already imports it or `--import` says so, because nothing in a
reparsed body distinguishes a package reference from `c.Host`.

## Options

Every flag is documented in the binary — `gopatch patch --help`, and
`--help` on any subcommand — so what follows is the same text with worked
examples, not a second source of truth.

- `-p file`, `--patch=file`

    Path to a patch file specifying a transformation. Read more about the
    patch file format in [Patches].

    Provide this flag multiple times to apply multiple patches in-order.

    ```shell
    $ gopatch patch -p foo.patch -p bar.patch path/to/my/project
    ```

    If this flag is omitted, a patch is expected on stdin.

    ```shell
    $ gopatch patch path/to/my/project << EOF
    @@
    @@
    -foo
    +bar
    EOF
    ```
- `-d`, `--diff`

    Flag to turn on diff mode. Provide this flag to write the diff to stdout instead
    of modifying the file and display applied patches' [description comments](#description-comments) if they exist. 
    Use in conjunction with -p to provide patch file.
    
    Only need to apply the flag once to turn on diff mode

    ```shell
    $ gopatch patch -d -p foo.patch -p bar.patch path/to/my/project
    ```

    If this flag is omitted, normal patching occurs which modifies the
    file instead.
- `--print-only`
  
  Flag to turn on print-only mode. Provide this flag to write the changed code to stdout instead of modifying the
  file and display applied patches' description comments to stderr if they exist.
  
    ```shell
    $ gopatch patch --print-only -p foo.patch -p bar.patch path/to/my/project
    ```  
- `--skip-import-processing`
  
  Flag to turn on skip-import-processing mode. Provide this flag to disable
  import formatting for imports that were not part of the patch changes.
    ```shell
    $ gopatch patch --skip-import-processing -p foo.patch -p bar.patch path/to/my/project
    ```

- `--skip-generated`
  
  Flag to turn on skip-generated code mode. Provide this flag to skip running the
  tool on generated code. A file is considered containing generated code if it 
  has `@generated` or `^// Code generated .* DO NOT EDIT\.$` in the comment
  header.
    ```shell
    $ gopatch patch --skip-generated -p foo.patch -p bar.patch path/to/my/project
    ```

## Agent skills

gopatch ships with a skill for AI coding agents that teaches them to reach for
it instead of `sed -i` when they have to change Go code in more than one place.
The files are embedded in the binary, so this needs no checkout and no network.

```shell
$ gopatch install
gopatch: 4 created  ~/.claude/skills/gopatch
gopatch: 4 created  ~/.codex/skills/gopatch
```

With no arguments it installs every skill into every agent it finds a
configuration directory for — `agents` (the client-agnostic `~/.agents/skills`
convention), `claude`, `codex`, `cursor`, `gemini` and `opencode`. Re-running is
safe: files that already match are reported as `unchanged`, and a skill
directory that is a symlink to a checkout is left alone rather than written
through.

```shell
$ gopatch install --list            # what this binary carries, and what is here
$ gopatch install --dry-run         # report without writing
$ gopatch install --agent claude    # one agent, detected or not
$ gopatch install --dir ./.agents/skills   # a directory of your choosing
```

The skill source lives in [`.agents/skills`](.agents/skills), and the same
directory is published as a plugin for the Claude Code and Codex marketplaces —
see [`.agents/README.md`](.agents/README.md).

# Patches

Patch files are the input to gopatch that specify how to transform code. Each
patch file contains one or more patches. This section provides an introduction
to writing patches; look at [Patches in depth] for a more detailed
explanation, or run `gopatch help patches`, which prints the same document from
inside the binary.

Each patch specifies a code transformation. These are formatted like unified
diffs: lines prefixed with `-` specify matching code should be deleted, and
lines prefixed with `+` specify that new code should be added.

Consider the following patch.

```diff
@@
@@
-foo
+bar
```

It specifies that we want to search for references to the identifier `foo` and
replace them with references to `bar`. (Ignore the lines with `@@` for now.
We will cover those below.)

A more selective version of this patch will search for uses of `foo` where it
is called as a function with specific arguments.

```diff
@@
@@
-foo(42)
+bar(42)
```

This will search for invocations of `foo` as a function with the specified
argument, and replace only those with `bar`.

gopatch understands Go syntax, so the above is equivalent to the following.

```diff
@@
@@
-foo(
+bar(
  42,
 )
```

## Metavariables

Searching for hard-coded exact parameters is limited. We should be able to
generalize our patches.

The previously ignored `@@` section of patches is referred to as the
**metavariable section**. That is where we specify **metavariables** for the
patch.

Metavariables will match any code, to be reproduced later. Think of them like
holes to be filled by the code we match. For example,

```diff
@@
var x expression
@@
# rest of the patch
```

This specifies that `x` should match any Go expression and record its match
for later reuse.

> **What is a Go expression?**
>
> Expressions usually refer to code that has value. You can pass these as
> arguments to functions. These include `x`, `foo()`, `user.Name`, etc.
>
> Check the [Identifiers vs expressions vs statements] section of the appendix
> for more.

  [Identifiers vs expressions vs statements]: docs/Appendix.md#identifiers-vs-expressions-vs-statements

So the following patch will search for invocations of `foo` with a single
argument---any argument---and replace them with invocations of `bar` with the
same argument.

```diff
@@
var x expression
@@
-foo(x)
+bar(x)
```

| Input              | Output             |
|--------------------|--------------------|
| `foo(42)`          | `bar(42)`          |
| `foo(answer)`      | `bar(answer)`      |
| `foo(getAnswer())` | `bar(getAnswer())` |


Metavariables hold the entire matched value, so we can add code around them
without risk of breaking anything.

```diff
@@
var x expression
@@
-foo(x)
+bar(x + 3, true)
```

| Input              | Output                       |
|--------------------|------------------------------|
| `foo(42)`          | `bar(42 + 3, true)`          |
| `foo(answer)`      | `bar(answer + 3, true)`      |
| `foo(getAnswer())` | `bar(getAnswer() + 3, true)` |

For more on metavariables see [Patches in depth/Metavariables].

  [Patches in depth/Metavariables]: docs/PatchesInDepth.md#metavariables

## Statements

gopatch patches are not limited to transforming basic expressions. You can
also transform statements.

> **What is a Go statements?**
>
> Statements are instructions to do things, and do not have value. They cannot
> be passed as parameters to other functions. These include assignments
> (`foo := bar()`), if statements (`if foo { bar() }`), variable declarations
> (`var foo Bar`), and so on.
>
> Check the [Identifiers vs expressions vs statements] section of the appendix
> for more.

For example, consider the following patch.

```diff
@@
var f expression
var err identifier
@@
-err = f
-if err != nil {
+if err := f; err != nil {
   return err
 }
```

The patch declares two metavariables:

- `f`: This represents an operation that possibly returns an `error`
- `err`: This represents the name of the `error` variable

The patch will search for code that assigns to an error variable immediately
before returning it, and inlines the assignment into the `if` statement. This
effectively [reduces the scope of the variable] to just the `if` statement.

  [reduces the scope of the variable]: https://github.com/uber-go/guide/blob/master/style.md#reduce-scope-of-variables

<table>
<thead><tr><th>Input</th><th>Output</th></tr></thead>
<tbody>
<tr><td>

```go
err = foo(bar, baz)
if err != nil {
   return err
}
```

</td><td>

```go
if err := foo(bar, baz); err != nil {
   return err
}
```

</td></tr>
<tr><td>

```go
err = comment.Submit(ctx)
if err != nil {
  return err
}
```

</td><td>

```go
if err := comment.Submit(ctx); err != nil {
  return err
}
```

</td></tr>
</tbody></table>

For more on transforming statements, see [Patches In Depth/Statements].

  [Patches In Depth/Statements]: docs/PatchesInDepth.md#statements

## Elision

Matching a single argument is still too selective and we may want to match a
wider criteria.

For this, gopatch supports **elision** of code by adding `...` in many places.
For example,

```diff
@@
@@
-foo(...)
+bar(...)
```

The patch above looks for all calls to the function `foo` and replaces them
with calls to the function `bar`, regardless of the number of arguments they
have.

| Input                      | Output                     |
|----------------------------|----------------------------|
| `foo(42)`                  | `bar(42)`                  |
| `foo(42, true, 1)`         | `bar(42, true, 1)`         |
| `foo(getAnswer(), x(y()))` | `bar(getAnswer(), x(y()))` |

Going back to the patch from [Statements], we can instead write the following
patch.

  [Statements]: #statements

```diff
@@
var f expression
var err identifier
@@
-err = f
-if err != nil {
+if err := f; err != nil {
   return ..., err
 }
```

This patch is almost exactly the same as before except the `return` statement
was changed to `return ..., err`. This will allow the patch to operate even on
functions that return multiple values.

<table>
<thead><tr><th>Input</th><th>Output</th></tr></thead>
<tbody>
<tr><td>

```go
err = foo()
if err != nil {
   return false, err
}
```

</td><td>

```go
if err := foo(); err != nil {
   return false, err
}
```

</td></tr>
</tbody></table>

For more on elision, see [Patches in depth/Elision].

  [Patches in depth/Elision]: docs/PatchesInDepth.md#elision

## Comments

Patches come with comments to give more context about what they do.

Comments are prefixed by '#'

For example:

```
# Replace time.Now().Sub(x) with time.Since(x)
@@
# var x is in the metavariable section 
var x identifier
@@

-time.Now().Sub(x)
+time.Since(x)
# We replace time.Now().Sub(x)
# with time.Since(x)
```

#### Description comments

Description comments are comments that appear directly above a patch's first
`@@` line.
gopatch will record these descriptions and display them to users with use of
the `--diff` or `--print-only` flags.

For example,

```
# Replace time.Now().Sub(x) with time.Since(x)
@@
# Not a description comment
var x identifier
@@

-time.Now().Sub(x)
+time.Since(x)
# Not a description comment
# Not a description comment
```

Patch files with multiple patches can have a separate description for each
patch.

```
# Replace redundant fmt.Sprintf with fmt.Errorf
@@
@@

-import "errors"
-errors.New(fmt.Sprintf(...))
+fmt.Errorf(...)

# Replace time.Now().Sub(x) with time.Since(x)
@@
var x identifier
@@

-time.Now().Sub(x)
+time.Since(x)
# Not a description comment
```

As these are messages that will be printed to users of the patch,
we recommend the following best practices for description comments.

- Keep them short and on a single-line
- Use imperative mood ("replace X with Y", not "replaces X with Y")

#### Usage with `--diff`

When diff mode is turned on by the `-d`/`--diff` flag, gopatch will print
description comments for patches that matched different files to stderr.

```shell
$ gopatch patch -d -p ~/s1028.patch testdata/test_files/diff_example/error.go
error.go:Replace redundant fmt.Sprintf with fmt.Errorf
--- error.go
+++ error.go
@@ -7,7 +7,7 @@

func foo() error {
        err := errors.New("test")
-       return errors.New(fmt.Sprintf("error: %v", err))
+       return fmt.Errorf("error: %v", err)
}

 func main() {
```

Note that gopatch will print only the description comments in diff mode.
Other comments will be ignored.

# Examples

This section lists various example patches you can try in your code.
Note that some of these patches are not perfect and may have false positives.

All five ship inside the binary: `gopatch help examples` prints them with a
description of what each one demonstrates.

- [s1012.patch](examples/s1012.patch): Fix for staticcheck [S1012](https://staticcheck.io/docs/checks#S1012).
- [s1028.patch](examples/s1028.patch): Fix for staticcheck [S1028](https://staticcheck.io/docs/checks#S1028).
- [s1038.patch](examples/s1038.patch): Fix for staticcheck [S1038](https://staticcheck.io/docs/checks#S1038).
- [gomock-v1.5.0.patch](examples/gomock-v1.5.0.patch): Drops unnecessary call to `Finish` method for users of gomock.
- [destutter.patch](examples/destutter.patch): Demonstrates renaming a type and updating its consumers.

# Project status

The project is currently is in a beta state. It works but significant features
are planned that may result in breaking changes to the patch format.

## Goals

gopatch aims to be a generic power tool that you can use in lieu of simple
search-and-replace.

gopatch will attempt to do 80% of the work for you in a transformation, but it
cannot guarantee 100% correctness or completeness. Part of this is owing to
the decision that gopatch must be able to operate on code that doesn't yet
compile, which can often be the case in the middle of a refactor. We may add
features in the future that require compilable code, but we plan to always
support transformation of partially-valid Go code.

## Known issues

Beyond the known issues highlighted above, there are a handful of other issues
with using gopatch today.

- It's very quiet, so there's no indication of progress. [#7]
- Error messages for invalid patch files are hard to decipher. [#8]
- Matching elisions between the `-` and `+` sections does not always work in a
  desirable way. We may consider replacing anonymous `...` elision with a
  different named elision syntax to address this issue. [#9]
- When elision is used, gopatch stops replacing after the first instance in
  the given scope which is often not what you want. [#10]
- Formatting of output generated by gopatch isn't always perfect.

  [#7]: https://github.com/uber-go/gopatch/issues/7
  [#8]: https://github.com/uber-go/gopatch/issues/8
  [#9]: https://github.com/uber-go/gopatch/issues/9
  [#10]: https://github.com/uber-go/gopatch/issues/10

## Upcoming

Besides addressing the various limitations and issues we've already mentioned,
we have a number of features planned for gopatch.

- Contextual matching: match context (like a function declaration), and then
  run a transformation inside the function body repeatedly, at any depth. [#11]
- Collateral changes: Match and capture values in one patch, and use those in
  a following patch in the same file.
- Metavariable constraints: Specify constraints on metavariables, e.g.
  matching a string, or part of another metavariable.
- Condition elision: An elision should match only if a specified condition is
  also true.

  [#11]: https://github.com/uber-go/gopatch/issues/11

# Contributing

If you'd like to contribute to gopatch, you may find the following documents
useful:

- [HACKING](docs/HACKING.md) documents the architecture, code organization, and
  other information necessary to contribute to the project.
- [RELEASE](docs/RELEASE.md) documents the process for releasing a new version
  of gopatch.

# Similar Projects

- [rf] is a refactoring tool with a custom DSL
- [gofmt rewrite rules] support simple transformations on expressions
- [eg] supports basic example-based refactoring
- [Coccinelle] is a tool for C from which gopatch takes inspiration heavily
- [Semgrep] is a cross-language semantic search tool
- [Comby] is a language-agnostic search and transformation tool

  [gofmt rewrite rules]: https://golang.org/cmd/gofmt/
  [eg]: https://godoc.org/golang.org/x/tools/cmd/eg
  [Coccinelle]: https://coccinelle.gitlabpages.inria.fr/website/
  [Semgrep]: https://semgrep.dev/
  [Comby]: https://comby.dev/
  [rf]: https://github.com/rsc/rf

# Credits

gopatch is heavily inspired by [Coccinelle].
