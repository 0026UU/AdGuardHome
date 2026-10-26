package cluster

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"sync"

	clustermerge "github.com/AdguardTeam/AdGuardHome/internal/cluster/merge"
)

type readKind int

const (
	readStats readKind = iota
	readStatsConfig
	readQueryLog
	readClients
	readClientSearch
)

func aggregateRoute(method, path string) (readKind, bool) {
	switch {
	case method == http.MethodGet && path == "/control/stats":
		return readStats, true
	case method == http.MethodGet && path == "/control/stats/config":
		return readStatsConfig, true
	case method == http.MethodGet && path == "/control/querylog":
		return readQueryLog, true
	case method == http.MethodGet && path == "/control/clients":
		return readClients, true
	case (method == http.MethodPost && path == "/control/clients/search") ||
		(method == http.MethodGet && path == "/control/clients/find"):
		return readClientSearch, true
	default:
		return 0, false
	}
}

type readTarget struct {
	name   string
	local  bool
	member Member
}

type targetResult struct {
	name string
	body []byte
	err  error
}

func (c *Cluster) readTargets() (targets []readTarget, degraded []string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	targets = append(targets, readTarget{name: c.conf.Name, local: true})
	for _, member := range c.conf.Members {
		state := c.nodes[member.Name]
		online := state != nil && state.status.Online
		if c.conf.Degradation.SkipOffline && !online {
			degraded = append(degraded, member.Name)
			continue
		}
		targets = append(targets, readTarget{name: member.Name, member: member})
	}

	return targets, degraded
}

func (c *Cluster) fetchTarget(
	ctx context.Context,
	local http.Handler,
	target readTarget,
	req dispatchRequest,
) (result targetResult) {
	result.name = target.name
	var resp dispatchResponse
	var err error
	if target.local {
		resp = c.localDispatch(ctx, local, req)
	} else {
		resp, err = c.remoteDispatch(ctx, target.member, "/control/cluster/read", req, readTimeout)
	}
	if err != nil {
		result.err = err
		return result
	}
	if resp.Status < 200 || resp.Status >= 300 {
		result.err = fmt.Errorf("node returned status %d", resp.Status)
		return result
	}
	if !json.Valid(resp.Body) {
		result.err = errors.New("node returned invalid JSON")
		return result
	}
	result.body = append([]byte(nil), resp.Body...)

	return result
}

func (c *Cluster) fetchAll(
	ctx context.Context,
	local http.Handler,
	targets []readTarget,
	req dispatchRequest,
) []targetResult {
	results := make([]targetResult, len(targets))
	var wg sync.WaitGroup
	for i, target := range targets {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = c.fetchTarget(ctx, local, target, req)
		}()
	}
	wg.Wait()

	return results
}

func successfulReads(results []targetResult) (sources []clustermerge.NodeData, degraded []string) {
	for _, result := range results {
		if result.err != nil {
			degraded = append(degraded, result.name)
			continue
		}
		sources = append(sources, clustermerge.NodeData{Name: result.name, Data: result.body})
	}

	return sources, degraded
}

