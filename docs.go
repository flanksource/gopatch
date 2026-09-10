package main

import (
	"embed"
	"fmt"
	"regexp"
	"strings"
)

// _docsFS carries the reference documentation that the help topics print, so
// that a binary installed with `go install` can answer "how do I write a
// patch?" without the repository. Both files are git-tracked, so this embeds
// cleanly from a fresh clone.
//
//go:embed docs/PatchesInDepth.md docs/Appendix.md
var _docsFS embed.FS

// _examplesFS carries the runnable example patches. Each one is also exercised
// by a testdata archive, so what a user is shown here is known to apply.
//
//go:embed examples/*.patch
var _examplesFS embed.FS

// Long text for the help topics.
//
// These are package-level so that a broken embed fails on the first command
// construction — in practice at the first test — rather than at the moment a
// user asks for help.
var (
	_patchesTopicLong       = terminalize(mustDoc("docs/PatchesInDepth.md"))
	_metavariablesTopicLong = terminalize(mustDoc("docs/Appendix.md"))
	_examplesTopicLong      = renderExamplePatches()
)

func mustDoc(name string) []byte {
	b, err := _docsFS.ReadFile(name)
	if err != nil {
		panic(fmt.Sprintf("embedded document %v is missing: %v", name, err))
	}
	return b
}

// Markdown constructs that are navigation rather than content. They are worth
// reading on GitHub and are noise in a terminal.
var (
	// [Some text]: https://example.com — a reference-style link definition.
	_refDefRe = regexp.MustCompile(`^\s*\[[^\]]+\]:\s+\S`)

	// <a name="..."></a> and <!-- ... -->
	_htmlNoiseRe = regexp.MustCompile(`^\s*(<a\s+name=|<!--)`)

	// [text](target) — an inline link. The target is a URL or an intra-document
	// anchor; neither is clickable here, and the anchors name sections that are
	// printed a few lines further down anyway.
	_inlineLinkRe = regexp.MustCompile(`\[([^\]\[]+)\]\([^)]*\)`)

	// [text] — a shortcut reference whose definition we have just dropped. The
	// trailing character is captured to prove it is not "[text](...)" or a
	// definition, and is put back by the replacement.
	_shortcutRefRe = regexp.MustCompile(`\[([^\]\[]+)\]([^(:]|$)`)
)

// terminalize turns one of the embedded markdown documents into something worth
// printing to a terminal, without pulling in a markdown renderer.
//
// It only removes navigation: the table of contents, link definitions, HTML
// anchors, and the link syntax around text that is kept. Prose, fenced code
// blocks and pipe tables are passed through untouched, which is what lets the
// documents stay the single source of truth — they are written hard-wrapped
// under 100 columns and read correctly as plain text.
func terminalize(md []byte) string {
	var (
		out     []string
		inFence bool
		started bool

		// pending blank lines seen outside a fence, emitted as at most one so
		// that dropped link definitions do not leave holes behind.
		pendingBlank bool
	)

	emit := func(line string) {
		if pendingBlank && len(out) > 0 {
			out = append(out, "")
		}
		pendingBlank = false
		out = append(out, line)
	}

	for _, line := range strings.Split(string(md), "\n") {
		// A fence may be indented, as it is inside the list items in
		// Appendix.md, so the marker is checked after trimming.
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			started = true
			emit(line)
			inFence = !inFence
			continue
		}

		// Inside a fence every line is content, blank ones included: the
		// examples contain blank lines that separate a package clause from its
		// imports, and the diagrams are column-aligned.
		if inFence {
			out = append(out, line)
			continue
		}

		// Drop the table of contents, which is a list of links to the headings
		// immediately below it.
		if !started {
			if strings.HasPrefix(line, "# ") && !strings.EqualFold(line, "# Table of contents") {
				started = true
			} else {
				continue
			}
		}

		if strings.TrimSpace(line) == "" {
			pendingBlank = true
			continue
		}

		if _refDefRe.MatchString(line) || _htmlNoiseRe.MatchString(line) {
			continue
		}

		line = _inlineLinkRe.ReplaceAllString(line, "$1")
		line = _shortcutRefRe.ReplaceAllString(line, "${1}${2}")

		emit(line)
	}

	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}

// examplePatch is one of the patches shipped in examples/, with the short
// description the topic prints above it.
//
// Gloss is hand-wrapped rather than reflowed at print time, the same way every
// other block of help text in this package is: the alternative is a wrapping
// dependency for five strings.
type examplePatch struct {
	File  string
	Gloss string
}

// examplePatches lists the shipped patches in increasing order of difficulty,
// which is the order someone reading them for the first time wants.
func examplePatches() []examplePatch {
	return []examplePatch{
		{
			File: "examples/s1038.patch",
			Gloss: "staticcheck S1038: fmt.Print(fmt.Sprintf(...)) becomes fmt.Printf(...).\n" +
				"The smallest useful patch: no metavariables, one elided argument list.",
		},
		{
			File: "examples/s1012.patch",
			Gloss: "staticcheck S1012: time.Now().Sub(x) becomes time.Since(x). One\n" +
				"identifier metavariable, captured on the left and reused on the right.",
		},
		{
			File: "examples/s1028.patch",
			Gloss: "staticcheck S1028: errors.New(fmt.Sprintf(...)) becomes fmt.Errorf(...),\n" +
				"and the now-unused errors import is removed with it.",
		},
		{
			File: "examples/gomock-v1.5.0.patch",
			Gloss: "gomock 1.5.0 calls Controller.Finish itself. Deletes the redundant defer,\n" +
				"matching a named import and eliding the statements in between.",
		},
		{
			File: "examples/destutter.patch",
			Gloss: "Two changes in one file, applied in order: rename HTTPClient to Client in\n" +
				"the package that declares it, then update http.HTTPClient in consumers.",
		},
	}
}

// renderExamplePatches builds the body of the "examples" help topic.
func renderExamplePatches() string {
	var b strings.Builder

	b.WriteString("Worked example patches.\n\n")
	b.WriteString("Each of these ships inside gopatch and is exercised by its test suite, so\n")
	b.WriteString("they are known to apply. Write one to a file and run it with:\n\n")
	b.WriteString("  gopatch patch -p <file> ./...")

	for _, ex := range examplePatches() {
		body, err := _examplesFS.ReadFile(ex.File)
		if err != nil {
			panic(fmt.Sprintf("embedded example %v is missing: %v", ex.File, err))
		}

		b.WriteString("\n\n")
		b.WriteString(strings.TrimSuffix(strings.TrimPrefix(ex.File, "examples/"), ".patch"))
		b.WriteString("\n\n")
		for _, line := range strings.Split(ex.Gloss, "\n") {
			b.WriteString("  ")
			b.WriteString(line)
			b.WriteString("\n")
		}
		b.WriteString("\n")

		for _, line := range strings.Split(strings.TrimRight(string(body), "\n"), "\n") {
			if line == "" {
				b.WriteString("\n")
				continue
			}
			b.WriteString("    ")
			b.WriteString(line)
			b.WriteString("\n")
		}
	}

	return strings.TrimRight(b.String(), "\n")
}
