package provider

import (
	"encoding/json"
	"strings"
	"testing"
)

// baseFabricInput is a valid Aviz RA 1.0 dgx fabric with E-W and N-S enabled.
func baseFabricInput() fabricInput {
	in := fabricInput{
		Type:              fabricTypeAviz,
		NodeType:          "dgx",
		NumOfSUs:          1,
		MaxNumOfSUs:       1,
		FlatHostMap:       map[string]string{"0": "2"},
		TenantControl:     tenantControlOnes,
		EnableEW:          true,
		StartingSubnetGpu: "192",
		EnableNS:          true,
		NsOperatingSystem: "cumulus",
		StartingSubnetCpu: "10.2",
	}
	in.applyForcedDefaults()
	return in
}

func hasIssue(issues []fabricIssue, summary string) bool {
	for _, i := range issues {
		if i.Summary == summary {
			return true
		}
	}
	return false
}

func TestFabricValidateNsBgpMode(t *testing.T) {
	cases := []struct {
		name      string
		mode      string
		enableNS  bool
		wantIssue string // "" means no issues expected
	}{
		{name: "unset", mode: "", enableNS: true},
		{name: "unset without N-S", mode: "", enableNS: false},
		{name: "numbered", mode: nsBgpModeNumbered, enableNS: true},
		{name: "numbered without N-S", mode: nsBgpModeNumbered, enableNS: false},
		{name: "unnumbered", mode: nsBgpModeUnnumbered, enableNS: true},
		{name: "unnumbered without N-S", mode: nsBgpModeUnnumbered, enableNS: false, wantIssue: "BGP unnumbered requires N-S networking"},
		{name: "invalid", mode: "ebgp", enableNS: true, wantIssue: "Unsupported N-S BGP mode"},
		{name: "wrong case", mode: "Unnumbered", enableNS: true, wantIssue: "Unsupported N-S BGP mode"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := baseFabricInput()
			in.EnableNS = tc.enableNS
			if !tc.enableNS {
				in.StartingSubnetCpu = ""
			}
			in.NsBgpMode = tc.mode
			issues := in.validate()
			if tc.wantIssue == "" {
				if len(issues) != 0 {
					t.Fatalf("expected no issues, got %+v", issues)
				}
				return
			}
			if !hasIssue(issues, tc.wantIssue) {
				t.Fatalf("expected issue %q, got %+v", tc.wantIssue, issues)
			}
		})
	}
}

// The wire field must be omitted unless unnumbered, so existing requests are
// unchanged and older ONES builds see exactly what they did before.
func TestFabricDataRequestNsBgpModeOmitEmpty(t *testing.T) {
	b, err := json.Marshal(FabricDataRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "nsBgpMode") {
		t.Fatalf("nsBgpMode should be omitted when empty: %s", b)
	}

	b, err = json.Marshal(FabricDataRequest{NsBgpMode: nsBgpModeUnnumbered})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"nsBgpMode":"unnumbered"`) {
		t.Fatalf("expected nsBgpMode unnumbered in request: %s", b)
	}
}
