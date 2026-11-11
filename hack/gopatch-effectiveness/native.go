package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/flanksource/captain/pkg/ai/history"
	"github.com/flanksource/captain/pkg/claude"
)

type nativeRow struct {
	call
	Response    string `json:"response,omitempty"`
	Error       bool   `json:"is_error,omitempty"`
	Model       string `json:"model,omitempty"`
	ResultAt    string `json:"result_at,omitempty"`
	ResultFound bool   `json:"result_found"`
	Source      string `json:"source"`
}

func nativeRows(s session) ([]nativeRow, error) {
	if _, err := os.Stat(s.HistoryFile); os.IsNotExist(err) {
		path, resolveErr := resolveNativePath(s.HistoryFile)
		if resolveErr != nil {
			return nil, resolveErr
		}
		s.HistoryFile = path
	} else if err != nil {
		return nil, err
	}
	var rows []nativeRow
	if s.Source == "codex" {
		uses, err := history.ExtractCodexToolUses(s.HistoryFile)
		if err != nil {
			return nil, err
		}
		resultTimes, err := codexResultTimes(s.HistoryFile)
		if err != nil {
			return nil, err
		}
		for _, u := range uses {
			if u.ToolUseID == "" || !strings.HasPrefix(u.RecordType, "response_item.") {
				continue
			}
			c := call{SessionID: s.ID, TurnID: u.TurnID, ID: u.ToolUseID, Tool: u.Tool, Sequence: u.SourceLine, SourceLine: u.SourceLine, OutputPresent: u.Response != ""}
			if u.Timestamp != nil {
				c.At = u.Timestamp.Format(time.RFC3339Nano)
			}
			data, err := json.Marshal(u.Input)
			if err != nil {
				return nil, err
			}
			if err := json.Unmarshal(data, &c.Input); err != nil {
				return nil, err
			}
			at, found := resultTimes[u.SourceLine]
			rows = append(rows, nativeRow{call: c, Response: u.Response, Model: u.Model, ResultFound: found, ResultAt: at, Source: s.Source})
		}
		return rows, nil
	}
	tr, _, err := claude.ParseTranscript(s.HistoryFile)
	if err != nil {
		return nil, err
	}
	lines := map[string]int{}
	resultAt := map[string]string{}
	for _, e := range tr.Entries {
		for _, u := range e.Message.GetToolUses() {
			lines[u.ID] = e.Line
		}
		for _, r := range e.Message.GetToolResults() {
			resultAt[r.ToolUseID] = e.Timestamp
		}
	}
	for _, u := range tr.ToolUses {
		if _, ok := lines[u.ToolUseID]; !ok {
			continue
		}
		c := call{SessionID: s.ID, ID: u.ToolUseID, Tool: u.Tool, Sequence: int64(lines[u.ToolUseID]), SourceLine: int64(lines[u.ToolUseID]), OutputPresent: u.Response != ""}
		if u.Timestamp != nil {
			c.At = u.Timestamp.Format(time.RFC3339Nano)
		}
		data, err := json.Marshal(u.Input)
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &c.Input); err != nil {
			return nil, err
		}
		_, found := resultAt[u.ToolUseID]
		rows = append(rows, nativeRow{call: c, Response: u.Response, Error: u.IsError, Model: u.Model, ResultAt: resultAt[u.ToolUseID], ResultFound: found, Source: s.Source})
	}
	return rows, nil
}

func codexResultTimes(path string) (map[int64]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 65536), 32<<20)
	callLines := map[string]int64{}
	times := map[int64]string{}
	line := int64(0)
	for s.Scan() {
		line++
		event, err := history.ParseCodexLine(s.Text())
		if err != nil {
			return nil, err
		}
		if event.Type != "response_item" || event.Payload.CallID == "" {
			continue
		}
		kind := event.Payload.Type
		if kind == "function_call" || kind == "custom_tool_call" {
			callLines[event.Payload.CallID] = line
		}
		if kind == "function_call_output" || kind == "custom_tool_call_output" {
			if sourceLine, ok := callLines[event.Payload.CallID]; ok {
				times[sourceLine] = event.Timestamp
			}
		}
	}
	return times, s.Err()
}

