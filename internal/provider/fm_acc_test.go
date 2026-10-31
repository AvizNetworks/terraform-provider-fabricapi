package provider

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Acceptance tests for the Fabric Manager 4.4 APIs against a live ONES/FM.
// They run only with TF_ACC=1 and the provider's usual environment:
//
//	FABRIC_API_ENDPOINT        Fabric Manager, e.g. https://<ones-host>:8443
//	FABRIC_API_AUTH_ENDPOINT   ONES login base, e.g. https://<ones-host>/api/user
//	FABRIC_API_USERNAME / FABRIC_API_PASSWORD (or FABRIC_API_ACCESS_TOKEN)
//	FABRICAPI_INSECURE_TLS=true for self-signed certificates
//	FABRIC_NAME                a fabric with External tenant control (VXLAN for DHCP)
//
// Without anything else they assume a fabric that is not deployed — no
// tenants, no DHCP server, no rails — and check the full lifecycle of
// everything that works there (webhooks, IPAM config, policy listing) plus
// Fabric Manager's real error responses for the rest. On a deployed fabric,
// set any of these to switch the corresponding success-path tests on:
//
//	TFACC_TENANT_A, TFACC_TENANT_B   two existing tenants (network policy lifecycle)
//	TFACC_SERVICE_ENDPOINTS          comma-separated endpoints in TENANT_B for the policy (default 192.2.127.129/32,192.2.127.130/32)
//	TFACC_SERVER                     a compute server in the fabric inventory (IPAM address queries)
//	TFACC_DHCP_TENANT                a tenant DHCP applies to (DHCP read/retry/reconcile)
//	TFACC_RAIL                       a rail name with a CIDRPool (rail data sources)
//
// Every resource the tests create is destroyed at the end of its test.

func accPreCheck(t *testing.T) {
	t.Helper()
	for _, v := range []string{"FABRIC_API_ENDPOINT", "FABRIC_NAME"} {
		if os.Getenv(v) == "" {
			t.Fatalf("%s must be set for acceptance tests", v)
		}
	}
	if os.Getenv("FABRIC_API_ACCESS_TOKEN") == "" && (os.Getenv("FABRIC_API_USERNAME") == "" || os.Getenv("FABRIC_API_PASSWORD") == "") {
		t.Fatal("set FABRIC_API_ACCESS_TOKEN or FABRIC_API_USERNAME/FABRIC_API_PASSWORD for acceptance tests")
	}
}

func accProviderFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"fabricapi": providerserver.NewProtocol6WithError(New("acc")()),
	}
}

const accProvider = `provider "fabricapi" {}
`

// accClient is a direct API client for CheckDestroy, built from the same
// environment the provider reads.
func accClient(t *testing.T) *APIClient {
	auth := os.Getenv("FABRIC_API_AUTH_ENDPOINT")
	if auth == "" {
		auth = os.Getenv("FABRIC_API_ENDPOINT")
	}
	return &APIClient{
		Endpoint:     os.Getenv("FABRIC_API_ENDPOINT"),
		Fabric:       os.Getenv("FABRIC_NAME"),
		AuthEndpoint: auth,
		Token:        strings.TrimSpace(os.Getenv("FABRIC_API_ACCESS_TOKEN")),
		Username:     os.Getenv("FABRIC_API_USERNAME"),
		Password:     os.Getenv("FABRIC_API_PASSWORD"),
		InsecureTLS:  parseBoolEnvDefaultFalse(os.Getenv("FABRICAPI_INSECURE_TLS")),
	}
}

