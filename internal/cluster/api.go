package cluster

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

const maxManagementBodyBytes = 1 << 20

// NodeAPI returns the private node-to-node API.  Every endpoint requires the
// listening node's independent bearer token.
func (c *Cluster) NodeAPI() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/control/cluster/ping", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		c.mu.RLock()
		name := c.conf.Name
		c.mu.RUnlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"pong":    true,
			"name":    name,
			"version": c.deps.Version,
		})
	})
	mux.HandleFunc("/control/cluster/read", c.handleInternalRead)
	mux.HandleFunc("/control/cluster/apply", c.handleInternalApply)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !c.validBearerToken(r.Header.Get("Authorization")) {
			writeAPIError(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		mux.ServeHTTP(w, r)
	})
}

// ManagementAPI returns browser-facing cluster management endpoints.  The host
// web server must wrap this handler in the normal AGH session authentication.
func (c *Cluster) ManagementAPI() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/control/cluster/status", func(w http.ResponseWriter, r *http.Request) {
		if requireMethod(w, r, http.MethodGet) {
			writeJSON(w, http.StatusOK, c.Status())
		}
	})
	mux.HandleFunc("/control/cluster/nodes", func(w http.ResponseWriter, r *http.Request) {
		if requireMethod(w, r, http.MethodGet) {
			writeJSON(w, http.StatusOK, map[string]any{"nodes": c.Status().Nodes})
		}
	})
	mux.HandleFunc("/control/cluster/register", c.handleRegister)
	mux.HandleFunc("/control/cluster/unregister", c.handleUnregister)
	mux.HandleFunc("/control/cluster/settings", c.handleSettings)
	mux.HandleFunc("/control/cluster/align", c.handleAlign)
	mux.HandleFunc("/control/cluster/audit", func(w http.ResponseWriter, r *http.Request) {
		if requireMethod(w, r, http.MethodGet) {
			writeJSON(w, http.StatusOK, map[string]any{"entries": c.auditSnapshot()})
		}
	})

	return mux
}

func (c *Cluster) validBearerToken(header string) bool {
	token, ok := strings.CutPrefix(header, "Bearer ")
	if !ok || token == "" {
		return false
	}
	c.mu.RLock()
	expected := c.conf.Token
	c.mu.RUnlock()

	return subtle.ConstantTimeCompare([]byte(token), []byte(expected)) == 1
}

type registerRequest struct {
	Name     string   `json:"name"`
	APIBase  string   `json:"api_base"`
	Token    string   `json:"token"`
	Timeout  Duration `json:"timeout"`
	Timezone string   `json:"timezone"`
}

type unregisterRequest struct {
	Name string `json:"name"`
}

type settingsRequest struct {
	Sync        SyncConfig        `json:"sync"`
	Stats       StatsConfig       `json:"stats"`
	Degradation DegradationConfig `json:"degradation"`
}

type apiError struct {
	status int
	code   string
}

func (e *apiError) Error() string { return e.code }

func (c *Cluster) handleRegister(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req registerRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_json")
		return
	}

	err := c.updateConfig(func(conf *Config) error {
		if !conf.Enabled {
			return &apiError{http.StatusConflict, "cluster_disabled"}
		}
		if conf.Mode != ModeMaster {
			return &apiError{http.StatusForbidden, "master_required"}
		}
		for _, member := range conf.Members {
			if member.Name == req.Name || req.Name == conf.Name {
				return &apiError{http.StatusConflict, "node_exists"}
			}
		}
		conf.Members = append(conf.Members, Member{
			Name:     req.Name,
			APIBase:  req.APIBase,
			Token:    req.Token,
			Timeout:  req.Timeout,
			Timezone: req.Timezone,
		})

		return nil
	})
	if err != nil {
		c.writeUpdateError(w, err)
		return
	}
	c.checkNode(r.Context(), req.Name)
	c.scheduleAlignment(req.Name)
	c.addAudit("register", c.localName(), req.Name, "completed", "registered")
	writeJSON(w, http.StatusOK, c.Status())
}

func (c *Cluster) handleUnregister(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPost) {
		return
	}
	var req unregisterRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_json")
		return
	}

	err := c.updateConfig(func(conf *Config) error {
		if conf.Mode != ModeMaster {
			return &apiError{http.StatusForbidden, "master_required"}
		}
		for i, member := range conf.Members {
			if member.Name == req.Name {
				conf.Members = append(conf.Members[:i], conf.Members[i+1:]...)
				return nil
			}
		}

		return &apiError{http.StatusNotFound, "node_not_found"}
	})
	if err != nil {
		c.writeUpdateError(w, err)
		return
	}
	c.addAudit("unregister", c.localName(), req.Name, "completed", "unregistered")
	writeJSON(w, http.StatusOK, c.Status())
}

func (c *Cluster) handleSettings(w http.ResponseWriter, r *http.Request) {
	if !requireMethod(w, r, http.MethodPut) {
		return
	}
	var req settingsRequest
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_json")
		return
	}

	err := c.updateConfig(func(conf *Config) error {
		if !conf.Enabled {
			return &apiError{http.StatusConflict, "cluster_disabled"}
		}
		if conf.Mode != ModeMaster {
			return &apiError{http.StatusForbidden, "master_required"}
		}
		conf.Sync = req.Sync
		conf.Stats = req.Stats
		conf.Degradation = req.Degradation

		return nil
	})
	if err != nil {
		c.writeUpdateError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, c.Status())
}

func (c *Cluster) updateConfig(mutate func(*Config) error) error {
	c.configMu.Lock()
	defer c.configMu.Unlock()

	c.mu.RLock()
	next := cloneConfig(c.conf)
	c.mu.RUnlock()
	if err := mutate(next); err != nil {
		return err
	}
	applyDefaults(next)
	if err := next.Validate(); err != nil {
		return &apiError{http.StatusBadRequest, "invalid_settings"}
	}
	if err := SaveConfig(c.deps.WorkDir, next); err != nil {
		return fmt.Errorf("saving cluster settings: %w", err)
	}

	c.mu.Lock()
	c.conf = next
	c.reconcileNodesLocked()
	c.mu.Unlock()

	return nil
}

func (c *Cluster) writeUpdateError(w http.ResponseWriter, err error) {
	var apiErr *apiError
	if errors.As(err, &apiErr) {
		writeAPIError(w, apiErr.status, apiErr.code)
		return
	}
	c.deps.Logger.Error("could not persist cluster settings", slog.Any("error", err))
	writeAPIError(w, http.StatusInternalServerError, "persistence_failed")
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method == method {
		return true
	}
	w.Header().Set("Allow", method)
	writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed")

	return false
}

func decodeJSON(w http.ResponseWriter, r *http.Request, dest any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxManagementBodyBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("request body must contain one JSON value")
	}

	return nil
}

func writeAPIError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, map[string]string{"error": code})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
