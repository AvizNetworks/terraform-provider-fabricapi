package provider

import (
	"fmt"
	"net/http"
	"regexp"
	"testing"

	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

// These tests drive the real Terraform CLI against fakeFM (see
// fm_fake_test.go's serveTopologyAudit/serveOperation), covering the
// GFX-6223 acceptance criteria: trigger on-demand, capture + poll the
// job id, surface PASS/FAIL/INCOMPLETE plus per-link detail, and never
// duplicate-run on refresh/plan.

func topologyAuditConfig(fabric, trigger, failOnNonPass string) string {
	extra := ""
	if fabric != "" {
		extra += fmt.Sprintf("\n  fabric_name = %q", fabric)
	}
	if failOnNonPass != "" {
		extra += fmt.Sprintf("\n  fail_on_non_pass = %s", failOnNonPass)
	}
	return fmt.Sprintf(`
resource "fabricapi_topology_audit" "a" {
  triggers = { run = %q }%s
}`, trigger, extra)
}

func TestTopologyAuditResource_PassRunsOnCreateAndNoOpOnRefresh(t *testing.T) {
	f := newFakeFM(t)
	f.queueTopologyAudit("fab1", fakeOperationResponse{
		Status: "SUCCESS",
		Result: `{"fabricName":"fab1","intentVersion":"1:test","overallStatus":"PASS",` +
			`"links":[{"device":"spine1","localInterface":"Ethernet1","remoteDevice":"leaf1",` +
			`"remoteInterface":"Ethernet1","observedRemoteDevice":"leaf1","observedRemoteInterface":"Ethernet1",` +
			`"status":"PASS","message":""}],"deviceIpMap":{}}`,
	})
	addr := "fabricapi_topology_audit.a"
	cfg := f.providerConfig() + topologyAuditConfig("", "1", "")

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{
			{
				Config: cfg,
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(addr, "fabric_name", "fab1"),
					tfresource.TestCheckResourceAttr(addr, "job_status", "SUCCESS"),
					tfresource.TestCheckResourceAttr(addr, "overall_status", "PASS"),
					tfresource.TestCheckResourceAttrSet(addr, "operation_id"),
					tfresource.TestMatchResourceAttr(addr, "discrepancies", regexp.MustCompile(`"device":"spine1"`)),
					checkCalls(f, http.MethodPost, "/fabrics/fab1/topology-audit", 1),
				),
			},
			// Refresh/plan with no config change must never re-trigger the audit.
			{Config: cfg, PlanOnly: true, Check: checkCalls(f, http.MethodPost, "/fabrics/fab1/topology-audit", 1)},
		},
	})
}

func TestTopologyAuditResource_TriggerChangeForcesRerun(t *testing.T) {
	f := newFakeFM(t)
	f.queueTopologyAudit("fab1", fakeOperationResponse{
		Status: "SUCCESS",
		Result: `{"fabricName":"fab1","overallStatus":"PASS","links":[],"deviceIpMap":{}}`,
	})
	addr := "fabricapi_topology_audit.a"

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{
			{
				Config: f.providerConfig() + topologyAuditConfig("", "1", ""),
				Check:  checkCalls(f, http.MethodPost, "/fabrics/fab1/topology-audit", 1),
			},
			{
				Config: f.providerConfig() + topologyAuditConfig("", "2", ""),
				ConfigPlanChecks: tfresource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionDestroyBeforeCreate)},
				},
				Check: checkCalls(f, http.MethodPost, "/fabrics/fab1/topology-audit", 2),
			},
		},
	})
}

func TestTopologyAuditResource_PollsAcrossPendingThenSuccess(t *testing.T) {
	f := newFakeFM(t)
	f.queueTopologyAudit("fab1",
		fakeOperationResponse{Status: "PENDING"},
		fakeOperationResponse{Status: "SUCCESS", Result: `{"fabricName":"fab1","overallStatus":"PASS","links":[],"deviceIpMap":{}}`},
	)
	addr := "fabricapi_topology_audit.a"

	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{
			{
				Config: f.providerConfig() + topologyAuditConfig("", "1", ""),
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(addr, "job_status", "SUCCESS"),
					tfresource.TestCheckResourceAttr(addr, "overall_status", "PASS"),
					checkCalls(f, http.MethodGet, "", 2),
				),
			},
		},
	})
}

func TestTopologyAuditResource_NonExistentFabric(t *testing.T) {
	f := newFakeFM(t)
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{
			{
				Config:      f.providerConfig() + topologyAuditConfig("nope", "1", ""),
				ExpectError: regexp.MustCompile(`404 INVALID_FABRIC`),
			},
		},
	})
}

func TestTopologyAuditResource_JobExecutionFailure(t *testing.T) {
	f := newFakeFM(t)
	f.queueTopologyAudit("fab1", fakeOperationResponse{
		Status:       "FAILED",
		ErrorMessage: "No intent found for fabric 'fab1'.",
	})
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{
			{
				Config:      f.providerConfig() + topologyAuditConfig("", "1", ""),
				ExpectError: regexp.MustCompile(`Topology audit job failed[\s\S]*No intent found for fabric`),
			},
		},
	})
}

func TestTopologyAuditResource_IncompleteSucceedsWithWarningByDefault(t *testing.T) {
	f := newFakeFM(t)
	f.queueTopologyAudit("fab1", fakeOperationResponse{
		Status: "SUCCESS",
		Result: `{"fabricName":"fab1","overallStatus":"INCOMPLETE",` +
			`"links":[{"device":"spine1","localInterface":"Ethernet1","status":"UNVERIFIABLE","message":"unreachable"}],"deviceIpMap":{}}`,
	})
	addr := "fabricapi_topology_audit.a"
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{
			{
				Config: f.providerConfig() + topologyAuditConfig("", "1", ""),
				Check: tfresource.ComposeAggregateTestCheckFunc(
					tfresource.TestCheckResourceAttr(addr, "job_status", "SUCCESS"),
					tfresource.TestCheckResourceAttr(addr, "overall_status", "INCOMPLETE"),
				),
			},
		},
	})
}

func TestTopologyAuditResource_FailOnNonPassHardFails(t *testing.T) {
	f := newFakeFM(t)
	f.queueTopologyAudit("fab1", fakeOperationResponse{
		Status: "SUCCESS",
		Result: `{"fabricName":"fab1","overallStatus":"FAIL",` +
			`"links":[{"device":"spine1","localInterface":"Ethernet1","status":"MISSING_LINK","message":"no neighbor seen"}],"deviceIpMap":{}}`,
	})
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: f.factories(),
		Steps: []tfresource.TestStep{
			{
				Config:      f.providerConfig() + topologyAuditConfig("", "1", "true"),
				ExpectError: regexp.MustCompile(`Topology audit did not pass[\s\S]*overall_status=FAIL`),
			},
		},
	})
}
