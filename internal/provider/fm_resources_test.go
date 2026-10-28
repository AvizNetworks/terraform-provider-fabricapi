package provider

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// These tests drive the real Terraform CLI (plan/apply/import/destroy) against
// fakeFM, so they need a `terraform` binary on PATH but no Fabric Manager.

func checkCalls(f *fakeFM, method, path string, want int) tfresource.TestCheckFunc {
	return func(*terraform.State) error {
		if got := len(f.calls(method, path)); got != want {
			return fmt.Errorf("%s %s: %d requests, want %d", method, path, got, want)
		}
		return nil
	}
}

func checkLastBody(f *fakeFM, method, path string, check func(map[string]any) error) tfresource.TestCheckFunc {
	return func(*terraform.State) error {
		c := f.calls(method, path)
		if len(c) == 0 {
			return fmt.Errorf("no %s %s request", method, path)
		}
		return check(c[len(c)-1].Body)
	}
}

func stringsAt(m map[string]any, keys ...string) []string {
	var cur any = m
	for _, k := range keys {
		mm, _ := cur.(map[string]any)
		cur = mm[k]
	}
	items, _ := cur.([]any)
	out := []string{}
	for _, it := range items {
		out = append(out, fmt.Sprint(it))
	}
	sort.Strings(out)
	return out
}

// ------------------------------------------------------------ network policy

func networkPolicyConfig(f *fakeFM, srcType, srcEndpoints, dstEndpoints string) string {
	src := ""
	if srcEndpoints != "" {
		src = "endpoints = [" + srcEndpoints + "]"
	}
	return f.providerConfig() + fmt.Sprintf(`
resource "fabricapi_network_policy" "p" {
  name = "np1"
  source = {
    type        = %q
    tenant_name = "TenantA"
    %s
  }
  destination = {
    type         = "service"
    tenant_name  = "TenantB"
    service_name = "storage"
    endpoints    = [%s]
  }
}
`, srcType, src, dstEndpoints)
}

func TestNetworkPolicyResource_Lifecycle(t *testing.T) {
	f := newFakeFM(t)
	addr := "fabricapi_network_policy.p"
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{
			{
				Config: networkPolicyConfig(f, "tenant", "", `"10.0.0.1/32"`),
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(addr, "id", "fab1/np1"),
					tfresource.TestCheckResourceAttr(addr, "fabric_name", "fab1"),
					tfresource.TestCheckResourceAttr(addr, "destination.endpoints.#", "1"),
					checkLastBody(f, http.MethodPost, "/fabrics/fab1/networkpolicy", func(b map[string]any) error {
						if got := stringsAt(b, "spec", "destination", "endpoints"); strings.Join(got, ",") != "10.0.0.1/32" {
							return fmt.Errorf("create endpoints = %v", got)
						}
						return nil
					}),
				),
			},
			{
				// Adding an endpoint is an in-place PATCH carrying only the new one.
				Config: networkPolicyConfig(f, "tenant", "", `"10.0.0.1/32", "10.0.0.2/32"`),
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate)},
				},
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(addr, "destination.endpoints.#", "2"),
					checkLastBody(f, http.MethodPatch, "/fabrics/fab1/networkpolicy/np1", func(b map[string]any) error {
						if got := stringsAt(b, "spec", "destination", "endpoints"); strings.Join(got, ",") != "10.0.0.2/32" {
							return fmt.Errorf("patch should send only the new endpoint, got %v", got)
						}
						if b["policy"].(map[string]any)["name"] != "np1" {
							return fmt.Errorf("patch body lacks policy.name")
						}
						return nil
					}),
					checkCalls(f, http.MethodDelete, "/fabrics/fab1/networkpolicy/np1", 0),
				),
			},
			{
				// Removing an endpoint can't be expressed by FM's add-only PATCH: replace.
				Config: networkPolicyConfig(f, "tenant", "", `"10.0.0.2/32"`),
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(addr, "destination.endpoints.#", "1"),
					checkCalls(f, http.MethodDelete, "/fabrics/fab1/networkpolicy/np1", 1),
					checkCalls(f, http.MethodPost, "/fabrics/fab1/networkpolicy", 2),
				),
			},
			{
				ResourceName:      addr,
				ImportState:       true,
				ImportStateId:     "fab1/np1",
				ImportStateVerify: true,
			},
		},
		CheckDestroy: func(*terraform.State) error {
			if len(f.policies["fab1"]) != 0 {
				return fmt.Errorf("policy still present after destroy: %+v", f.policies["fab1"])
			}
			return nil
		},
	})
}

