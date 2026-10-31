package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testClient(f *fakeFM) *APIClient {
	return &APIClient{Endpoint: f.srv.URL, Fabric: "fab1", Token: "test-token"}
}

func TestParseFMError_Shapes(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		body       string
		wantCode   string
		wantMsg    string
		wantString string
	}{
		{
			name:     "ApiResponse envelope",
			status:   404,
			body:     `{"status":"error","message":"Specified fabric does not exist.","error":{"code":"FABRIC_NOT_FOUND","detail":"Specified fabric does not exist."}}`,
			wantCode: "FABRIC_NOT_FOUND", wantMsg: "Specified fabric does not exist.",
			wantString: "API returned 404 FABRIC_NOT_FOUND: Specified fabric does not exist.",
		},
		{
			name:   "webhook plain error",
			status: 400, body: `{"error":"callbackUrl is required"}`,
			wantMsg: "callbackUrl is required", wantString: "API returned 400: callbackUrl is required",
		},
		{
			name:   "network policy list error",
			status: 404, body: `{"status":"error","error":"Fabric 'x' not found."}`,
			wantMsg: "Fabric 'x' not found.",
		},
		{
			name:     "permission interceptor",
			status:   403,
			body:     `{"errorCode":"PERMISSION_DENIED","message":"Access denied. Required permission: fabric.tenancy"}`,
			wantCode: "PERMISSION_DENIED", wantMsg: "Access denied. Required permission: fabric.tenancy",
		},
		{
			name:    "licence check plain text",
			status:  403,
			body:    "[createNetworkPolicy] Operation blocked: the following device(s) in fabric 'f' are not in LICENSED state: [10.0.0.1]",
			wantMsg: "[createNetworkPolicy] Operation blocked: the following device(s) in fabric 'f' are not in LICENSED state: [10.0.0.1]",
		},
		{
			name: "empty body", status: 502, body: "",
			wantString: "API returned 502: Bad Gateway",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := parseFMError(tc.status, []byte(tc.body))
			if e.StatusCode != tc.status || e.Code != tc.wantCode {
				t.Fatalf("got status=%d code=%q, want %d %q", e.StatusCode, e.Code, tc.status, tc.wantCode)
			}
			if tc.wantMsg != "" && e.Message != tc.wantMsg {
				t.Fatalf("message = %q, want %q", e.Message, tc.wantMsg)
			}
			if tc.wantString != "" && e.Error() != tc.wantString {
				t.Fatalf("Error() = %q, want %q", e.Error(), tc.wantString)
			}
		})
	}
}

func TestDoFM_GatewayHTMLIsAnError(t *testing.T) {
	// The ONES gateway answers routes it doesn't proxy with the UI's index.html
	// and HTTP 200; that must not be mistaken for success.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<!DOCTYPE html>\n<html lang=\"en\"><head></head></html>")
	}))
	defer srv.Close()
	c := &APIClient{Endpoint: srv.URL, Token: "t"}
	_, err := c.ListGlobalWebhooks(context.Background())
	if err == nil || !strings.Contains(err.Error(), "HTML page") || !strings.Contains(err.Error(), ":8443") {
		t.Fatalf("expected an HTML/gateway error pointing at :8443, got %v", err)
	}
}

func TestDoFM_SendsBearerTokenAndAccept(t *testing.T) {
	var gotAuth, gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotAccept = r.Header.Get("Authorization"), r.Header.Get("Accept")
		fmt.Fprint(w, `[]`)
	}))
	defer srv.Close()
	c := &APIClient{Endpoint: srv.URL + "/", Token: "abc"}
	if _, err := c.ListGlobalWebhooks(context.Background()); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer abc" || gotAccept != "application/json" {
		t.Fatalf("Authorization=%q Accept=%q", gotAuth, gotAccept)
	}
}

func TestFmPath_EscapesNames(t *testing.T) {
	got := fmPath("fabrics", "fab 1", "tenants", "a/b", "webhook")
	if got != "fabrics/fab%201/tenants/a%2Fb/webhook" {
		t.Fatalf("fmPath = %q", got)
	}
}

