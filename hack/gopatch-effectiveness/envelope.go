package main

import (
	"encoding/json"
	"io"
	"regexp"
	"strconv"
	"strings"
)

type envelope struct {
	Exits   []int     `json:"exit_codes,omitempty"`
	Seconds []float64 `json:"seconds,omitempty"`
	Jobs    []int     `json:"jobs,omitempty"`
	Output  string    `json:"output,omitempty"`
}

var processExit = regexp.MustCompile(`(?i)(?:Process exited with code|exit code:)\s*(\d+)`)
var wallSeconds = regexp.MustCompile(`(?i)Wall time:?\s*([0-9.]+)\s*seconds`)

func resultEnvelope(text string) envelope {
	e := envelope{}
	d := json.NewDecoder(strings.NewReader(text))
	parsed := false
	for {
		var value any
		err := d.Decode(&value)
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}
		parsed = true
		observeEnvelope(value, &e)
	}
	if !parsed || e.Output == "" {
		e.Output = text
	}
	if len(e.Exits) == 0 {
		for _, m := range processExit.FindAllStringSubmatch(text, -1) {
			n, err := strconv.Atoi(m[1])
			if err == nil {
				e.Exits = append(e.Exits, n)
			}
		}
	}
	if len(e.Seconds) == 0 {
		for _, m := range wallSeconds.FindAllStringSubmatch(text, -1) {
			n, err := strconv.ParseFloat(m[1], 64)
			if err == nil {
				e.Seconds = append(e.Seconds, n)
			}
		}
	}
	return e
}

func observeEnvelope(value any, e *envelope) {
	switch v := value.(type) {
	case []any:
		for _, item := range v {
			observeEnvelope(item, e)
		}
	case map[string]any:
		if n, ok := v["exit_code"].(float64); ok {
			e.Exits = append(e.Exits, int(n))
		}
		if n, ok := v["wall_time_seconds"].(float64); ok {
			e.Seconds = append(e.Seconds, n)
		}
		if n, ok := v["session_id"].(float64); ok {
			e.Jobs = append(e.Jobs, int(n))
		}
		if out, ok := v["output"].(string); ok {
			e.Output += out + "\n"
		}
		for _, key := range []string{"value", "result", "content"} {
			if nested, ok := v[key]; ok {
				observeEnvelope(nested, e)
			}
		}
	}
}

func resultStatus(row nativeRow, e envelope) string {
	if row.Error {
		return "failed"
	}
	for _, exit := range e.Exits {
		if exit != 0 {
			return "failed"
		}
	}
	if len(e.Exits) > 0 {
		return "succeeded"
	}
	if len(e.Jobs) > 0 || strings.Contains(row.Response, "Script running with cell ID") || strings.Contains(row.Response, "running in background") {
		return "pending"
	}
	if row.Source == "claude" && row.ResultFound && row.ResultAt != "" {
		return "succeeded"
	}
	return "unknown"
}

func failureKind(output string) string {
	text := strings.ToLower(output)
	for _, pair := range [][2]string{
		{"unknown command", "cli_or_syntax"}, {"unknown flag", "cli_or_syntax"}, {"expected declaration", "patch_syntax"}, {"expected operand", "patch_syntax"}, {"parse patch", "patch_syntax"}, {"syntax error", "patch_syntax"},
		{"stale", "index_or_snapshot"}, {"not indexed", "index_or_snapshot"}, {"snapshot", "index_or_snapshot"},
		{"no such file", "missing_file_or_path"}, {"not a directory", "missing_file_or_path"},
		{"error loading", "package_or_type_loading"}, {"failed to load", "package_or_type_loading"}, {"undefined:", "package_or_type_loading"}, {"cannot use", "package_or_type_loading"}, {"found packages", "package_or_type_loading"}, {"could not import", "package_or_type_loading"},
		{"no changes", "no_change_report"}, {"no matches", "no_change_report"}, {"permission denied", "permissions"},
	} {
		if strings.Contains(text, pair[0]) {
			return pair[1]
		}
	}
	return "other_or_unattributed"
}
