package main

import (
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

type sourceRecord struct {
	Kind      string `json:"source_kind"`
	SessionID string `json:"session_id"`
	Path      string `json:"path"`
	Offset    int64  `json:"byte_offset"`
	Size      int64  `json:"observed_size"`
}

func extendNativeSessions(o options) error {
	data, err := os.ReadFile(filepath.Join(o.out, "sessions.ndjson"))
	if err != nil {
		return err
	}
	var sessions []session
	if err := decodeLines(data, &sessions); err != nil {
		return err
	}
	known := map[string]bool{}
	for _, s := range sessions {
		known[s.ProviderID] = true
	}
	data, err = os.ReadFile(filepath.Join(o.out, "unregistered-files.ndjson"))
	if err != nil {
		return err
	}
	var files []struct {
		Path       string    `json:"path"`
		Source     string    `json:"source"`
		ProviderID string    `json:"provider_session_id"`
		AutoReview bool      `json:"auto_review"`
		MTime      time.Time `json:"mtime"`
	}
	if err := decodeLines(data, &files); err != nil {
		return err
	}
	since, _ := time.Parse(time.RFC3339, o.since)
	added := 0
	for _, f := range files {
		if f.AutoReview || f.MTime.Before(since) {
			continue
		}
		id := f.ProviderID
		if id == "" {
			id = strings.TrimPrefix(strings.TrimSuffix(filepath.Base(f.Path), ".jsonl"), "agent-")
		}
		if known[id] {
			continue
		}
		s := session{ID: "native-" + id, ProviderID: id, Source: f.Source, HistoryFile: f.Path}
		if f.Source == "claude" {
			tr, _, err := claude.ParseTranscript(f.Path)
			if err != nil {
				return err
			}
			s.Messages = len(tr.Entries)
			for _, e := range tr.Entries {
				if e.CWD != "" {
					s.CWD = e.CWD
				}
			}
			for _, u := range tr.ToolUses {
				if u.Model != "" {
					s.Model = u.Model
				}
			}
		}
		sessions = append(sessions, s)
		known[id] = true
		added++
	}
	if err := writeJSONL(filepath.Join(o.out, "sessions.ndjson"), sessions); err != nil {
		return err
	}
	fmt.Printf("added %d native-only sessions; total %d\n", added, len(sessions))
	return nil
}

func coverage(o options) error {
	data, err := query(o, "coverage", nil)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(o.out, "database-sources.ndjson"), data, 0600); err != nil {
		return err
	}
	var sources []sourceRecord
	if err := decodeLines(data, &sources); err != nil {
		return err
	}
	known := map[string]bool{}
	behind := 0
	missing := 0
	for _, s := range sources {
		known[s.Path] = true
		if s.Offset < s.Size {
			behind++
		}
		if _, err := os.Stat(s.Path); os.IsNotExist(err) {
			missing++
		} else if err != nil {
			return err
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	roots := []string{filepath.Join(home, ".claude/projects"), filepath.Join(home, ".codex/sessions"), filepath.Join(home, ".codex/archived_sessions"), filepath.Join(home, ".captain/codex-read-only/sessions")}
	counts := map[string]int{}
	unregistered := []map[string]any{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
				return nil
			}
			kind := "codex"
			if strings.Contains(path, "/.claude/") {
				kind = "claude"
			}
			counts[kind]++
			if known[path] {
				return nil
			}
			r := map[string]any{"path": path, "source": kind}
			info, err := d.Info()
			if err != nil {
				return err
			}
			r["mtime"] = info.ModTime()
			if kind == "codex" {
				meta, err := history.ReadCodexSessionInfo(path)
				if err != nil {
					return err
				}
				if meta != nil {
					r["provider_session_id"] = meta.ID
					r["model"] = meta.Model
					r["auto_review"] = history.IsCodexAutoReviewModel(meta.Model)
					r["started_at"] = meta.StartedAt
				}
			}
			unregistered = append(unregistered, r)
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	if err := writeJSONL(filepath.Join(o.out, "unregistered-files.ndjson"), unregistered); err != nil {
		return err
	}
	recentNonReview := 0
	autoReview := 0
	since, _ := time.Parse(time.RFC3339, o.since)
	before, _ := time.Parse(time.RFC3339, o.before)
	for _, r := range unregistered {
		if r["auto_review"] == true {
			autoReview++
			continue
		}
		if at, ok := r["started_at"].(*time.Time); ok && at != nil && !at.Before(since) && at.Before(before) {
			recentNonReview++
		}
	}
	summary := map[string]any{"database_sources": len(sources), "sources_behind_recorded_size": behind, "recorded_paths_missing_on_disk": missing, "native_file_counts": counts, "unregistered_files": len(unregistered), "unregistered_auto_review_files": autoReview, "unregistered_recent_non_review_codex": recentNonReview, "note": "Offsets compare against the last observed file size, not a live completeness guarantee."}
	data, err = json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(o.out, "coverage.json"), data, 0600); err != nil {
		return err
	}
	fmt.Println(string(data))
	return nil
}