func TestIsFMNotFound(t *testing.T) {
	if !isFMNotFound(fmt.Errorf("wrapped: %w", &FMAPIError{StatusCode: 404})) {
		t.Fatal("wrapped 404 not detected")
	}
	if isFMNotFound(&FMAPIError{StatusCode: 500}) || isFMNotFound(errors.New("x")) || isFMNotFound(nil) {
		t.Fatal("false positive")
	}
}

func TestDeleteNetworkPolicy_MissingPolicyIsNotAnError(t *testing.T) {
	f := newFakeFM(t)
	c := testClient(f)
	// FM answers a missing policy with HTTP 500 "does not exist".
	deleted, err := c.DeleteNetworkPolicy(context.Background(), "fab1", "nope")
	if err != nil || deleted {
		t.Fatalf("deleted=%v err=%v, want false nil", deleted, err)
	}
	// Unknown fabric: 404.
	deleted, err = c.DeleteNetworkPolicy(context.Background(), "nofab", "nope")
	if err != nil || deleted {
		t.Fatalf("unknown fabric: deleted=%v err=%v", deleted, err)
	}
	// Any other 500 is a real failure.
	f.override(http.MethodDelete, "/fabrics/fab1/networkpolicy/p1", 500,
		`{"status":"error","message":"10.0.0.1: timeout","error":{"code":"NETWORK_POLICY_DELETION_FAILED","detail":"10.0.0.1: timeout"}}`)
	if _, err := c.DeleteNetworkPolicy(context.Background(), "fab1", "p1"); err == nil || !strings.Contains(err.Error(), "10.0.0.1: timeout") {
		t.Fatalf("expected device failure, got %v", err)
	}
}

func TestPatchNetworkPolicy_SendsNameInBody(t *testing.T) {
	f := newFakeFM(t)
	c := testClient(f)
	p := newNetworkPolicy("np1",
		NetworkPolicySide{Type: "tenant", TenantName: "A"},
		NetworkPolicySide{Type: "service", TenantName: "B", Endpoints: []string{"10.0.0.1/32"}})
	if err := c.CreateNetworkPolicy(context.Background(), "fab1", p); err != nil {
		t.Fatal(err)
	}
	add := newNetworkPolicy("np1",
		NetworkPolicySide{Type: "tenant", TenantName: "A"},
		NetworkPolicySide{Type: "service", TenantName: "B", Endpoints: []string{"10.0.0.2/32"}})
	if err := c.PatchNetworkPolicy(context.Background(), "fab1", add); err != nil {
		t.Fatal(err)
	}
	body := f.lastCall(http.MethodPatch, "/fabrics/fab1/networkpolicy/np1").Body
	if body["policy"].(map[string]any)["name"] != "np1" {
		t.Fatalf("policy.name missing from PATCH body: %v", body)
	}
	got, _ := c.FindNetworkPolicy(context.Background(), "fab1", "np1")
	if got == nil || strings.Join(got.Spec.Destination.Endpoints, ",") != "10.0.0.1/32,10.0.0.2/32" {
		t.Fatalf("policy after patch = %+v", got)
	}
	if none, err := c.FindNetworkPolicy(context.Background(), "fab1", "other"); err != nil || none != nil {
		t.Fatalf("FindNetworkPolicy(other) = %v, %v", none, err)
	}
}

func TestUpdateIpamConfig_DropsFabricNameAndSendsForce(t *testing.T) {
	f := newFakeFM(t)
	c := testClient(f)
	assign := true
	cfg, err := c.UpdateIpamConfig(context.Background(), "fab1",
		IpamConfig{FabricName: "stale", EnableIpam: true, Static: &IpamStaticConfig{Enabled: true, EnableIPAssign: &assign}}, true)
	if err != nil {
		t.Fatal(err)
	}
	req := f.lastCall(http.MethodPatch, "/fabrics/fab1/ipam/config")
	if _, ok := req.Body["fabricName"]; ok {
		t.Fatalf("fabricName must not be sent: %s", req.Raw)
	}
	if req.Query != "force=true" {
		t.Fatalf("query = %q", req.Query)
	}
	if !cfg.EnableIpam || cfg.Static == nil || !*cfg.Static.EnableIPAssign {
		t.Fatalf("response not decoded: %+v", cfg)
	}
}

