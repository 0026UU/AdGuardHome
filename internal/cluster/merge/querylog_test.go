package merge

import (
	"encoding/json"
	"testing"
)

func TestQueryLogSortLimitAndSource(t *testing.T) {
	master := []byte(`{"oldest":"2026-10-26T10:00:00.0000001Z","data":[
		{"time":"2026-10-26T10:00:00.0000003Z","question":{"name":"master-new"}},
		{"time":"2026-10-26T10:00:00.0000001Z","question":{"name":"master-old"}}
	]}`)
	member := []byte(`{"oldest":"2026-10-26T10:00:00.0000002Z","data":[
		{"time":"2026-10-26T10:00:00.0000004Z","question":{"name":"member-new"}},
		{"time":"2026-10-26T10:00:00.0000002Z","question":{"name":"member-mid"}}
	]}`)

	data, err := QueryLog([]NodeData{{Name: "master", Data: master}, {Name: "member", Data: member}}, 3)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Data []struct {
			Time     string `json:"time"`
			AGHNode  string `json:"agh_node"`
			Question struct {
				Name string `json:"name"`
			} `json:"question"`
		} `json:"data"`
		Oldest string `json:"oldest"`
	}
	if err = json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Data) != 3 {
		t.Fatalf("entries=%d", len(got.Data))
	}
	wantNames := []string{"member-new", "master-new", "member-mid"}
	wantNodes := []string{"member", "master", "member"}
	for i := range got.Data {
		if got.Data[i].Question.Name != wantNames[i] || got.Data[i].AGHNode != wantNodes[i] {
			t.Fatalf("entry %d: %+v", i, got.Data[i])
		}
	}
	if got.Oldest != "2026-10-26T10:00:00.0000002Z" {
		t.Fatalf("oldest=%q", got.Oldest)
	}
}

func TestQueryLogEmpty(t *testing.T) {
	data, err := QueryLog([]NodeData{{Name: "master", Data: []byte(`{"data":[],"oldest":""}`)}}, 500)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err = json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if got["oldest"] != "" {
		t.Fatalf("oldest=%v", got["oldest"])
	}
	if entries, ok := got["data"].([]any); !ok || len(entries) != 0 {
		t.Fatalf("data=%v", got["data"])
	}
}

func TestQueryLogPageAppliesGlobalOffset(t *testing.T) {
	master := []byte(`{"data":[
		{"time":"2026-10-26T10:00:03Z"},{"time":"2026-10-26T10:00:01Z"}
	]}`)
	member := []byte(`{"data":[
		{"time":"2026-10-26T10:00:04Z"},{"time":"2026-10-26T10:00:02Z"}
	]}`)
	data, err := QueryLogPage([]NodeData{{Name: "master", Data: master}, {Name: "member", Data: member}}, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Data []struct {
			Time string `json:"time"`
		} `json:"data"`
		Oldest string `json:"oldest"`
	}
	if err = json.Unmarshal(data, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Data) != 2 || got.Data[0].Time != "2026-10-26T10:00:03Z" ||
		got.Data[1].Time != "2026-10-26T10:00:02Z" || got.Oldest != "2026-10-26T10:00:02Z" {
		t.Fatalf("unexpected page: %+v", got)
	}
}
