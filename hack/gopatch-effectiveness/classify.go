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
	captainsession "github.com/flanksource/captain/pkg/session"
)

type candidate struct {
	call
	Source      string   `json:"source"`
	ProviderID  string   `json:"provider_session_id"`
	CWD         string   `json:"cwd"`
	HistoryFile string   `json:"history_file"`
	Model       string   `json:"session_model"`
	Category    string   `json:"candidate_category"`
	Fingerprint string   `json:"fingerprint"`
	Executions  []string `json:"executions,omitempty"`
	ParseError  string   `json:"parse_error,omitempty"`
}

var goPath = regexp.MustCompile(`(?m)(?:^|[\s"'/:])[^\s"']*\.go(?:$|[\s"':])`)
var renameWords = regexp.MustCompile(`(?i)\b(rename|move|refactor|signature)\b`)
var packageName = regexp.MustCompile(`(?m)^\s*package\s+([A-Za-z_]\w*)`)
var functionHead = regexp.MustCompile(`(?m)^func\s+(?:\([^)]*\)\s*)?([A-Za-z_]\w*)[^\n{]*`)

func classify(o options) error {
	data, err := os.ReadFile(filepath.Join(o.out, "sessions.ndjson"))
	if err != nil {
		return err
	}
	var sessions []session
	if err := decodeLines(data, &sessions); err != nil {
		return err
	}
	byID := map[string]session{}
	for _, s := range sessions {
		byID[s.ID] = s
	}
	calls, err := readCalls(o)
	if err != nil {
		return err
	}
	var gopatch, alternatives, negative []candidate
	counts := map[string]map[string]int{}
	seen := map[string]bool{}
	duplicates := 0
	for _, c := range calls {
		if captainsession.IsSyntheticEventTool(c.Tool) {
			continue
		}
		s, ok := byID[c.SessionID]
		if !ok {
			return fmt.Errorf("missing session %s", c.SessionID)
		}
		if counts[s.Source] == nil {
			counts[s.Source] = map[string]int{}
		}
		input, _ := json.Marshal(c.Input)
		hash := sha256.Sum256(input)
		key := s.Source + ":" + c.ID + ":" + hex.EncodeToString(hash[:])
		if c.ID == "" {
			key = fmt.Sprintf("%s:%s:%d", s.Source, c.MessageID, c.PartIndex)
		}
		if seen[key] {
			duplicates++
			continue
		}
		seen[key] = true
		counts[s.Source]["tool_calls"]++
		counts[s.Source]["tool_"+c.Tool]++
		v := candidate{call: c, Source: s.Source, ProviderID: s.ProviderID, CWD: s.CWD, HistoryFile: s.HistoryFile, Model: s.Model, Fingerprint: key}
		command := commandText(c)
		if strings.Contains(strings.ToLower(command), "gopatch") {
			if c.Tool == "Bash" {
				v.Executions, err = executedGopatch(command)
				if err != nil {
					v.ParseError = err.Error()
				}
			}
			v.Category = "gopatch_mention_in_command"
			gopatch = append(gopatch, v)
		}
		if isGoEdit(c) {
			counts[s.Source]["go_edit_candidates"]++
			v.Category = family(c)
			alternatives = append(alternatives, v)
		} else if c.Tool == "Edit" || c.Tool == "Write" || c.Tool == "ApplyPatch" || c.Tool == "Bash" || c.Tool == "CodexExecScript" {
			v.Category = "excluded_edit_or_shell"
			negative = append(negative, v)
		}
	}
	if err := writeJSONL(filepath.Join(o.out, "gopatch-candidates.ndjson"), gopatch); err != nil {
		return err
	}
	if err := writeJSONL(filepath.Join(o.out, "alternative-candidates.ndjson"), alternatives); err != nil {
		return err
	}
	if err := writeJSONL(filepath.Join(o.out, "negative-audit.ndjson"), sample(negative, 20)); err != nil {
		return err
	}
	if err := writeJSONL(filepath.Join(o.out, "alternative-sample.ndjson"), sample(alternatives, 6)); err != nil {
		return err
	}
	summary := map[string]any{"counts": counts, "duplicate_invocations_removed": duplicates, "gopatch_command_candidates": len(gopatch), "go_edit_candidates": len(alternatives), "sessions": len(sessions), "sampling_seed": "20261025", "candidate_labels_are_not_semantic_verdicts": true}
	if err := os.WriteFile(filepath.Join(o.out, "inventory-summary.json"), mustJSON(summary), 0600); err != nil {
		return err
	}
	fmt.Printf("%d sessions; %d command mentions; %d Go edit candidates; %d duplicate records\n", len(sessions), len(gopatch), len(alternatives), duplicates)
	return nil
}

