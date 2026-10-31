package provider

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

// fakeFM is an in-memory stand-in for the Fabric Manager 4.4 endpoints, used by
// the unit tests. It mirrors the real server's quirks that the provider has to
// work around (verified against SES_FM and a live FM 4.4):
//   - network policy: list is not enveloped, a missing policy is a 500, PATCH is add-only
//   - webhooks: register always stores enabled=true, PUT reads a missing enabled as true,
//     global register upserts on callbackUrl (201 new / 200 existing), 204 on delete
//   - IPAM config: PATCH replaces both modes, rejects a mismatched fabricName and a
//     direct static<->dhcp switch
//
// Every request is recorded so tests can assert on exactly what was sent.
type fakeFM struct {
	t   *testing.T
	srv *httptest.Server

	mu       sync.Mutex
	requests []fakeRequest

	fabrics  map[string]bool
	policies map[string][]NetworkPolicy // fabric -> policies
	global   map[string]*WebhookRegistration
	scoped   map[string]*WebhookRegistration // path -> registration
	ipam     map[string]IpamConfig
	nextID   int64

	// Optional overrides: return this status/body for "METHOD path".
	overrides map[string]fakeResponse

	// Paged data for the data source tests.
	tenantIPs map[string][]IpamAllocation // fabric/tenant -> allocations
	leases    map[string][]DhcpLease      // fabric/tenant -> leases
}

type fakeRequest struct {
	Method string
	Path   string
	Query  string
	Auth   string
	Body   map[string]any
	Raw    string
}

type fakeResponse struct {
	Status int
	Body   string
}

func newFakeFM(t *testing.T) *fakeFM {
	f := &fakeFM{
		t:         t,
		fabrics:   map[string]bool{"fab1": true},
		policies:  map[string][]NetworkPolicy{},
		global:    map[string]*WebhookRegistration{},
		scoped:    map[string]*WebhookRegistration{},
		ipam:      map[string]IpamConfig{},
		overrides: map[string]fakeResponse{},
		tenantIPs: map[string][]IpamAllocation{},
		leases:    map[string][]DhcpLease{},
	}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// providerConfig is an HCL provider block pointing at the fake, with a static
// token so no login is attempted.
func (f *fakeFM) providerConfig() string {
	return fmt.Sprintf(`
provider "fabricapi" {
  endpoint     = %q
  fabric       = "fab1"
  access_token = "test-token"
}
`, f.srv.URL)
}

func (f *fakeFM) factories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"fabricapi": providerserver.NewProtocol6WithError(New("test")()),
	}
}

func (f *fakeFM) override(method, path string, status int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.overrides[method+" "+path] = fakeResponse{status, body}
}

// calls returns the recorded requests matching method and path (path "" = any).
func (f *fakeFM) calls(method, path string) []fakeRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeRequest
	for _, r := range f.requests {
		if r.Method == method && (path == "" || r.Path == path) {
			out = append(out, r)
		}
	}
	return out
}

func (f *fakeFM) lastCall(method, path string) fakeRequest {
	c := f.calls(method, path)
	if len(c) == 0 {
		f.t.Fatalf("no %s %s request was made", method, path)
	}
	return c[len(c)-1]
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func envelopeOK(data any) map[string]any {
	return map[string]any{"status": "success", "message": "ok", "data": data}
}

func envelopeErr(code, msg string) map[string]any {
	return map[string]any{"status": "error", "message": msg, "error": map[string]any{"code": code, "detail": msg}}
}

func (f *fakeFM) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	req := fakeRequest{Method: r.Method, Path: r.URL.Path, Query: r.URL.RawQuery, Auth: r.Header.Get("Authorization"), Raw: string(raw)}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &req.Body)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, req)

	if o, ok := f.overrides[r.Method+" "+r.URL.Path]; ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(o.Status)
		_, _ = io.WriteString(w, o.Body)
		return
	}

	parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
	switch {
	case parts[0] == "webhooks":
		f.serveGlobalWebhooks(w, r, parts, raw)
	case len(parts) >= 3 && parts[0] == "fabrics" && parts[len(parts)-1] == "webhook":
		f.serveScopedWebhook(w, r, parts, raw)
	case len(parts) >= 3 && parts[0] == "fabrics" && parts[2] == "networkpolicy":
		f.serveNetworkPolicy(w, r, parts, raw)
	case len(parts) == 4 && parts[0] == "fabrics" && parts[2] == "ipam" && parts[3] == "config":
		f.serveIpamConfig(w, r, parts[1], raw)
	case len(parts) == 5 && parts[0] == "fabrics" && parts[2] == "tenants" && parts[4] == "ip-addresses":
		f.serveTenantIPs(w, r, parts[1], parts[3])
	case len(parts) == 5 && parts[0] == "fabrics" && parts[2] == "tenants" && parts[4] == "dhcp":
		f.serveTenantDhcp(w, r, parts[1], parts[3])
	default:
		writeJSON(w, http.StatusNotFound, envelopeErr("NOT_FOUND", "no fake route for "+r.Method+" "+r.URL.Path))
	}
}