func TestNetworkPolicyResource_SourceOnlyAdditionReplaces(t *testing.T) {
	// FM's PATCH validation requires new destination endpoints for a service
	// destination, so adding only source endpoints must plan a replacement.
	f := newFakeFM(t)
	addr := "fabricapi_network_policy.p"
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{
			{Config: networkPolicyConfig(f, "service", `"192.168.1.1/32"`, `"10.0.0.1/32"`)},
			{
				Config: networkPolicyConfig(f, "service", `"192.168.1.1/32", "192.168.1.2/32"`, `"10.0.0.1/32"`),
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: checkCalls(f, http.MethodPatch, "", 0),
			},
			{
				// Adding on both sides is a valid PATCH.
				Config: networkPolicyConfig(f, "service", `"192.168.1.1/32", "192.168.1.2/32", "192.168.1.3/32"`, `"10.0.0.1/32", "10.0.0.9/32"`),
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate)},
				},
				Check: checkLastBody(f, http.MethodPatch, "/fabrics/fab1/networkpolicy/np1", func(b map[string]any) error {
					if got := stringsAt(b, "spec", "source", "endpoints"); strings.Join(got, ",") != "192.168.1.3/32" {
						return fmt.Errorf("source additions = %v", got)
					}
					if got := stringsAt(b, "spec", "destination", "endpoints"); strings.Join(got, ",") != "10.0.0.9/32" {
						return fmt.Errorf("destination additions = %v", got)
					}
					return nil
				}),
			},
		},
	})
}

func TestNetworkPolicyResource_ValidationErrors(t *testing.T) {
	f := newFakeFM(t)
	cfg := func(body string) string { return f.providerConfig() + body }
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{
			{
				Config: cfg(`
resource "fabricapi_network_policy" "p" {
  name        = "this-name-is-longer-than-24"
  source      = { type = "tenant", tenant_name = "A" }
  destination = { type = "tenant", tenant_name = "B" }
}`),
				ExpectError: regexp.MustCompile(`string length must be between 1 and 24`),
			},
			{
				Config: cfg(`
resource "fabricapi_network_policy" "p" {
  name        = "np"
  source      = { type = "service", tenant_name = "A", endpoints = ["1.1.1.1/32"] }
  destination = { type = "tenant", tenant_name = "B" }
}`),
				ExpectError: regexp.MustCompile(`A tenant destination requires a tenant source`),
			},
			{
				Config: cfg(`
resource "fabricapi_network_policy" "p" {
  name        = "np"
  source      = { type = "tenant", tenant_name = "A" }
  destination = { type = "service", tenant_name = "B" }
}`),
				ExpectError: regexp.MustCompile(`destination.endpoints is required`),
			},
			{
				Config: cfg(`
resource "fabricapi_network_policy" "p" {
  name        = "np"
  source      = { type = "tenant", tenant_name = "A", endpoints = ["1.1.1.1/32"] }
  destination = { type = "service", tenant_name = "B", endpoints = ["2.2.2.2/32"] }
}`),
				ExpectError: regexp.MustCompile(`source.endpoints must be unset`),
			},
			{
				Config: cfg(`
resource "fabricapi_network_policy" "p" {
  name        = "np"
  source      = { type = "vpc", tenant_name = "A" }
  destination = { type = "tenant", tenant_name = "B" }
}`),
				ExpectError: regexp.MustCompile(`value must be one of`),
			},
		},
	})
	if n := len(f.calls(http.MethodPost, "")); n != 0 {
		t.Fatalf("invalid configs must fail before any request; %d POSTs sent", n)
	}
}

func TestNetworkPolicyResource_TenantToTenantAndDrift(t *testing.T) {
	f := newFakeFM(t)
	addr := "fabricapi_network_policy.t2t"
	config := f.providerConfig() + `
resource "fabricapi_network_policy" "t2t" {
  name        = "t2t"
  source      = { type = "tenant", tenant_name = "TenantA" }
  destination = { type = "tenant", tenant_name = "TenantB" }
}`
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{
			{
				Config: config,
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckNoResourceAttr(addr, "source.endpoints"),
					tfresource.TestCheckNoResourceAttr(addr, "destination.endpoints"),
				),
			},
			{
				// Someone deletes the policy outside Terraform: the next plan recreates it.
				PreConfig: func() {
					f.mu.Lock()
					f.policies["fab1"] = nil
					f.mu.Unlock()
				},
				Config: config,
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionCreate)},
				},
			},
		},
	})
}