func isGoEdit(c call) bool {
	if c.Tool == "Edit" || c.Tool == "Write" || c.Tool == "MultiEdit" || c.Tool == "ApplyPatch" {
		path := textField(c, "file_path", "path", "filePath", "filename")
		if strings.HasSuffix(path, ".go") {
			return true
		}
		patch := textField(c, "patch", "input", "content")
		return goPath.MatchString(patch) && strings.Contains(patch, "*** ")
	}
	command := commandText(c)
	return goPath.MatchString(command) && (strings.Contains(command, "mv ") || strings.Contains(command, "sed -i") || strings.Contains(command, "perl -") || strings.Contains(command, "write_text(") || strings.Contains(command, "writeFile") || strings.Contains(command, "os.rename(") || strings.Contains(command, "apply_patch"))
}

func family(c call) string {
	before := textField(c, "old_string", "oldText", "old_text")
	after := textField(c, "new_string", "newText", "new_text")
	if c.Tool == "ApplyPatch" {
		var files []struct {
			Path string `json:"path"`
			Old  string `json:"old"`
			New  string `json:"new"`
		}
		if json.Unmarshal(c.Input["files"], &files) == nil {
			for _, f := range files {
				if strings.HasSuffix(f.Path, ".go") {
					before = f.Old
					after = f.New
					break
				}
			}
		}
	}
	command := commandText(c)
	oldPkg, newPkg := packageName.FindStringSubmatch(before), packageName.FindStringSubmatch(after)
	oldFn, newFn := functionHead.FindStringSubmatch(before), functionHead.FindStringSubmatch(after)
	switch {
	case strings.Contains(command, "mv ") || strings.Contains(command, "os.rename("):
		return "package_file_move"
	case len(oldPkg) > 1 && len(newPkg) > 1 && oldPkg[1] != newPkg[1]:
		return "package_file_move"
	case len(oldFn) > 1 && len(newFn) > 1 && oldFn[1] != newFn[1]:
		return "symbol_rename_candidate"
	case len(oldFn) > 1 && len(newFn) > 1 && oldFn[0] != newFn[0]:
		return "signature_or_function_change"
	case before != "" && after != "" && len(strings.Fields(before)) <= 3 && len(strings.Fields(after)) <= 3:
		return "symbol_rename_candidate"
	case renameWords.MatchString(command):
		return "rename_or_move_candidate"
	default:
		return "structural_edit_candidate"
	}
}

func commandText(c call) string {
	if c.Tool == "CodexExecScript" {
		return textField(c, "script")
	}
	return textField(c, "command", "cmd")
}

func executedGopatch(script string) ([]string, error) {
	a, err := bash.Analyze(script)
	if err != nil {
		return nil, err
	}
	var commands []string
	for _, cmd := range a.Commands {
		words := strings.Fields(cmd)
		if len(words) == 0 {
			continue
		}
		if words[0] == "env" {
			words = words[1:]
			for len(words) > 0 && (strings.HasPrefix(words[0], "-") || strings.Contains(words[0], "=")) {
				words = words[1:]
			}
		}
		if len(words) > 0 && filepath.Base(words[0]) == "gopatch" {
			commands = append(commands, strings.Join(words, " "))
		}
	}
	return commands, nil
}

func sample(values []candidate, n int) []candidate {
	groups := map[string][]candidate{}
	for _, c := range values {
		key := c.Source + ":" + c.Category
		groups[key] = append(groups[key], c)
	}
	var keys []string
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out []candidate
	for _, key := range keys {
		group := groups[key]
		sort.Slice(group, func(i, j int) bool { return sampleHash(group[i]) < sampleHash(group[j]) })
		out = append(out, group[:min(n, len(group))]...)
	}
	return out
}

func sampleHash(c candidate) string {
	hash := sha256.Sum256([]byte("20261025:" + c.Fingerprint))
	return hex.EncodeToString(hash[:])
}