func accName(suffix string) string {
	return "tfacc-" + suffix
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func TestAccWebhookResource(t *testing.T) {
	addr := "fabricapi_webhook.g"
	url := "https://hooks.example.com/" + accName("global")
	fabric := os.Getenv("FABRIC_NAME")
	cfg := func(enabled bool, events string, filter bool) string {
		f := ""
		if filter {
			f = fmt.Sprintf("filter_fabric_names = [%q]", fabric)
		}
		return accProvider + fmt.Sprintf(`
resource "fabricapi_webhook" "g" {
  callback_url = %q
  event_types  = [%s]
  enabled      = %t
  %s
}
data "fabricapi_webhooks" "all" { depends_on = [fabricapi_webhook.g] }
`, url, events, enabled, f)
	}
	var id string
	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { accPreCheck(t) },
		ProtoV6ProviderFactories: accProviderFactories(),
		Steps: []tfresource.TestStep{
			{
				Config: cfg(false, `"fabric.deploy", "tenant.create"`, true),
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(addr, "enabled", "false"),
					tfresource.TestCheckResourceAttr(addr, "event_types.#", "2"),
					tfresource.TestCheckResourceAttr(addr, "filter_fabric_names.0", fabric),
					tfresource.TestCheckResourceAttrSet(addr, "webhook_id"),
					func(s *terraform.State) error {
						id = s.RootModule().Resources[addr].Primary.Attributes["webhook_id"]
						return nil
					},
					tfresource.TestCheckTypeSetElemNestedAttrs("data.fabricapi_webhooks.all", "webhooks.*", map[string]string{"callback_url": url, "enabled": "false"}),
				),
			},
			{
				Config: cfg(true, `"tenant.delete"`, false),
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate)},
				},
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(addr, "enabled", "true"),
					tfresource.TestCheckResourceAttr(addr, "event_types.0", "tenant.delete"),
					tfresource.TestCheckResourceAttr(addr, "filter_fabric_names.#", "0"),
				),
			},
			{ResourceName: addr, ImportState: true, ImportStateVerify: true},
		},
		CheckDestroy: func(*terraform.State) error {
			if _, err := accClient(t).GetGlobalWebhook(context.Background(), id); !isFMNotFound(err) {
				return fmt.Errorf("global webhook %s still exists (err=%v)", id, err)
			}
			return nil
		},
	})
}

func TestAccWebhookResource_RejectsLoopbackURL(t *testing.T) {
	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { accPreCheck(t) },
		ProtoV6ProviderFactories: accProviderFactories(),
		Steps: []tfresource.TestStep{{
			Config: accProvider + `
resource "fabricapi_webhook" "bad" {
  callback_url = "https://127.0.0.1/hook"
  event_types  = ["fabric.deploy"]
}`,
			ExpectError: regexp.MustCompile(`(?s)API returned 400.*loopback`),
		}},
	})
}

func TestAccScopedWebhookResources(t *testing.T) {
	fabric := os.Getenv("FABRIC_NAME")
	tenant := envOr("TFACC_TENANT_A", accName("t1"))
	cfg := func(enabled bool, events string) string {
		return accProvider + fmt.Sprintf(`
resource "fabricapi_fabric_webhook" "f" {
  callback_url = "https://hooks.example.com/%s"
  enabled      = %[2]t
  %[3]s
}
resource "fabricapi_tenant_webhook" "t" {
  tenant_name  = %[4]q
  callback_url = "https://hooks.example.com/%[5]s"
  enabled      = %[2]t
}
`, accName("fabric"), enabled, events, tenant, accName("tenant"))
	}
	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { accPreCheck(t) },
		ProtoV6ProviderFactories: accProviderFactories(),
		Steps: []tfresource.TestStep{
			{
				Config: cfg(false, ""),
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr("fabricapi_fabric_webhook.f", "enabled", "false"),
					tfresource.TestCheckNoResourceAttr("fabricapi_fabric_webhook.f", "event_types"),
					tfresource.TestCheckResourceAttr("fabricapi_fabric_webhook.f", "id", fabric),
					tfresource.TestCheckResourceAttr("fabricapi_tenant_webhook.t", "id", fabric+"/"+tenant),
					tfresource.TestCheckResourceAttr("fabricapi_tenant_webhook.t", "enabled", "false"),
				),
			},
			{
				Config: cfg(true, `event_types = ["fabric.delete"]`),
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr("fabricapi_fabric_webhook.f", "enabled", "true"),
					tfresource.TestCheckResourceAttr("fabricapi_fabric_webhook.f", "event_types.0", "fabric.delete"),
				),
			},
			{ResourceName: "fabricapi_fabric_webhook.f", ImportState: true, ImportStateId: fabric, ImportStateVerify: true},
			{ResourceName: "fabricapi_tenant_webhook.t", ImportState: true, ImportStateId: fabric + "/" + tenant, ImportStateVerify: true},
		},
		CheckDestroy: func(*terraform.State) error {
			c := accClient(t)
			if _, err := c.GetScopedWebhook(context.Background(), fabric, ""); !isFMNotFound(err) {
				return fmt.Errorf("fabric webhook still exists (err=%v)", err)
			}
			if _, err := c.GetScopedWebhook(context.Background(), fabric, tenant); !isFMNotFound(err) {
				return fmt.Errorf("tenant webhook still exists (err=%v)", err)
			}
			return nil
		},
	})
}

