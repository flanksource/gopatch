package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/flanksource/captain/pkg/bash"
)

type observation struct {
	invocation
	Status           string    `json:"tool_status"`
	FailureKind      string    `json:"failure_kind,omitempty"`
	Excerpt          string    `json:"response_excerpt,omitempty"`
	Seconds          []float64 `json:"recorded_seconds,omitempty"`
	Isolated         bool      `json:"isolated_gopatch_call"`
	ProviderTurnID   string    `json:"provider_turn_id,omitempty"`
	EpisodeID        string    `json:"episode_id"`
	ResolvedFollowup bool      `json:"resolved_followup,omitempty"`
	Attribution      string    `json:"result_attribution"`
}

type episode struct {
	ID                   string   `json:"id"`
	Source               string   `json:"source"`
	SessionID            string   `json:"session_id"`
	ProviderID           string   `json:"provider_session_id"`
	Project              string   `json:"cwd"`
	Operation            string   `json:"operation"`
	Key                  string   `json:"target_key"`
	First                int64    `json:"first_line"`
	Last                 int64    `json:"last_line"`
	Commands             int      `json:"commands"`
	Applies              int      `json:"apply_commands"`
	Succeeded            int      `json:"successful_apply_tool_calls"`
	Failed               int      `json:"failed_apply_tool_calls"`
	Pending              int      `json:"pending_apply_tool_calls"`
	Previews             int      `json:"preview_commands"`
	PreviewFailed        int      `json:"failed_preview_tool_calls"`
	Failures             []string `json:"failure_kinds,omitempty"`
	RepairCandidates     []string `json:"subsequent_go_edit_ids,omitempty"`
	ValidationCandidates []string `json:"subsequent_validation_ids,omitempty"`
	Transcript           string   `json:"history_file"`
}

func analyze(o options) error {
	data, err := os.ReadFile(filepath.Join(o.out, "gopatch-invocations.ndjson"))
	if err != nil {
		return err
	}
	var invocations []invocation
	if err := decodeLines(data, &invocations); err != nil {
		return err
	}
	evidence := map[string]nativeRow{}
	paths, err := filepath.Glob(filepath.Join(o.out, "native-evidence", "*.ndjson"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var rows []nativeRow
		if err := decodeLines(data, &rows); err != nil {
			return err
		}
		for _, r := range rows {
			evidence[r.SessionID+":"+r.ID] = r
		}
	}
	jobs := map[string][]nativeRow{}
	for _, r := range evidence {
		if r.Tool == "write_stdin" {
			var id int
			if json.Unmarshal(r.Input["session_id"], &id) == nil {
				key := fmt.Sprintf("%s:%d", r.SessionID, id)
				jobs[key] = append(jobs[key], r)
			}
		}
	}
	for key := range jobs {
		sort.Slice(jobs[key], func(i, j int) bool { return jobs[key][i].SourceLine < jobs[key][j].SourceLine })
	}
	turns := map[string]string{}
	paths, err = filepath.Glob(filepath.Join(o.out, "native-context", "*.ndjson"))
	if err != nil {
		return err
	}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var rows []contextRow
		if err := decodeLines(data, &rows); err != nil {
			return err
		}
		for _, r := range rows {
			for _, id := range r.ToolIDs {
				turns[r.SessionID+":"+id] = r.TurnID
			}
		}
	}
	var observations []observation
	groups := map[string]*episode{}
	for _, v := range invocations {
		if v.Mode != "apply" && v.Mode != "preview" {
			continue
		}
		r := evidence[v.SessionID+":"+v.ID]
		e := resultEnvelope(r.Response)
		ob := observation{invocation: v, Status: resultStatus(r, e), Isolated: isolatedGopatch(v), Seconds: e.Seconds, ProviderTurnID: turns[v.SessionID+":"+v.ID]}
		if ob.Status == "pending" {
			for _, id := range e.Jobs {
				for _, follow := range jobs[fmt.Sprintf("%s:%d", v.SessionID, id)] {
					if follow.SourceLine <= v.SourceLine {
						continue
					}
					next := resultEnvelope(follow.Response)
					e.Output += "\n" + next.Output
					if status := resultStatus(follow, next); status == "succeeded" || status == "failed" {
						ob.Status = status
						ob.ResolvedFollowup = true
						ob.Seconds = nil
					}
				}
			}
		}
		ob.Excerpt = e.Output
		ob.Attribution = "compound_or_unknown"
		if ob.Isolated && (len(e.Exits) == 1 || v.Source == "claude") {
			ob.Attribution = "isolated_result"
		}
		if specificGopatchFailure(e.Output) && diagnosticMatchesCommand(e.Output, ob.Command) {
			ob.Attribution = "gopatch_failure_diagnostic"
		}
		if len(ob.Excerpt) > 1600 {
			ob.Excerpt = ob.Excerpt[:1600] + " [excerpt truncated]"
		}
		if ob.Status == "failed" {
			ob.FailureKind = failureKind(e.Output)
		}
		key := episodeKey(v)
		hash := sha256.Sum256([]byte(v.SessionID + ":" + key))
		ob.EpisodeID = v.Source + "-" + hex.EncodeToString(hash[:6])
		ep := groups[ob.EpisodeID]
		if ep == nil {
			ep = &episode{ID: ob.EpisodeID, Source: v.Source, SessionID: v.SessionID, ProviderID: v.ProviderID, Project: v.CWD, Operation: v.Operation, Key: key, First: v.SourceLine, Last: v.SourceLine, Transcript: v.HistoryFile}
			groups[ep.ID] = ep
		}
		ep.First = min(ep.First, v.SourceLine)
		ep.Last = max(ep.Last, v.SourceLine)
		ep.Commands++
		if v.Mode == "apply" {
			ep.Applies++
			switch ob.Status {
			case "succeeded":
				ep.Succeeded++
			case "failed":
				ep.Failed++
			case "pending":
				ep.Pending++
			}
		} else {
			ep.Previews++
			if ob.Status == "failed" {
				ep.PreviewFailed++
			}
		}
		if ob.FailureKind != "" {
			ep.Failures = appendUnique(ep.Failures, ob.FailureKind)
		}
		observations = append(observations, ob)
	}
	calls, err := readCalls(o)
	if err != nil {
		return err
	}
	for _, ep := range groups {
		for _, c := range calls {
			if c.SessionID != ep.SessionID || c.SourceLine <= ep.First || c.SourceLine > ep.Last+200 {
				continue
			}
			if isGoEdit(c) {
				ep.RepairCandidates = appendUnique(ep.RepairCandidates, c.ID)
			}
			if validationCommand(commandText(c)) {
				ep.ValidationCandidates = appendUnique(ep.ValidationCandidates, c.ID)
			}
		}
	}
	var episodes []episode
	for _, ep := range groups {
		episodes = append(episodes, *ep)
	}
	sort.Slice(episodes, func(i, j int) bool {
		if episodes[i].Source != episodes[j].Source {
			return episodes[i].Source < episodes[j].Source
		}
		return episodes[i].SessionID < episodes[j].SessionID
	})
	if err := writeJSONL(filepath.Join(o.out, "invocation-ledger.ndjson"), observations); err != nil {
		return err
	}
	if err := writeJSONL(filepath.Join(o.out, "episode-ledger.ndjson"), episodes); err != nil {
		return err
	}
	counts := map[string]map[string]int{}
	for _, ep := range episodes {
		if counts[ep.Source] == nil {
			counts[ep.Source] = map[string]int{}
		}
		c := counts[ep.Source]
		c["candidate_episodes"]++
		if ep.Applies > 0 {
			c["with_apply"]++
		}
		if ep.Succeeded > 0 {
			c["with_successful_apply_tool_result"]++
		}
		if ep.Failed > 0 || ep.PreviewFailed > 0 {
			c["with_failed_tool_result"]++
		}
		if len(ep.RepairCandidates) > 0 {
			c["with_subsequent_go_edit_candidates"]++
		}
	}
	if err := writeJSON(filepath.Join(o.out, "episode-summary.json"), counts); err != nil {
		return err
	}
	fmt.Printf("%d observations; %d candidate episodes\n", len(observations), len(episodes))
	return nil
}

