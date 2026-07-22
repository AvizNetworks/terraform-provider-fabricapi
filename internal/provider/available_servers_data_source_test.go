package provider

import (
	"encoding/json"
	"testing"
)

func TestAvailableServersResponseJSON(t *testing.T) {
	raw := []byte(`{"availableGPUs":["hgx-su00-h01","hgx-su00-h00"]}`)
	var resp AvailableServersResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.AvailableGPUs) != 2 {
		t.Fatalf("got %#v", resp.AvailableGPUs)
	}
}