func TestAccIpamConfigResource_Static(t *testing.T) {
	addr := "fabricapi_ipam_config.i"
	fabric := os.Getenv("FABRIC_NAME")
	cfg := func(assign bool) string {
		return accProvider + fmt.Sprintf(`
resource "fabricapi_ipam_config" "i" {
  mode             = "static"
  enable_ip_assign = %t
}
data "fabricapi_ipam_config" "i" { depends_on = [fabricapi_ipam_config.i] }
`, assign)
	}
	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { accPreCheck(t) },
		ProtoV6ProviderFactories: accProviderFactories(),
		Steps: []tfresource.TestStep{
			{
				Config: cfg(false),
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(addr, "mode", "static"),
					tfresource.TestCheckResourceAttr("data.fabricapi_ipam_config.i", "enable_ipam", "true"),
					tfresource.TestCheckResourceAttr("data.fabricapi_ipam_config.i", "mode", "static"),
				),
			},
			{
				Config: cfg(true),
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate)},
				},
				Check: tfresource.TestCheckResourceAttr("data.fabricapi_ipam_config.i", "enable_ip_assign", "true"),
			},
			{ResourceName: addr, ImportState: true, ImportStateId: fabric, ImportStateVerify: true, ImportStateVerifyIgnore: []string{"force"}},
		},
		CheckDestroy: func(*terraform.State) error {
			cfg, err := accClient(t).GetIpamConfig(context.Background(), fabric)
			if err != nil {
				return err
			}
			if cfg.EnableIpam {
				return fmt.Errorf("IPAM still enabled on %s after destroy", fabric)
			}
			return nil
		},
	})
}

func TestAccIpamConfigResource_Dhcp(t *testing.T) {
	step := tfresource.TestStep{Config: accProvider + `
resource "fabricapi_ipam_config" "i" {
  mode               = "dhcp"
  lease_time_seconds = 600
}`}
	if os.Getenv("TFACC_DHCP_TENANT") == "" {
		// No Kea server for the fabric (or Kea disabled for the whole
		// deployment, DHCP_DISABLED): FM refuses DHCP mode.
		step.ExpectError = regexp.MustCompile(`DHCP_DISABLED|DHCP_SERVER_NOT_CONFIGURED|DHCP_NODE_NOT_DECLARED|DHCP_FABRIC_TYPE_UNSUPPORTED`)
	} else {
		step.Check = tfresource.TestCheckResourceAttr("fabricapi_ipam_config.i", "lease_time_seconds", "600")
	}
	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { accPreCheck(t) },
		ProtoV6ProviderFactories: accProviderFactories(),
		Steps:                    []tfresource.TestStep{step},
	})
}