func nativeCollect(o options) error {
	data, err := os.ReadFile(filepath.Join(o.out, "sessions.ndjson"))
	if err != nil {
		return err
	}
	var sessions []session
	if err := decodeLines(data, &sessions); err != nil {
		return err
	}
	since, _ := time.Parse(time.RFC3339, o.since)
	before, _ := time.Parse(time.RFC3339, o.before)
	dir := filepath.Join(o.out, "native-batches")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	for i, s := range sessions {
		if i%o.shards != o.shard {
			continue
		}
		name := filepath.Join(dir, fmt.Sprintf("%05d.ndjson", i))
		if _, err := os.Stat(name); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		start := time.Now()
		rows, err := nativeRows(s)
		method := "captain_parser"
		if os.IsNotExist(err) {
			method = "retained_database_missing_native_file"
			data, queryErr := query(o, "inventory", map[string]string{"session_ids": s.ID})
			if queryErr != nil {
				return queryErr
			}
			var stored []call
			if err := decodeLines(data, &stored); err != nil {
				return err
			}
			for _, c := range stored {
				rows = append(rows, nativeRow{call: c})
			}
		} else if err != nil {
			return fmt.Errorf("native %s %s: %w", s.Source, s.HistoryFile, err)
		}
		var compact []call
		for _, r := range rows {
			at, err := time.Parse(time.RFC3339Nano, r.At)
			if err != nil {
				continue
			}
			if at.Before(since) || !at.Before(before) {
				continue
			}
			c := r.call
			if c.Tool == "Bash" {
				command := textField(c, "command", "cmd")
				if command != "" {
					data, _ := json.Marshal(command)
					c.Input = map[string]json.RawMessage{"command": data}
				}
			}
			if !interestingTool(c.Tool) {
				c.Input = nil
			}
			compact = append(compact, c)
		}
		if err := writeJSONL(name, compact); err != nil {
			return err
		}
		f, err := os.OpenFile(filepath.Join(o.out, "native-metrics.ndjson"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		err = json.NewEncoder(f).Encode(map[string]any{"session_id": s.ID, "source": s.Source, "seconds": time.Since(start).Seconds(), "records": len(compact), "history_file": s.HistoryFile, "method": method})
		closeErr := f.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if i%100 < o.shards || i == len(sessions)-1 {
			fmt.Printf("native sessions %d/%d (%s)\n", i+1, len(sessions), s.Source)
		}
	}
	return nil
}

func interestingTool(name string) bool {
	name = strings.ToLower(name)
	for _, part := range []string{"bash", "edit", "write", "exec", "shell", "patch", "rename", "move"} {
		if strings.Contains(name, part) {
			return true
		}
	}
	return false
}

var nativePathIndex map[string][]string

func resolveNativePath(requested string) (string, error) {
	if nativePathIndex == nil {
		nativePathIndex = map[string][]string{}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		for _, root := range []string{filepath.Join(home, ".claude/projects"), filepath.Join(home, ".codex/sessions"), filepath.Join(home, ".codex/archived_sessions")} {
			err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
					nativePathIndex[d.Name()] = append(nativePathIndex[d.Name()], path)
				}
				return nil
			})
			if err != nil && !os.IsNotExist(err) {
				return "", err
			}
		}
	}
	paths := nativePathIndex[filepath.Base(requested)]
	if len(paths) == 1 {
		return paths[0], nil
	}
	if len(paths) > 1 {
		return "", fmt.Errorf("ambiguous relocated transcript %s", requested)
	}
	return "", &os.PathError{Op: "resolve transcript", Path: requested, Err: os.ErrNotExist}
}

func nativeEvidence(o options) error {
	data, err := os.ReadFile(filepath.Join(o.out, "sessions.ndjson"))
	if err != nil {
		return err
	}
	var sessions []session
	if err := decodeLines(data, &sessions); err != nil {
		return err
	}
	data, err = os.ReadFile(filepath.Join(o.out, "selected-calls.ndjson"))
	if err != nil {
		return err
	}
	var selected []call
	if err := decodeLines(data, &selected); err != nil {
		return err
	}
	ids := map[string]map[string]bool{}
	for _, c := range selected {
		if ids[c.SessionID] == nil {
			ids[c.SessionID] = map[string]bool{}
		}
		ids[c.SessionID][c.ID] = true
	}
	dir := filepath.Join(o.out, "native-evidence")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	for i, s := range sessions {
		if i%o.shards != o.shard || len(ids[s.ID]) == 0 {
			continue
		}
		rows, err := nativeRows(s)
		if os.IsNotExist(err) {
			var callIDs []string
			for id := range ids[s.ID] {
				callIDs = append(callIDs, id)
			}
			data, queryErr := query(o, "results", map[string]string{"session_id": s.ID, "call_ids": strings.Join(callIDs, ",")})
			if queryErr != nil {
				return queryErr
			}
			var stored []struct {
				ID     string          `json:"tool_call_id"`
				Line   int64           `json:"source_line"`
				At     string          `json:"occurred_at"`
				State  string          `json:"state"`
				Error  string          `json:"error"`
				Output json.RawMessage `json:"output"`
			}
			if err := decodeLines(data, &stored); err != nil {
				return err
			}
			for _, r := range stored {
				response := string(r.Output)
				_ = json.Unmarshal(r.Output, &response)
				rows = append(rows, nativeRow{call: call{SessionID: s.ID, ID: r.ID, SourceLine: r.Line}, Response: response, ResultAt: r.At, ResultFound: true, Error: r.State == "output-error" || r.Error != ""})
			}
		} else if err != nil {
			return err
		}
		var out []nativeRow
		for _, r := range rows {
			if ids[s.ID][r.ID] {
				out = append(out, r)
			}
		}
		if err := writeJSONL(filepath.Join(dir, s.ID+".ndjson"), out); err != nil {
			return err
		}
	}
	return nil
}
