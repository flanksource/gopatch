package main

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type usageRow struct {
	SessionID  string `json:"session_id"`
	TurnID     string `json:"provider_turn_id"`
	Model      string `json:"model"`
	Provider   string `json:"provider"`
	StartedAt  string `json:"started_at"`
	Input      int64  `json:"input_tokens"`
	Output     int64  `json:"output_tokens"`
	Reasoning  int64  `json:"reasoning_tokens"`
	CacheRead  int64  `json:"cache_read_tokens"`
	CacheWrite int64  `json:"cache_write_tokens"`
}

func metrics(o options) error {
	data, err := os.ReadFile(filepath.Join(o.out, "invocation-ledger.ndjson"))
	if err != nil {
		return err
	}
	var observations []observation
	if err := decodeLines(data, &observations); err != nil {
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
	data, err = os.ReadFile(filepath.Join(o.out, "alternative-sample.ndjson"))
	if err != nil {
		return err
	}
	var samples []candidate
	if err := decodeLines(data, &samples); err != nil {
		return err
	}
	data, err = os.ReadFile(filepath.Join(o.out, "usage.ndjson"))
	if err != nil {
		return err
	}
	var usage []usageRow
	if err := decodeLines(data, &usage); err != nil {
		return err
	}
	toolTurns := map[string]string{}
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
				toolTurns[r.SessionID+":"+id] = r.TurnID
			}
		}
	}
	evidence := map[string]nativeRow{}
	paths, err = filepath.Glob(filepath.Join(o.out, "native-evidence", "*.ndjson"))
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
	counts := map[string]map[string]int{}
	durations := map[string][]float64{}
	payloads := map[string][]float64{}
	unique := map[string]bool{}
	turnApply := map[string]bool{}
	turnEdit := map[string]int{}
	for _, ob := range observations {
		if counts[ob.Source] == nil {
			counts[ob.Source] = map[string]int{}
		}
		c := counts[ob.Source]
		c[ob.Mode+"_"+ob.Status]++
		if ob.ResolvedFollowup {
			c["resolved_asynchronous"]++
		}
		if ob.Mode != "apply" {
			continue
		}
		key := ob.SessionID + ":" + ob.ID
		turnID := toolTurns[key]
		if turnID != "" {
			turnApply[ob.SessionID+":"+turnID] = true
		}
		if unique[key] {
			continue
		}
		unique[key] = true
		c["apply_tool_calls"]++
		if ob.Isolated {
			c["isolated_apply_tool_calls"]++
			if ob.Status == "succeeded" && ob.Attribution == "isolated_result" && len(ob.Seconds) == 1 {
				durations[ob.Source+":gopatch_recorded_wall_seconds"] = append(durations[ob.Source+":gopatch_recorded_wall_seconds"], ob.Seconds[0])
			}
		}
		r := evidence[key]
		if r.ResultAt != "" && !ob.ResolvedFollowup && ob.Status == "succeeded" && ob.Attribution == "isolated_result" {
			start, err1 := time.Parse(time.RFC3339Nano, r.At)
			end, err2 := time.Parse(time.RFC3339Nano, r.ResultAt)
			if err1 == nil && err2 == nil && !end.Before(start) && ob.Isolated {
				durations[ob.Source+":gopatch_call_result_seconds"] = append(durations[ob.Source+":gopatch_call_result_seconds"], end.Sub(start).Seconds())
			}
		}
		payloads[ob.Source+":gopatch_apply_command_bytes"] = append(payloads[ob.Source+":gopatch_apply_command_bytes"], float64(len(commandText(ob.call))))
	}
	for _, c := range edits {
		if counts[c.Source] == nil {
			counts[c.Source] = map[string]int{}
		}
		counts[c.Source]["go_edit_calls"]++
		counts[c.Source]["go_edit_"+c.Tool]++
		counts[c.Source]["candidate_family_"+c.Category]++
		key := c.SessionID + ":" + c.ID
		turnID := toolTurns[key]
		if turnID != "" {
			turnEdit[c.SessionID+":"+turnID]++
		}
		data, _ := json.Marshal(c.Input)
		payloads[c.Source+":go_edit_argument_bytes"] = append(payloads[c.Source+":go_edit_argument_bytes"], float64(len(data)))
	}
	var baselineResults []map[string]any
	for _, c := range samples {
		r := evidence[c.SessionID+":"+c.ID]
		e := resultEnvelope(r.Response)
		status := resultStatus(r, e)
		result := map[string]any{"session_id": c.SessionID, "tool_call_id": c.ID, "source": c.Source, "line": c.SourceLine, "family": c.Category, "tool": c.Tool, "tool_status": status, "result_found": r.ResultFound, "response_excerpt": excerpt(e.Output, 500)}
		if len(e.Seconds) == 1 && status == "succeeded" {
			durations[c.Source+":sample_manual_recorded_wall_seconds"] = append(durations[c.Source+":sample_manual_recorded_wall_seconds"], e.Seconds[0])
			result["recorded_wall_seconds"] = e.Seconds[0]
		}
		if r.ResultAt != "" && status == "succeeded" {
			start, err1 := time.Parse(time.RFC3339Nano, r.At)
			end, err2 := time.Parse(time.RFC3339Nano, r.ResultAt)
			if err1 == nil && err2 == nil && !end.Before(start) {
				durations[c.Source+":sample_manual_call_result_seconds"] = append(durations[c.Source+":sample_manual_call_result_seconds"], end.Sub(start).Seconds())
				result["call_result_seconds"] = end.Sub(start).Seconds()
			}
		}
		baselineResults = append(baselineResults, result)
	}
	turnGroups := map[string][]usageRow{}
	var joined []map[string]any
	since, _ := time.Parse(time.RFC3339, o.since)
	before, _ := time.Parse(time.RFC3339, o.before)
	for _, u := range usage {
		key := u.SessionID + ":" + u.TurnID
		apply := turnApply[key]
		manual := turnEdit[key]
		if !apply && manual == 0 {
			continue
		}
		at, err := time.Parse(time.RFC3339Nano, u.StartedAt)
		if err != nil || at.Before(since) || !at.Before(before) {
			continue
		}
		group := "manual_go_edits"
		if apply {
			group = "gopatch_only_go_transform"
			if manual > 0 {
				group = "hybrid_go_transform"
			}
		}
		turnGroups[u.Provider+":"+u.Model+":"+group] = append(turnGroups[u.Provider+":"+u.Model+":"+group], u)
		joined = append(joined, map[string]any{"session_id": u.SessionID, "provider_turn_id": u.TurnID, "provider": u.Provider, "model": u.Model, "group": group, "manual_go_edit_calls": manual, "usage": u})
	}
	turnStats := map[string]any{}
	for group, rows := range turnGroups {
		var input, output, reasoning, cacheRead, cacheWrite []float64
		for _, u := range rows {
			input = append(input, float64(u.Input))
			output = append(output, float64(u.Output))
			reasoning = append(reasoning, float64(u.Reasoning))
			cacheRead = append(cacheRead, float64(u.CacheRead))
			cacheWrite = append(cacheWrite, float64(u.CacheWrite))
		}
		turnStats[group] = map[string]any{"turns": len(rows), "stored_input_tokens": distribution(input), "stored_output_tokens": distribution(output), "stored_reasoning_tokens": distribution(reasoning), "stored_cache_read_tokens": distribution(cacheRead), "stored_cache_write_tokens": distribution(cacheWrite)}
	}
	durationStats := map[string]any{}
	for key, values := range durations {
		durationStats[key] = distribution(values)
	}
	payloadStats := map[string]any{}
	for key, values := range payloads {
		payloadStats[key] = distribution(values)
	}
	if err := writeJSONL(filepath.Join(o.out, "sample-results.ndjson"), baselineResults); err != nil {
		return err
	}
	if err := writeJSONL(filepath.Join(o.out, "turn-usage.ndjson"), joined); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(o.out, "metrics.json"), map[string]any{"counts": counts, "timing": durationStats, "argument_size": payloadStats, "selected_turn_usage": turnStats, "warning": "Turn groups are observational and selected by evidence retrieval, not a matched cost experiment. Argument bytes are not billed tokens."}); err != nil {
		return err
	}
	fmt.Printf("metrics: %d observations, %d sampled edits, %d joined turns\n", len(observations), len(samples), len(joined))
	return nil
}

func distribution(values []float64) map[string]any {
	sort.Float64s(values)
	if len(values) == 0 {
		return map[string]any{"n": 0}
	}
	var sum float64
	for _, v := range values {
		sum += v
	}
	median := (values[(len(values)-1)/2] + values[len(values)/2]) / 2
	return map[string]any{"n": len(values), "median": median, "p90": values[int(math.Ceil(float64(len(values))*0.9))-1], "min": values[0], "max": values[len(values)-1], "sum": sum}
}

func excerpt(text string, n int) string {
	text = strings.TrimSpace(text)
	if len(text) > n {
		return text[:n] + " [excerpt truncated]"
	}
	return text
}