func TestNetworkPolicyResource_CreateErrorIsReported(t *testing.T) {
	f := newFakeFM(t)
	// FM reports service-layer failures (unknown tenant, device push failure) as 500.
	f.override(http.MethodPost, "/fabrics/fab1/networkpolicy", 500,
		`{"status":"error","message":"Source tenant 'TenantA' not found in fabric 'fab1'.","data":{"policyName":"np1"},"error":{"code":"NETWORK_POLICY_CREATION_FAILED","detail":"Source tenant 'TenantA' not found in fabric 'fab1'."}}`)
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{{
			Config:      networkPolicyConfig(f, "tenant", "", `"10.0.0.1/32"`),
			ExpectError: regexp.MustCompile(`(?s)NETWORK_POLICY_CREATION_FAILED: Source tenant 'TenantA'\s+not\s+found`),
		}},
	})
}

// ------------------------------------------------------------------- webhooks

func globalWebhookConfig(f *fakeFM, enabled bool, events, fabrics string) string {
	filters := ""
	if fabrics != "" {
		filters = "filter_fabric_names = [" + fabrics + "]"
	}
	return f.providerConfig() + fmt.Sprintf(`
resource "fabricapi_webhook" "g" {
  callback_url = "https://hooks.example.com/global"
  event_types  = [%s]
  enabled      = %t
  %s
}
`, events, enabled, filters)
}

func TestWebhookResource_Lifecycle(t *testing.T) {
	f := newFakeFM(t)
	addr := "fabricapi_webhook.g"
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{
			{
				// FM ignores `enabled` on register, so a disabled webhook is
				// registered and then disabled with a PATCH.
				Config: globalWebhookConfig(f, false, `"fabric.deploy", "tenant.create"`, `"fab1"`),
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(addr, "enabled", "false"),
					tfresource.TestCheckResourceAttr(addr, "event_types.#", "2"),
					tfresource.TestCheckResourceAttr(addr, "filter_fabric_names.#", "1"),
					tfresource.TestCheckResourceAttr(addr, "filter_tenant_names.#", "0"),
					tfresource.TestCheckResourceAttrPair(addr, "id", addr, "webhook_id"),
					tfresource.TestCheckResourceAttrSet(addr, "created_at"),
					checkCalls(f, http.MethodPost, "/webhooks", 1),
					checkLastBody(f, http.MethodPatch, "", func(b map[string]any) error {
						if b["enabled"] != false {
							return fmt.Errorf("follow-up PATCH = %v", b)
						}
						return nil
					}),
				),
			},
			{
				// Dropping the filter must send [] so FM clears it.
				Config: globalWebhookConfig(f, true, `"fabric.deploy"`, ""),
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate)},
				},
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(addr, "enabled", "true"),
					tfresource.TestCheckResourceAttr(addr, "event_types.#", "1"),
					tfresource.TestCheckResourceAttr(addr, "filter_fabric_names.#", "0"),
					checkLastBody(f, http.MethodPatch, "", func(b map[string]any) error {
						fl, _ := b["filters"].(map[string]any)
						if fl == nil || fl["fabricNames"] == nil {
							return fmt.Errorf("filters.fabricNames must be sent as [] to clear: %v", b)
						}
						if len(stringsAt(b, "filters", "fabricNames")) != 0 {
							return fmt.Errorf("fabricNames = %v", fl["fabricNames"])
						}
						return nil
					}),
				),
			},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true},
		},
		CheckDestroy: func(*terraform.State) error {
			if len(f.global) != 0 {
				return fmt.Errorf("global webhooks left: %d", len(f.global))
			}
			return nil
		},
	})
}

func TestWebhookResource_ValidatesEventTypes(t *testing.T) {
	f := newFakeFM(t)
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{
			{Config: globalWebhookConfig(f, true, `"tenant.foo"`, ""), ExpectError: regexp.MustCompile(`value must be one of`)},
			{Config: globalWebhookConfig(f, true, ``, ""), ExpectError: regexp.MustCompile(`at least 1`)},
		},
	})
}

func TestWebhookResource_DeletedOutOfBand(t *testing.T) {
	f := newFakeFM(t)
	addr := "fabricapi_webhook.g"
	cfg := globalWebhookConfig(f, true, `"fabric.delete"`, "")
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{
			{Config: cfg},
			{
				PreConfig: func() {
					f.mu.Lock()
					f.global = map[string]*WebhookRegistration{}
					f.mu.Unlock()
				},
				Config: cfg,
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionCreate)},
				},
			},
		},
	})
}

