package cluster

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestWriteBroadcastPreservesRequest(t *testing.T) {
	payload := `{"rules":["alpha.example","beta.example"]}`
	var remoteCalls atomic.Int32
	memberLocal := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteCalls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/control/filtering/set_rules" ||
			r.URL.RawQuery != "confirm=yes" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("incorrect remote request: %s %s?%s", r.Method, r.URL.Path, r.URL.RawQuery)
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || string(body) != payload {
			t.Errorf("remote body=%q err=%v", body, err)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	masterLocal := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Cluster-Test", "local")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"result":"local"}`))
	})
	master, handler := newConnectedTestClusters(t, masterLocal, memberLocal)
	req := httptest.NewRequest(
		http.MethodPost,
		"/control/filtering/set_rules?confirm=yes",
		strings.NewReader(payload),
	)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusAccepted || rr.Body.String() != `{"result":"local"}` ||
		rr.Header().Get("X-Cluster-Test") != "local" {
		t.Fatalf("local response changed: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if got := master.nodeSnapshot("member").PendingSync; got != 1 {
		t.Fatalf("pending_sync=%d", got)
	}
	master.processDueOperations(context.Background(), time.Now())
	if remoteCalls.Load() != 1 {
		t.Fatalf("remote calls=%d", remoteCalls.Load())
	}
	if got := master.nodeSnapshot("member").PendingSync; got != 0 {
		t.Fatalf("pending after success=%d", got)
	}
}

func TestFailedLocalWriteDoesNotBroadcast(t *testing.T) {
	var remoteCalls atomic.Int32
	memberLocal := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		remoteCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	masterLocal := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write([]byte("invalid rule"))
	})
	master, handler := newConnectedTestClusters(t, masterLocal, memberLocal)
	req := httptest.NewRequest(http.MethodPost, "/control/filtering/set_rules", strings.NewReader(`{"rules":[]}`))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnprocessableEntity || rr.Body.String() != "invalid rule" {
		t.Fatalf("local failure was not preserved: %d %s", rr.Code, rr.Body.String())
	}
	master.processDueOperations(context.Background(), time.Now())
	if remoteCalls.Load() != 0 || master.nodeSnapshot("member").PendingSync != 0 {
		t.Fatalf("failed local write was broadcast: calls=%d", remoteCalls.Load())
	}
}

func TestWriteRetryAndSuccess(t *testing.T) {
	var remoteCalls atomic.Int32
	memberLocal := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if remoteCalls.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})
	master, handler := newConnectedTestClusters(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), memberLocal)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/control/dns_config", strings.NewReader(`{"upstream_dns":["1.1.1.1"]}`)))
	if rr.Code != http.StatusOK {
		t.Fatalf("local status=%d", rr.Code)
	}

	master.processDueOperations(context.Background(), time.Now())
	if remoteCalls.Load() != 1 || len(master.syncQueue) != 1 || master.syncQueue[0].Attempts != 1 {
		t.Fatalf("first retry state: calls=%d queue=%+v", remoteCalls.Load(), master.syncQueue)
	}
	master.processDueOperations(context.Background(), time.Now())
	if remoteCalls.Load() != 1 {
		t.Fatal("retry ran before next_attempt")
	}
	master.mu.Lock()
	master.syncQueue[0].NextAttempt = time.Now().Add(-time.Second)
	master.mu.Unlock()
	master.processDueOperations(context.Background(), time.Now())
	if remoteCalls.Load() != 2 || len(master.syncQueue) != 0 || master.nodeSnapshot("member").PendingSync != 0 {
		t.Fatalf("retry did not succeed: calls=%d queue=%+v", remoteCalls.Load(), master.syncQueue)
	}
}

func TestWriteRetryExhaustionAndAuditRedaction(t *testing.T) {
	var remoteCalls atomic.Int32
	memberLocal := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		remoteCalls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	})
	master, handler := newConnectedTestClusters(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}), memberLocal)
	secretPayload := `{"rules":["do-not-put-in-audit"]}`
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/control/filtering/set_rules", strings.NewReader(secretPayload)))
	for i := 0; i < 10; i++ {
		master.mu.Lock()
		master.syncQueue[0].NextAttempt = time.Now().Add(-time.Second)
		master.mu.Unlock()
		master.processDueOperations(context.Background(), time.Now())
	}
	if remoteCalls.Load() != 10 || len(master.syncQueue) != 1 || master.syncQueue[0].Attempts != 10 ||
		!master.syncQueue[0].NextAttempt.IsZero() || master.nodeSnapshot("member").PendingSync != 1 {
		t.Fatalf("exhaustion state: calls=%d queue=%+v", remoteCalls.Load(), master.syncQueue)
	}
	master.processDueOperations(context.Background(), time.Now().Add(time.Hour))
	if remoteCalls.Load() != 10 {
		t.Fatal("exhausted operation ran again")
	}

	audit, err := json.Marshal(master.auditSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"master-secret", "member-secret", "do-not-put-in-audit", "rules"} {
		if strings.Contains(string(audit), secret) {
			t.Fatalf("audit leaked %q: %s", secret, audit)
		}
	}
	if !strings.Contains(string(audit), "retry_exhausted") {
		t.Fatalf("missing exhaustion audit: %s", audit)
	}

	rr = httptest.NewRecorder()
	master.ManagementAPI().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/control/cluster/audit", nil))
	if rr.Code != http.StatusOK || strings.Contains(rr.Body.String(), "do-not-put-in-audit") {
		t.Fatalf("audit API: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

func TestSelectedRemoteWriteDoesNotBroadcast(t *testing.T) {
	var localCalls atomic.Int32
	var remoteCalls atomic.Int32
	masterLocal := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		localCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	memberLocal := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		remoteCalls.Add(1)
		w.WriteHeader(http.StatusOK)
	})
	master, handler := newConnectedTestClusters(t, masterLocal, memberLocal)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, httptest.NewRequest(
		http.MethodPost,
		"/control/filtering/set_rules?agh_node=member",
		strings.NewReader(`{"rules":[]}`),
	))
	master.processDueOperations(context.Background(), time.Now())
	if rr.Code != http.StatusOK || localCalls.Load() != 0 || remoteCalls.Load() != 1 ||
		master.nodeSnapshot("member").PendingSync != 0 {
		t.Fatalf("selected write: status=%d local=%d remote=%d", rr.Code, localCalls.Load(), remoteCalls.Load())
	}
}
