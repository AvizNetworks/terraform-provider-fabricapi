package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// This file holds the client methods for the Fabric Manager APIs added in ONES
// 4.4 (network policy, webhook registration, IPAM, tenant DHCP and rail
// CIDRPools). They are served by Fabric Manager itself and reached through the
// provider's `endpoint`, the same base URL the tenant/server calls in client.go
// use.
//
// All of these calls are synchronous: none of them honours `Prefer:
// respond-async` or returns an operation id, so there is nothing to poll.

// FMAPIError is a non-2xx response from Fabric Manager. Code is FM's own error
// code (e.g. "FABRIC_NOT_FOUND") when the body carries one.
type FMAPIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *FMAPIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.StatusCode)
	}
	if e.Code != "" {
		return fmt.Sprintf("API returned %d %s: %s", e.StatusCode, e.Code, msg)
	}
	return fmt.Sprintf("API returned %d: %s", e.StatusCode, msg)
}

// isFMNotFound reports whether err is a 404 from Fabric Manager.
func isFMNotFound(err error) bool {
	var apiErr *FMAPIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// parseFMError turns an FM error body into an FMAPIError. FM answers errors in
// four shapes depending on which layer rejected the call:
//
//	{"status":"error","message":"...","error":{"code":"...","detail":"..."}}  (ApiResponse envelope)
//	{"error":"..."}  or  {"status":"error","error":"..."}                     (webhook / network policy list)
//	{"errorCode":"PERMISSION_DENIED","message":"..."}                         (permission interceptor)
//	plain text                                                                 (licence check, servlet errors)
func parseFMError(status int, body []byte) *FMAPIError {
	out := &FMAPIError{StatusCode: status}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		out.Message = apiErrorBody(body)
		return out
	}
	str := func(v any) string {
		s, _ := v.(string)
		return strings.TrimSpace(s)
	}
	switch e := m["error"].(type) {
	case map[string]any:
		out.Code = str(e["code"])
		out.Message = str(e["detail"])
	case string:
		out.Message = strings.TrimSpace(e)
	}
	if out.Code == "" {
		out.Code = str(m["errorCode"])
	}
	if out.Message == "" {
		out.Message = str(m["message"])
	}
	if out.Message == "" {
		out.Message = apiErrorBody(body)
	}
	return out
}

// looksLikeHTML catches the ONES gateway's single-page-app fallback: routes the
// gateway doesn't proxy come back as the UI's index.html with HTTP 200 instead
// of an error.
func looksLikeHTML(body []byte) bool {
	b := bytes.TrimSpace(body)
	if len(b) > 64 {
		b = b[:64]
	}
	s := strings.ToLower(string(b))
	return strings.HasPrefix(s, "<!doctype html") || strings.HasPrefix(s, "<html")
}

