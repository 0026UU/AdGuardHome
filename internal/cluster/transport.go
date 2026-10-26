package cluster

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	maxInternalBodyBytes = 32 << 20
	maxRequestBodyBytes  = 16 << 20
	readTimeout          = 3 * time.Second
)

type dispatchRequest struct {
	Method      string `json:"method"`
	Path        string `json:"path"`
	Query       string `json:"query"`
	Body        []byte `json:"body,omitempty"`
	ContentType string `json:"content_type,omitempty"`
}

type dispatchResponse struct {
	Status      int    `json:"status"`
	Body        []byte `json:"body"`
	ContentType string `json:"content_type,omitempty"`
}

var internalReadMethods = map[string]string{
	"/control/stats":          http.MethodGet,
	"/control/stats/config":   http.MethodGet,
	"/control/querylog":       http.MethodGet,
	"/control/clients":        http.MethodGet,
	"/control/clients/search": http.MethodPost,
	"/control/clients/find":   http.MethodGet,
}

var internalWriteMethods = map[string]string{}

func (c *Cluster) handleInternalRead(w http.ResponseWriter, r *http.Request) {
	c.handleInternalDispatch(w, r, false)
}

func (c *Cluster) handleInternalApply(w http.ResponseWriter, r *http.Request) {
	c.handleInternalDispatch(w, r, true)
}

func (c *Cluster) handleInternalDispatch(w http.ResponseWriter, r *http.Request, apply bool) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxInternalBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	var req dispatchRequest
	if err := decoder.Decode(&req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_json")
		return
	}
	if err := validateDispatch(req, apply); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_internal_request")
		return
	}

	resp := c.localDispatch(r.Context(), c.deps.Local, req)
	writeJSON(w, http.StatusOK, resp)
}

func validateDispatch(req dispatchRequest, apply bool) error {
	if req.Path == "" || !strings.HasPrefix(req.Path, "/control/") || strings.Contains(req.Path, "..") {
		return errors.New("invalid path")
	}
	target := req.Path
	if req.Query != "" {
		target += "?" + req.Query
	}
	u, err := url.ParseRequestURI(target)
	if err != nil || u.IsAbs() || u.Path != req.Path || u.RawQuery != req.Query {
		return errors.New("invalid request URI")
	}
	allowed := internalReadMethods
	if apply {
		allowed = internalWriteMethods
	}
	method, ok := allowed[req.Path]
	if !ok || method != req.Method {
		return errors.New("path and method are not allowlisted")
	}

	return nil
}

type captureResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newCaptureResponse() *captureResponse {
	return &captureResponse{header: http.Header{}}
}

func (w *captureResponse) Header() http.Header { return w.header }

func (w *captureResponse) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}

func (w *captureResponse) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.body.Write(data)
}

func (c *Cluster) localDispatch(
	ctx context.Context,
	handler http.Handler,
	req dispatchRequest,
) (resp dispatchResponse) {
	target := req.Path
	if req.Query != "" {
		target += "?" + req.Query
	}
	httpReq, err := http.NewRequestWithContext(ctx, req.Method, target, bytes.NewReader(req.Body))
	if err != nil {
		return dispatchResponse{Status: http.StatusBadRequest, Body: []byte(`{"error":"invalid_internal_request"}`)}
	}
	if req.ContentType != "" {
		httpReq.Header.Set("Content-Type", req.ContentType)
	}
	capture := newCaptureResponse()
	handler.ServeHTTP(capture, httpReq)

	return dispatchResponse{
		Status:      capture.status,
		Body:        capture.body.Bytes(),
		ContentType: capture.Header().Get("Content-Type"),
	}
}

func (c *Cluster) remoteDispatch(
	ctx context.Context,
	member Member,
	endpoint string,
	req dispatchRequest,
	timeout time.Duration,
) (resp dispatchResponse, err error) {
	body, err := json.Marshal(req)
	if err != nil {
		return resp, fmt.Errorf("encoding node request: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		strings.TrimRight(member.APIBase, "/")+endpoint,
		bytes.NewReader(body),
	)
	if err != nil {
		return resp, fmt.Errorf("creating node request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+member.Token)
	httpReq.Header.Set("Content-Type", "application/json")
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	httpResp, err := client.Do(httpReq)
	if err != nil {
		return resp, fmt.Errorf("calling node: %w", err)
	}
	defer func() {
		_ = httpResp.Body.Close()
	}()
	if httpResp.StatusCode != http.StatusOK {
		return resp, fmt.Errorf("node API status %d", httpResp.StatusCode)
	}
	decoder := json.NewDecoder(io.LimitReader(httpResp.Body, maxInternalBodyBytes))
	if err = decoder.Decode(&resp); err != nil {
		return resp, fmt.Errorf("decoding node response: %w", err)
	}
	if resp.Status < 100 || resp.Status > 599 {
		return resp, errors.New("invalid internal response status")
	}

	return resp, nil
}

func writeDispatchResponse(w http.ResponseWriter, resp dispatchResponse) {
	if resp.ContentType != "" {
		w.Header().Set("Content-Type", resp.ContentType)
	}
	status := resp.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(resp.Body)
}
