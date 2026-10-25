package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	captainsession "github.com/flanksource/captain/pkg/session"
)

type contextRow struct {
	SessionID string   `json:"session_id"`
	Sequence  int64    `json:"sequence"`
	TurnID    string   `json:"provider_turn_id"`
	Role      string   `json:"role"`
	Text      string   `json:"text,omitempty"`
	ToolIDs   []string `json:"tool_call_ids,omitempty"`
}

func nativeContext(o options) error {
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
	wanted := map[string]bool{}
	for _, c := range selected {
		wanted[c.SessionID] = true
	}
	dir := filepath.Join(o.out, "native-context")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	for i, s := range sessions {
		if i%o.shards != o.shard || !wanted[s.ID] {
			continue
		}
		path := s.HistoryFile
		if _, err := os.Stat(path); os.IsNotExist(err) {
			path, err = resolveNativePath(path)
			if os.IsNotExist(err) {
				fmt.Printf("context retained only in database: %s\n", s.ID)
				continue
			}
			if err != nil {
				return err
			}
		} else if err != nil {
			return err
		}
		var built *captainsession.Session
		var err error
		if s.Source == "codex" {
			built, err = captainsession.BuildCodexFile(path)
		} else {
			built, _, err = captainsession.BuildTranscriptFile(path)
		}
		if err != nil {
			return err
		}
		var out []contextRow
		for _, m := range built.Messages {
			if m.Role == "system" {
				continue
			}
			r := contextRow{SessionID: s.ID, Sequence: m.SourceLine, TurnID: m.TurnID, Role: m.Role}
			var text []string
			for _, p := range m.Parts {
				if p.Type == "text" && p.Text != "" {
					text = append(text, p.Text)
				}
				if p.ToolCallID != "" {
					r.ToolIDs = append(r.ToolIDs, p.ToolCallID)
				}
			}
			r.Text = strings.Join(text, "\n")
			if r.Text != "" || len(r.ToolIDs) > 0 {
				out = append(out, r)
			}
		}
		if err := writeJSONL(filepath.Join(dir, s.ID+".ndjson"), out); err != nil {
			return err
		}
	}
	return nil
}