func TestAccNetworkPolicy(t *testing.T) {
	tenantA, tenantB := os.Getenv("TFACC_TENANT_A"), os.Getenv("TFACC_TENANT_B")
	listStep := tfresource.TestStep{
		Config: accProvider + `data "fabricapi_network_policies" "all" {}`,
		Check:  tfresource.TestCheckResourceAttrSet("data.fabricapi_network_policies.all", "id"),
	}
	if tenantA == "" || tenantB == "" {
		// Undeployed fabric: no intent and no tenants, so FM rejects the policy
		// (as a 500).
		tfresource.Test(t, tfresource.TestCase{
			PreCheck:                 func() { accPreCheck(t) },
			ProtoV6ProviderFactories: accProviderFactories(),
			Steps: []tfresource.TestStep{
				listStep,
				{
					Config: accProvider + fmt.Sprintf(`
resource "fabricapi_network_policy" "p" {
  name        = %q
  source      = { type = "tenant", tenant_name = %q }
  destination = { type = "service", tenant_name = %q, endpoints = ["192.2.127.129/32"] }
}`, accName("np"), accName("missing-a"), accName("missing-b")),
					ExpectError: regexp.MustCompile(`(?s)API returned 500 NETWORK_POLICY_CREATION_FAILED.*(not\s+found|does\s+not\s+have\s+an\s+associated\s+intent)`),
				},
			},
		})
		return
	}

	addr := "fabricapi_network_policy.p"
	endpoints := strings.Split(envOr("TFACC_SERVICE_ENDPOINTS", "192.2.127.129/32,192.2.127.130/32"), ",")
	cfg := func(eps []string) string {
		return accProvider + fmt.Sprintf(`
resource "fabricapi_network_policy" "p" {
  name        = %q
  source      = { type = "tenant", tenant_name = %q }
  destination = { type = "service", tenant_name = %q, service_name = "tfacc", endpoints = ["%s"] }
}
data "fabricapi_network_policies" "all" { depends_on = [fabricapi_network_policy.p] }
`, accName("np"), tenantA, tenantB, strings.Join(eps, `", "`))
	}
	fabric := os.Getenv("FABRIC_NAME")
	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { accPreCheck(t) },
		ProtoV6ProviderFactories: accProviderFactories(),
		Steps: []tfresource.TestStep{
			{
				Config: cfg(endpoints[:1]),
				Check: tfresource.TestCheckTypeSetElemNestedAttrs("data.fabricapi_network_policies.all", "policies.*",
					map[string]string{"name": accName("np")}),
			},
			{
				Config: cfg(endpoints),
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionUpdate)},
				},
				Check: tfresource.TestCheckResourceAttr(addr, "destination.endpoints.#", fmt.Sprint(len(endpoints))),
			},
			{ResourceName: addr, ImportState: true, ImportStateId: fabric + "/" + accName("np"), ImportStateVerify: true},
		},
		CheckDestroy: func(*terraform.State) error {
			p, err := accClient(t).FindNetworkPolicy(context.Background(), fabric, accName("np"))
			if err != nil {
				return err
			}
			if p != nil {
				return fmt.Errorf("network policy %s still exists", accName("np"))
			}
			return nil
		},
	})
}

func TestAccIpamAddressDataSources(t *testing.T) {
	server, tenant := os.Getenv("TFACC_SERVER"), os.Getenv("TFACC_TENANT_A")
	var steps []tfresource.TestStep
	if server == "" {
		steps = append(steps,
			tfresource.TestStep{
				Config:      accProvider + fmt.Sprintf(`data "fabricapi_server_ip_addresses" "s" { server_name = %q }`, accName("no-server")),
				ExpectError: regexp.MustCompile(`404 SERVER_NOT_FOUND`),
			},
			tfresource.TestStep{
				Config: accProvider + fmt.Sprintf(`data "fabricapi_tenant_compute_ip" "c" {
  tenant_name = %q
  server_name = %q
}`, accName("no-tenant"), accName("no-server")),
				ExpectError: regexp.MustCompile(`404 IPAM_NOT_CONFIGURED`),
			},
			tfresource.TestStep{
				Config:      accProvider + fmt.Sprintf(`data "fabricapi_tenant_ip_addresses" "t" { tenant_name = %q }`, accName("no-tenant")),
				ExpectError: regexp.MustCompile(`404 IPAM_NOT_CONFIGURED`),
			},
		)
	} else {
		steps = append(steps, tfresource.TestStep{
			Config: accProvider + fmt.Sprintf(`
data "fabricapi_server_ip_addresses" "s" { server_name = %q }
data "fabricapi_tenant_ip_addresses" "t" { tenant_name = %q }
`, server, tenant),
			Check: tfresource.ComposeAggregateTestCheckFunc(
				tfresource.TestCheckResourceAttrSet("data.fabricapi_server_ip_addresses.s", "allocations.#"),
				tfresource.TestCheckResourceAttrSet("data.fabricapi_tenant_ip_addresses.t", "pool_cidr"),
			),
		})
	}
	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { accPreCheck(t) },
		ProtoV6ProviderFactories: accProviderFactories(),
		Steps:                    steps,
	})
}