// ------------------------------------------------------------ network policy

func (f *fakeFM) serveNetworkPolicy(w http.ResponseWriter, r *http.Request, parts []string, raw []byte) {
	fabric := parts[1]
	if !f.fabrics[fabric] {
		writeJSON(w, http.StatusNotFound, map[string]any{"status": "error", "error": fmt.Sprintf("Fabric '%s' not found.", fabric)})
		return
	}
	list := f.policies[fabric]
	find := func(name string) int {
		for i, p := range list {
			if p.Policy.Name == name {
				return i
			}
		}
		return -1
	}
	switch {
	case r.Method == http.MethodGet && len(parts) == 3:
		writeJSON(w, http.StatusOK, map[string]any{"status": "success", "fabricName": fabric, "count": len(list), "policies": list})
	case r.Method == http.MethodPost && len(parts) == 3:
		var p NetworkPolicy
		_ = json.Unmarshal(raw, &p)
		if find(p.Policy.Name) >= 0 {
			msg := fmt.Sprintf("Network policy '%s' already exists in fabric '%s'.", p.Policy.Name, fabric)
			writeJSON(w, http.StatusInternalServerError, envelopeErr("NETWORK_POLICY_CREATION_FAILED", msg))
			return
		}
		f.policies[fabric] = append(list, p)
		writeJSON(w, http.StatusOK, envelopeOK(map[string]any{"policyName": p.Policy.Name}))
	case r.Method == http.MethodPatch && len(parts) == 4:
		i := find(parts[3])
		if i < 0 {
			writeJSON(w, http.StatusInternalServerError, envelopeErr("NETWORK_POLICY_UPDATE_FAILED", fmt.Sprintf("Network policy '%s' not found in fabric '%s'.", parts[3], fabric)))
			return
		}
		var p NetworkPolicy
		_ = json.Unmarshal(raw, &p)
		if p.Policy.Name == "" {
			writeJSON(w, http.StatusInternalServerError, envelopeErr("NETWORK_POLICY_UPDATE_FAILED", "policy.name is required."))
			return
		}
		existing := &list[i]
		for _, e := range p.Spec.Source.Endpoints {
			if containsString(existing.Spec.Source.Endpoints, e) {
				writeJSON(w, http.StatusInternalServerError, envelopeErr("NETWORK_POLICY_UPDATE_FAILED", fmt.Sprintf("Endpoint '%s' is already part of network policy '%s'.", e, parts[3])))
				return
			}
		}
		for _, e := range p.Spec.Destination.Endpoints {
			if containsString(existing.Spec.Destination.Endpoints, e) {
				writeJSON(w, http.StatusInternalServerError, envelopeErr("NETWORK_POLICY_UPDATE_FAILED", fmt.Sprintf("Endpoint '%s' is already part of network policy '%s'.", e, parts[3])))
				return
			}
		}
		existing.Spec.Source.Endpoints = append(existing.Spec.Source.Endpoints, p.Spec.Source.Endpoints...)
		existing.Spec.Destination.Endpoints = append(existing.Spec.Destination.Endpoints, p.Spec.Destination.Endpoints...)
		writeJSON(w, http.StatusOK, envelopeOK(map[string]any{"policyName": parts[3]}))
	case r.Method == http.MethodDelete && len(parts) == 4:
		i := find(parts[3])
		if i < 0 {
			// FM reports a missing policy as a 500, not a 404.
			writeJSON(w, http.StatusInternalServerError, envelopeErr("NETWORK_POLICY_DELETION_FAILED", fmt.Sprintf("Network policy '%s' does not exist in fabric '%s'.", parts[3], fabric)))
			return
		}
		f.policies[fabric] = append(list[:i:i], list[i+1:]...)
		writeJSON(w, http.StatusOK, envelopeOK(map[string]any{"policyName": parts[3]}))
	default:
		writeJSON(w, http.StatusMethodNotAllowed, envelopeErr("METHOD", r.Method))
	}
}

