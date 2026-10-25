package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type invocation struct {
	candidate
	Command     string `json:"command"`
	Mode        string `json:"mode"`
	Operation   string `json:"operation"`
	Development bool   `json:"development_context"`
}

func selectEvidence(o options) error {
	data, err := os.ReadFile(filepath.Join(o.out, "gopatch-candidates.ndjson"))
	if err != nil {
		return err
	}
	var candidates []candidate
	if err := decodeLines(data, &candidates); err != nil {
		return err
	}
	selected := map[string]call{}
	windows := map[string][]int64{}
	var invocations []invocation
	for _, c := range candidates {
		for _, command := range c.Executions {
			mode, operation := invocationKind(command)
			v := invocation{candidate: c, Command: command, Mode: mode, Operation: operation, Development: strings.Contains(c.CWD, "/gopatch") && (strings.Contains(command, "testdata") || strings.Contains(command, "fixture") || strings.Contains(command, "/tmp/"))}
			invocations = append(invocations, v)
			if mode == "apply" || mode == "preview" {
				windows[c.SessionID] = append(windows[c.SessionID], c.Sequence)
			}
		}
		if len(c.Executions) > 0 || c.ParseError != "" || c.Tool == "CodexExecScript" {
			selected[c.SessionID+":"+c.ID] = c.call
		}
	}
	for _, name := range []string{"alternative-sample.ndjson", "negative-audit.ndjson"} {
		data, err := os.ReadFile(filepath.Join(o.out, name))
		if err != nil {
			return err
		}
		var sample []candidate
		if err := decodeLines(data, &sample); err != nil {
			return err
		}
		for _, c := range sample {
			selected[c.SessionID+":"+c.ID] = c.call
		}
	}
	calls, err := readCalls(o)
	if err != nil {
		return err
	}
	var ordered []call
	for _, c := range calls {
		for _, at := range windows[c.SessionID] {
			if c.Sequence >= at-10 && c.Sequence <= at+200 && (isGoEdit(c) || validationCommand(commandText(c)) || c.Tool == "write_stdin" || c.Tool == "Wait") {
				selected[c.SessionID+":"+c.ID] = c
				break
			}
		}
		if _, ok := selected[c.SessionID+":"+c.ID]; ok {
			ordered = append(ordered, c)
		}
	}
	if err := writeJSONL(filepath.Join(o.out, "selected-calls.ndjson"), ordered); err != nil {
		return err
	}
	if err := writeJSONL(filepath.Join(o.out, "gopatch-invocations.ndjson"), invocations); err != nil {
		return err
	}
	counts := map[string]map[string]int{}
	for _, v := range invocations {
		if counts[v.Source] == nil {
			counts[v.Source] = map[string]int{}
		}
		counts[v.Source][v.Mode]++
		counts[v.Source]["operation_"+v.Operation]++
		if v.Development {
			counts[v.Source]["development"]++
		}
	}
	if err := os.WriteFile(filepath.Join(o.out, "invocation-summary.json"), mustJSON(counts), 0600); err != nil {
		return err
	}
	fmt.Printf("%d invocations; %d selected result calls\n", len(invocations), len(ordered))
	return nil
}

func usageEvidence(o options) error {
	data, err := os.ReadFile(filepath.Join(o.out, "selected-calls.ndjson"))
	if err != nil {
		return err
	}
	var selected []call
	if err := decodeLines(data, &selected); err != nil {
		return err
	}
	seen := map[string]bool{}
	var ids []string
	for _, c := range selected {
		if !seen[c.SessionID] {
			seen[c.SessionID] = true
			ids = append(ids, c.SessionID)
		}
	}
	rows, err := query(o, "usage", map[string]string{"session_ids": strings.Join(ids, ",")})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(o.out, "usage.ndjson"), rows, 0600)
}

func invocationKind(command string) (string, string) {
	words := strings.Fields(command)
	if len(words) < 2 {
		return "help", "none"
	}
	for _, w := range words[1:] {
		if w == "--help" || w == "-h" || w == "--version" {
			return "help", "none"
		}
	}
	op := "patch"
	switch words[1] {
	case "help", "version":
		return "help", "none"
	case "tree", "ref", "inspect", "list":
		return "inspect", words[1]
	case "rename", "move", "edit", "add", "delete", "replace":
		if len(words) > 2 {
			op = words[1] + "_" + words[2]
		} else {
			op = words[1]
		}
	case "patch":
		op = "patch"
	}
	for _, w := range words[1:] {
		if w == "-d" || w == "--diff" || w == "--dry-run" || w == "--print-only" || w == "-n" {
			return "preview", op
		}
	}
	return "apply", op
}

func validationCommand(cmd string) bool {
	for _, s := range []string{"go test", "gavel test", "make test", "make build", "go vet", "git diff", "git status", "gavel lint", "make lint", "gofmt", "goimports"} {
		if strings.Contains(cmd, s) {
			return true
		}
	}
	return false
}

func countsFromFile(path string) (map[string]int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var rows []call
	if err := decodeLines(data, &rows); err != nil {
		return nil, err
	}
	out := map[string]int{}
	for _, r := range rows {
		out[r.Tool]++
	}
	return out, nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0600)
}