// fmPath joins already-literal path parts and path-escaped name segments, e.g.
// fmPath("fabrics", fabric, "tenants", tenant, "webhook").
func fmPath(literalAndNames ...string) string {
	parts := make([]string, len(literalAndNames))
	for i, p := range literalAndNames {
		// Every other element is a user-supplied name; escape everything to be
		// safe — literals contain no characters that PathEscape changes.
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// doFM sends one request to Fabric Manager and returns the raw body of a 2xx
// response. Non-2xx responses become *FMAPIError.
func (c *APIClient) doFM(ctx context.Context, method, path string, query url.Values, body any, accept string) ([]byte, int, error) {
	u := strings.TrimRight(c.Endpoint, "/") + "/" + strings.TrimLeft(path, "/")
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	if accept == "" {
		accept = "application/json"
	}
	respBody, status, err := c.doRequestRawWithHeaders(ctx, method, u, body, 60*time.Minute, map[string]string{"Accept": accept})
	if err != nil {
		return nil, status, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return nil, status, parseFMError(status, respBody)
	}
	if looksLikeHTML(respBody) {
		return nil, status, fmt.Errorf(
			"%s %s returned an HTML page instead of a Fabric Manager response. The provider `endpoint` (FABRIC_API_ENDPOINT) "+
				"probably points at the ONES gateway, which does not proxy this route; point it at Fabric Manager directly, e.g. https://<ones-host>:8443",
			method, u,
		)
	}
	return respBody, status, nil
}

// fmEnvelope is FM's standard success wrapper.
type fmEnvelope[T any] struct {
	Status  string `json:"status"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

// doFMData sends a request and decodes the `data` field of the success envelope.
func doFMData[T any](ctx context.Context, c *APIClient, method, path string, query url.Values, body any) (*T, error) {
	raw, _, err := c.doFM(ctx, method, path, query, body, "")
	if err != nil {
		return nil, err
	}
	var env fmEnvelope[T]
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("decode %s %s response: %w (body=%s)", method, path, err, apiErrorBody(raw))
	}
	return &env.Data, nil
}

// doFMJSON sends a request and decodes the whole body (for the endpoints that
// don't use the envelope). A 204 or empty body leaves out untouched.
func doFMJSON(ctx context.Context, c *APIClient, method, path string, query url.Values, body, out any) (int, error) {
	raw, status, err := c.doFM(ctx, method, path, query, body, "")
	if err != nil {
		return status, err
	}
	if out == nil || len(bytes.TrimSpace(raw)) == 0 {
		return status, nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return status, fmt.Errorf("decode %s %s response: %w (body=%s)", method, path, err, apiErrorBody(raw))
	}
	return status, nil
}

// ---------------------------------------------------------- network policy ---

// NetworkPolicySide is one end (source or destination) of a network policy.
type NetworkPolicySide struct {
	Type        string   `json:"type"`
	TenantName  string   `json:"tenantName"`
	ServiceName string   `json:"serviceName,omitempty"`
	Endpoints   []string `json:"endpoints,omitempty"`
}

// NetworkPolicy is the request body for create/patch and one entry of the list.
type NetworkPolicy struct {
	Policy struct {
		Name string `json:"name"`
	} `json:"policy"`
	Spec struct {
		Source      NetworkPolicySide `json:"source"`
		Destination NetworkPolicySide `json:"destination"`
	} `json:"spec"`
}

func newNetworkPolicy(name string, source, destination NetworkPolicySide) NetworkPolicy {
	var p NetworkPolicy
	p.Policy.Name = name
	p.Spec.Source = source
	p.Spec.Destination = destination
	return p
}

// ListNetworkPolicies: GET /fabrics/{fabric}/networkpolicy. The body is not
// wrapped in the usual envelope.
func (c *APIClient) ListNetworkPolicies(ctx context.Context, fabricName string) ([]NetworkPolicy, error) {
	var out struct {
		Policies []NetworkPolicy `json:"policies"`
	}
	if _, err := doFMJSON(ctx, c, http.MethodGet, fmPath("fabrics", fabricName, "networkpolicy"), nil, nil, &out); err != nil {
		return nil, err
	}
	return out.Policies, nil
}

// FindNetworkPolicy returns the named policy, or nil when the fabric has none by
// that name. FM has no GET for a single policy.
func (c *APIClient) FindNetworkPolicy(ctx context.Context, fabricName, policyName string) (*NetworkPolicy, error) {
	policies, err := c.ListNetworkPolicies(ctx, fabricName)
	if err != nil {
		return nil, err
	}
	for i := range policies {
		if policies[i].Policy.Name == policyName {
			return &policies[i], nil
		}
	}
	return nil, nil
}

// CreateNetworkPolicy: POST /fabrics/{fabric}/networkpolicy. Pushes config to
// the fabric's devices before answering.
func (c *APIClient) CreateNetworkPolicy(ctx context.Context, fabricName string, p NetworkPolicy) error {
	_, err := doFMJSON(ctx, c, http.MethodPost, fmPath("fabrics", fabricName, "networkpolicy"), nil, p, nil)
	return err
}

// PatchNetworkPolicy: PATCH /fabrics/{fabric}/networkpolicy/{name}. Adds the
// endpoints in p to the existing policy; FM cannot remove endpoints. FM checks
// the body with its create-time validation, so p must carry policy.name.
func (c *APIClient) PatchNetworkPolicy(ctx context.Context, fabricName string, p NetworkPolicy) error {
	_, err := doFMJSON(ctx, c, http.MethodPatch, fmPath("fabrics", fabricName, "networkpolicy", p.Policy.Name), nil, p, nil)
	return err
}

// DeleteNetworkPolicy: DELETE /fabrics/{fabric}/networkpolicy/{name}. Returns
// (false, nil) when the policy or fabric is already gone — FM reports a missing
// policy as HTTP 500 with a "does not exist" message rather than a 404.
func (c *APIClient) DeleteNetworkPolicy(ctx context.Context, fabricName, policyName string) (deleted bool, err error) {
	_, err = doFMJSON(ctx, c, http.MethodDelete, fmPath("fabrics", fabricName, "networkpolicy", policyName), nil, nil, nil)
	if err == nil {
		return true, nil
	}
	if isFMNotFound(err) || isNetworkPolicyMissing(err) {
		return false, nil
	}
	return false, err
}

func isNetworkPolicyMissing(err error) bool {
	var apiErr *FMAPIError
	if !errors.As(err, &apiErr) {
		return false
	}
	msg := strings.ToLower(apiErr.Message)
	return strings.Contains(msg, "does not exist") || strings.Contains(msg, "not found in fabric")
}

// ---------------------------------------------------------------- webhooks ---

// WebhookFilters narrows a global registration to some fabrics/tenants.
type WebhookFilters struct {
	FabricNames []string `json:"fabricNames"`
	TenantNames []string `json:"tenantNames"`
}

// WebhookRegistration is a registration as FM returns it at any scope. ID is
// set on fabric/tenant rows, WebhookID on global rows. EventTypes is omitted
// when the registration receives every event.
type WebhookRegistration struct {
	ID          int64           `json:"id,omitempty"`
	WebhookID   string          `json:"webhookId,omitempty"`
	TenantName  string          `json:"tenantName,omitempty"`
	FabricName  string          `json:"fabricName,omitempty"`
	CallbackURL string          `json:"callbackUrl"`
	Enabled     bool            `json:"enabled"`
	EventTypes  []string        `json:"eventTypes,omitempty"`
	Filters     *WebhookFilters `json:"filters,omitempty"`
	CreatedAt   string          `json:"createdAt,omitempty"`
	UpdatedAt   string          `json:"updatedAt,omitempty"`
}

// GlobalWebhookRequest is the POST/PATCH body for /webhooks. Nil fields are
// left out, which PATCH reads as "unchanged"; an empty (non-nil) slice clears.
type GlobalWebhookRequest struct {
	CallbackURL *string         `json:"callbackUrl,omitempty"`
	Enabled     *bool           `json:"enabled,omitempty"`
	EventTypes  []string        `json:"eventTypes,omitempty"`
	Filters     *WebhookFilters `json:"filters,omitempty"`
}

// ScopedWebhookRequest is the POST/PUT body for fabric- and tenant-scoped
// registrations. Enabled is always sent: FM reads a missing value as true.
// EventTypes is always sent as a list; an empty list means every event.
type ScopedWebhookRequest struct {
	CallbackURL string   `json:"callbackUrl"`
	Enabled     bool     `json:"enabled"`
	EventTypes  []string `json:"eventTypes"`
}

// RegisterGlobalWebhook: POST /webhooks. FM upserts on callbackUrl: created is
// false when a registration with this URL already existed and was reused.
func (c *APIClient) RegisterGlobalWebhook(ctx context.Context, req GlobalWebhookRequest) (reg *WebhookRegistration, created bool, err error) {
	var out WebhookRegistration
	status, err := doFMJSON(ctx, c, http.MethodPost, "webhooks", nil, req, &out)
	if err != nil {
		return nil, false, err
	}
	return &out, status == http.StatusCreated, nil
}

// ListGlobalWebhooks: GET /webhooks, newest first.
func (c *APIClient) ListGlobalWebhooks(ctx context.Context) ([]WebhookRegistration, error) {
	var out []WebhookRegistration
	if _, err := doFMJSON(ctx, c, http.MethodGet, "webhooks", nil, nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// GetGlobalWebhook: GET /webhooks/{id}. 404 when unknown.
func (c *APIClient) GetGlobalWebhook(ctx context.Context, webhookID string) (*WebhookRegistration, error) {
	var out WebhookRegistration
	if _, err := doFMJSON(ctx, c, http.MethodGet, fmPath("webhooks", webhookID), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateGlobalWebhook: PATCH /webhooks/{id} — only the fields sent change.
func (c *APIClient) UpdateGlobalWebhook(ctx context.Context, webhookID string, req GlobalWebhookRequest) (*WebhookRegistration, error) {
	var out WebhookRegistration
	if _, err := doFMJSON(ctx, c, http.MethodPatch, fmPath("webhooks", webhookID), nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteGlobalWebhook: DELETE /webhooks/{id}. A 404 counts as already deleted.
func (c *APIClient) DeleteGlobalWebhook(ctx context.Context, webhookID string) error {
	_, err := doFMJSON(ctx, c, http.MethodDelete, fmPath("webhooks", webhookID), nil, nil, nil)
	if isFMNotFound(err) {
		return nil
	}
	return err
}

// scopedWebhookPath is /fabrics/{f}/webhook, or /fabrics/{f}/tenants/{t}/webhook
// when tenantName is set.
func scopedWebhookPath(fabricName, tenantName string) string {
	if tenantName == "" {
		return fmPath("fabrics", fabricName, "webhook")
	}
	return fmPath("fabrics", fabricName, "tenants", tenantName, "webhook")
}

// RegisterScopedWebhook: POST to the fabric or tenant webhook path. FM
// registers or silently replaces, and always stores enabled=true.
func (c *APIClient) RegisterScopedWebhook(ctx context.Context, fabricName, tenantName string, req ScopedWebhookRequest) (*WebhookRegistration, error) {
	var out WebhookRegistration
	if _, err := doFMJSON(ctx, c, http.MethodPost, scopedWebhookPath(fabricName, tenantName), nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// GetScopedWebhook: GET the fabric or tenant registration. 404 when none.
func (c *APIClient) GetScopedWebhook(ctx context.Context, fabricName, tenantName string) (*WebhookRegistration, error) {
	var out WebhookRegistration
	if _, err := doFMJSON(ctx, c, http.MethodGet, scopedWebhookPath(fabricName, tenantName), nil, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// UpdateScopedWebhook: PUT the fabric or tenant registration.
func (c *APIClient) UpdateScopedWebhook(ctx context.Context, fabricName, tenantName string, req ScopedWebhookRequest) (*WebhookRegistration, error) {
	var out WebhookRegistration
	if _, err := doFMJSON(ctx, c, http.MethodPut, scopedWebhookPath(fabricName, tenantName), nil, req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteScopedWebhook: DELETE the fabric or tenant registration. A 404 counts
// as already deleted.
func (c *APIClient) DeleteScopedWebhook(ctx context.Context, fabricName, tenantName string) error {
	_, err := doFMJSON(ctx, c, http.MethodDelete, scopedWebhookPath(fabricName, tenantName), nil, nil, nil)
	if isFMNotFound(err) {
		return nil
	}
	return err
}

// -------------------------------------------------------------------- IPAM ---

// IpamStaticConfig is the static assignment mode of an IPAM config.
type IpamStaticConfig struct {
	Enabled        bool  `json:"enabled"`
	EnableIPAssign *bool `json:"enableIpAssign,omitempty"`
}

// IpamDhcpConfig is the DHCP assignment mode of an IPAM config.
type IpamDhcpConfig struct {
	Enabled          bool   `json:"enabled"`
	LeaseTimeSeconds *int64 `json:"leaseTimeSeconds,omitempty"`
}

// IpamConfig is the body of GET/PATCH /fabrics/{f}/ipam/config. FM rejects a
// PATCH whose fabricName disagrees with the URL, so it is never sent.
type IpamConfig struct {
	FabricName string            `json:"fabricName,omitempty"`
	EnableIpam bool              `json:"enableIpam"`
	Static     *IpamStaticConfig `json:"static,omitempty"`
	Dhcp       *IpamDhcpConfig   `json:"dhcp,omitempty"`
}

// GetIpamConfig: GET /fabrics/{f}/ipam/config. A fabric never configured
// returns everything disabled, not 404.
func (c *APIClient) GetIpamConfig(ctx context.Context, fabricName string) (*IpamConfig, error) {
	return doFMData[IpamConfig](ctx, c, http.MethodGet, fmPath("fabrics", fabricName, "ipam", "config"), nil, nil)
}

// UpdateIpamConfig: PATCH /fabrics/{f}/ipam/config. This replaces both modes on
// every call (a mode left out is disabled). force=true lets IPAM be disabled
// while unused pools exist and skips the DHCP server reachability probe.
func (c *APIClient) UpdateIpamConfig(ctx context.Context, fabricName string, cfg IpamConfig, force bool) (*IpamConfig, error) {
	cfg.FabricName = ""
	var q url.Values
	if force {
		q = url.Values{"force": []string{"true"}}
	}
	return doFMData[IpamConfig](ctx, c, http.MethodPatch, fmPath("fabrics", fabricName, "ipam", "config"), q, cfg)
}

// IpamAllocation is one address IPAM has allocated.
type IpamAllocation struct {
	ID            int64   `json:"id"`
	PoolID        int64   `json:"poolId"`
	FabricName    string  `json:"fabricName"`
	TenantName    string  `json:"tenantName"`
	TenantID      *int64  `json:"tenantId"`
	ServerName    string  `json:"serverName"`
	IPAddress     string  `json:"ipAddress"`
	PrefixLength  int64   `json:"prefixLength"`
	AssignStatus  string  `json:"assignStatus"`
	CleanupStatus string  `json:"cleanupStatus"`
	AllocatedAt   string  `json:"allocatedAt"`
	InterfaceName *string `json:"interfaceName"`
}

// FMPagination is the paging block FM's paged listings return.
type FMPagination struct {
	Page          int   `json:"page"`
	Size          int   `json:"size"`
	TotalElements int64 `json:"totalElements"`
	TotalPages    int   `json:"totalPages"`
}

// ServerIPAddresses is the body of GET /fabrics/{f}/computes/{s}/ip-addresses.
type ServerIPAddresses struct {
	FabricName  string           `json:"fabricName"`
	ServerName  string           `json:"serverName"`
	Allocations []IpamAllocation `json:"allocations"`
}

// GetServerIPAddresses: every allocation one server holds, across tenants.
func (c *APIClient) GetServerIPAddresses(ctx context.Context, fabricName, serverName string) (*ServerIPAddresses, error) {
	return doFMData[ServerIPAddresses](ctx, c, http.MethodGet, fmPath("fabrics", fabricName, "computes", serverName, "ip-addresses"), nil, nil)
}

// GetTenantComputeIP: GET /fabrics/{f}/tenants/{t}/computes/{s}/ip-address.
func (c *APIClient) GetTenantComputeIP(ctx context.Context, fabricName, tenantName, serverName string) (*IpamAllocation, error) {
	return doFMData[IpamAllocation](ctx, c, http.MethodGet, fmPath("fabrics", fabricName, "tenants", tenantName, "computes", serverName, "ip-address"), nil, nil)
}

// IpamPoolSummary is the tenant pool's counters.
type IpamPoolSummary struct {
	Cidr                    string `json:"cidr"`
	AllocatableAddressCount int64  `json:"allocatableAddressCount"`
	AllocatedCount          int64  `json:"allocatedCount"`
}

// TenantIPAddressesPage is one page of GET /fabrics/{f}/tenants/{t}/ip-addresses.
type TenantIPAddressesPage struct {
	FabricName  string           `json:"fabricName"`
	TenantName  string           `json:"tenantName"`
	Pool        IpamPoolSummary  `json:"pool"`
	Allocations []IpamAllocation `json:"allocations"`
	Pagination  FMPagination     `json:"pagination"`
}

// GetTenantIPAddressesPage fetches one page. status is an assignment state
// filter ("" for none).
func (c *APIClient) GetTenantIPAddressesPage(ctx context.Context, fabricName, tenantName string, page, size int, status string) (*TenantIPAddressesPage, error) {
	q := url.Values{"page": {strconv.Itoa(page)}, "size": {strconv.Itoa(size)}}
	if status != "" {
		q.Set("status", status)
	}
	return doFMData[TenantIPAddressesPage](ctx, c, http.MethodGet, fmPath("fabrics", fabricName, "tenants", tenantName, "ip-addresses"), q, nil)
}

// maxFMPages bounds the page loops below against a server that never reports
// the last page.
const maxFMPages = 10000

// GetAllTenantIPAddresses walks every page and returns the pool summary with
// all allocations.
func (c *APIClient) GetAllTenantIPAddresses(ctx context.Context, fabricName, tenantName, status string) (*TenantIPAddressesPage, error) {
	const pageSize = 500
	var all *TenantIPAddressesPage
	for page := 0; page < maxFMPages; page++ {
		p, err := c.GetTenantIPAddressesPage(ctx, fabricName, tenantName, page, pageSize, status)
		if err != nil {
			return nil, err
		}
		if all == nil {
			all = p
		} else {
			all.Allocations = append(all.Allocations, p.Allocations...)
		}
		if len(p.Allocations) == 0 || page+1 >= p.Pagination.TotalPages {
			break
		}
	}
	return all, nil
}

// --------------------------------------------------------------------- DHCP ---

// TenantDhcpSubnet is the tenant's DHCP subnet as FM derives it.
type TenantDhcpSubnet struct {
	FabricName  string `json:"fabricName"`
	TenantName  string `json:"tenantName"`
	VlanID      int64  `json:"vlanId"`
	SubnetID    int64  `json:"subnetId"`
	NetworkName string `json:"networkName"`
	SubnetCidr  string `json:"subnetCidr"`
	GatewayIP   string `json:"gatewayIp"`
	PoolStart   string `json:"poolStart"`
	PoolEnd     string `json:"poolEnd"`
}

// DhcpLease is one lease Kea handed out in the tenant subnet.
type DhcpLease struct {
	IPAddress            string  `json:"ipAddress"`
	MacAddress           *string `json:"macAddress"`
	Hostname             *string `json:"hostname"`
	State                string  `json:"state"`
	ValidLifetimeSeconds int64   `json:"validLifetimeSeconds"`
	LastRenewedAt        string  `json:"lastRenewedAt"`
	ExpiresAt            string  `json:"expiresAt"`
}

// TenantDhcpPage is one page of GET /fabrics/{f}/tenants/{t}/dhcp. LiveInKea and
// Leases are nil when Kea could not be queried; the *CheckError fields say why.
type TenantDhcpPage struct {
	Subnet          TenantDhcpSubnet `json:"subnet"`
	LiveInKea       *bool            `json:"liveInKea"`
	LiveCheckError  string           `json:"liveCheckError,omitempty"`
	Leases          []DhcpLease      `json:"leases"`
	LeaseCheckError string           `json:"leaseCheckError,omitempty"`
	Pagination      *FMPagination    `json:"pagination,omitempty"`
}

// maxDhcpLeasePageSize is the largest lease page FM returns.
const maxDhcpLeasePageSize = 1000

// GetTenantDhcpPage fetches one page of the tenant's DHCP subnet and leases.
func (c *APIClient) GetTenantDhcpPage(ctx context.Context, fabricName, tenantName string, page, size int) (*TenantDhcpPage, error) {
	q := url.Values{"page": {strconv.Itoa(page)}, "size": {strconv.Itoa(size)}}
	return doFMData[TenantDhcpPage](ctx, c, http.MethodGet, fmPath("fabrics", fabricName, "tenants", tenantName, "dhcp"), q, nil)
}

// GetTenantDhcp walks every lease page.
func (c *APIClient) GetTenantDhcp(ctx context.Context, fabricName, tenantName string) (*TenantDhcpPage, error) {
	var all *TenantDhcpPage
	for page := 0; page < maxFMPages; page++ {
		p, err := c.GetTenantDhcpPage(ctx, fabricName, tenantName, page, maxDhcpLeasePageSize)
		if err != nil {
			return nil, err
		}
		if all == nil {
			all = p
		} else {
			all.Leases = append(all.Leases, p.Leases...)
		}
		if p.Leases == nil || p.Pagination == nil || len(p.Leases) == 0 || page+1 >= p.Pagination.TotalPages {
			break
		}
	}
	return all, nil
}

// dhcpSummary renders a reconcile/retry `data` value as text: FM returns a
// plain summary string, but any JSON value is accepted and kept verbatim.
func dhcpSummary(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	t := strings.TrimSpace(string(raw))
	if t == "null" {
		return ""
	}
	return t
}

// ReconcileFabricDhcp: POST /fabrics/{f}/dhcp/reconcile. Re-applies every
// tenant DHCP subnet to Kea and returns FM's summary.
func (c *APIClient) ReconcileFabricDhcp(ctx context.Context, fabricName string) (string, error) {
	data, err := doFMData[json.RawMessage](ctx, c, http.MethodPost, fmPath("fabrics", fabricName, "dhcp", "reconcile"), nil, nil)
	if err != nil {
		return "", err
	}
	return dhcpSummary(*data), nil
}

// RetryTenantDhcp: POST /fabrics/{f}/tenants/{t}/dhcp/retry. FM re-applies the
// whole fabric's DHCP state, gated on DHCP applying to this tenant.
func (c *APIClient) RetryTenantDhcp(ctx context.Context, fabricName, tenantName string) (string, error) {
	data, err := doFMData[json.RawMessage](ctx, c, http.MethodPost, fmPath("fabrics", fabricName, "tenants", tenantName, "dhcp", "retry"), nil, nil)
	if err != nil {
		return "", err
	}
	return dhcpSummary(*data), nil
}

// ---------------------------------------------------------- rail CIDRPools ---

// RailCidrPools is the JSON form of GET /fabrics/{f}/rails/cidrpools.
type RailCidrPools struct {
	FabricName string   `json:"fabricName"`
	Rails      []string `json:"rails"`
	YAML       string   `json:"yaml"`
}

// GetRailCidrPools returns every East-West rail's CIDRPool manifest.
func (c *APIClient) GetRailCidrPools(ctx context.Context, fabricName string) (*RailCidrPools, error) {
	return doFMData[RailCidrPools](ctx, c, http.MethodGet, fmPath("fabrics", fabricName, "rails", "cidrpools"), nil, nil)
}

// RailCidrPool is the JSON form of GET /fabrics/{f}/rails/{rail}/cidrpool.
type RailCidrPool struct {
	FabricName string `json:"fabricName"`
	RailName   string `json:"railName"`
	YAML       string `json:"yaml"`
}

// GetRailCidrPool returns one rail's CIDRPool document.
func (c *APIClient) GetRailCidrPool(ctx context.Context, fabricName, railName string) (*RailCidrPool, error) {
	return doFMData[RailCidrPool](ctx, c, http.MethodGet, fmPath("fabrics", fabricName, "rails", railName, "cidrpool"), nil, nil)
}