func scopedWebhookConfig(f *fakeFM, kind, tenant string, enabled bool, events string) string {
	tenantLine, eventsLine := "", ""
	if tenant != "" {
		tenantLine = fmt.Sprintf("tenant_name = %q", tenant)
	}
	if events != "" {
		eventsLine = "event_types = [" + events + "]"
	}
	return f.providerConfig() + fmt.Sprintf(`
resource "fabricapi_%s_webhook" "w" {
  %s
  callback_url = "https://hooks.example.com/%s"
  enabled      = %t
  %s
}
`, kind, tenantLine, kind, enabled, eventsLine)
}

func TestFabricWebhookResource_Lifecycle(t *testing.T) {
	f := newFakeFM(t)
	addr := "fabricapi_fabric_webhook.w"
	path := "/fabrics/fab1/webhook"
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{
			{
				// event_types unset = every event; sent as [].
				Config: scopedWebhookConfig(f, "fabric", "", true, ""),
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(addr, "id", "fab1"),
					tfresource.TestCheckNoResourceAttr(addr, "event_types"),
					tfresource.TestCheckResourceAttrSet(addr, "registration_id"),
					checkCalls(f, http.MethodPost, path, 1),
					checkCalls(f, http.MethodPut, path, 0),
				),
			},
			{
				Config: scopedWebhookConfig(f, "fabric", "", false, `"fabric.deploy"`),
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate)},
				},
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(addr, "enabled", "false"),
					tfresource.TestCheckResourceAttr(addr, "event_types.#", "1"),
					checkLastBody(f, http.MethodPut, path, func(b map[string]any) error {
						if b["enabled"] != false {
							return fmt.Errorf("PUT must always carry enabled: %v", b)
						}
						return nil
					}),
				),
			},
			{
				// Back to every event: PUT sends [] to clear the subscription.
				Config: scopedWebhookConfig(f, "fabric", "", false, ""),
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckNoResourceAttr(addr, "event_types"),
					checkLastBody(f, http.MethodPut, path, func(b map[string]any) error {
						if evs, ok := b["eventTypes"].([]any); !ok || len(evs) != 0 {
							return fmt.Errorf("eventTypes must be [] to clear: %v", b["eventTypes"])
						}
						return nil
					}),
				),
			},
			{ResourceName: addr, ImportState: true, ImportStateId: "fab1", ImportStateVerify: true},
		},
		CheckDestroy: func(*terraform.State) error {
			if len(f.scoped) != 0 {
				return fmt.Errorf("fabric webhook left after destroy")
			}
			return nil
		},
	})
}

func TestFabricWebhookResource_RefusesToTakeOverExisting(t *testing.T) {
	f := newFakeFM(t)
	f.scoped["fabrics/fab1/webhook"] = &WebhookRegistration{ID: 9, FabricName: "fab1", CallbackURL: "https://someone-else", Enabled: true}
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{{
			Config:      scopedWebhookConfig(f, "fabric", "", true, ""),
			ExpectError: regexp.MustCompile(`(?s)already has a webhook registration.*terraform import`),
		}},
	})
	if len(f.calls(http.MethodPost, "")) != 0 {
		t.Fatal("must not overwrite an existing registration")
	}
}

func TestTenantWebhookResource_Lifecycle(t *testing.T) {
	f := newFakeFM(t)
	addr := "fabricapi_tenant_webhook.w"
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{
			{
				Config:      scopedWebhookConfig(f, "tenant", "t1", true, `"fabric.deploy"`),
				ExpectError: regexp.MustCompile(`value must be one of`),
			},
			{
				Config: scopedWebhookConfig(f, "tenant", "t1", true, `"tenant.allocate", "tenant.deallocate"`),
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(addr, "id", "fab1/t1"),
					tfresource.TestCheckResourceAttr(addr, "event_types.#", "2"),
					checkCalls(f, http.MethodPost, "/fabrics/fab1/tenants/t1/webhook", 1),
				),
			},
			{ResourceName: addr, ImportState: true, ImportStateId: "fab1/t1", ImportStateVerify: true},
			{ResourceName: addr, ImportState: true, ImportStateId: "fab1", ExpectError: regexp.MustCompile(`Expected .<fabric>/<tenant>.`)},
		},
	})
}

