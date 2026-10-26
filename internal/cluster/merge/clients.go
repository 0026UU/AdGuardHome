package merge

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
)

type clientList struct {
	Clients     []map[string]any `json:"clients"`
	AutoClients []map[string]any `json:"auto_clients"`
	Tags        []string         `json:"supported_tags"`
}

// Clients merges persistent and runtime clients while preserving local fields.
func Clients(sources []NodeData, degraded []string) ([]byte, error) {
	if len(sources) == 0 {
		return nil, errors.New("clients requires at least one response")
	}

	clients := map[string]map[string]any{}
	autoClients := map[string]map[string]any{}
	tags := map[string]struct{}{}
	for _, source := range sources {
		var list clientList
		if err := json.Unmarshal(source.Data, &list); err != nil {
			return nil, fmt.Errorf("decoding %s clients: %w", source.Name, err)
		}
		for _, client := range list.Clients {
			name, _ := client["name"].(string)
			mergeClient(clients, name, client, source.Name)
		}
		for _, client := range list.AutoClients {
			key, _ := client["ip"].(string)
			if key == "" {
				key, _ = client["name"].(string)
			}
			mergeClient(autoClients, key, client, source.Name)
		}
		for _, tag := range list.Tags {
			tags[tag] = struct{}{}
		}
	}

	clientNames := sortedKeys(clients)
	clientResult := make([]map[string]any, 0, len(clientNames))
	for _, name := range clientNames {
		clientResult = append(clientResult, clients[name])
	}
	autoNames := sortedKeys(autoClients)
	autoResult := make([]map[string]any, 0, len(autoNames))
	for _, name := range autoNames {
		autoResult = append(autoResult, autoClients[name])
	}
	tagResult := make([]string, 0, len(tags))
	for tag := range tags {
		tagResult = append(tagResult, tag)
	}
	sort.Strings(tagResult)
	numOK, degradedNodes := metadata(len(sources), degraded)

	return json.Marshal(map[string]any{
		"clients":        clientResult,
		"auto_clients":   autoResult,
		"supported_tags": tagResult,
		"num_nodes_ok":   numOK,
		"degraded_nodes": degradedNodes,
	})
}

func mergeClient(target map[string]map[string]any, key string, client map[string]any, node string) {
	existing := target[key]
	if existing == nil {
		existing = client
		target[key] = existing
	} else {
		for _, field := range []string{"ids", "tags", "upstreams"} {
			existing[field] = unionStrings(stringsValue(existing[field]), stringsValue(client[field]))
		}
	}
	existing["node_list"] = nodeList(existing, node)
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	return keys
}

// ClientSearch merges current array and legacy {found: []} search responses.
func ClientSearch(sources []NodeData, degraded []string) ([]byte, error) {
	if len(sources) == 0 {
		return nil, errors.New("client search requires at least one response")
	}

	merged := map[string]map[string]any{}
	for _, source := range sources {
		var items []map[string]any
		if err := json.Unmarshal(source.Data, &items); err != nil {
			var legacy struct {
				Found []map[string]any `json:"found"`
			}
			if legacyErr := json.Unmarshal(source.Data, &legacy); legacyErr != nil {
				return nil, fmt.Errorf("decoding %s client search: %w", source.Name, err)
			}
			items = legacy.Found
		}
		for _, item := range items {
			for key, value := range item {
				client, ok := value.(map[string]any)
				if !ok {
					continue
				}
				mergeClient(merged, key, client, source.Name)
			}
		}
	}

	keys := sortedKeys(merged)
	result := make([]map[string]any, 0, len(keys))
	for _, key := range keys {
		result = append(result, map[string]any{key: merged[key]})
	}

	return json.Marshal(result)
}
