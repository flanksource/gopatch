# From `sed` to gopatch — a cookbook from real sessions

Every "found" block below is a real command, taken verbatim from local agent session transcripts (Claude Code and Codex) across the flanksource repos — captain, gavel, recon, incident-commander, clicky, and a makerprint side project. A scan of that history turns up **449 distinct in-place `sed -i` / `perl -pi` / `gsed -i` invocations against `.go` files**. Almost all of them are one of the dozen shapes below, and almost all of them have a gopatch command that is shorter, scoped, and cannot half-apply.

Read this as: *the pattern you are about to type* → *the command to type instead*.

---

## 1. Rename a struct field

The single most expensive shape, because one rename has three textual forms — the declaration, the `x.Field` selector, and the `T{Field: …}` literal key — and a text tool needs a separate expression for each.

**Found** (recon, `TemplateID` → `CheckID`, spread across five separate invocations in four sessions):

```shell
perl -pi -e 's/\.TemplateID\b/.CheckID/g'      internal/missioncontrol/resolve_test.go internal/missioncontrol/upload_test.go
perl -pi -e 's/finding\.TemplateID/finding.CheckID/g' internal/engines/scan/prowler/ocsf_corpus_test.go
perl -pi -e 's/\bTemplateID: /CheckID: /g'     internal/api/report_test.go
perl -pi -e 's/finding\.TemplateID/finding.CheckID/'  internal/engines/scan/inspec/resources_test.go
```

**gopatch** — one command, whole module, all three forms:

```shell
gopatch rename field api.Finding.TemplateID CheckID -d ./...
gopatch rename field api.Finding.TemplateID CheckID ./...
```

**What the text version risked:** `\bTemplateID: ` matches that key in *any* struct and in map literals; `\.TemplateID\b` matches the field on any type that has one; the file list was hand-assembled, so the sixth file was found only when the build broke. gopatch's `rename field` is type-aware — it moves exactly the references that resolve to `api.Finding.TemplateID` and leaves every other `TemplateID` alone.

---

## 2. Rename a field whose receiver has many names

**Found** (captain):

```shell
perl -pi -e 's/\bmode\.Backend\b/mode.Mode/g; s/\bm\.Backend\b/m.Mode/g' pkg/api/runtime_arguments_ginkgo_test.go pkg/api/runtime_catalog_ginkgo_test.go
perl -pi -e 's/\bp\.backend\b/p.provider/g' pkg/ai/provider/genkit/genkit.go pkg/ai/provider/genkit/tools.go pkg/ai/provider/genkit/options.go
```

**gopatch:**

```shell
gopatch rename field genkit.Provider.backend provider ./...
```

**What the text version risked:** the receiver name is enumerated by hand. `cfg.Backend`, `s.model.Backend`, `models[i].Backend`, `getModel().Backend` and every future spelling are missed silently. gopatch does not look at the receiver expression at all — it looks at what the selector resolves to.

---

## 3. Rename a function, and its calls

**Found** (captain, four separate commands over three sessions):

```shell
perl -pi -e 's/ai\.ResolveModelSelectors\(/ai.Resolve(/g' pkg/aichat/messages.go pkg/aichat/provider_config.go pkg/aichat/threads.go
perl -pi -e 's/SchemaJSONForBackend\(/SchemaJSONForRuntime(/g' pkg/ai/schema_claude_cli_ginkgo_test.go
sed  -i '' 's/verifyPassed(verdicts)/VerifyPassed(verdicts)/g'  pkg/ai/agent/verify_order_test.go
sed  -i    's/\boidcTokenExpiring(/OIDCTokenExpiring(/g'        clientcmd/mccontext/client.go
```

**gopatch:**

```shell
# inside the declaring package
gopatch rename func verifyPassed VerifyPassed ./pkg/ai/agent

# exact declaration plus every typed reference
gopatch rename func github.com/flanksource/captain/pkg/ai.ResolveModelSelectors Resolve ./...
```

**What the text version risked:** the trailing `(` in the pattern is there to avoid false hits, and it means every use of the function *as a value* — `f := ai.ResolveModelSelectors`, a struct field holding it, a `map[string]func()` entry — is skipped. The file list is hand-assembled again. `s/verifyPassed(verdicts)/…/` is narrower still: it only matches the call that passes an argument named `verdicts`.

This is a typed command, so the module must compile and same-named local variables remain unchanged.

---

## 4. Swap an import path

**Found** (captain):

