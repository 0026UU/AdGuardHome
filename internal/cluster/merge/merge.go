// Package merge combines compatible AdGuard Home API responses.
package merge

import "encoding/json"

// NodeData is a successful JSON response attributed to one node.
type NodeData struct {
	Name string
	Data []byte
}

func metadata(ok int, degraded []string) (numOK int, nodes []string) {
	nodes = append([]string{}, degraded...)

	return ok, nodes
}

func decodeObject(data []byte) (map[string]any, error) {
	var object map[string]any
	err := json.Unmarshal(data, &object)
	if object == nil && err == nil {
		object = map[string]any{}
	}

	return object, err
}

func stringsValue(value any) []string {
	switch values := value.(type) {
	case []string:
		return append([]string(nil), values...)
	case []any:
		result := make([]string, 0, len(values))
		for _, value := range values {
			if text, ok := value.(string); ok {
				result = append(result, text)
			}
		}
		return result
	default:
		return nil
	}
}

func unionStrings(groups ...[]string) []string {
	seen := map[string]struct{}{}
	result := []string{}
	for _, group := range groups {
		for _, value := range group {
			if _, ok := seen[value]; ok {
				continue
			}
			seen[value] = struct{}{}
			result = append(result, value)
		}
	}

	return result
}

func nodeList(object map[string]any, node string) []string {
	return unionStrings(stringsValue(object["node_list"]), []string{node})
}