func (c *Cluster) serveAggregate(w http.ResponseWriter, r *http.Request, local http.Handler, body []byte) {
	kind, _ := aggregateRoute(r.Method, r.URL.Path)
	targets, degraded := c.readTargets()
	if len(targets) == 0 {
		writeAPIError(w, http.StatusBadGateway, "all_nodes_failed")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), readTimeout)
	defer cancel()
	query := r.URL.Query()
	query.Del("agh_node")
	queryLimit, queryOffset := 0, 0
	if kind == readQueryLog {
		var err error
		queryLimit, queryOffset, err = queryLogPage(query)
		if err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_querylog_pagination")
			return
		}
		amplified := 5000
		if queryOffset <= 5000 && queryLimit <= 5000 {
			amplified = queryOffset + queryLimit
		}
		if amplified > 0 {
			if amplified <= 5000/len(targets) {
				amplified *= len(targets)
			} else {
				amplified = 5000
			}
		}
		query.Set("offset", "0")
		query.Set("limit", strconv.Itoa(amplified))
	}
	req := dispatchRequest{
		Method:      r.Method,
		Path:        r.URL.Path,
		Query:       query.Encode(),
		Body:        body,
		ContentType: r.Header.Get("Content-Type"),
	}

	if kind == readStats {
		c.serveAggregateStats(ctx, w, local, targets, degraded, query)
		return
	}
	results := c.fetchAll(ctx, local, targets, req)
	sources, failed := successfulReads(results)
	degraded = uniqueNames(append(degraded, failed...))
	if len(sources) == 0 {
		writeAPIError(w, http.StatusBadGateway, "all_nodes_failed")
		return
	}

	var data []byte
	var err error
	switch kind {
	case readStatsConfig:
		data, err = clustermerge.StatsConfig(sources, degraded)
	case readQueryLog:
		data, err = clustermerge.QueryLogPage(sources, queryOffset, queryLimit)
	case readClients:
		data, err = clustermerge.Clients(sources, degraded)
	case readClientSearch:
		data, err = clustermerge.ClientSearch(sources, degraded)
	}
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "invalid_node_response")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (c *Cluster) serveAggregateStats(
	ctx context.Context,
	w http.ResponseWriter,
	local http.Handler,
	targets []readTarget,
	degraded []string,
	query url.Values,
) {
	configReq := dispatchRequest{Method: http.MethodGet, Path: "/control/stats/config"}
	configResults := c.fetchAll(ctx, local, targets, configReq)
	configSources, failed := successfulReads(configResults)
	degraded = uniqueNames(append(degraded, failed...))
	if len(configSources) == 0 {
		writeAPIError(w, http.StatusBadGateway, "all_nodes_failed")
		return
	}

	recent, err := commonRecent(query, configSources)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_recent")
		return
	}
	statsQuery := url.Values{}
	for key, values := range query {
		statsQuery[key] = append([]string(nil), values...)
	}
	if recent > 0 {
		statsQuery.Set("recent", strconv.FormatInt(recent, 10))
	}
	healthyTargets := make([]readTarget, 0, len(configSources))
	for i, result := range configResults {
		if result.err == nil {
			healthyTargets = append(healthyTargets, targets[i])
		}
	}
	results := c.fetchAll(ctx, local, healthyTargets, dispatchRequest{
		Method: http.MethodGet,
		Path:   "/control/stats",
		Query:  statsQuery.Encode(),
	})
	sources, failed := successfulReads(results)
	degraded = uniqueNames(append(degraded, failed...))
	if len(sources) == 0 {
		writeAPIError(w, http.StatusBadGateway, "all_nodes_failed")
		return
	}
	data, err := clustermerge.Stats(sources, degraded)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "invalid_node_response")
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func commonRecent(query url.Values, configs []clustermerge.NodeData) (int64, error) {
	minimum := int64(0)
	for _, source := range configs {
		var config struct {
			Interval float64 `json:"interval"`
		}
		if err := json.Unmarshal(source.Data, &config); err != nil {
			return 0, err
		}
		interval := int64(config.Interval)
		if interval > 0 && (minimum == 0 || interval < minimum) {
			minimum = interval
		}
	}
	requested := int64(0)
	if raw := query.Get("recent"); raw != "" {
		parsed, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || parsed < 3600000 || parsed%3600000 != 0 {
			return 0, errors.New("invalid recent interval")
		}
		requested = parsed
	}
	if requested > 0 && (minimum == 0 || requested < minimum) {
		minimum = requested
	}
	if minimum > 0 && minimum < 3600000 {
		minimum = 3600000
	}

	return minimum, nil
}

func queryLogPage(query url.Values) (limit, offset int, err error) {
	limit = 500
	if raw := query.Get("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 0 {
			return 0, 0, errors.New("invalid limit")
		}
	}
	if raw := query.Get("offset"); raw != "" {
		offset, err = strconv.Atoi(raw)
		if err != nil || offset < 0 {
			return 0, 0, errors.New("invalid offset")
		}
	}

	return limit, offset, nil
}

func uniqueNames(names []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(names))
	for _, name := range names {
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		result = append(result, name)
	}
	sort.Strings(result)

	return result
}

func annotateQueryLogNode(body []byte, name string) []byte {
	data, err := clustermerge.QueryLog([]clustermerge.NodeData{{Name: name, Data: body}}, -1)
	if err != nil {
		return body
	}

	return data
}