```shell
rg -l '"encoding/json"' pkg/claude pkg/ai/history | rg -v '_test\.go$' \
  | xargs perl -pi -e 's#"encoding/json"#"github.com/segmentio/encoding/json"#g'
```

**gopatch:**

```shell
gopatch rename import encoding/json github.com/segmentio/encoding/json ./pkg/claude ./pkg/ai/history
```

**What the text version risked:** the literal `"encoding/json"` also appears in doc comments, error strings, and test fixtures, all of which get rewritten. And it only works because the package name happened to stay `json` — had it changed, every `json.X` selector would have needed a second sweep. `rename import` moves the path *and* the qualifier together, with `--name` / `--to-name` when the path's last segment is not the package name.

Bonus hazard in the pipeline itself: `xargs` with no matches still runs the command once on BSD `xargs`, and a path with a space is split.

---

## 5. Rename a type used through a package qualifier

**Found** (captain):

```shell
rg -l 'models\.EntityCache'        pkg --glob '*.go' | xargs perl -pi -e 's/models\.EntityCache/models.Entity/g'
rg -l 'models\.EntityBindingCache' pkg --glob '*.go' | xargs perl -pi -e 's/models\.EntityBindingCache/models.EntityBinding/g'
```

**gopatch:**

```shell
gopatch rename struct github.com/flanksource/captain/pkg/models.EntityCache        Entity        ./...
gopatch rename struct github.com/flanksource/captain/pkg/models.EntityBindingCache EntityBinding ./...
```

**What the text version risked:** overlapping names make the *order* of the two sweeps load-bearing, which is a bug waiting for the day someone adds `EntityCacheEntry`. Gopatch resolves the exact type object and picks up references through any package alias.

---

## 6. Drop (or add) an argument at every call site

**Found** (captain, three variants of the same refactor):

```shell
perl -pi -e 's/getInstance\((\w+), ai\.(Anthropic|OpenAI|Google|DeepSeek), ai\.ModeAPI, /getInstance($1, ai.$2, /g' pkg/ai/provider/genkit/instance_test.go
perl -pi -e 's/pluginFor\(ai\.(Anthropic|OpenAI|Google|DeepSeek), ai\.ModeAPI, /pluginFor(ai.$1, /g'                 pkg/ai/provider/genkit/instance_test.go
perl -pi -e 's/runtimeOutputSchema\(ai\.(OpenAI|Google|Anthropic|DeepSeek), ai\.ModeAPI, req\)/runtimeOutputSchema(ai.$1, req)/g' pkg/ai/provider/genkit/genkit_test.go
sed  -i '' 's/resolved, err := ResolveAPIKey(backend)/resolved, err := ResolveAPIKey(p, ModeAPI)/'                    pkg/ai/models_remote.go
```

**gopatch** — no subcommand covers "drop the third argument", so this is a patch file. Metavariables replace the alternation:

```diff
# Drop the Mode argument from getInstance calls, whatever mode it names.
@@
var key, p, m, opts expression
@@
-getInstance(key, p, m, opts)
+getInstance(key, p, opts)
```

```shell
gopatch patch -d -p drop-mode.patch ./...
gopatch patch -p drop-mode.patch ./...
```

Pair it with the declaration change:

```shell
gopatch edit func getInstance --params 'key string, p Provider, opts map[string]string' ./...
```

**What the text version risked:** `(Anthropic|OpenAI|Google|DeepSeek)` is a closed list — a fifth provider, or a call that passes a *variable* holding one, is skipped with no error. `(\w+)` fails on `opts.key` or `cfg.Name()`. And any call written across two lines never matches at all, because `sed` and `perl -p` see one line at a time.

---

## 7. Rewrite a composite literal

**Found** (gavel, makerprint):

```shell
sed -i.bak 's|permissions.Tools = api.Tools{Allow: todopkg.DefaultAgentTools()}|permissions.Tools = api.ToolsFromLists(todopkg.DefaultAgentTools(), nil)|' todos/headless/request.go
sed -i ''  's/\[\]github\.PullRequest{/github.PRSearchResults{/g' pr/ui/pr_stream_dedupe_test.go
sed -i ''  's/prepared:    map\[uuid.UUID\]uuid.UUID{}/prepared:    map[uuid.UUID]map[uuid.UUID]struct{}{}/' todos/runtime/provider.go
sed -i ''  's/Provider: string(api.Anthropic)/Provider: api.Anthropic.Name/g' pkg/cli/prompt_schema_verifiers_ginkgo_test.go
```

**gopatch:**

