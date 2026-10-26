package merge

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestClients(t *testing.T) {
	master := []byte(`{
		"clients":[{"name":"phone","ids":["10.0.0.1"],"tags":["user"],"upstreams":["1.1.1.1"],"filtering_enabled":true}],
		"auto_clients":[{"ip":"10.0.0.1","name":"phone","source":"runtime"}],
		"supported_tags":["user","vendor"]
	}`)
	member := []byte(`{
		"clients":[
			{"name":"tv","ids":["10.0.0.2"],"tags":[],"upstreams":[]},
			{"name":"phone","ids":["10.0.0.1","phone-id"],"tags":["child"],"upstreams":["8.8.8.8"],"filtering_enabled":false}
		],
		"auto_clients":[{"ip":"10.0.0.1","name":"phone","source":"runtime"}],
		"supported_tags":["user","child"]
	}`)

	data, err := Clients([]NodeData{{Name: "master", Data: master}, {Name: "member", Data: member}}, []string{"offline"})
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Clients []struct {
			Name      string   `json:"name"`
			IDs       []string `json:"ids"`
			Tags      []string `json:"tags"`
			Upstreams []string `json:"upstreams"`
			NodeList  []string `json:"node_list"`
			Filtering bool     `json:"filtering_enabled"`
		} `json:"clients"`
		AutoClients []struct {
			IP       string   `json:"ip"`
			NodeList []string `json:"node_list"`
		} `json:"auto_clients"`
		Tags          []string `json:"supported_tags"`
		NumNodesOK    int      `json:"num_nodes_ok"`
		DegradedNodes []string `json:"degraded_nodes"`
	}
	if err = json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Clients) != 2 || got.Clients[0].Name != "phone" || got.Clients[1].Name != "tv" {
		t.Fatalf("clients=%+v", got.Clients)
	}
	phone := got.Clients[0]
	if !phone.Filtering || !reflect.DeepEqual(phone.IDs, []string{"10.0.0.1", "phone-id"}) ||
		!reflect.DeepEqual(phone.Tags, []string{"user", "child"}) ||
		!reflect.DeepEqual(phone.Upstreams, []string{"1.1.1.1", "8.8.8.8"}) ||
		!reflect.DeepEqual(phone.NodeList, []string{"master", "member"}) {
		t.Fatalf("merged phone=%+v", phone)
	}
	if len(got.AutoClients) != 1 || !reflect.DeepEqual(got.AutoClients[0].NodeList, []string{"master", "member"}) {
		t.Fatalf("auto_clients=%+v", got.AutoClients)
	}
	if !reflect.DeepEqual(got.Tags, []string{"child", "user", "vendor"}) {
		t.Fatalf("supported_tags=%v", got.Tags)
	}
	if got.NumNodesOK != 2 || !reflect.DeepEqual(got.DegradedNodes, []string{"offline"}) {
		t.Fatalf("metadata: ok=%d degraded=%v", got.NumNodesOK, got.DegradedNodes)
	}
}

func TestClientSearchCurrentAndLegacy(t *testing.T) {
	current := []byte(`[{"10.0.0.1":{"name":"phone","ids":["10.0.0.1"],"tags":["user"]}}]`)
	legacy := []byte(`{"found":[{"10.0.0.1":{"name":"phone","ids":["10.0.0.1","phone-id"],"upstreams":["1.1.1.1"]}}]}`)
	data, err := ClientSearch([]NodeData{{Name: "master", Data: current}, {Name: "member", Data: legacy}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]struct {
		Name      string   `json:"name"`
		IDs       []string `json:"ids"`
		Upstreams []string `json:"upstreams"`
		NodeList  []string `json:"node_list"`
	}
	if err = json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("search result=%+v", got)
	}
	client := got[0]["10.0.0.1"]
	if client.Name != "phone" || !reflect.DeepEqual(client.IDs, []string{"10.0.0.1", "phone-id"}) ||
		!reflect.DeepEqual(client.NodeList, []string{"master", "member"}) {
		t.Fatalf("merged search client=%+v", client)
	}
}
