package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"time"

	clustersync "github.com/AdguardTeam/AdGuardHome/internal/cluster/sync"
)

type alignmentSpec struct {
	name        string
	readPath    string
	writePath   string
	writeMethod string
	ignore      []string
	toggle      bool
}

var objectAlignmentSpecs = []alignmentSpec{{
	name: "stats", readPath: "/control/stats/config", writePath: "/control/stats/config/update", writeMethod: http.MethodPut,
}, {
	name: "querylog", readPath: "/control/querylog/config", writePath: "/control/querylog/config/update", writeMethod: http.MethodPut,
}, {
	name: "dns", readPath: "/control/dns_info", writePath: "/control/dns_config", writeMethod: http.MethodPost,
	ignore: []string{"default_local_ptr_upstreams", "protection_disabled_until"},
}, {
	name: "access", readPath: "/control/access/list", writePath: "/control/access/set", writeMethod: http.MethodPost,
}, {
	name: "rewrite_settings", readPath: "/control/rewrite/settings", writePath: "/control/rewrite/settings/update", writeMethod: http.MethodPut,
}, {
	name: "blocked_services", readPath: "/control/blocked_services/get", writePath: "/control/blocked_services/update", writeMethod: http.MethodPut,
}, {
	name: "safe_search", readPath: "/control/safesearch/status", writePath: "/control/safesearch/settings", writeMethod: http.MethodPut,
}, {
	name: "profile", readPath: "/control/profile", writePath: "/control/profile/update", writeMethod: http.MethodPut,
	ignore: []string{"name"},
}, {
	name: "tls", readPath: "/control/tls/status", writePath: "/control/tls/configure", writeMethod: http.MethodPost,
	ignore: []string{
		"subject", "issuer", "key_type", "not_before", "not_after", "warning_validation", "dns_names",
		"valid_cert", "valid_chain", "valid_key", "valid_pair", "private_key_saved",
	},
}, {
	name: "safe_browsing", readPath: "/control/safebrowsing/status", toggle: true,
}, {
	name: "parental", readPath: "/control/parental/status", toggle: true,
}}

type alignRequest struct {
	Target string `json:"target"`
}

type alignmentRun struct {
	cluster  *Cluster
	ctx      context.Context
	member   Member
	conflict string
	result   *clustersync.AlignmentResult
}

