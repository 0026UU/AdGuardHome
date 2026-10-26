package cluster

import (
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strings"
)

func (r *alignmentRun) alignFiltering() {
	masterData, targetData, ok := r.readPair("/control/filtering/status")
	if !ok {
		return
	}
	master, err := decodeAlignmentObject(masterData)
	if err != nil {
		r.fail("invalid_master_response")
		return
	}
	target, err := decodeAlignmentObject(targetData)
	if err != nil {
		r.fail("invalid_target_response")
		return
	}
	ops := []dispatchRequest{}
	if master["enabled"] != target["enabled"] || master["interval"] != target["interval"] {
		body, _ := json.Marshal(map[string]any{"enabled": master["enabled"], "interval": master["interval"]})
		ops = append(ops, dispatchRequest{Method: http.MethodPost, Path: "/control/filtering/config", Body: body})
	}
	if !reflect.DeepEqual(master["user_rules"], target["user_rules"]) {
		body, _ := json.Marshal(map[string]any{"rules": master["user_rules"]})
		ops = append(ops, dispatchRequest{Method: http.MethodPost, Path: "/control/filtering/set_rules", Body: body})
	}
	ops = append(ops, filterCollectionOps(master, target, "filters", false)...)
	ops = append(ops, filterCollectionOps(master, target, "whitelist_filters", true)...)
	r.executeCollection(ops)
}

func filterCollectionOps(master, target map[string]any, field string, whitelist bool) []dispatchRequest {
	masterList := alignmentObjectList(master[field])
	targetList := alignmentObjectList(target[field])
	targetByURL := map[string]map[string]any{}
	for _, filter := range targetList {
		url, _ := filter["url"].(string)
		targetByURL[url] = filter
	}
	masterURLs := map[string]bool{}
	ops := []dispatchRequest{}
	for _, filter := range masterList {
		url, _ := filter["url"].(string)
		if url == "" {
			continue
		}
		masterURLs[url] = true
		current := targetByURL[url]
		if current != nil && filterEquivalent(filter, current) {
			continue
		}
		if current == nil {
			body, _ := json.Marshal(map[string]any{
				"name": filter["name"], "url": url, "whitelist": whitelist,
			})
			ops = append(ops, dispatchRequest{Method: http.MethodPost, Path: "/control/filtering/add_url", Body: body})
			if enabled, _ := filter["enabled"].(bool); !enabled {
				ops = append(ops, filterSetOperation(filter, url, whitelist))
			}
			continue
		}
		ops = append(ops, filterSetOperation(filter, url, whitelist))
	}
	extraURLs := []string{}
	for url := range targetByURL {
		if !masterURLs[url] {
			extraURLs = append(extraURLs, url)
		}
	}
	sort.Strings(extraURLs)
	for _, url := range extraURLs {
		body, _ := json.Marshal(map[string]any{"url": url, "whitelist": whitelist})
		ops = append(ops, dispatchRequest{Method: http.MethodPost, Path: "/control/filtering/remove_url", Body: body})
	}

	return ops
}

func filterEquivalent(a, b map[string]any) bool {
	for _, field := range []string{"name", "url", "enabled"} {
		if !reflect.DeepEqual(a[field], b[field]) {
			return false
		}
	}

	return true
}

func filterSetOperation(filter map[string]any, originalURL string, whitelist bool) dispatchRequest {
	body, _ := json.Marshal(map[string]any{
		"url": originalURL, "whitelist": whitelist,
		"data": map[string]any{"name": filter["name"], "url": filter["url"], "enabled": filter["enabled"]},
	})

	return dispatchRequest{Method: http.MethodPost, Path: "/control/filtering/set_url", Body: body}
}

func (r *alignmentRun) alignRewrites() {
	masterData, targetData, ok := r.readPair("/control/rewrite/list")
	if !ok {
		return
	}
	var master, target []map[string]any
	if err := json.Unmarshal(masterData, &master); err != nil {
		r.fail("invalid_master_response")
		return
	}
	if err := json.Unmarshal(targetData, &target); err != nil {
		r.fail("invalid_target_response")
		return
	}
	targetByKey := map[string]map[string]any{}
	for _, rewrite := range target {
		targetByKey[rewriteKey(rewrite)] = rewrite
	}
	masterKeys := map[string]bool{}
	ops := []dispatchRequest{}
	for _, rewrite := range master {
		key := rewriteKey(rewrite)
		masterKeys[key] = true
		current := targetByKey[key]
		if current != nil && reflect.DeepEqual(rewrite, current) {
			continue
		}
		if current == nil {
			body, _ := json.Marshal(rewrite)
			ops = append(ops, dispatchRequest{Method: http.MethodPost, Path: "/control/rewrite/add", Body: body})
		} else {
			body, _ := json.Marshal(map[string]any{"target": current, "update": rewrite})
			ops = append(ops, dispatchRequest{Method: http.MethodPut, Path: "/control/rewrite/update", Body: body})
		}
	}
	for key, rewrite := range targetByKey {
		if masterKeys[key] {
			continue
		}
		body, _ := json.Marshal(map[string]any{"domain": rewrite["domain"], "answer": rewrite["answer"]})
		ops = append(ops, dispatchRequest{Method: http.MethodPost, Path: "/control/rewrite/delete", Body: body})
	}
	r.executeCollection(ops)
}

