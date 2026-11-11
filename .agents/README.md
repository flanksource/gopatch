# Agent skills for gopatch

This directory is a plugin for both Claude Code and Codex. It ships one skill, [`skills/gopatch`](skills/gopatch/SKILL.md), which teaches an agent to reach for `gopatch` instead of `sed -i` / `perl -pi` / `awk` when it needs to change Go code in more than one place.

```
.agents/
├── .claude-plugin/plugin.json   Claude Code plugin manifest
├── .codex-plugin/plugin.json    Codex plugin manifest
├── plugins/marketplace.json     Codex marketplace manifest (root: the repo)
└── skills/gopatch/
    ├── SKILL.md                 decision table, the two engines, workflow, limits
    └── references/
        ├── commands.md          every subcommand, its flags, and what it refuses
        ├── patches.md           the patch language: metavariables, elision, imports
        └── from-sed.md          real sed/perl invocations → the gopatch that replaces them
```

The Claude Code marketplace manifest lives at [`../.claude-plugin/marketplace.json`](../.claude-plugin/marketplace.json), where that client expects it; the Codex one lives at `plugins/marketplace.json` here. Both point at this directory as the plugin, so the two clients share one copy of the skill.

## Install — from the gopatch binary

The skill tree in this directory is embedded into the binary, so gopatch can install it itself, with no checkout and no network:

```shell
gopatch install                     # every agent detected on this machine
gopatch install --list              # what it carries, and where it would go
gopatch install --dry-run           # report without writing
gopatch install --agent claude      # just one
gopatch install --dir ./.agents/skills
```

Re-running is safe: unchanged files are reported as such, and a skill directory that is a symlink to a checkout is left alone rather than written through.

## Install — Claude Code

```shell
/plugin marketplace add flanksource/gopatch
/plugin install gopatch-skills@flanksource-gopatch
```

From a local checkout:

```shell
/plugin marketplace add ~/go/src/github.com/flanksource/gopatch
```

## Install — Codex

```shell
codex plugin marketplace add https://github.com/flanksource/gopatch
codex plugin add gopatch-skills@flanksource-gopatch
```

From a local checkout, point at the repo root — the marketplace manifest is found at `.agents/plugins/marketplace.json` and its `source.path` is resolved from there:

```shell
codex plugin marketplace add ~/go/src/github.com/flanksource/gopatch
```

## Install — plain `~/.agents/skills`

For a setup that reads skills straight off disk, symlink the skill rather than copying it, so it tracks the checkout:

```shell
ln -s ~/go/src/github.com/flanksource/gopatch/.agents/skills/gopatch ~/.agents/skills/gopatch
```

## Publishing

Many developer machines carry `.agents/` in a global gitignore, which makes this whole directory invisible to `git status`. Commit it explicitly:

```shell
git add -f .agents .claude-plugin
```

Without that, the marketplace manifest resolves only against a local checkout and `/plugin marketplace add flanksource/gopatch` finds an empty plugin.

## Validating a change

```shell
claude plugin validate .           # marketplace manifest at the repo root
claude plugin validate .agents     # plugin manifest
claude plugin validate .agents/skills
```

Codex has no offline validator; `codex plugin marketplace add <path>` against a local checkout is the equivalent smoke test.

## Keeping the skill honest

Every command in the skill was run against gopatch built from this repo, and the `from-sed.md` cookbook quotes commands that actually appeared in agent session transcripts. When the CLI changes, re-check the skill against `gopatch <verb> --help` — the help text is the source of truth for flags, and `--print-patch` is the source of truth for what a subcommand generates.
