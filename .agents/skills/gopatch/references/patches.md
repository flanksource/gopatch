# Writing gopatch patches

Reach for a patch file when the change is a *pattern* rather than a named declaration: an argument to drop, two statements to fold into one, a call whose arguments must be carried through. Everything the subcommands do, they do by generating one of these — `--print-patch` on any syntactic subcommand prints a working example for the change at hand, which is the fastest way to start.

## Shape of a file

```diff
# Drop the Mode argument from getInstance calls, whatever mode it names.
@@
var key, p, m, opts expression
@@
-getInstance(key, p, m, opts)
+getInstance(key, p, opts)
```

- Everything above the first `@@` is the **description**, printed by `--diff` and `--print-only` next to each file that matched. Keep it to one imperative line ("replace X with Y", not "replaces X with Y").
- Between the two `@@` lines is the **metavariable section**: Go `var` declarations naming the holes.
- Below is a **unified diff** over Go syntax. `-` lines are matched and removed, `+` lines are added, unprefixed lines are context that must be present and is left alone.

One file may hold several changes, each with its own `@@` pair and description. They are applied in order.

Comments (`#`) are allowed anywhere; only the one directly above the first `@@` is a description.

```shell
gopatch patch -d -p rules.patch ./...    # preview
gopatch patch -p rules.patch ./...       # apply
gopatch patch ./... < rules.patch        # patch on stdin
gopatch patch -p a.patch -p b.patch ./... # several, in order
gopatch patch -P patchlist.txt ./...     # one path per line
```

## Metavariables

Two types:

| Type | Matches | Examples |
|---|---|---|
| `identifier` | a name | `err`, `foo`, `Bar` |
| `expression` | anything with a value | `42`, `x`, `bar.Baz()`, `foo(bar.Baz())`, `nil` |

```diff
@@
var x expression
@@
-foo(x)
+bar(x + 3, true)
```

| Input | Output |
|---|---|
| `foo(42)` | `bar(42 + 3, true)` |
| `foo(getAnswer())` | `bar(getAnswer() + 3, true)` |

A metavariable holds the entire matched node, so you can wrap it, reorder it, or drop it without breaking anything inside.

**Repetition is a constraint, not a wildcard.** The first occurrence captures; every later occurrence in the same change must match the *same* code. That is what makes this safe:

```diff
@@
var x identifier
@@
-time.Now().Sub(x)
+time.Since(x)
```

Declare several of one type at once: `var foo, x identifier`.

## Elision: `...`

`...` stands for "the rest of it", and is accepted in most slice-shaped positions.

```diff
@@
@@
-foo(...)
+bar(...)
```

matches `foo(42)`, `foo(a, b, c)`, and `foo()` alike.

Where it works:

| Position | Example |
|---|---|
| Call arguments | `foo(...)` |
| Struct literal fields | `T{..., Name: v, ...}` |
| Slice elements, map items | `[]int{...}`, `map[string]int{...}` |
| Statement blocks | `...` on its own line inside a body |
| Function bodies, parameters, results, receivers | `func f(...) (...) { ... }` |
| Struct fields, interface methods in a type declaration | see below |
| `return` values | `return ..., err` |
| `for` / `range` | `for ... { ... }` matches both |

Deleting a statement from anywhere in a block:

```diff
@@
var ctrl, gomock identifier
var t expression
@@
 import gomock "github.com/golang/mock/gomock"

 ctrl := gomock.NewController(t)
 ...
-defer ctrl.Finish()
```

Folding an assignment into an `if`, tolerating any number of return values:

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