```diff
# Build Tools through its constructor.
@@
var v expression
@@
 package headless

-api.Tools{Allow: v}
+api.ToolsFromLists(v, nil)
```

```diff
# Return the named slice type.
@@
@@
 package ui

-[]github.PullRequest{...}
+github.PRSearchResults{...}
```

**What the text version risked:** the first pattern is pinned to one exact argument expression, so a second call site spelled differently is missed. The third is pinned to gofmt's *current alignment* — `prepared:` followed by four spaces — so it stops matching the moment a longer key is added to the struct. `...` and metavariables have neither problem.

---

## 8. Add a struct field, or a leading parameter

**Found** (makerprint):

```shell
sed -i '' 's|^\targvPath   string$|\targvPath   string\n\tsourcePath string|' internal/mockscad/mockscad.go
```

**gopatch:**

```shell
gopatch add field mockScad.sourcePath string ./internal/mockscad
gopatch add param Send ctx context.Context --import context ./...
```

**What the text version risked:** `^\targvPath   string$` hard-codes one leading tab and the three spaces gofmt happens to emit today. Add a longer field name to the struct, alignment shifts, and the command matches nothing — exiting 0 while doing nothing. gopatch appends the field to the struct and re-formats the block.

---

## 9. Delete a declaration

The most dangerous shape, and the one where gopatch's refusal is worth more than its speed.

**Found** (captain, gavel, incident-commander):

```shell
sed -i ''    '228,263d' pkg/ai/tools/tools.go
sed -i.bak   '271,406d' pkg/cli/session_get.go && rm -f pkg/cli/session_get.go.bak && gofmt -w pkg/cli/session_get.go
sed -i ''    '156d;144d;131d' todos/native/execution_integration_test.go
sed -i    -e '503,568d' -e '21,238d' clientcmd/context.go
sed -i.bak   '/^type promptObservingExecutor struct {$/,/^}$/d' todos/runtime/provider_integration_test.go
awk 'NR<390 || NR>534' pkg/rules/approval_input_planner.go > pkg/rules/.planner.tmp && mv pkg/rules/.planner.tmp pkg/rules/approval_input_planner.go
```

**gopatch:**

```shell
gopatch delete func   sessionDetailView ./...
gopatch delete struct promptObservingExecutor ./...
gopatch delete type   Interval ./...
gopatch delete method Options.Validate ./...
```

**What the text version risked:**

- Line numbers are stale the moment anything above them moves. Look at `'156d;144d;131d'` and `-e '503,568d' -e '21,238d'` — both are written in **descending** order specifically to dodge renumbering. That is a hand-maintained invariant in a throwaway command.
- `/^type X struct {$/,/^}$/d` ends at the first `}` in column 0. Correct until the struct contains a func literal or the file is not gofmt-clean.
- Nothing checks the callers. gopatch's type-aware delete refuses and tells you where they are:

  ```
  func getInstance is still used at instance.go:24:6, instance.go:25:6; remove those references first
  ```

- Note the `gofmt -w` chaser in the second line. gopatch's output is already formatted.

---

## 10. Line-addressed substitution

**Found** (gavel, captain):

```shell
sed -i '' -e '110s/runningSupervisorRoots()/s.supervisorRoots()/' \
          -e '140s/runningSupervisorRoots()/s.supervisorRoots()/' \
          -e '303s/runningSupervisorRoots()/s.supervisorRoots()/' \
          -e '321s/runningSupervisorRoots()/s.supervisorRoots()/' \
          -e '339s/runningSupervisorRoots()/s.supervisorRoots()/' pr/ui/task_source.go
sed -i '' -e '25s/.*/\t\t}, tools.ResolveOptions{...}/' pkg/ai/tools/definitions_ginkgo_test.go
```

**gopatch** — if it is every occurrence, it is a rename; if it is one function's body, `replace` scopes it there:

```shell
gopatch rename func runningSupervisorRoots supervisorRoots ./pr/ui
gopatch replace func taskSources --match 'runningSupervisorRoots\(\)' --content 's.supervisorRoots()' ./...
```

**What the text version risked:** five line numbers transcribed by hand from a `rg -n` a few commands earlier — any edit in between shifts them. `25s/.*/…/` is worse: it replaces *whatever is on line 25 today*, sight unseen. `replace` finds the body through the type checker and reparses the result as Go before writing, so a substitution that would break the file is refused instead.

---

## 11. Insert an import by line number

**Found** (captain, gavel):

