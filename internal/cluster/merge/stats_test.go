package merge

import (
	"encoding/json"
	"math"
	"reflect"
	"testing"

	"github.com/AdguardTeam/AdGuardHome/internal/stats"
)

func TestStats(t *testing.T) {
	master := []byte(`{
		"time_units":"hours",
		"num_dns_queries":100,"num_blocked_filtering":10,
		"num_replaced_safebrowsing":1,"num_replaced_safesearch":2,"num_replaced_parental":3,
		"avg_processing_time":0.1,
		"dns_queries":[10,20,30],"blocked_filtering":[1,2,3],
		"replaced_safebrowsing":[0,1,0],"replaced_parental":[1,0,1],
		"top_queried_domains":[{"alpha":20},{"beta":10}],
		"top_clients":[{"client-a":7}],"top_blocked_domains":[{"blocked-a":4}],
		"top_upstreams_responses":[{"up-a":80,"up-b":20}],
		"top_upstreams_avg_time":[{"up-a":0.1,"up-b":0.4}]
	}`)
	member := []byte(`{
		"time_units":"hours",
		"num_dns_queries":300,"num_blocked_filtering":20,
		"num_replaced_safebrowsing":2,"num_replaced_safesearch":3,"num_replaced_parental":4,
		"avg_processing_time":0.3,
		"dns_queries":[40,50],"blocked_filtering":[4,5],
		"replaced_safebrowsing":[1,1],"replaced_parental":[0,1],
		"top_queried_domains":[{"alpha":30},{"charlie":50}],
		"top_clients":[{"client-b":8}],"top_blocked_domains":[{"blocked-a":6}],
		"top_upstreams_responses":[{"up-a":20,"up-c":50}],
		"top_upstreams_avg_time":[{"up-a":0.3,"up-c":0.2}]
	}`)

	data, err := Stats([]NodeData{{Name: "master", Data: master}, {Name: "member", Data: member}}, []string{"offline"})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		stats.StatsResp
		NumNodesOK    int      `json:"num_nodes_ok"`
		DegradedNodes []string `json:"degraded_nodes"`
	}
	if err = json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got.NumDNSQueries != 400 || got.NumBlockedFiltering != 30 || got.NumReplacedSafesearch != 5 {
		t.Fatalf("incorrect totals: %+v", got.StatsResp)
	}
	if math.Abs(got.AvgProcessingTime-0.25) > 1e-9 {
		t.Fatalf("avg_processing_time=%f", got.AvgProcessingTime)
	}
	if !reflect.DeepEqual(got.DNSQueries, []uint64{60, 80}) {
		t.Fatalf("dns_queries=%v", got.DNSQueries)
	}
	if !reflect.DeepEqual(got.BlockedFiltering, []uint64{6, 8}) {
		t.Fatalf("blocked_filtering=%v", got.BlockedFiltering)
	}
	wantTop := []map[string]uint64{{"alpha": 50}, {"charlie": 50}, {"beta": 10}}
	if !reflect.DeepEqual(got.TopQueried, wantTop) {
		t.Fatalf("top_queried_domains=%v", got.TopQueried)
	}
	wantResponses := []map[string]uint64{{"up-a": 100}, {"up-c": 50}, {"up-b": 20}}
	if !reflect.DeepEqual(got.TopUpstreamsResponses, wantResponses) {
		t.Fatalf("top_upstreams_responses=%v", got.TopUpstreamsResponses)
	}
	averages := flattenFloat(got.TopUpstreamsAvgTime)
	if math.Abs(averages["up-a"]-0.14) > 1e-9 || averages["up-b"] != 0.4 || averages["up-c"] != 0.2 {
		t.Fatalf("top_upstreams_avg_time=%v", averages)
	}
	if got.NumNodesOK != 2 || !reflect.DeepEqual(got.DegradedNodes, []string{"offline"}) {
		t.Fatalf("metadata: ok=%d degraded=%v", got.NumNodesOK, got.DegradedNodes)
	}
}

func TestStatsTopTen(t *testing.T) {
	payload := []byte(`{"top_queried_domains":[
		{"d0":1},{"d1":2},{"d2":3},{"d3":4},{"d4":5},{"d5":6},
		{"d6":7},{"d7":8},{"d8":9},{"d9":10},{"d10":11},{"d11":12}
	]}`)
	data, err := Stats([]NodeData{{Name: "master", Data: payload}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got stats.StatsResp
	if err = json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.TopQueried) != 10 || got.TopQueried[0]["d11"] != 12 || got.TopQueried[9]["d2"] != 3 {
		t.Fatalf("unexpected top ten: %v", got.TopQueried)
	}
}

func TestStatsConfigUsesMinimumHealthyInterval(t *testing.T) {
	sources := []NodeData{
		{Name: "master", Data: []byte(`{"enabled":1,"interval":604800000,"ignored":["local"]}`)},
		{Name: "member", Data: []byte(`{"enabled":1,"interval":86400000,"ignored":[]}`)},
	}
	data, err := StatsConfig(sources, []string{"offline"})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err = json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["interval"] != float64(86400000) || got["enabled"] != float64(1) {
		t.Fatalf("unexpected config: %v", got)
	}
	if !reflect.DeepEqual(got["ignored"], []any{"local"}) {
		t.Fatalf("local fields were not preserved: %v", got)
	}
}

func flattenFloat(items []map[string]float64) map[string]float64 {
	result := map[string]float64{}
	for _, item := range items {
		for key, value := range item {
			result[key] = value
		}
	}

	return result
}