func TestAccDhcp(t *testing.T) {
	tenant := os.Getenv("TFACC_DHCP_TENANT")
	var steps []tfresource.TestStep
	if tenant == "" {
		steps = []tfresource.TestStep{
			{
				Config:      accProvider + fmt.Sprintf(`data "fabricapi_tenant_dhcp" "d" { tenant_name = %q }`, accName("no-tenant")),
				ExpectError: regexp.MustCompile(`404 DHCP_SUBNET_NOT_FOUND`),
			},
			{
				Config:      accProvider + fmt.Sprintf(`resource "fabricapi_tenant_dhcp_retry" "r" { tenant_name = %q }`, accName("no-tenant")),
				ExpectError: regexp.MustCompile(`404 DHCP_SUBNET_NOT_FOUND`),
			},
			{
				Config:      accProvider + `resource "fabricapi_dhcp_reconcile" "r" {}`,
				ExpectError: regexp.MustCompile(`502 (DHCP_SERVER_NOT_CONFIGURED|DHCP_DISABLED)`),
			},
		}
	} else {
		steps = []tfresource.TestStep{{
			Config: accProvider + fmt.Sprintf(`
data "fabricapi_tenant_dhcp" "d" { tenant_name = %[1]q }
resource "fabricapi_tenant_dhcp_retry" "r" { tenant_name = %[1]q }
resource "fabricapi_dhcp_reconcile" "r" {}
`, tenant),
			Check: tfresource.ComposeAggregateTestCheckFunc(
				tfresource.TestCheckResourceAttrSet("data.fabricapi_tenant_dhcp.d", "subnet.subnet_cidr"),
				tfresource.TestCheckResourceAttrSet("fabricapi_tenant_dhcp_retry.r", "summary"),
				tfresource.TestCheckResourceAttrSet("fabricapi_dhcp_reconcile.r", "summary"),
			),
		}}
	}
	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { accPreCheck(t) },
		ProtoV6ProviderFactories: accProviderFactories(),
		Steps:                    steps,
	})
}

func TestAccRailCidrPools(t *testing.T) {
	rail := os.Getenv("TFACC_RAIL")
	var steps []tfresource.TestStep
	if rail == "" {
		steps = []tfresource.TestStep{
			{
				Config:      accProvider + `data "fabricapi_rail_cidr_pools" "r" {}`,
				ExpectError: regexp.MustCompile(`404 RAIL_CIDRPOOL_NOT_FOUND`),
			},
			{
				Config:      accProvider + `data "fabricapi_rail_cidr_pool" "r" { rail_name = "r0" }`,
				ExpectError: regexp.MustCompile(`404 INVALID_RAIL`),
			},
		}
	} else {
		steps = []tfresource.TestStep{{
			Config: accProvider + fmt.Sprintf(`
data "fabricapi_rail_cidr_pools" "r" {}
data "fabricapi_rail_cidr_pool" "one" { rail_name = %q }
`, rail),
			Check: tfresource.ComposeAggregateTestCheckFunc(
				tfresource.TestCheckResourceAttrSet("data.fabricapi_rail_cidr_pools.r", "yaml"),
				tfresource.TestCheckTypeSetElemAttr("data.fabricapi_rail_cidr_pools.r", "rails.*", rail),
				tfresource.TestCheckResourceAttrSet("data.fabricapi_rail_cidr_pool.one", "yaml"),
			),
		}}
	}
	tfresource.Test(t, tfresource.TestCase{
		PreCheck:                 func() { accPreCheck(t) },
		ProtoV6ProviderFactories: accProviderFactories(),
		Steps:                    steps,
	})
}
