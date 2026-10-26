package merge

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

type queryEntry struct {
	data    map[string]any
	time    time.Time
	rawTime string
}

// QueryLog merges newest-first log pages, annotates sources, and applies limit.
func QueryLog(sources []NodeData, limit int) ([]byte, error) {
	return QueryLogPage(sources, 0, limit)
}

// QueryLogPage applies offset after globally sorting all node responses.
func QueryLogPage(sources []NodeData, offset, limit int) ([]byte, error) {
	if len(sources) == 0 {
		return nil, errors.New("query log requires at least one response")
	}

	entries := []queryEntry{}
	for _, source := range sources {
		var response struct {
			Data []map[string]any `json:"data"`
		}
		if err := json.Unmarshal(source.Data, &response); err != nil {
			return nil, fmt.Errorf("decoding %s query log: %w", source.Name, err)
		}
		for _, entry := range response.Data {
			rawTime, _ := entry["time"].(string)
			parsed, err := time.Parse(time.RFC3339Nano, rawTime)
			if err != nil {
				return nil, fmt.Errorf("decoding %s query log time: %w", source.Name, err)
			}
			entry["agh_node"] = source.Name
			entries = append(entries, queryEntry{data: entry, time: parsed, rawTime: rawTime})
		}
	}

	sort.SliceStable(entries, func(i, j int) bool { return entries[i].time.After(entries[j].time) })
	if offset > 0 {
		if offset >= len(entries) {
			entries = []queryEntry{}
		} else {
			entries = entries[offset:]
		}
	}
	if limit == 0 {
		entries = []queryEntry{}
	} else if limit > 0 && len(entries) > limit {
		entries = entries[:limit]
	}
	data := make([]map[string]any, 0, len(entries))
	for _, entry := range entries {
		data = append(data, entry.data)
	}
	oldest := ""
	if len(entries) > 0 {
		oldest = entries[len(entries)-1].rawTime
	}

	return json.Marshal(map[string]any{"data": data, "oldest": oldest})
}