// ------------------------------------------------------------------------ IPAM

func ipamConfig(f *fakeFM, body string) string {
	return f.providerConfig() + "\nresource \"fabricapi_ipam_config\" \"i\" {\n" + body + "\n}\n"
}

func TestIpamConfigResource_Lifecycle(t *testing.T) {
	f := newFakeFM(t)
	addr := "fabricapi_ipam_config.i"
	path := "/fabrics/fab1/ipam/config"
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{
			{
				Config: ipamConfig(f, `mode = "static"`),
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(addr, "id", "fab1"),
					tfresource.TestCheckResourceAttr(addr, "enable_ip_assign", "false"),
					tfresource.TestCheckNoResourceAttr(addr, "lease_time_seconds"),
					checkLastBody(f, http.MethodPatch, path, func(b map[string]any) error {
						if _, ok := b["fabricName"]; ok {
							return fmt.Errorf("fabricName must not be sent")
						}
						if b["enableIpam"] != true || b["static"].(map[string]any)["enabled"] != true || b["dhcp"] != nil {
							return fmt.Errorf("body = %v", b)
						}
						return nil
					}),
				),
			},
			{
				Config: ipamConfig(f, "mode = \"static\"\nenable_ip_assign = true"),
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate)},
				},
				Check: tfresource.TestCheckResourceAttr(addr, "enable_ip_assign", "true"),
			},
			{
				// FM refuses a direct static->dhcp switch, so the mode change is a
				// replacement: IPAM off, then on in DHCP mode.
				Config: ipamConfig(f, "mode = \"dhcp\"\nlease_time_seconds = 120\nforce = true"),
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(addr, "mode", "dhcp"),
					tfresource.TestCheckResourceAttr(addr, "lease_time_seconds", "120"),
					func(*terraform.State) error {
						c := f.calls(http.MethodPatch, path)
						off, on := c[len(c)-2], c[len(c)-1]
						if off.Body["enableIpam"] != false || on.Body["dhcp"].(map[string]any)["leaseTimeSeconds"] != float64(120) {
							return fmt.Errorf("expected off then dhcp-on, got %s then %s", off.Raw, on.Raw)
						}
						if on.Query != "force=true" {
							return fmt.Errorf("force not sent: %q", on.Query)
						}
						return nil
					},
				),
			},
			{
				// Unset lease time keeps the saved value: no diff.
				Config:   ipamConfig(f, "mode = \"dhcp\"\nforce = true"),
				PlanOnly: true,
			},
			{ResourceName: addr, ImportState: true, ImportStateId: "fab1", ImportStateVerify: true, ImportStateVerifyIgnore: []string{"force"}},
		},
		CheckDestroy: func(*terraform.State) error {
			if f.ipam["fab1"].EnableIpam {
				return fmt.Errorf("IPAM still enabled after destroy")
			}
			return nil
		},
	})
}

func TestIpamConfigResource_ErrorsAndValidation(t *testing.T) {
	f := newFakeFM(t)
	f.override(http.MethodPatch, "/fabrics/fab1/ipam/config", 409,
		`{"status":"error","message":"IPAM configuration can only be modified for externally managed (non-ONES-controlled) fabrics.","error":{"code":"FABRIC_NOT_EXTERNALLY_MANAGED","detail":"IPAM configuration can only be modified for externally managed (non-ONES-controlled) fabrics."}}`)
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{
			{Config: ipamConfig(f, "mode = \"static\"\nlease_time_seconds = 60"), ExpectError: regexp.MustCompile(`lease_time_seconds only applies`)},
			{Config: ipamConfig(f, "mode = \"dhcp\"\nenable_ip_assign = true"), ExpectError: regexp.MustCompile(`enable_ip_assign only applies`)},
			{Config: ipamConfig(f, "mode = \"dhcp\"\nlease_time_seconds = 2592001"), ExpectError: regexp.MustCompile(`must be between 1 and 2592000`)},
			{Config: ipamConfig(f, `mode = "static"`), ExpectError: regexp.MustCompile(`409 FABRIC_NOT_EXTERNALLY_MANAGED`)},
		},
	})
}

func TestIpamConfigResource_DisabledOutOfBand(t *testing.T) {
	f := newFakeFM(t)
	addr := "fabricapi_ipam_config.i"
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{
			{Config: ipamConfig(f, `mode = "static"`)},
			{
				PreConfig: func() {
					f.mu.Lock()
					f.ipam["fab1"] = IpamConfig{}
					f.mu.Unlock()
				},
				Config: ipamConfig(f, `mode = "static"`),
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionCreate)},
				},
			},
		},
	})
}

