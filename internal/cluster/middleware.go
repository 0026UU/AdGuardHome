package cluster

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func (c *Cluster) wrapMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/control/cluster/ui/") {
			clone := r.Clone(r.Context())
			urlCopy := *r.URL
			clone.URL = &urlCopy
			clone.URL.Path = "/control/cluster/" + strings.TrimPrefix(
				clone.URL.Path,
				"/control/cluster/ui/",
			)
			c.ManagementAPI().ServeHTTP(w, clone)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/control/cluster/") {
			c.ManagementAPI().ServeHTTP(w, r)
			return
		}

		_, aggregate := aggregateRoute(r.Method, r.URL.Path)
		synchronizedWrite := internalWriteMethods[r.URL.Path] == r.Method
		selected := r.URL.Query().Get("agh_node")
		if !aggregate && !synchronizedWrite && (selected == "" || selected == "all") {
			next.ServeHTTP(w, r)
			return
		}

		body, err := readRequestBody(w, r)
		if err != nil {
			writeAPIError(w, http.StatusRequestEntityTooLarge, "request_body_too_large")
			return
		}
		if selected != "" && selected != "all" {
			c.serveSelectedNode(w, r, next, selected, body)
			return
		}
		if synchronizedWrite {
			c.serveSynchronizedWrite(w, r, next, body)
			return
		}
		if aggregate {
			c.serveAggregate(w, r, next, body)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func readRequestBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	r.ContentLength = int64(len(body))

	return body, nil
}

func cleanedRequest(r *http.Request, body []byte) *http.Request {
	clone := r.Clone(r.Context())
	urlCopy := *r.URL
	clone.URL = &urlCopy
	query := clone.URL.Query()
	query.Del("agh_node")
	clone.URL.RawQuery = query.Encode()
	clone.Body = io.NopCloser(bytes.NewReader(body))
	clone.ContentLength = int64(len(body))

	return clone
}

func (c *Cluster) serveSelectedNode(
	w http.ResponseWriter,
	r *http.Request,
	next http.Handler,
	name string,
	body []byte,
) {
	c.mu.RLock()
	localName := c.conf.Name
	var member Member
	found := false
	online := false
	for _, configured := range c.conf.Members {
		if configured.Name == name {
			member = configured
			found = true
			online = c.nodes[name] != nil && c.nodes[name].status.Online
			break
		}
	}
	c.mu.RUnlock()
	if name == localName {
		next.ServeHTTP(w, cleanedRequest(r, body))
		return
	}
	if !found {
		writeAPIError(w, http.StatusBadRequest, "unknown_node")
		return
	}
	if !online {
		writeAPIError(w, http.StatusServiceUnavailable, "node_offline")
		return
	}

	query := r.URL.Query()
	query.Del("agh_node")
	req := dispatchRequest{
		Method:      r.Method,
		Path:        r.URL.Path,
		Query:       query.Encode(),
		Body:        body,
		ContentType: r.Header.Get("Content-Type"),
	}
	apply := false
	if method, ok := internalReadMethods[req.Path]; !ok || method != req.Method {
		apply = true
	}
	if err := validateDispatch(req, apply); err != nil {
		writeAPIError(w, http.StatusBadRequest, "unsupported_node_request")
		return
	}
	endpoint := "/control/cluster/read"
	timeout := readTimeout
	if apply {
		endpoint = "/control/cluster/apply"
		timeout = member.Timeout.Duration
	}
	ctx, cancel := contextWithRequestTimeout(r, timeout)
	defer cancel()
	resp, err := c.remoteDispatch(ctx, member, endpoint, req, timeout)
	if err != nil {
		writeAPIError(w, http.StatusBadGateway, "node_request_failed")
		return
	}
	if req.Path == "/control/querylog" && resp.Status == http.StatusOK {
		resp.Body = annotateQueryLogNode(resp.Body, name)
	}
	writeDispatchResponse(w, resp)
}

func contextWithRequestTimeout(r *http.Request, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), timeout)
}

func encodeQuery(query url.Values) string { return query.Encode() }