func (c *Cluster) handleAlign(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	req := alignRequest{}
	if err := decodeJSON(w, r, &req); err != nil && !errors.Is(err, io.EOF) {
		writeAPIError(w, http.StatusBadRequest, "invalid_json")
		return
	}

	c.mu.RLock()
	mode := c.conf.Mode
	names := make([]string, 0, len(c.conf.Members))
	for _, member := range c.conf.Members {
		if req.Target == "" || req.Target == member.Name {
			names = append(names, member.Name)
		}
	}
	c.mu.RUnlock()
	if mode != ModeMaster {
		writeAPIError(w, http.StatusForbidden, "master_required")
		return
	}
	if req.Target != "" && len(names) == 0 {
		writeAPIError(w, http.StatusBadRequest, "unknown_node")
		return
	}

	results := make([]clustersync.AlignmentResult, 0, len(names))
	for _, name := range names {
		results = append(results, c.alignNode(r.Context(), name))
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

func (c *Cluster) alignNode(ctx context.Context, name string) (result clustersync.AlignmentResult) {
	result.Target = name
	result.StartedAt = time.Now().UTC()
	defer func() { result.FinishedAt = time.Now().UTC() }()

	c.configMu.Lock()
	defer c.configMu.Unlock()
	member, ok := c.memberConfig(name)
	if !ok {
		result.Failed = 1
		return result
	}
	c.mu.RLock()
	source := c.conf.Name
	conflict := c.conf.Sync.Conflict
	c.mu.RUnlock()
	run := alignmentRun{cluster: c, ctx: ctx, member: member, conflict: conflict, result: &result}
	for _, spec := range objectAlignmentSpecs {
		run.alignObject(spec)
	}
	run.alignFiltering()
	run.alignRewrites()
	run.alignClients()
	run.alignDHCP()

	if result.Failed == 0 && conflict == ConflictMasterWins {
		c.clearAlignedOperations(name)
	}
	message := "completed"
	if result.Failed > 0 {
		message = "partial_failure"
	}
	c.addAudit("align", source, name, "completed", message)

	return result
}

func (r *alignmentRun) readPair(path string) (master, target []byte, ok bool) {
	localResp := r.cluster.localDispatch(r.ctx, r.cluster.deps.Local, dispatchRequest{
		Method: http.MethodGet, Path: path,
	})
	if localResp.Status < 200 || localResp.Status >= 300 {
		r.fail("read_master_failed")
		return nil, nil, false
	}
	targetResp, err := r.cluster.remoteDispatch(r.ctx, r.member, "/control/cluster/read", dispatchRequest{
		Method: http.MethodGet, Path: path,
	}, r.member.Timeout.Duration)
	if err != nil || targetResp.Status < 200 || targetResp.Status >= 300 {
		r.fail("read_target_failed")
		return nil, nil, false
	}

	return localResp.Body, targetResp.Body, true
}

func (r *alignmentRun) alignObject(spec alignmentSpec) {
	masterData, targetData, ok := r.readPair(spec.readPath)
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
	masterPrivateKeySaved, _ := master["private_key_saved"].(bool)
	masterPrivateKey, _ := master["private_key"].(string)
	tlsCredentialsUnavailable := spec.name == "tls" && masterPrivateKeySaved && masterPrivateKey == ""
	master = sanitizedObject(master, spec.ignore)
	target = sanitizedObject(target, spec.ignore)
	if reflect.DeepEqual(master, target) {
		r.result.Succeeded++
		return
	}
	if r.conflict == ConflictReject {
		r.reject()
		return
	}
	if tlsCredentialsUnavailable {
		r.result.Skipped++
		r.cluster.addAudit("align", r.cluster.localName(), r.member.Name, "skipped", "tls_credentials_unavailable")
		return
	}

	path := spec.writePath
	body := []byte("{}")
	if spec.toggle {
		enabled, _ := master["enabled"].(bool)
		path = spec.readPath[:len(spec.readPath)-len("/status")] + "/disable"
		if enabled {
			path = spec.readPath[:len(spec.readPath)-len("/status")] + "/enable"
		}
	} else {
		body, err = json.Marshal(master)
		if err != nil {
			r.fail("encode_failed")
			return
		}
	}
	r.apply(spec.writeMethod, path, body)
}

func (r *alignmentRun) apply(method, path string, body []byte) {
	resp, err := r.cluster.remoteDispatch(r.ctx, r.member, "/control/cluster/apply", dispatchRequest{
		Method: method, Path: path, Body: body, ContentType: "application/json",
	}, r.member.Timeout.Duration)
	if err != nil || resp.Status < 200 || resp.Status >= 300 {
		r.fail("apply_failed")
		return
	}
	r.result.Overwritten++
}

func (r *alignmentRun) executeCollection(ops []dispatchRequest) {
	if len(ops) == 0 {
		r.result.Succeeded++
		return
	}
	if r.conflict == ConflictReject {
		r.reject()
		return
	}
	for _, op := range ops {
		r.apply(op.Method, op.Path, op.Body)
	}
}

func (r *alignmentRun) reject() {
	r.result.Skipped++
	r.cluster.addAudit("align", r.cluster.localName(), r.member.Name, "skipped", "conflict_rejected")
}

func (r *alignmentRun) fail(code string) {
	r.result.Failed++
	r.cluster.addAudit("align", r.cluster.localName(), r.member.Name, "failed", code)
}

func decodeAlignmentObject(data []byte) (map[string]any, error) {
	var object map[string]any
	if err := json.Unmarshal(data, &object); err != nil || object == nil {
		return nil, fmt.Errorf("expected JSON object: %w", err)
	}

	return object, nil
}

func sanitizedObject(object map[string]any, ignore []string) map[string]any {
	clone := make(map[string]any, len(object))
	for key, value := range object {
		clone[key] = value
	}
	for _, key := range ignore {
		delete(clone, key)
	}

	return clone
}

func (c *Cluster) clearAlignedOperations(target string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	kept := c.syncQueue[:0]
	for _, op := range c.syncQueue {
		if op.Target != target {
			kept = append(kept, op)
		}
	}
	c.syncQueue = kept
	if state := c.nodes[target]; state != nil {
		state.status.PendingSync = 0
		state.status.Degraded = !state.status.Online
	}
}

func (c *Cluster) processAlignments(ctx context.Context) {
	c.mu.RLock()
	names := make([]string, 0, len(c.conf.Members))
	for _, member := range c.conf.Members {
		names = append(names, member.Name)
	}
	c.mu.RUnlock()
	for _, name := range names {
		if !c.consumeAlignmentNeeded(name) {
			continue
		}
		result := c.alignNode(ctx, name)
		if result.Failed > 0 {
			c.mu.Lock()
			if state := c.nodes[name]; state != nil {
				state.alignmentNeeded = true
				state.status.Degraded = true
			}
			c.mu.Unlock()
		}
	}
}

func (c *Cluster) scheduleAlignment(name string) {
	c.mu.Lock()
	if state := c.nodes[name]; state != nil {
		state.alignmentNeeded = true
	}
	c.mu.Unlock()
	c.wakeSyncWorker()
}