// --------------------------------------------------------------------- DHCP

func TestDhcpReconcileResource_RunsOnCreateAndTriggerChange(t *testing.T) {
	f := newFakeFM(t)
	f.override(http.MethodPost, "/fabrics/fab1/dhcp/reconcile", 200,
		`{"status":"success","message":"Tenant DHCP subnets reconciled successfully.","data":"fab1: CONFIG_TEST OK; fab1: CONFIG_SET OK; fab1: CONFIG_WRITE OK"}`)
	addr := "fabricapi_dhcp_reconcile.r"
	cfg := func(trigger string) string {
		return f.providerConfig() + fmt.Sprintf(`
resource "fabricapi_dhcp_reconcile" "r" {
  triggers = { run = %q }
}`, trigger)
	}
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{
			{
				Config: cfg("1"),
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(addr, "summary", "fab1: CONFIG_TEST OK; fab1: CONFIG_SET OK; fab1: CONFIG_WRITE OK"),
					tfresource.TestCheckResourceAttr(addr, "id", "dhcpreconcile:fab1"),
					checkCalls(f, http.MethodPost, "/fabrics/fab1/dhcp/reconcile", 1),
				),
			},
			{Config: cfg("1"), PlanOnly: true},
			{
				Config: cfg("2"),
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: checkCalls(f, http.MethodPost, "/fabrics/fab1/dhcp/reconcile", 2),
			},
		},
	})
}

func TestTenantDhcpRetryResource_ReportsFMErrors(t *testing.T) {
	f := newFakeFM(t)
	f.override(http.MethodPost, "/fabrics/fab1/tenants/t1/dhcp/retry", 404,
		`{"status":"error","message":"DHCP does not apply to this tenant: the fabric does not assign addresses by DHCP, or the tenant has no VLAN or IPAM pool.","error":{"code":"DHCP_SUBNET_NOT_FOUND","detail":"DHCP does not apply to this tenant: the fabric does not assign addresses by DHCP, or the tenant has no VLAN or IPAM pool."}}`)
	f.override(http.MethodPost, "/fabrics/fab1/tenants/t2/dhcp/retry", 200,
		`{"status":"success","message":"Tenant DHCP subnet re-applied successfully.","data":{"changed":true}}`)
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{
			{
				Config:      f.providerConfig() + `resource "fabricapi_tenant_dhcp_retry" "r" { tenant_name = "t1" }`,
				ExpectError: regexp.MustCompile(`404 DHCP_SUBNET_NOT_FOUND`),
			},
			{
				// A JSON object summary (as the SDK expects) is kept verbatim.
				Config: f.providerConfig() + `resource "fabricapi_tenant_dhcp_retry" "r" { tenant_name = "t2" }`,
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr("fabricapi_tenant_dhcp_retry.r", "summary", `{"changed":true}`),
					tfresource.TestCheckResourceAttr("fabricapi_tenant_dhcp_retry.r", "id", "dhcpretry:fab1/t2"),
				),
			},
		},
	})
}

// -------------------------------------------------------------- data sources