```shell
for f in pkg/ai/provider/genkit/tool_preferences_ginkgo_test.go pkg/ai/provider/genkit/tools_test.go; do
  sed -i '8i\	captools "github.com/flanksource/captain/pkg/ai/tools"' "$f"
done
sed -i.bak 's/^import (/import (\n\t"encoding\/json"/' todos/runtime/phases_test.go
```

**gopatch:** don't. The commands that introduce new source take `--import`, as `path` or `qualifier=path`:

```shell
gopatch add param  Send ctx context.Context --import context ./...
gopatch replace func report --match 'log\.Printf\((.*)\)' --content 'slog.Info($1)' --import log/slog ./...
gopatch edit func  Send --content 'return errors.New("nope")' --import errors ./...
```

In a **patch file** you write the import into the diff itself, which is both the change and the constraint:

```diff
@@
var r expression
@@
-import "errors"
+import "encoding/json"

-r.MarshalJSON()
+json.Marshal(r)
```

**What the text version risked:** line 8 is only the right place until a build tag, a license header, or another import lands above it. `s/^import (/…/` fails outright on a file with a single unparenthesized `import "x"`.

---

## 12. Package clause after a move

**Found** (incident-commander):

```shell
git mv clientcmd/context_credentials.go clientcmd/mccontext/credentials.go \
  && sed -i '' '1s/^package clientcmd$/package mccontext/' clientcmd/mccontext/credentials.go
```

**gopatch:**

```shell
git mv clientcmd/context_credentials.go clientcmd/mccontext/credentials.go
gopatch rename package clientcmd mccontext ./clientcmd/mccontext
```

**What the text version risked:** `1s/` assumes the package clause is on line 1 — false for any file with a build constraint or a license header, and it fails silently.

---

## 13. Three ways the text edit silently did nothing

These are the reason "it exited 0" is not evidence.

**A pattern `sed` can never match.** `sed` reads one line at a time, so `\n` in the search pattern matches nothing, ever:

```shell
sed -i.bak 's|^import (\n|X|' pkg/render/helpers_test.go && rm pkg/render/helpers_test.go.bak
```

Exit 0. File unchanged. Session continued as though the edit had landed.

**A substitution with identical sides:**

```shell
sed -i '' 's/resolution, err := host.Resolve/resolution, err := host.Resolve/' todos/lifecycle/host_resolve_ginkgo_test.go
```

**A confused invocation that reads as an edit:**

```shell
sed -i '1,20p;d' /dev/null 2>/dev/null
```

gopatch fails loudly instead. A `replace` whose `--match` finds nothing is an error; a type-aware command on a package that does not compile prints the build errors and writes nothing; a `delete` with live references names them.

---

## 14. The ceremony you stop paying

Visible throughout the corpus, and all of it goes away:

- **The `.bak` dance.** `sed -i.bak … && rm -f ….bak` — needed because BSD and GNU `sed` disagree about `-i`, and it leaves a stray `.bak` beside your source every time the `&&` chain breaks partway.
- **`sed -i ''` vs `sed -i` vs `gsed -i`.** The same session used all three, one per platform assumption. gopatch is one binary.
- **The `gofmt -w` / `goimports -w` chaser.** Appears after a large share of the in-place edits. gopatch formats its own output.
- **The `rg -l … | xargs` file discovery.** gopatch takes `./...` and walks it, skipping dot directories, `testdata/` and `vendor/`.

---

## Choosing quickly

| The sed you were about to write | Instead |
|---|---|
| `s/\.OldField/.NewField/` | `gopatch rename field T.OldField NewField ./...` |
| `s/oldFunc(/newFunc(/` | `gopatch rename func oldFunc newFunc ./...` |
| `s/OldType/NewType/` | `gopatch rename struct\|type OldType NewType ./...` |
| `s#"old/path"#"new/path"#` | `gopatch rename import old/path new/path ./...` |
| `'1s/^package a$/package b/'` | `gopatch rename package a b ./dir` |
| `'NNN,MMMd'` | `gopatch delete func\|struct\|type\|method Name ./...` |
| `'Ns/…/…/'` (line-addressed) | `gopatch replace func Enclosing --match … --content …` |
| `'Ni\ …'` (insert an import) | `--import` on the command, or an import line in the patch |
| `s/^\tField   type$/…/` (add a field) | `gopatch add field T.name type ./...` |
| `s/f(a, b, c)/f(a, c)/` over many call sites | a patch file with `expression` metavariables |
| anything with `\n` in the pattern | a patch file — the diff is over syntax, and spans lines by construction |
