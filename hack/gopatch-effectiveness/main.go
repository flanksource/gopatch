package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type session struct {
	ID          string  `json:"id"`
	Source      string  `json:"source"`
	ProviderID  string  `json:"provider_session_id"`
	HistoryFile string  `json:"history_file"`
	CWD         string  `json:"cwd"`
	Model       string  `json:"model"`
	Messages    int     `json:"message_count"`
	Tools       int     `json:"tool_call_count"`
	Parent      *string `json:"parent_session_id"`
	Root        *string `json:"root_session_id"`
}

type call struct {
	SessionID     string                     `json:"session_id"`
	MessageID     string                     `json:"message_id"`
	TurnID        string                     `json:"turn_id"`
	Sequence      int64                      `json:"sequence"`
	SourceLine    int64                      `json:"source_line"`
	PartIndex     int                        `json:"part_index"`
	At            string                     `json:"occurred_at"`
	ID            string                     `json:"tool_call_id"`
	Tool          string                     `json:"tool"`
	State         string                     `json:"state"`
	Input         map[string]json.RawMessage `json:"input"`
	OutputPresent bool                       `json:"output_present"`
}

type options struct {
	mode   string
	out    string
	sql    string
	since  string
	before string
	limit  int
	batch  int
	shard  int
	shards int
}

func main() {
	o := options{}
	flag.StringVar(&o.mode, "mode", "collect", "collect, classify, evidence, or context")
	flag.StringVar(&o.out, "output", ".tmp/gopatch-effectiveness", "artifact directory")
	flag.StringVar(&o.sql, "sql-dir", "hack/gopatch-effectiveness", "SQL source directory")
	flag.StringVar(&o.since, "since", "2026-09-25T05:50:25Z", "inclusive UTC cutoff")
	flag.StringVar(&o.before, "before", "2026-10-25T05:50:25Z", "exclusive UTC cutoff")
	flag.IntVar(&o.limit, "limit", 0, "pilot session limit; zero means all")
	flag.IntVar(&o.batch, "batch", 1, "sessions per SQL batch")
	flag.IntVar(&o.shard, "shard", 0, "zero-based batch partition")
	flag.IntVar(&o.shards, "shards", 1, "number of batch partitions")
	flag.Parse()
	if err := run(o); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(o options) error {
	if _, err := time.Parse(time.RFC3339, o.since); err != nil {
		return err
	}
	if _, err := time.Parse(time.RFC3339, o.before); err != nil {
		return err
	}
	if o.batch < 1 || o.batch > 100 {
		return fmt.Errorf("batch must be 1..100")
	}
	if o.shard < 0 || o.shard >= o.shards {
		return fmt.Errorf("invalid shard")
	}
	if os.Getenv("GAVEL_DB_DSN") == "" {
		return fmt.Errorf("GAVEL_DB_DSN is required")
	}
	if err := os.MkdirAll(o.out, 0700); err != nil {
		return err
	}
	switch o.mode {
	case "prepare":
		_, err := prepare(o)
		return err
	case "native":
		return nativeCollect(o)
	case "native-evidence":
		return nativeEvidence(o)
	case "native-context":
		return nativeContext(o)
	case "coverage":
		return coverage(o)
	case "select":
		return selectEvidence(o)
	case "usage":
		return usageEvidence(o)
	case "analyze":
		return analyze(o)
	case "metrics":
		return metrics(o)
	case "review":
		return reviewCandidates(o)
	case "extend":
		return extendNativeSessions(o)
	case "collect":
		return collect(o)
	case "classify":
		return classify(o)
	case "evidence":
		return evidence(o)
	case "context":
		return contextEvidence(o)
	default:
		return fmt.Errorf("unknown mode %q", o.mode)
	}
}

func query(o options, name string, vars map[string]string) ([]byte, error) {
	args := []string{"-d", os.Getenv("GAVEL_DB_DSN"), "-X", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-v", "since=" + o.since, "-v", "before=" + o.before}
	for k, v := range vars {
		args = append(args, "-v", k+"="+v)
	}
	args = append(args, "-f", filepath.Join(o.sql, name+".sql"))
	cmd := exec.Command("psql", args...)
	cmd.Env = append(os.Environ(), "PGOPTIONS=-c default_transaction_read_only=on -c statement_timeout=60000")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	start := time.Now()
	data, err := cmd.Output()
	record := map[string]any{"query": name, "seconds": time.Since(start).Seconds(), "bytes": len(data), "variables": vars, "ok": err == nil}
	f, fileErr := os.OpenFile(filepath.Join(o.out, "query-metrics.ndjson"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if fileErr != nil {
		return nil, fileErr
	}
	encodeErr := json.NewEncoder(f).Encode(record)
	closeErr := f.Close()
	if encodeErr != nil {
		return nil, encodeErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %s", name, err, stderr.String())
	}
	return data, nil
}

func prepare(o options) ([]session, error) {
	data, err := query(o, "sessions", nil)
	if err != nil {
		return nil, err
	}
	var sessions []session
	if err := decodeLines(data, &sessions); err != nil {
		return nil, err
	}
	if o.limit > 0 && len(sessions) > o.limit {
		sessions = pilotSessions(sessions, o.limit)
	}
	if err := writeJSONL(filepath.Join(o.out, "sessions.ndjson"), sessions); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(o.out, "manifest.json"), mustJSON(map[string]any{"since": o.since, "before": o.before, "sessions": len(sessions), "pilot": o.limit > 0, "batch_size": o.batch, "created_at": time.Now().UTC(), "database_read_only": true}), 0600); err != nil {
		return nil, err
	}
	files, err := filepath.Glob(filepath.Join(o.sql, "*.sql"))
	if err != nil {
		return nil, err
	}
	for _, path := range files {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(o.out, filepath.Base(path)), data, 0600); err != nil {
			return nil, err
		}
	}
	return sessions, nil
}

func collect(o options) error {
	var sessions []session
	data, err := os.ReadFile(filepath.Join(o.out, "sessions.ndjson"))
	if os.IsNotExist(err) {
		sessions, err = prepare(o)
	} else if err == nil {
		err = decodeLines(data, &sessions)
	}
	if err != nil {
		return err
	}
	dir := filepath.Join(o.out, "batches")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	for start := 0; start < len(sessions); start += o.batch {
		if (start/o.batch)%o.shards != o.shard {
			continue
		}
		end := min(start+o.batch, len(sessions))
		name := filepath.Join(dir, fmt.Sprintf("%05d.ndjson", start/o.batch))
		if _, err := os.Stat(name); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return err
		}
		ids := make([]string, 0, end-start)
		for _, s := range sessions[start:end] {
			ids = append(ids, s.ID)
		}
		rows, err := query(o, "inventory", map[string]string{"session_ids": strings.Join(ids, ",")})
		if err != nil {
			return err
		}
		if err := os.WriteFile(name, rows, 0600); err != nil {
			return err
		}
		if start/o.batch%20 == o.shard || end == len(sessions) {
			fmt.Printf("sessions %d/%d; batch %d bytes\n", end, len(sessions), len(rows))
		}
	}
	if o.shards > 1 {
		return nil
	}
	return classify(o)
}

func decodeLines[T any](data []byte, values *[]T) error {
	s := bufio.NewScanner(bytes.NewReader(data))
	s.Buffer(make([]byte, 65536), 32<<20)
	for s.Scan() {
		if len(bytes.TrimSpace(s.Bytes())) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(s.Bytes(), &v); err != nil {
			return err
		}
		*values = append(*values, v)
	}
	return s.Err()
}

func writeJSONL[T any](path string, values []T) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	e := json.NewEncoder(f)
	for _, v := range values {
		if err := e.Encode(v); err != nil {
			_ = f.Close()
			return err
		}
	}
	return f.Close()
}

