package cluster

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestCluster(t *testing.T, conf *Config) *Cluster {
	t.Helper()
	if conf == nil {
		conf = validTestConfig()
	}
	c, err := New(conf, Dependencies{
		Local:   http.NotFoundHandler(),
		Version: "test-version",
		WorkDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return c
}

func TestNodeAPIRequiresBearerToken(t *testing.T) {
	c := newTestCluster(t, nil)
	tests := []struct {
		name   string
		header string
		code   int
	}{{
		name: "missing",
		code: http.StatusUnauthorized,
	}, {
		name:   "wrong scheme",
		header: "Basic master-secret",
		code:   http.StatusUnauthorized,
	}, {
		name:   "wrong token",
		header: "Bearer wrong",
		code:   http.StatusUnauthorized,
	}, {
		name:   "valid",
		header: "Bearer master-secret",
		code:   http.StatusOK,
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/control/cluster/ping", nil)
			req.Header.Set("Authorization", tc.header)
			rr := httptest.NewRecorder()
			c.NodeAPI().ServeHTTP(rr, req)
			if rr.Code != tc.code {
				t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestNodeAPIPing(t *testing.T) {
	c := newTestCluster(t, nil)
	req := httptest.NewRequest(http.MethodGet, "/control/cluster/ping", nil)
	req.Header.Set("Authorization", "Bearer master-secret")
	rr := httptest.NewRecorder()
	c.NodeAPI().ServeHTTP(rr, req)

	var body map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["pong"] != true || body["version"] != "test-version" || body["name"] != "master" {
		t.Fatalf("unexpected ping: %+v", body)
	}
	if strings.Contains(rr.Body.String(), "master-secret") {
		t.Fatal("ping leaked a token")
	}
}

func TestManagementRegisterAndRedact(t *testing.T) {
	conf := validTestConfig()
	conf.Members = nil
	c := newTestCluster(t, conf)

	req := httptest.NewRequest(http.MethodPost, "/control/cluster/register", strings.NewReader(`{
		"name":"new-member","api_base":"http://127.0.0.1:9092",
		"token":"new-secret","timeout":"5s","timezone":"UTC"
	}`))
	rr := httptest.NewRecorder()
	c.ManagementAPI().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("register code=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	c.ManagementAPI().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/control/cluster/nodes", nil))
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "new-secret") {
		t.Fatalf("nodes response leaked token: code=%d body=%s", rr.Code, rr.Body.String())
	}

	loaded, err := LoadConfig(c.deps.WorkDir)
	if err != nil || len(loaded.Members) != 1 || loaded.Members[0].Token != "new-secret" {
		t.Fatalf("registration was not persisted: conf=%+v err=%v", loaded, err)
	}
}

func TestManagementSettingsPersist(t *testing.T) {
	c := newTestCluster(t, nil)
	req := httptest.NewRequest(http.MethodPut, "/control/cluster/settings", strings.NewReader(`{
		"sync":{"enabled":true,"interval":"45s","on_change":"broadcast","conflict":"reject"},
		"stats":{"window":"align_to_master"},
		"degradation":{"skip_offline":false}
	}`))
	rr := httptest.NewRecorder()
	c.ManagementAPI().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("settings code=%d body=%s", rr.Code, rr.Body.String())
	}
	if c.conf.Sync.Conflict != ConflictReject || c.conf.Sync.Interval.Duration != 45e9 ||
		c.conf.Stats.Window != StatsWindowMaster || c.conf.Degradation.SkipOffline {
		t.Fatalf("settings were not applied: %+v", c.conf)
	}

	loaded, err := LoadConfig(c.deps.WorkDir)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Sync.Conflict != ConflictReject || loaded.Stats.Window != StatsWindowMaster {
		t.Fatalf("settings were not persisted: %+v", loaded)
	}
}

func TestManagementUnregister(t *testing.T) {
	c := newTestCluster(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/control/cluster/unregister", strings.NewReader(`{"name":"member"}`))
	rr := httptest.NewRecorder()
	c.ManagementAPI().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("unregister code=%d body=%s", rr.Code, rr.Body.String())
	}
	if len(c.conf.Members) != 0 {
		t.Fatalf("members=%+v", c.conf.Members)
	}

	loaded, err := LoadConfig(c.deps.WorkDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Members) != 0 {
		t.Fatalf("unregistration was not persisted: %+v", loaded.Members)
	}
}