func TestScopedWebhookRequest_AlwaysSendsEnabledAndEventTypes(t *testing.T) {
	// FM's PUT reads a missing `enabled` as true and a null eventTypes as
	// "unchanged", so both must always be on the wire.
	b, _ := json.Marshal(ScopedWebhookRequest{CallbackURL: "https://x", Enabled: false, EventTypes: []string{}})
	if string(b) != `{"callbackUrl":"https://x","enabled":false,"eventTypes":[]}` {
		t.Fatalf("body = %s", b)
	}
	// Global PATCH: empty (non-nil) filter lists clear, so they must serialise as [].
	b, _ = json.Marshal(GlobalWebhookRequest{Filters: &WebhookFilters{FabricNames: []string{}, TenantNames: []string{}}})
	if string(b) != `{"filters":{"fabricNames":[],"tenantNames":[]}}` {
		t.Fatalf("body = %s", b)
	}
}

func TestRegisterGlobalWebhook_ReportsCreatedVsExisting(t *testing.T) {
	f := newFakeFM(t)
	c := testClient(f)
	url := "https://hooks.example.com/a"
	req := GlobalWebhookRequest{CallbackURL: &url, EventTypes: []string{"fabric.deploy"}}
	reg, created, err := c.RegisterGlobalWebhook(context.Background(), req)
	if err != nil || !created || reg.WebhookID == "" {
		t.Fatalf("first register: %+v created=%v err=%v", reg, created, err)
	}
	reg2, created, err := c.RegisterGlobalWebhook(context.Background(), req)
	if err != nil || created || reg2.WebhookID != reg.WebhookID {
		t.Fatalf("second register should reuse %s: %+v created=%v err=%v", reg.WebhookID, reg2, created, err)
	}
	if err := c.DeleteGlobalWebhook(context.Background(), reg.WebhookID); err != nil {
		t.Fatal(err)
	}
	// Deleting again (404) is treated as already gone.
	if err := c.DeleteGlobalWebhook(context.Background(), reg.WebhookID); err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if _, err := c.GetGlobalWebhook(context.Background(), reg.WebhookID); !isFMNotFound(err) {
		t.Fatalf("get after delete: %v", err)
	}
}

func TestGetAllTenantIPAddresses_WalksEveryPage(t *testing.T) {
	f := newFakeFM(t)
	var all []IpamAllocation
	for i := 0; i < 1203; i++ {
		st := "ASSIGNED"
		if i%2 == 1 {
			st = "ALLOCATED_ONLY"
		}
		all = append(all, IpamAllocation{ID: int64(i), IPAddress: fmt.Sprintf("172.1.%d.%d", i/250, i%250), AssignStatus: st})
	}
	f.tenantIPs["fab1/t1"] = all
	c := testClient(f)
	res, err := c.GetAllTenantIPAddresses(context.Background(), "fab1", "t1", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Allocations) != 1203 || res.Pool.Cidr != "172.1.0.0/18" {
		t.Fatalf("got %d allocations, pool %q", len(res.Allocations), res.Pool.Cidr)
	}
	if n := len(f.calls(http.MethodGet, "/fabrics/fab1/tenants/t1/ip-addresses")); n != 3 {
		t.Fatalf("expected 3 page requests, got %d", n)
	}
	filtered, err := c.GetAllTenantIPAddresses(context.Background(), "fab1", "t1", "ASSIGNED")
	if err != nil || len(filtered.Allocations) != 602 {
		t.Fatalf("status filter: %d allocations, err %v", len(filtered.Allocations), err)
	}
	if q := f.lastCall(http.MethodGet, "/fabrics/fab1/tenants/t1/ip-addresses").Query; !strings.Contains(q, "status=ASSIGNED") {
		t.Fatalf("status not sent: %q", q)
	}
	if _, err := c.GetAllTenantIPAddresses(context.Background(), "fab1", "missing", ""); err == nil || !strings.Contains(err.Error(), "IPAM_NOT_CONFIGURED") {
		t.Fatalf("expected IPAM_NOT_CONFIGURED, got %v", err)
	}
}