func rewriteKey(rewrite map[string]any) string {
	return fmt.Sprintf("%v\x00%v", rewrite["domain"], rewrite["answer"])
}

func (r *alignmentRun) alignClients() {
	masterData, targetData, ok := r.readPair("/control/clients")
	if !ok {
		return
	}
	master, err := decodeAlignmentObject(masterData)
	if err != nil {
		r.fail("invalid_master_response")
		return
	}
	target, err := decodeAlignmentObject(targetData)
	if err != nil {
		r.fail("invalid_target_response")
		return
	}
	masterClients := clientsByName(alignmentObjectList(master["clients"]))
	targetClients := clientsByName(alignmentObjectList(target["clients"]))
	ops := []dispatchRequest{}
	for _, name := range sortedAlignmentKeys(masterClients) {
		client := masterClients[name]
		current := targetClients[name]
		if current != nil && reflect.DeepEqual(client, current) {
			continue
		}
		if current == nil {
			body, _ := json.Marshal(client)
			ops = append(ops, dispatchRequest{Method: http.MethodPost, Path: "/control/clients/add", Body: body})
		} else {
			body, _ := json.Marshal(map[string]any{"name": name, "data": client})
			ops = append(ops, dispatchRequest{Method: http.MethodPost, Path: "/control/clients/update", Body: body})
		}
	}
	for _, name := range sortedAlignmentKeys(targetClients) {
		if masterClients[name] != nil {
			continue
		}
		body, _ := json.Marshal(map[string]any{"name": name})
		ops = append(ops, dispatchRequest{Method: http.MethodPost, Path: "/control/clients/delete", Body: body})
	}
	r.executeCollection(ops)
}

func clientsByName(clients []map[string]any) map[string]map[string]any {
	result := map[string]map[string]any{}
	for _, client := range clients {
		name, _ := client["name"].(string)
		if name != "" {
			result[name] = client
		}
	}

	return result
}

func (r *alignmentRun) alignDHCP() {
	masterData, targetData, ok := r.readPair("/control/dhcp/status")
	if !ok {
		return
	}
	master, err := decodeAlignmentObject(masterData)
	if err != nil {
		r.fail("invalid_master_response")
		return
	}
	target, err := decodeAlignmentObject(targetData)
	if err != nil {
		r.fail("invalid_target_response")
		return
	}
	ops := []dispatchRequest{}
	if !reflect.DeepEqual(master["v4"], target["v4"]) || !reflect.DeepEqual(master["v6"], target["v6"]) {
		body, _ := json.Marshal(map[string]any{
			"v4": master["v4"], "v6": master["v6"],
			"interface_name": target["interface_name"], "enabled": target["enabled"],
		})
		ops = append(ops, dispatchRequest{Method: http.MethodPost, Path: "/control/dhcp/set_config", Body: body})
	}
	masterLeases := leasesByMAC(alignmentObjectList(master["static_leases"]))
	targetLeases := leasesByMAC(alignmentObjectList(target["static_leases"]))
	for _, key := range sortedAlignmentKeys(masterLeases) {
		lease := masterLeases[key]
		current := targetLeases[key]
		path := "/control/dhcp/add_static_lease"
		if current != nil {
			if reflect.DeepEqual(lease, current) {
				continue
			}
			path = "/control/dhcp/update_static_lease"
		}
		body, _ := json.Marshal(lease)
		ops = append(ops, dispatchRequest{Method: http.MethodPost, Path: path, Body: body})
	}
	for _, key := range sortedAlignmentKeys(targetLeases) {
		if masterLeases[key] != nil {
			continue
		}
		body, _ := json.Marshal(targetLeases[key])
		ops = append(ops, dispatchRequest{Method: http.MethodPost, Path: "/control/dhcp/remove_static_lease", Body: body})
	}
	r.executeCollection(ops)
}

func leasesByMAC(leases []map[string]any) map[string]map[string]any {
	result := map[string]map[string]any{}
	for _, lease := range leases {
		mac, _ := lease["mac"].(string)
		result[strings.ToLower(mac)] = lease
	}

	return result
}

func alignmentObjectList(value any) []map[string]any {
	items, _ := value.([]any)
	result := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if object, ok := item.(map[string]any); ok {
			result = append(result, object)
		}
	}

	return result
}

func sortedAlignmentKeys[V any](objects map[string]V) []string {
	keys := make([]string, 0, len(objects))
	for key := range objects {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	return keys
}