func TestFMDataSources(t *testing.T) {
	f := newFakeFM(t)
	f.policies["fab1"] = []NetworkPolicy{newNetworkPolicy("np1",
		NetworkPolicySide{Type: "TENANT", TenantName: "A"},
		NetworkPolicySide{Type: "service", TenantName: "B", ServiceName: "storage", Endpoints: []string{"10.0.0.2/32", "10.0.0.1/32"}})}
	f.global["w1"] = &WebhookRegistration{WebhookID: "w1", CallbackURL: "https://x", Enabled: true, EventTypes: []string{"fabric.deploy"}, Filters: &WebhookFilters{FabricNames: []string{"fab1"}, TenantNames: []string{}}}
	lease := int64(300)
	f.ipam["fab1"] = IpamConfig{EnableIpam: true, Dhcp: &IpamDhcpConfig{Enabled: true, LeaseTimeSeconds: &lease}}
	var allocs []IpamAllocation
	vf := "vf4"
	tid := int64(12)
	for i := 0; i < 501; i++ {
		allocs = append(allocs, IpamAllocation{ID: int64(i), TenantName: "t1", TenantID: &tid, ServerName: "gpu-01", IPAddress: fmt.Sprintf("172.1.0.%d", i%250), PrefixLength: 18, AssignStatus: "ASSIGNED", CleanupStatus: "NONE"})
	}
	allocs[0].InterfaceName = &vf
	f.tenantIPs["fab1/t1"] = allocs
	mac := "aa:bb:cc:dd:ee:01"
	f.leases["fab1/t1"] = []DhcpLease{{IPAddress: "172.1.64.2", MacAddress: &mac, State: "ASSIGNED", ValidLifetimeSeconds: 300}}
	f.override(http.MethodGet, "/fabrics/fab1/computes/gpu-01/ip-addresses", 200,
		`{"status":"success","data":{"fabricName":"fab1","serverName":"gpu-01","allocations":[{"id":7,"poolId":3,"tenantName":"t1","tenantId":null,"serverName":"gpu-01","ipAddress":"10.12.0.2","prefixLength":18,"assignStatus":"ASSIGNED","cleanupStatus":"NONE","allocatedAt":"2026-10-26T10:15:30Z","interfaceName":null}]}}`)
	f.override(http.MethodGet, "/fabrics/fab1/tenants/t1/computes/gpu-01/ip-address", 200,
		`{"status":"success","data":{"id":7,"poolId":3,"tenantName":"t1","serverName":"gpu-01","ipAddress":"10.12.0.2","prefixLength":18,"assignStatus":"ALLOCATED_ONLY","cleanupStatus":"NONE","allocatedAt":"2026-10-26T10:15:30Z","interfaceName":null}}`)
	f.override(http.MethodGet, "/fabrics/fab1/rails/cidrpools", 200,
		`{"status":"success","data":{"fabricName":"fab1","rails":["r0","r1"],"yaml":"apiVersion: aviz/rails/\n"}}`)
	f.override(http.MethodGet, "/fabrics/fab1/rails/r0/cidrpool", 200,
		`{"status":"success","data":{"fabricName":"fab1","railName":"r0","yaml":"apiVersion: aviz/rails/\nkind: CIDRPool\n"}}`)

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{{
			Config: f.providerConfig() + `
data "fabricapi_network_policies" "p" {}
data "fabricapi_webhooks" "w" {}
data "fabricapi_ipam_config" "i" {}
data "fabricapi_tenant_ip_addresses" "t" { tenant_name = "t1" }
data "fabricapi_server_ip_addresses" "s" { server_name = "gpu-01" }
data "fabricapi_tenant_compute_ip" "c" {
  tenant_name = "t1"
  server_name = "gpu-01"
}
data "fabricapi_tenant_dhcp" "d" { tenant_name = "t1" }
data "fabricapi_rail_cidr_pools" "r" {}
data "fabricapi_rail_cidr_pool" "r0" { rail_name = "r0" }
`,
			Check: tfresource.ComposeAggregateTestCheckFunc(
				tfresource.TestCheckResourceAttr("data.fabricapi_network_policies.p", "policies.#", "1"),
				tfresource.TestCheckResourceAttr("data.fabricapi_network_policies.p", "policies.0.source.type", "tenant"),
				tfresource.TestCheckResourceAttr("data.fabricapi_network_policies.p", "policies.0.destination.endpoints.0", "10.0.0.2/32"),
				tfresource.TestCheckResourceAttr("data.fabricapi_webhooks.w", "webhooks.0.filter_fabric_names.0", "fab1"),
				tfresource.TestCheckResourceAttr("data.fabricapi_ipam_config.i", "mode", "dhcp"),
				tfresource.TestCheckResourceAttr("data.fabricapi_ipam_config.i", "lease_time_seconds", "300"),
				tfresource.TestCheckNoResourceAttr("data.fabricapi_ipam_config.i", "enable_ip_assign"),
				tfresource.TestCheckResourceAttr("data.fabricapi_tenant_ip_addresses.t", "allocations.#", "501"),
				tfresource.TestCheckResourceAttr("data.fabricapi_tenant_ip_addresses.t", "pool_cidr", "172.1.0.0/18"),
				tfresource.TestCheckResourceAttr("data.fabricapi_tenant_ip_addresses.t", "allocations.0.interface_name", "vf4"),
				tfresource.TestCheckResourceAttr("data.fabricapi_tenant_ip_addresses.t", "allocations.0.tenant_id", "12"),
				tfresource.TestCheckResourceAttr("data.fabricapi_server_ip_addresses.s", "allocations.0.ip_address", "10.12.0.2"),
				tfresource.TestCheckNoResourceAttr("data.fabricapi_server_ip_addresses.s", "allocations.0.interface_name"),
				tfresource.TestCheckNoResourceAttr("data.fabricapi_server_ip_addresses.s", "allocations.0.tenant_id"),
				tfresource.TestCheckResourceAttr("data.fabricapi_tenant_compute_ip.c", "assign_status", "ALLOCATED_ONLY"),
				tfresource.TestCheckResourceAttr("data.fabricapi_tenant_compute_ip.c", "id", "fab1/t1/gpu-01"),
				tfresource.TestCheckResourceAttr("data.fabricapi_tenant_dhcp.d", "subnet.vlan_id", "100"),
				tfresource.TestCheckResourceAttr("data.fabricapi_tenant_dhcp.d", "live_in_kea", "true"),
				tfresource.TestCheckResourceAttr("data.fabricapi_tenant_dhcp.d", "leases.0.mac_address", mac),
				tfresource.TestCheckNoResourceAttr("data.fabricapi_tenant_dhcp.d", "leases.0.hostname"),
				tfresource.TestCheckResourceAttr("data.fabricapi_rail_cidr_pools.r", "rails.#", "2"),
				tfresource.TestCheckResourceAttr("data.fabricapi_rail_cidr_pool.r0", "yaml", "apiVersion: aviz/rails/\nkind: CIDRPool\n"),
			),
		}},
	})
}