**Two limits to plan around.** Once elision is used, gopatch replaces only the first instance in a given scope ([#10]) — re-run until the diff is empty, or write the pattern without `...`. And matching elisions between the `-` and `+` sections is imperfect ([#9]); prefer a named metavariable when you need a value carried across.

  [#9]: https://github.com/uber-go/gopatch/issues/9
  [#10]: https://github.com/uber-go/gopatch/issues/10

## Package and import context

A bare pattern matches in every package your paths cover. A ` package X` context line scopes it to one:

```diff
@@
@@
 package http

-HTTPClient
+Client
```

**Always add it for anything shaped like a struct field or a signature.** Without it, a same-named struct in another package under `./...` is silently rewritten too. Every subcommand-generated patch carries one; write yours the same way.

Rename a package clause with a `-`/`+` pair:

```diff
@@
@@
-package foo
+package bar
```

Imports go at the top of the diff, after the package clause. As **context** they constrain the match:

```diff
@@
@@
 import "example.com/bar"

# rest of the patch
```

Matching is exact — that context line matches only files importing it *unnamed*. To match named and unnamed alike, and to pick up whatever local name the file uses, bind it to an identifier metavariable:

```diff
@@
var foo identifier
@@
 import foo "example.com/foo"

-foo.FooClient
+foo.Client
```

As a `-`/`+` pair they change the import:

```diff
@@
@@
-import "example.com/foo"
+import "example.com/bar"

-foo.Client
+bar.Client
```

**State import changes explicitly.** In a patch file, do not assume the import block will be fixed for you — a `+` section that starts using `json.Marshal` in a file with no `encoding/json` import produces a file that does not compile, and an import left unused by a `-` section stays behind. Write the pair, the way the shipped `examples/s1028.patch` does:

```diff
@@
@@
-import "errors"

-errors.New(fmt.Sprintf(...))
+fmt.Errorf(...)
```

(The type-aware subcommands — `replace`, `edit … --content` — are different: they rebuild the import block themselves, which is what `--import path` feeds.)

## Statements and declarations

The diff is over syntax, not text, so these are equivalent:

```diff
-foo(42)
+bar(42)
```

```diff
-foo(
+bar(
  42,
 )
```

Function declarations match with elided bodies, which is how a signature changes without touching what is inside:

```diff
@@
@@
 package spike

-func getInstance(key string, p Provider, m Mode, opts map[string]string) *Instance {
+func getInstance(key string, p Provider, opts map[string]string) *Instance {
   ...
 }
```

Type declarations elide their fields, which is how one field changes without listing the rest:

```diff
@@
@@
 package spike

 type User struct {
   ...
-  Nickname string `json:"nickname,omitempty"`
+  Nickname string `json:"nick"`
   ...
 }
```

Note that a struct tag is part of the field declaration and cannot be wildcarded — reproduce it exactly, or the field will not match.

## The `+` section may not be empty

A change that only deletes is fine, but the `+` side of the diff has to be left with something to parse. This is rejected:

```diff
@@
var name expression
@@
 package spike

-log.Printf("done %s", name)
```

```
parse: droplog.patch/0.plus:1:15: expected a declaration or an expression, found EOF
```

Keep a context line in the body — not just the package clause — and it works:

```diff
@@
var name, n expression
@@
 package spike

 log.Printf("processed %s: %d", name, n)
-log.Printf("done %s", name)
```

This is also why no patch can delete a whole declaration. That is what `gopatch delete func|struct|type|method` is for.

## Debugging a patch that does nothing

1. Run with `-v` to confirm gopatch is even seeing the files.
2. Cut the pattern down until it matches, then add constraints back one at a time. A too-specific literal (a tag, an exact argument, an unnamed import) is the usual cause.
3. Generate a known-good program for a similar change with `gopatch <verb> … --print-patch` and diff it against yours.
4. Remember the scope: no ` package X` line means every package; a `-`/`+` on an import means the file must import it exactly that way.

Error messages for malformed patch files are terse ([#8]) — when parsing is the problem, bisect the file.

  [#8]: https://github.com/uber-go/gopatch/issues/8

## Further reading in the gopatch repo

- `docs/PatchesInDepth.md` — the authoritative reference, including the grammar.
- `docs/Appendix.md` — identifiers vs expressions vs statements.
- `examples/*.patch` — runnable patches for staticcheck S1012/S1028/S1038, gomock v1.5.0 cleanup, and destuttering.