func TestGetTenantDhcp_WalksLeasePagesAndHandlesKeaDown(t *testing.T) {
	f := newFakeFM(t)
	var leases []DhcpLease
	for i := 0; i < 1500; i++ {
		leases = append(leases, DhcpLease{IPAddress: fmt.Sprintf("172.1.64.%d", i), State: "ASSIGNED"})
	}
	f.leases["fab1/t1"] = leases
	c := testClient(f)
	res, err := c.GetTenantDhcp(context.Background(), "fab1", "t1")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Leases) != 1500 || res.Subnet.VlanID != 100 || res.LiveInKea == nil || !*res.LiveInKea {
		t.Fatalf("unexpected result: %d leases, subnet %+v", len(res.Leases), res.Subnet)
	}
	if q := f.lastCall(http.MethodGet, "/fabrics/fab1/tenants/t1/dhcp").Query; !strings.Contains(q, "size=1000") {
		t.Fatalf("lease page size not capped at 1000: %q", q)
	}

	// Kea unreachable: FM still answers 200, with null leases and an error string.
	f.override(http.MethodGet, "/fabrics/fab1/tenants/t2/dhcp", 200,
		`{"status":"success","data":{"subnet":{"vlanId":7},"liveInKea":null,"liveCheckError":"connection refused","leases":null,"leaseCheckError":"connection refused"}}`)
	res, err = c.GetTenantDhcp(context.Background(), "fab1", "t2")
	if err != nil || res.LiveInKea != nil || res.Leases != nil || res.LeaseCheckError != "connection refused" {
		t.Fatalf("kea-down result: %+v err=%v", res, err)
	}
}

func TestDhcpSummary_StringOrObject(t *testing.T) {
	if got := dhcpSummary(json.RawMessage(`"fab1: CONFIG_SET OK"`)); got != "fab1: CONFIG_SET OK" {
		t.Fatalf("string: %q", got)
	}
	if got := dhcpSummary(json.RawMessage(`{"changed":true}`)); got != `{"changed":true}` {
		t.Fatalf("object: %q", got)
	}
	if got := dhcpSummary(json.RawMessage(`null`)); got != "" {
		t.Fatalf("null: %q", got)
	}
}

func TestReconcileFabricDhcp_ErrorsCarryFMCode(t *testing.T) {
	f := newFakeFM(t)
	f.override(http.MethodPost, "/fabrics/fab1/dhcp/reconcile", 502,
		`{"status":"error","message":"No DHCP server is configured for fabric fab1.","error":{"code":"DHCP_SERVER_NOT_CONFIGURED","detail":"No DHCP server is configured for fabric fab1."}}`)
	_, err := testClient(f).ReconcileFabricDhcp(context.Background(), "fab1")
	var apiErr *FMAPIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != 502 || apiErr.Code != "DHCP_SERVER_NOT_CONFIGURED" {
		t.Fatalf("got %v", err)
	}
}

func TestRailCidrPools_DecodeJSON(t *testing.T) {
	f := newFakeFM(t)
	f.override(http.MethodGet, "/fabrics/fab1/rails/cidrpools", 200,
		`{"status":"success","message":"Rail CIDRPools retrieved successfully.","data":{"fabricName":"fab1","rails":["r0","r1"],"yaml":"apiVersion: aviz/rails/\nkind: CIDRPool\n"}}`)
	f.override(http.MethodGet, "/fabrics/fab1/rails/r9/cidrpool", 404,
		`{"status":"error","message":"Rail 'r9' does not exist in fabric 'fab1'.","error":{"code":"INVALID_RAIL","detail":"Rail 'r9' does not exist in fabric 'fab1'."}}`)
	c := testClient(f)
	pools, err := c.GetRailCidrPools(context.Background(), "fab1")
	if err != nil || strings.Join(pools.Rails, ",") != "r0,r1" || !strings.HasPrefix(pools.YAML, "apiVersion") {
		t.Fatalf("pools = %+v, err %v", pools, err)
	}
	if req := f.lastCall(http.MethodGet, "/fabrics/fab1/rails/cidrpools"); req.Method != http.MethodGet {
		t.Fatal("unexpected")
	}
	if _, err := c.GetRailCidrPool(context.Background(), "fab1", "r9"); err == nil || !strings.Contains(err.Error(), "INVALID_RAIL") {
		t.Fatalf("expected INVALID_RAIL, got %v", err)
	}
}