func episodeKey(v invocation) string {
	words := strings.Fields(v.Command)
	var patches []string
	for i, w := range words {
		if (w == "-p" || w == "--patch") && i+1 < len(words) {
			patches = append(patches, words[i+1])
		}
		if strings.HasPrefix(w, "--patch=") {
			patches = append(patches, strings.TrimPrefix(w, "--patch="))
		}
	}
	if len(patches) > 0 {
		return "patch:" + strings.Join(patches, ",")
	}
	var kept []string
	for _, w := range words[1:] {
		switch w {
		case "-d", "--diff", "--dry-run", "--print-only", "-n":
			continue
		}
		kept = append(kept, w)
	}
	return strings.Join(kept, " ")
}

func isolatedGopatch(v invocation) bool {
	a, err := bash.Analyze(commandText(v.call))
	if err != nil {
		return false
	}
	n := 0
	for _, command := range a.Commands {
		words := strings.Fields(command)
		if len(words) == 0 {
			continue
		}
		name := filepath.Base(words[0])
		if name == "cd" || name == "export" {
			continue
		}
		if name == "env" {
			words = words[1:]
			for len(words) > 0 && (strings.HasPrefix(words[0], "-") || strings.Contains(words[0], "=")) {
				words = words[1:]
			}
			if len(words) == 0 {
				return false
			}
			name = filepath.Base(words[0])
		}
		if name != "gopatch" {
			return false
		}
		n++
	}
	return n == 1
}

func specificGopatchFailure(output string) bool {
	text := strings.ToLower(output)
	for _, s := range []string{"load patch ", "code does not type-check", "type-aware refactoring cannot be trusted", "command not found: gopatch", "could not find value for metavariable", "load consumer module", "load import consumer", "unknown flag: --print-patch", "unknown metavariable type", "github.com/uber-go/gopatch", "unexpected declaration: patches can have"} {
		if strings.Contains(text, s) {
			return true
		}
	}
	return false
}

var failedPatch = regexp.MustCompile(`load patch "([^"]+)"`)

func diagnosticMatchesCommand(output, command string) bool {
	matches := failedPatch.FindAllStringSubmatch(output, -1)
	if len(matches) == 0 {
		return true
	}
	for _, m := range matches {
		if strings.Contains(command, m[1]) {
			return true
		}
	}
	return false
}

func appendUnique(values []string, value string) []string {
	for _, v := range values {
		if v == value {
			return values
		}
	}
	return append(values, value)
}