// ------------------------------------------------------------------- webhooks

func (f *fakeFM) stamp(reg *WebhookRegistration) {
	f.nextID++
	if reg.CreatedAt == "" {
		reg.CreatedAt = "2026-10-26T10:00:00Z"
	}
	reg.UpdatedAt = fmt.Sprintf("2026-10-26T10:00:%02dZ", f.nextID%60)
}

func canonicalEvents(in []any) []string {
	out := []string{}
	for _, e := range in {
		s := strings.ToLower(strings.TrimSpace(fmt.Sprint(e)))
		s = strings.Replace(s, "fabric.tenant_", "tenant.", 1)
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func (f *fakeFM) serveGlobalWebhooks(w http.ResponseWriter, r *http.Request, parts []string, raw []byte) {
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	switch {
	case r.Method == http.MethodGet && len(parts) == 1:
		list := []*WebhookRegistration{}
		for _, reg := range f.global {
			list = append(list, reg)
		}
		sort.Slice(list, func(i, j int) bool { return list[i].WebhookID < list[j].WebhookID })
		writeJSON(w, http.StatusOK, list)
	case r.Method == http.MethodPost && len(parts) == 1:
		url, _ := body["callbackUrl"].(string)
		if url == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "callbackUrl is required"})
			return
		}
		evs, _ := body["eventTypes"].([]any)
		if len(evs) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "eventTypes is required and must be non-empty."})
			return
		}
		var existing *WebhookRegistration
		for _, reg := range f.global {
			if reg.CallbackURL == url {
				existing = reg
			}
		}
		status := http.StatusOK
		if existing == nil {
			f.nextID++
			existing = &WebhookRegistration{WebhookID: fmt.Sprintf("00000000-0000-0000-0000-%012d", f.nextID), CallbackURL: url}
			f.global[existing.WebhookID] = existing
			status = http.StatusCreated
		}
		existing.Enabled = true // FM ignores `enabled` on register
		existing.EventTypes = canonicalEvents(evs)
		existing.Filters = &WebhookFilters{FabricNames: []string{}, TenantNames: []string{}}
		if fl, ok := body["filters"].(map[string]any); ok {
			existing.Filters = filtersFrom(fl, existing.Filters)
		}
		f.stamp(existing)
		writeJSON(w, status, existing)
	case len(parts) == 2:
		reg, ok := f.global[parts[1]]
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "No global webhook registered with id: " + parts[1]})
			return
		}
		switch r.Method {
		case http.MethodGet:
			writeJSON(w, http.StatusOK, reg)
		case http.MethodPatch:
			if v, ok := body["callbackUrl"].(string); ok && v != "" {
				reg.CallbackURL = v
			}
			if v, ok := body["enabled"].(bool); ok {
				reg.Enabled = v
			}
			if v, ok := body["eventTypes"].([]any); ok {
				reg.EventTypes = canonicalEvents(v)
			}
			if fl, ok := body["filters"].(map[string]any); ok {
				reg.Filters = filtersFrom(fl, reg.Filters)
			}
			f.stamp(reg)
			writeJSON(w, http.StatusOK, reg)
		case http.MethodDelete:
			delete(f.global, parts[1])
			w.WriteHeader(http.StatusNoContent)
		}
	}
}

