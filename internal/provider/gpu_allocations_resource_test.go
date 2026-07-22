package provider

import (
	"encoding/json"
	"testing"
)

func TestBuildSuidMapMatchesCurlContract(t *testing.T) {
	entries := []gpuAllocationEntry{
		{Suid: 0, Server: "su00-node00", Gpus: []string{"G6", "G7"}},
	}
	req := GpuAllocationsRequest{
		Operation: "ADD",
		Suid:      buildSuidMap(entries),
	}
	b, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"operation":"ADD","suid":{"0":{"su00-node00":{"gpus":["G6","G7"]}}}}`
	if string(b) != want {
		t.Fatalf("body mismatch\nwant: %s\ngot:  %s", want, string(b))
	}
}

func TestNormalizeGpuListUppercasesAndSorts(t *testing.T) {
	got := normalizeGpuList([]string{"g7", " G6 ", "g6", ""})
	if len(got) != 2 || got[0] != "G6" || got[1] != "G7" {
		t.Fatalf("unexpected: %#v", got)
	}
}
