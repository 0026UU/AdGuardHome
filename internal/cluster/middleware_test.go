package cluster

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newConnectedTestClusters(
	t *testing.T,
	masterLocal http.Handler,
	memberLocal http.Handler,
) (master *Cluster, handler http.Handler) {
	t.Helper()
	memberConf := validTestConfig()
	memberConf.Mode = ModeMember
	memberConf.Name = "member"
	memberConf.Token = "member-secret"
	memberConf.Members = nil
	member, err := New(memberConf, Dependencies{Local: memberLocal, WorkDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	memberServer := httptest.NewServer(member.NodeAPI())
	t.Cleanup(memberServer.Close)

	masterConf := validTestConfig()
	masterConf.Members[0].APIBase = memberServer.URL
	master, err = New(masterConf, Dependencies{Local: masterLocal, WorkDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	return master, master.Middleware(masterLocal)
}

func statsHandler(t *testing.T, queries uint64, interval string, expectedRecent string) http.Handler {
	t.Helper()

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/control/stats/config":
			_, _ = w.Write([]byte(`{"enabled":1,"interval":` + interval + `}`))
		case "/control/stats":
			if got := r.URL.Query().Get("recent"); got != expectedRecent {
				t.Errorf("stats recent=%q, want %q", got, expectedRecent)
			}
			_, _ = w.Write([]byte(`{"time_units":"hours","num_dns_queries":` +
				string(rune('0'+queries)) + `,"avg_processing_time":0.1,"dns_queries":[1]}`))
		default:
			http.NotFound(w, r)
		}
	})
}

func TestMiddlewareAggregatesStatsWithCommonWindow(t *testing.T) {
	masterLocal := statsHandler(t, 1, "604800000", "86400000")
	memberLocal := statsHandler(t, 2, "86400000", "86400000")
	_, handler := newConnectedTestClusters(t, masterLocal, memberLocal)
	req := httptest.NewRequest(http.MethodGet, "/control/stats?recent=604800000", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	var body struct {
		Queries    uint64   `json:"num_dns_queries"`
		NumNodesOK int      `json:"num_nodes_ok"`
		Degraded   []string `json:"degraded_nodes"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Queries != 3 || body.NumNodesOK != 2 || len(body.Degraded) != 0 {
		t.Fatalf("unexpected stats: %+v", body)
	}
}

func TestMiddlewareStatsDegradesAndAllFail(t *testing.T) {
	masterLocal := statsHandler(t, 1, "86400000", "86400000")
	memberFailure := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	_, handler := newConnectedTestClusters(t, masterLocal, memberFailure)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/control/stats", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"degraded_nodes":["member"]`) {
		t.Fatalf("degraded response: status=%d body=%s", rr.Code, rr.Body.String())
	}

	_, handler = newConnectedTestClusters(t, memberFailure, memberFailure)
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/control/stats", nil))
	if rr.Code != http.StatusBadGateway || !strings.Contains(rr.Body.String(), "all_nodes_failed") {
		t.Fatalf("all-failed response: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestMiddlewareSelectsOnlyRemoteNode(t *testing.T) {
	masterLocal := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("local handler called for selected remote node: %s", r.URL.Path)
		http.NotFound(w, r)
	})
	memberLocal := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("agh_node") != "" || r.URL.Query().Get("limit") != "1" {
			t.Errorf("query was not preserved and stripped: %s", r.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"time":"2026-10-26T10:00:00Z"}],"oldest":""}`))
	})
	master, handler := newConnectedTestClusters(t, masterLocal, memberLocal)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/control/querylog?limit=1&agh_node=member", nil))
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"agh_node":"member"`) {
		t.Fatalf("remote response: status=%d body=%s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/control/stats?agh_node=unknown", nil))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("unknown node status=%d", rr.Code)
	}

	master.mu.Lock()
	master.nodes["member"].status.Online = false
	master.mu.Unlock()
	rr = httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/control/querylog?agh_node=member", nil))
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("offline node status=%d", rr.Code)
	}
}

func TestMiddlewareClientSearchPreservesBody(t *testing.T) {
	local := func(node string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decoding forwarded body: %v", err)
			}
			if _, ok := body["clients"]; !ok {
				t.Errorf("missing clients in body: %v", body)
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`[{"10.0.0.1":{"name":"` + node + `"}}]`))
		})
	}
	_, handler := newConnectedTestClusters(t, local("master"), local("member"))
	req := httptest.NewRequest(http.MethodPost, "/control/clients/search", strings.NewReader(`{"clients":[{"id":"10.0.0.1"}]}`))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"node_list":["master","member"]`) {
		t.Fatalf("search response: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestMiddlewareSelectedNodeEmptySuccess(t *testing.T) {
	masterLocal := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("local handler called for selected remote node: %s", r.URL.Path)
		http.NotFound(w, r)
	})
	memberLocal := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		// Several AGH write handlers return success without writing a response.
	})
	_, handler := newConnectedTestClusters(t, masterLocal, memberLocal)

	req := httptest.NewRequest(
		http.MethodPut,
		"/control/stats/config/update?agh_node=member",
		strings.NewReader(`{}`),
	)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("empty successful response: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestMiddlewareExposesManagementUI(t *testing.T) {
	c := newTestCluster(t, nil)
	handler := c.Middleware(http.NotFoundHandler())
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/control/cluster/ui/status", nil))
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "master-secret") {
		t.Fatalf("management response: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestNodeAPIRejectsNonAllowlistedInternalPath(t *testing.T) {
	c := newTestCluster(t, nil)
	req := httptest.NewRequest(http.MethodPost, "/control/cluster/read", strings.NewReader(
		`{"method":"GET","path":"/control/install/configure","query":""}`,
	))
	req.Header.Set("Authorization", "Bearer master-secret")
	rr := httptest.NewRecorder()
	c.NodeAPI().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
}