func TestFMDataSources_ErrorsAreReported(t *testing.T) {
	f := newFakeFM(t)
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{
			{Config: f.providerConfig() + `data "fabricapi_tenant_dhcp" "d" { tenant_name = "nope" }`, ExpectError: regexp.MustCompile(`404 DHCP_SUBNET_NOT_FOUND`)},
			{Config: f.providerConfig() + `data "fabricapi_tenant_ip_addresses" "t" { tenant_name = "nope" }`, ExpectError: regexp.MustCompile(`404 IPAM_NOT_CONFIGURED`)},
			{Config: f.providerConfig() + `data "fabricapi_ipam_config" "i" { fabric_name = "nofab" }`, ExpectError: regexp.MustCompile(`404 FABRIC_NOT_FOUND`)},
			{Config: f.providerConfig() + `data "fabricapi_network_policies" "p" { fabric_name = "nofab" }`, ExpectError: regexp.MustCompile(`Fabric 'nofab' not found`)},
			{Config: f.providerConfig() + `data "fabricapi_tenant_ip_addresses" "t" {
  tenant_name = "t1"
  status      = "BOGUS"
}`, ExpectError: regexp.MustCompile(`value must be one of`)},
		},
	})
}

// ------------------------------------------------------- backward compatibility

func TestProvider_ExistingResourcesAndDataSourcesStillRegistered(t *testing.T) {
	p := New("test")().(*FabricAPIProvider)
	var resources, dataSources []string
	for _, fn := range p.Resources(context.Background()) {
		var resp resource.MetadataResponse
		fn().Metadata(context.Background(), resource.MetadataRequest{ProviderTypeName: "fabricapi"}, &resp)
		resources = append(resources, resp.TypeName)
	}
	for _, fn := range p.DataSources(context.Background()) {
		var resp datasource.MetadataResponse
		fn().Metadata(context.Background(), datasource.MetadataRequest{ProviderTypeName: "fabricapi"}, &resp)
		dataSources = append(dataSources, resp.TypeName)
	}
	for _, want := range []string{
		"fabricapi_tenant", "fabricapi_tenant_servers", "fabricapi_gpu_allocations", "fabricapi_vf_assign",
		"fabricapi_tenant_gpus", "fabricapi_vpcpeering", "fabricapi_auth_logout", "fabricapi_fabric",
		"fabricapi_fabric_deploy", "fabricapi_inventory_sync",
	} {
		if !containsString(resources, want) {
			t.Errorf("existing resource %s is no longer registered", want)
		}
	}
	for _, want := range []string{"fabricapi_tenants", "fabricapi_available_servers", "fabricapi_vf_interfaces", "fabricapi_fabric_yaml"} {
		if !containsString(dataSources, want) {
			t.Errorf("existing data source %s is no longer registered", want)
		}
	}
	for kind, names := range map[string][]string{"resource": resources, "data source": dataSources} {
		seen := map[string]bool{}
		for _, n := range names {
			if seen[n] {
				t.Errorf("duplicate %s type name %s", kind, n)
			}
			seen[n] = true
		}
	}
}
