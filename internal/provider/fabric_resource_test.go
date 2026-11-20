package provider

import (
	"fmt"
	"net/http"
	"regexp"
	"testing"

	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// fabricTestConfig is a fabricapi_fabric against fakeFM (which also serves the
// config endpoint), with networkConfigExtra appended inside network_config.
func fabricTestConfig(f *fakeFM, networkConfigExtra string) string {
	return fmt.Sprintf(`
provider "fabricapi" {
  endpoint        = %[1]q
  config_endpoint = %[1]q
  fabric          = "fab1"
  access_token    = "test-token"
}

resource "fabricapi_fabric" "f" {
  name = "tf-test-fabric"
  type = "Aviz RA 1.0"
  su_config = {
    number_of_sus     = 1
    max_number_of_sus = 1
    hosts_per_su      = "{0:2}"
    tenant_ctrl       = "ones"
  }
  network_config = {
    enable_east_west_networking = true
    starting_subnet_gpu         = "192"
    %[2]s
  }
}
`, f.srv.URL, networkConfigExtra)
}

const (
	fabricAddPath    = "/api/config/addFabricData"
	fabricDeletePath = "/api/config/deletefabricbyname/tf-test-fabric"
)

// "unnumbered" without N-S must fail at plan time, before anything is sent.
func TestFabricResource_UnnumberedWithoutNSRejectedAtPlan(t *testing.T) {
	f := newFakeFM(t)
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{
			{
				Config:      fabricTestConfig(f, `ns_bgp_mode = "unnumbered"`),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`BGP unnumbered requires N-S networking`),
			},
			{
				Config: fabricTestConfig(f, `
    enable_north_south_networking = false
    ns_bgp_mode                   = "unnumbered"`),
				PlanOnly:    true,
				ExpectError: regexp.MustCompile(`BGP unnumbered requires N-S networking`),
			},
		},
	})
	if n := len(f.calls(http.MethodPost, fabricAddPath)); n != 0 {
		t.Fatalf("addFabricData called %d times, want 0", n)
	}
}

// An enable_north_south_networking value unknown at plan time must not be
// rejected (Create re-checks it once known).
func TestFabricResource_UnnumberedWithUnknownNSPlans(t *testing.T) {
	f := newFakeFM(t)
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{
			{
				Config: `
resource "terraform_data" "ns" {
  input = true
}
` + fabricTestConfig(f, `
    enable_north_south_networking = terraform_data.ns.output
    starting_subnet_cpu           = "10.2"
    ns_bgp_mode                   = "unnumbered"`),
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
		},
	})
}

// Regression: switching an existing fabric to an invalid unnumbered config
// replaces it, so the error must come at plan time — the existing fabric must
// not be deleted before the config is rejected.
func TestFabricResource_InvalidUnnumberedDoesNotDeleteExisting(t *testing.T) {
	f := newFakeFM(t)
	f.override(http.MethodPost, fabricAddPath, http.StatusOK, "")
	f.override(http.MethodDelete, fabricDeletePath, http.StatusOK, "")
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{
			{
				Config: fabricTestConfig(f, `
    enable_north_south_networking = true
    starting_subnet_cpu           = "10.2"`),
				Check: tfresource.ComposeTestCheckFunc(
					checkCalls(f, http.MethodPost, fabricAddPath, 1),
					checkLastBody(f, http.MethodPost, fabricAddPath, func(b map[string]any) error {
						if _, ok := b["nsBgpMode"]; ok {
							return fmt.Errorf("nsBgpMode sent for default mode: %v", b["nsBgpMode"])
						}
						return nil
					}),
				),
			},
			{
				Config:      fabricTestConfig(f, `ns_bgp_mode = "unnumbered"`),
				ExpectError: regexp.MustCompile(`BGP unnumbered requires N-S networking`),
			},
			{
				// Runs right after the rejected step: the existing fabric must
				// not have been deleted.
				PreConfig: func() {
					if n := len(f.calls(http.MethodDelete, fabricDeletePath)); n != 0 {
						t.Fatalf("rejected config deleted the existing fabric (%d delete calls)", n)
					}
				},
				// Valid unnumbered: replaces the fabric and sends nsBgpMode.
				Config: fabricTestConfig(f, `
    enable_north_south_networking = true
    starting_subnet_cpu           = "10.2"
    ns_bgp_mode                   = "unnumbered"`),
				Check: tfresource.ComposeTestCheckFunc(
					checkCalls(f, http.MethodPost, fabricAddPath, 2),
					checkLastBody(f, http.MethodPost, fabricAddPath, func(b map[string]any) error {
						if b["nsBgpMode"] != nsBgpModeUnnumbered {
							return fmt.Errorf("nsBgpMode = %v, want %q", b["nsBgpMode"], nsBgpModeUnnumbered)
						}
						return nil
					}),
				),
			},
		},
	})
}
