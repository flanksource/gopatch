# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## Unreleased
### Added
- Refactoring subcommands that take the change as arguments instead of requiring
  a patch to be written by hand. `gopatch rename struct|type|func|package|import`,
  `gopatch add field|param`, `gopatch delete field` and `gopatch edit field|func`
  generate a patch and run it through the existing engine, so they work on code
  that does not compile; `--print-patch` writes the generated patch to stdout
  instead of applying it.
- `gopatch rename field|method`, `gopatch delete struct|type|func|method`,
  `gopatch add struct|func|method` and `gopatch edit method`, which the patch
  language cannot express: a patch matching `x.Name` would rewrite every type's
  `Name`, and a patch can neither delete a declaration nor create one. These
  resolve the target with `go/types` and rewrite the source with `dave/dst`, so
  they change exactly the references that mean the same symbol — and they require
  the module to compile, reporting the build errors and changing nothing when it
  does not.
- `--content` on `gopatch edit func` and `gopatch edit method`, which replaces a
  function's body with the Go statements you give it, or with the contents of a
  file when the value is `@<path>`. The content is parsed before anything is
  written, so source that is not a valid body is refused rather than left on
  disk.
- `gopatch replace func|method --match <regex> --content <text>`, which replaces
  every match of a regular expression *inside* one function's body. The body is
  located with type information, so the expression cannot reach another function,
  another type's same-named method, or another package. A pattern that matches
  nothing is an error, and a substitution that does not produce valid Go is
  refused.
- Refactoring targets may be qualified with the package or the directory that
  declares them: `store.User.Name`, `example.com/store.User.Name`,
  `./internal/store:User.Name` and `user.go:User.Name` all name the same field.
  This replaces having to pass `--package`, which still works; giving both is
  refused. An ambiguous name is reported with the qualified form to re-run, and
  never resolved by guessing.
- Flags and file patterns may now be given in any order. `gopatch -- <path>`
  forces a path that shares a name with a subcommand to be read as a path.
- `make install` builds from a checkout into `$GOBIN` (or `$GOPATH/bin`) with the
  version stamped from `git describe`, so a locally built gopatch reports the
  commit it came from instead of `unknown`.
- `gopatch patch`, which applies patch files. This is what the root command has
  always done; it now has a name, its own `--help`, and its own examples, so the
  root command can be a dispatcher rather than one command wearing two hats.
- `gopatch install`, which writes the agent skills that ship inside the binary
  into the skills directory of every AI coding agent it finds — `~/.agents`,
  Claude Code, Codex, Cursor, Gemini and opencode — or into `--agent`/`--dir`.
  The skill tree is embedded, so this works from a `go install` build with no
  checkout and no network. Re-running reports files as `unchanged`, and a skill
  directory that is a symlink to a checkout is left alone rather than written
  through. `--list` shows what the binary carries and which agents are here;
  `--dry-run` reports without writing.

- Help topics carrying the reference documentation inside the binary:
  `gopatch help patches` (the patch language, from `docs/PatchesInDepth.md`),
  `gopatch help metavariables` (identifiers vs expressions vs statements, from
  `docs/Appendix.md`) and `gopatch help examples` (the five patches in
  `examples/`, each of which the test suite runs). Previously `gopatch patch
  --help` pointed at a path in the repository, which a `go install` build does
  not have.

### Changed
- `gopatch patch --help` now explains the patch language — context lines,
  metavariable repetition, elision and the two elision limits — instead of
  naming a file. Nothing in the help text refers to a path outside the binary,
  and a test walks the command tree to keep it that way.
- `--diff` and `--print-only` now say that they print each matching patch's
  description comment.
- The three HTML tables in `docs/PatchesInDepth.md` are now plain two-column
  code blocks, the idiom the document already used elsewhere. They render the
  same on GitHub and are legible when the document is printed to a terminal.
- The command line is now parsed by `spf13/cobra`. Every existing flag, and
  applying patch files with `-p`/`-P` or on stdin, behaves as before.
- `--help` now writes to stdout and exits 0. It previously wrote to stderr and
  exited 1.
- `--version` now writes to stdout. It previously wrote to stderr. `gopatch
  version` does the same thing.
- gopatch now requires Go 1.23. The type-aware commands need
  `golang.org/x/tools/go/packages`, whose older releases no longer build on
  current Go toolchains.

### Deprecated
- Applying patches from the root command — `gopatch -p rules.patch ./...` — is
  deprecated in favour of `gopatch patch -p rules.patch ./...`. The old spelling
  still works and still writes the same output on stdout; it additionally prints
  a warning on stderr, so a pipeline that reads `--diff` or `--print-only` is
  unaffected. It will be removed in v0.6.0.

## 0.4.0 - 2024-04-03
### Added
- ([#150]) `--skip-generated` flag to skip running on files containing
  generated code.

  [#150]: https://github.com/uber-go/gopatch/pull/150

## 0.3.0 - 2023-07-14
### Added
- ([#112]) `--skip-import-processing` flag to skip import processing.

  [#112]: https://github.com/uber-go/gopatch/pull/112

## 0.2.0 - 2023-04-03
### Added
- ([#64]) `--diff` flag reports a diff of the changes that would be made, but does not
  make them.
- ([#70]) `--print-only` prints the modified contents of all matched files to stdout in
  their entirety without modifying files on disk.
- ([#77]) `-P`/`--patches-file` reads a list of patch files from another file on-disk.

  [#64]: https://github.com/uber-go/gopatch/pull/64
  [#70]: https://github.com/uber-go/gopatch/pull/70
  [#77]: https://github.com/uber-go/gopatch/pull/77

## 0.1.1 - 2022-07-26
### Fixed
- ([#54]) Preserve top-level comments in files when updating imports.
- Parse generics syntax introduced in Go 1.18.

  [#54]: https://github.com/uber-go/gopatch/issues/54

Thanks to @breml for their contribution to this release.

## 0.1.0 - 2021-08-19
Starting this release, we will include pre-built binaries of gopatch for
different systems.

### Added
- ([#7]): Add support for verbose logging with a `-v` flag.
- Add [introductory documentation] and [patch guide].

  [introductory documentation]: https://github.com/uber-go/gopatch/blob/main/README.md
  [patch guide]: https://github.com/uber-go/gopatch/blob/main/docs/PatchesInDepth.md
  [#7]: https://github.com/uber-go/gopatch/issues/7

### Changed
- Only the `--version` flag now prints the version number. The `-v` is used for
  verbose logging instead.

### Fixed
- ([#2]): Patches with named imports now support matching and manipulating any
  import.
- Fix issue where rewrites of unnamed imports would end up with duplicate
  entries.

  [#2]: https://github.com/uber-go/gopatch/issues/2

## 0.0.2 - 2020-11-04
### Fixed
- Fixed unintended deletion of unchanged named imports.

## 0.0.1 - 2020-01-14

- Initial alpha release.
