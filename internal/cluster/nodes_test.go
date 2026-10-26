package cluster

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestHealthOfflineAfterThreeFailuresAndRecovery(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer member-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if calls.Add(1) <= 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"pong":true,"version":"v1","name":"member"}`))
	}))
	defer srv.Close()

	conf := validTestConfig()
	conf.Members[0].APIBase = srv.URL
	c := newTestCluster(t, conf)
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		c.checkNode(ctx, "member")
		if !c.nodeSnapshot("member").Online {
			t.Fatalf("node went offline after %d failures", i+1)
		}
	}
	c.checkNode(ctx, "member")
	if c.nodeSnapshot("member").Online {
		t.Fatal("node must be offline after three failures")
	}

	c.checkNode(ctx, "member")
	got := c.nodeSnapshot("member")
	if !got.Online || got.Version != "v1" || got.RTTMillis < 0 {
		t.Fatalf("node did not recover: %+v", got)
	}
	if !c.consumeAlignmentNeeded("member") {
		t.Fatal("recovery did not schedule alignment")
	}
}

func TestStatusIncludesLocalAndRemoteWithoutSecrets(t *testing.T) {
	c := newTestCluster(t, nil)
	status := c.Status()
	if len(status.Nodes) != 2 {
		t.Fatalf("nodes=%d", len(status.Nodes))
	}
	if !status.Nodes[0].Local || status.Nodes[0].Name != "master" {
		t.Fatalf("local node is not first: %+v", status.Nodes)
	}
	if status.Config.Token != "" || status.Config.Members[0].Token != "" {
		t.Fatal("status leaked token")
	}
}

func TestNodeAPIServerLifecycle(t *testing.T) {
	probe := httptest.NewServer(http.NotFoundHandler())
	addr := probe.Listener.Addr().String()
	probe.Close()

	conf := validTestConfig()
	conf.Members = nil
	conf.Listen = addr
	conf.APIBase = "http://" + addr
	c := newTestCluster(t, conf)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := c.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() {
		_ = c.Close(context.Background())
	}()

	req, err := http.NewRequest(http.MethodGet, conf.APIBase+"/control/cluster/ping", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer master-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("ping status=%d", resp.StatusCode)
	}
	if err = c.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
