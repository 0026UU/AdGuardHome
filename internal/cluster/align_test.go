package cluster

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

type capturedWrite struct {
	Path string
	Body string
}

type fakeAlignmentNode struct {
	read map[string]string

	mu     sync.Mutex
	writes []capturedWrite
}

func (n *fakeAlignmentNode) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			body, ok := n.read[r.URL.Path]
			if !ok {
				switch r.URL.Path {
				case "/control/rewrite/list":
					body = `[]`
				case "/control/clients":
					body = `{"clients":[]}`
				case "/control/dhcp/status":
					body = `{"static_leases":[]}`
				default:
					body = `{}`
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
			return
		}
		var body strings.Builder
		_, _ = io.Copy(&body, r.Body)
		n.mu.Lock()
		n.writes = append(n.writes, capturedWrite{Path: r.URL.Path, Body: body.String()})
		n.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	})
}

func (n *fakeAlignmentNode) snapshotWrites() []capturedWrite {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]capturedWrite(nil), n.writes...)
}

func alignmentFixtures() (master, member *fakeAlignmentNode) {
	master = &fakeAlignmentNode{read: map[string]string{
		"/control/filtering/status": `{
			"enabled":true,"interval":24,"user_rules":["||example.com^"],
			"filters":[{"id":9,"name":"master-filter","url":"https://filters.example/list.txt","enabled":true,"rules_count":100}],
			"whitelist_filters":[]
		}`,
		"/control/dns_info": `{
			"upstream_dns":["1.1.1.1"],"protection_enabled":true,
			"default_local_ptr_upstreams":["192.0.2.1"],"protection_disabled_until":"2026-10-26T00:00:00Z"
		}`,
	}}
	member = &fakeAlignmentNode{read: map[string]string{
		"/control/filtering/status": `{"enabled":false,"interval":1,"user_rules":[],"filters":[],"whitelist_filters":[]}`,
		"/control/dns_info":         `{"upstream_dns":["9.9.9.9"],"protection_enabled":false}`,
	}}

	return master, member
}

func TestFullAlignmentFiltersAndDNS(t *testing.T) {
	masterNode, memberNode := alignmentFixtures()
	master, _ := newConnectedTestClusters(t, masterNode.handler(), memberNode.handler())
	req := httptest.NewRequest(http.MethodPost, "/control/cluster/align", strings.NewReader(`{"target":"member"}`))
	rr := httptest.NewRecorder()
	master.ManagementAPI().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("align status=%d body=%s", rr.Code, rr.Body.String())
	}
	var response struct {
		Results []struct {
			Target      string `json:"target"`
			Failed      int    `json:"failed"`
			Overwritten int    `json:"overwritten"`
		} `json:"results"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if len(response.Results) != 1 || response.Results[0].Target != "member" ||
		response.Results[0].Failed != 0 || response.Results[0].Overwritten == 0 {
		t.Fatalf("unexpected alignment result: %+v", response)
	}

	writes := memberNode.snapshotWrites()
	byPath := map[string]string{}
	for _, write := range writes {
		byPath[write.Path] = write.Body
	}
	if !strings.Contains(byPath["/control/filtering/config"], `"interval":24`) ||
		!strings.Contains(byPath["/control/filtering/set_rules"], `"||example.com^"`) {
		t.Fatalf("filtering was not aligned: %+v", writes)
	}
	if !strings.Contains(byPath["/control/filtering/add_url"], `"https://filters.example/list.txt"`) {
		t.Fatalf("filter collection was not aligned: %+v", writes)
	}
	dnsBody := byPath["/control/dns_config"]
	if !strings.Contains(dnsBody, `"1.1.1.1"`) || strings.Contains(dnsBody, "default_local_ptr_upstreams") ||
		strings.Contains(dnsBody, "protection_disabled_until") {
		t.Fatalf("DNS payload was not sanitized and preserved: %s", dnsBody)
	}
}

func TestAlignmentSkipsTLSWithoutMasterPrivateKey(t *testing.T) {
	masterNode, memberNode := alignmentFixtures()
	masterNode.read["/control/tls/status"] = `{
		"enabled":true,"server_name":"dns.example","private_key_saved":true,"private_key":""
	}`
	memberNode.read["/control/tls/status"] = `{
		"enabled":false,"server_name":"old.example","private_key_saved":true,"private_key":""
	}`
	master, _ := newConnectedTestClusters(t, masterNode.handler(), memberNode.handler())
	rr := httptest.NewRecorder()
	master.ManagementAPI().ServeHTTP(rr, httptest.NewRequest(
		http.MethodPost,
		"/control/cluster/align",
		strings.NewReader(`{"target":"member"}`),
	))
	if rr.Code != http.StatusOK {
		t.Fatalf("align status=%d body=%s", rr.Code, rr.Body.String())
	}
	for _, write := range memberNode.snapshotWrites() {
		if write.Path == "/control/tls/configure" {
			t.Fatalf("TLS credentials were overwritten without a master private key: %+v", write)
		}
	}
	audit, err := json.Marshal(master.auditSnapshot())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(audit), "tls_credentials_unavailable") {
		t.Fatalf("missing TLS skip audit: %s", audit)
	}
}

func TestAlignmentConflictRejectDoesNotWrite(t *testing.T) {
	masterNode, memberNode := alignmentFixtures()
	master, _ := newConnectedTestClusters(t, masterNode.handler(), memberNode.handler())
	master.mu.Lock()
	master.conf.Sync.Conflict = ConflictReject
	master.mu.Unlock()
	rr := httptest.NewRecorder()
	master.ManagementAPI().ServeHTTP(rr, httptest.NewRequest(
		http.MethodPost,
		"/control/cluster/align",
		strings.NewReader(`{"target":"member"}`),
	))
	if rr.Code != http.StatusOK {
		t.Fatalf("align status=%d body=%s", rr.Code, rr.Body.String())
	}
	if writes := memberNode.snapshotWrites(); len(writes) != 0 {
		t.Fatalf("reject policy wrote to target: %+v", writes)
	}
	audit, _ := json.Marshal(master.auditSnapshot())
	if !strings.Contains(string(audit), "conflict_rejected") {
		t.Fatalf("missing conflict audit: %s", audit)
	}
}
