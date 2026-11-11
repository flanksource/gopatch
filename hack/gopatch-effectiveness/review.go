package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type reviewCard struct {
	EpisodeID      string      `json:"episode_id"`
	Source         string      `json:"source"`
	SessionID      string      `json:"session_id"`
	Command        string      `json:"command"`
	FailureLine    int64       `json:"failure_line"`
	FailureExcerpt string      `json:"failure_excerpt"`
	Recovered      bool        `json:"later_apply_success_same_key"`
	Manual         []candidate `json:"same_turn_target_edits,omitempty"`
}

func reviewCandidates(o options) error {
	data, err := os.ReadFile(filepath.Join(o.out, "invocation-ledger.ndjson"))
	if err != nil {
		return err
	}
	var obs []observation
	if err := decodeLines(data, &obs); err != nil {
		return err
	}
	data, err = os.ReadFile(filepath.Join(o.out, "alternative-candidates.ndjson"))
	if err != nil {
		return err
	}
	var edits []candidate
	if err := decodeLines(data, &edits); err != nil {
		return err
	}
	turns := map[string]string{}
	paths, err := filepath.Glob(filepath.Join(o.out, "native-context", "*.ndjson"))
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
	failed := map[string]observation{}
	for _, ob := range obs {
		if ob.Attribution == "gopatch_failure_diagnostic" {
			old, ok := failed[ob.EpisodeID]
			if !ok || ob.SourceLine > old.SourceLine {
				failed[ob.EpisodeID] = ob
			}
		}
	}
	var cards []reviewCard
	for id, ob := range failed {
		r := reviewCard{EpisodeID: id, Source: ob.Source, SessionID: ob.SessionID, Command: ob.Command, FailureLine: ob.SourceLine, FailureExcerpt: ob.Excerpt}
		for _, other := range obs {
			if other.EpisodeID == id && other.SourceLine > ob.SourceLine && other.Mode == "apply" && other.Status == "succeeded" && other.Attribution != "gopatch_failure_diagnostic" {
				r.Recovered = true
			}
		}
		var targets []string
		for _, word := range strings.Fields(ob.Command) {
			if strings.HasSuffix(word, ".go") {
				targets = append(targets, filepath.Base(word))
			}
		}
		for _, c := range edits {
			if c.SessionID != ob.SessionID || c.SourceLine <= ob.SourceLine || turns[c.SessionID+":"+c.ID] != ob.ProviderTurnID || ob.ProviderTurnID == "" {
				continue
			}
			for _, target := range targets {
				if editsTarget(c, target) {
					r.Manual = append(r.Manual, c)
					break
				}
			}
		}
		cards = append(cards, r)
	}
	sort.Slice(cards, func(i, j int) bool { return cards[i].EpisodeID < cards[j].EpisodeID })
	if err := writeJSONL(filepath.Join(o.out, "failure-review.ndjson"), cards); err != nil {
		return err
	}
	counts := map[string]map[string]int{}
	for _, c := range cards {
		if counts[c.Source] == nil {
			counts[c.Source] = map[string]int{}
		}
		counts[c.Source]["diagnostic_target_groups"]++
		if c.Recovered {
			counts[c.Source]["later_apply_success_same_key"]++
		}
		if !c.Recovered && len(c.Manual) > 0 {
			counts[c.Source]["same_turn_same_file_manual_candidates"]++
		}
	}
	if err := writeJSON(filepath.Join(o.out, "failure-review-summary.json"), counts); err != nil {
		return err
	}
	fmt.Printf("%d diagnostic target groups\n", len(cards))
	return nil
}

func editsTarget(c candidate, target string) bool {
	path := textField(c.call, "file_path", "path", "filePath")
	if path != "" && filepath.Base(path) == target {
		return true
	}
	var files []struct {
		Path string `json:"path"`
	}
	if json.Unmarshal(c.Input["files"], &files) == nil {
		for _, f := range files {
			if filepath.Base(f.Path) == target {
				return true
			}
		}
	}
	if c.Tool == "Bash" || c.Tool == "CodexExecScript" {
		return strings.Contains(commandText(c.call), target)
	}
	return false
}