func mustJSON(v any) []byte {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		panic(err)
	}
	return append(data, '\n')
}

func pilotSessions(sessions []session, n int) []session {
	var claude, codex []session
	for _, s := range sessions {
		if s.Source == "claude" {
			claude = append(claude, s)
		} else {
			codex = append(codex, s)
		}
	}
	var out []session
	for i := 0; len(out) < n; i++ {
		if i < len(claude) && len(out) < n {
			out = append(out, claude[i])
		}
		if i < len(codex) && len(out) < n {
			out = append(out, codex[i])
		}
	}
	return out
}

func readCalls(o options) ([]call, error) {
	dir := "batches"
	if _, err := os.Stat(filepath.Join(o.out, "native-batches")); err == nil {
		dir = "native-batches"
	}
	paths, err := filepath.Glob(filepath.Join(o.out, dir, "*.ndjson"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("no extracted batches")
	}
	var calls []call
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if err := decodeLines(data, &calls); err != nil {
			return nil, err
		}
	}
	sort.Slice(calls, func(i, j int) bool {
		if calls[i].SessionID != calls[j].SessionID {
			return calls[i].SessionID < calls[j].SessionID
		}
		return calls[i].Sequence < calls[j].Sequence
	})
	return calls, nil
}

func textField(c call, keys ...string) string {
	for _, k := range keys {
		var value string
		if json.Unmarshal(c.Input[k], &value) == nil && value != "" {
			return value
		}
	}
	return ""
}

func evidence(o options) error {
	data, err := os.ReadFile(filepath.Join(o.out, "selected-calls.ndjson"))
	if err != nil {
		return err
	}
	var selected []call
	if err := decodeLines(data, &selected); err != nil {
		return err
	}
	bySession := map[string][]string{}
	for _, c := range selected {
		bySession[c.SessionID] = append(bySession[c.SessionID], c.ID)
	}
	var sessions []string
	for id := range bySession {
		sessions = append(sessions, id)
	}
	sort.Strings(sessions)
	dir := filepath.Join(o.out, "evidence")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	for _, id := range sessions {
		rows, err := query(o, "results", map[string]string{"session_id": id, "call_ids": strings.Join(bySession[id], ",")})
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, id+".ndjson"), rows, 0600); err != nil {
			return err
		}
	}
	rows, err := query(o, "usage", map[string]string{"session_ids": strings.Join(sessions, ",")})
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(o.out, "usage.ndjson"), rows, 0600)
}

func contextEvidence(o options) error {
	if len(flag.Args()) != 3 {
		return fmt.Errorf("context requires session_id first_sequence last_sequence")
	}
	id := flag.Arg(0)
	first, err := strconv.ParseInt(flag.Arg(1), 10, 64)
	if err != nil {
		return err
	}
	last, err := strconv.ParseInt(flag.Arg(2), 10, 64)
	if err != nil {
		return err
	}
	rows, err := query(o, "context", map[string]string{"session_id": id, "first": fmt.Sprint(first), "last": fmt.Sprint(last)})
	if err != nil {
		return err
	}
	dir := filepath.Join(o.out, "context")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, fmt.Sprintf("%s-%d-%d.ndjson", id, first, last)), rows, 0600)
}