func filtersFrom(fl map[string]any, prev *WebhookFilters) *WebhookFilters {
	out := &WebhookFilters{FabricNames: []string{}, TenantNames: []string{}}
	if prev != nil {
		out.FabricNames, out.TenantNames = prev.FabricNames, prev.TenantNames
	}
	conv := func(v any) []string {
		items, _ := v.([]any)
		s := []string{}
		for _, it := range items {
			s = append(s, fmt.Sprint(it))
		}
		return s
	}
	if v, ok := fl["fabricNames"]; ok && v != nil {
		out.FabricNames = conv(v)
	}
	if v, ok := fl["tenantNames"]; ok && v != nil {
		out.TenantNames = conv(v)
	}
	return out
}

func (f *fakeFM) serveScopedWebhook(w http.ResponseWriter, r *http.Request, parts []string, raw []byte) {
	key := strings.Join(parts, "/")
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	notFound := func() {
		if len(parts) == 5 {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "No webhook registered for tenant: " + parts[3]})
		} else {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "No webhook registered for fabric: " + parts[1]})
		}
	}
	reg := f.scoped[key]
	switch r.Method {
	case http.MethodGet:
		if reg == nil {
			notFound()
			return
		}
		writeJSON(w, http.StatusOK, reg)
	case http.MethodPost:
		url, _ := body["callbackUrl"].(string)
		if url == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "callbackUrl is required"})
			return
		}
		if reg == nil {
			f.nextID++
			reg = &WebhookRegistration{ID: f.nextID, FabricName: parts[1]}
			if len(parts) == 5 {
				reg.TenantName = parts[3]
			}
			f.scoped[key] = reg
		}
		reg.CallbackURL = url
		reg.Enabled = true // FM always stores enabled=true on register
		reg.EventTypes = nil
		if evs, ok := body["eventTypes"].([]any); ok && len(evs) > 0 {
			reg.EventTypes = canonicalEvents(evs)
		}
		f.stamp(reg)
		writeJSON(w, http.StatusCreated, reg)
	case http.MethodPut:
		if reg == nil {
			notFound()
			return
		}
		if v, ok := body["callbackUrl"].(string); ok && v != "" {
			reg.CallbackURL = v
		}
		// FM's request DTO uses a primitive boolean: a missing value is true.
		enabled := true
		if v, ok := body["enabled"].(bool); ok {
			enabled = v
		}
		reg.Enabled = enabled
		if evs, ok := body["eventTypes"].([]any); ok {
			reg.EventTypes = nil
			if len(evs) > 0 {
				reg.EventTypes = canonicalEvents(evs)
			}
		}
		f.stamp(reg)
		writeJSON(w, http.StatusOK, reg)
	case http.MethodDelete:
		if reg == nil {
			notFound()
			return
		}
		delete(f.scoped, key)
		w.WriteHeader(http.StatusNoContent)
	}
}

// ------------------------------------------------------------------------ IPAM

func ipamDescribe(fabric string, c IpamConfig) map[string]any {
	static := map[string]any{"enabled": c.Static != nil && c.Static.Enabled}
	if c.Static != nil && c.Static.Enabled {
		assign := c.Static.EnableIPAssign != nil && *c.Static.EnableIPAssign
		static["enableIpAssign"] = assign
	}
	dhcp := map[string]any{"enabled": c.Dhcp != nil && c.Dhcp.Enabled}
	if c.Dhcp != nil && c.Dhcp.Enabled {
		lease := int64(60)
		if c.Dhcp.LeaseTimeSeconds != nil {
			lease = *c.Dhcp.LeaseTimeSeconds
		}
		dhcp["leaseTimeSeconds"] = lease
	}
	return map[string]any{"fabricName": fabric, "enableIpam": c.EnableIpam, "static": static, "dhcp": dhcp}
}

func (f *fakeFM) serveIpamConfig(w http.ResponseWriter, r *http.Request, fabric string, raw []byte) {
	if !f.fabrics[fabric] {
		writeJSON(w, http.StatusNotFound, envelopeErr("FABRIC_NOT_FOUND", "Specified fabric does not exist."))
		return
	}
	cur := f.ipam[fabric]
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, envelopeOK(ipamDescribe(fabric, cur)))
		return
	}
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	if fn, ok := body["fabricName"].(string); ok && fn != fabric {
		writeJSON(w, http.StatusBadRequest, envelopeErr("INVALID_REQUEST_BODY", "fabricName in the body does not match the fabric in the URL."))
		return
	}
	var next IpamConfig
	_ = json.Unmarshal(raw, &next)
	curMode, nextMode := ipamMode(cur), ipamMode(next)
	if cur.EnableIpam && next.EnableIpam && curMode != nextMode {
		writeJSON(w, http.StatusConflict, envelopeErr("ASSIGN_MODE_CHANGE_NOT_ALLOWED", "This fabric already uses "+curMode+" IP assignment and cannot switch to "+nextMode+" directly."))
		return
	}
	// Settings omitted inside the enabled mode keep their saved values.
	if next.Dhcp != nil && next.Dhcp.Enabled && next.Dhcp.LeaseTimeSeconds == nil && cur.Dhcp != nil {
		next.Dhcp.LeaseTimeSeconds = cur.Dhcp.LeaseTimeSeconds
	}
	f.ipam[fabric] = next
	writeJSON(w, http.StatusOK, envelopeOK(ipamDescribe(fabric, next)))
}

func ipamMode(c IpamConfig) string {
	switch {
	case c.Static != nil && c.Static.Enabled:
		return "static"
	case c.Dhcp != nil && c.Dhcp.Enabled:
		return "dhcp"
	}
	return ""
}

// ------------------------------------------------------------ paged listings

func pageOf(r *http.Request, total int) (page, size, start, end, pages int) {
	page, _ = strconv.Atoi(r.URL.Query().Get("page"))
	size, _ = strconv.Atoi(r.URL.Query().Get("size"))
	if size < 1 {
		size = 50
	}
	pages = (total + size - 1) / size
	start = page * size
	if start > total {
		start = total
	}
	end = start + size
	if end > total {
		end = total
	}
	return
}

func (f *fakeFM) serveTenantIPs(w http.ResponseWriter, r *http.Request, fabric, tenant string) {
	all, ok := f.tenantIPs[fabric+"/"+tenant]
	if !ok {
		writeJSON(w, http.StatusNotFound, envelopeErr("IPAM_NOT_CONFIGURED", "No IPAM pool exists for the tenant."))
		return
	}
	if st := r.URL.Query().Get("status"); st != "" {
		var filtered []IpamAllocation
		for _, a := range all {
			if a.AssignStatus == st {
				filtered = append(filtered, a)
			}
		}
		all = filtered
	}
	page, size, start, end, pages := pageOf(r, len(all))
	writeJSON(w, http.StatusOK, envelopeOK(map[string]any{
		"fabricName": fabric, "tenantName": tenant,
		"pool":        map[string]any{"cidr": "172.1.0.0/18", "allocatableAddressCount": 16381, "allocatedCount": len(all)},
		"allocations": append([]IpamAllocation{}, all[start:end]...),
		"pagination":  map[string]any{"page": page, "size": size, "totalElements": len(all), "totalPages": pages},
	}))
}

func (f *fakeFM) serveTenantDhcp(w http.ResponseWriter, r *http.Request, fabric, tenant string) {
	all, ok := f.leases[fabric+"/"+tenant]
	if !ok {
		writeJSON(w, http.StatusNotFound, envelopeErr("DHCP_SUBNET_NOT_FOUND", "DHCP does not apply to this tenant: the fabric does not assign addresses by DHCP, or the tenant has no VLAN or IPAM pool."))
		return
	}
	page, size, start, end, pages := pageOf(r, len(all))
	writeJSON(w, http.StatusOK, envelopeOK(map[string]any{
		"subnet": map[string]any{
			"fabricName": fabric, "tenantName": tenant, "vlanId": 100, "subnetId": 1010100,
			"networkName": fabric + "_vlan100", "subnetCidr": "172.1.0.0/16", "gatewayIp": "172.1.128.1",
			"poolStart": "172.1.64.2", "poolEnd": "172.1.127.254",
		},
		"liveInKea":  true,
		"leases":     append([]DhcpLease{}, all[start:end]...),
		"pagination": map[string]any{"page": page, "size": size, "totalElements": len(all), "totalPages": pages},
	}))
}
