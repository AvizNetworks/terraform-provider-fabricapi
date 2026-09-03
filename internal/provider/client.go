package provider

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type APIClient struct {
	Endpoint     string
	Fabric       string
	AuthEndpoint string

	// ConfigEndpoint is the base URL for the ONES UI "config" service
	// (e.g. POST {ConfigEndpoint}/api/config/addFabricData). This is a
	// separate backend/host from Endpoint (the Fabric API on :8089).
	ConfigEndpoint string

	// Auth: Username/Password are used to fetch an access token (and refresh token) via POST /login.
	// Token is then used as an Authorization Bearer token for all requests.
	// When RefreshToken is present, the client will refresh the access token once on 401 responses.
	Token        string
	RefreshToken string
	Username     string
	Password     string
	InsecureTLS  bool

	mu             sync.Mutex
	loginAttempted bool
}

type requestOptions struct {
	Prefer          string // respond-sync | respond-async (underscore forms normalized in preferHeaderValue)
	WebhooksEnabled bool
	WebhookURL      string
	WebhookEvents   []string
}

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func (c *APIClient) authHeader() string {
	tok := strings.TrimSpace(c.Token)
	if tok == "" {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(tok), "bearer ") {
		return tok
	}
	return "Bearer " + tok
}

func (c *APIClient) refreshTokenOnce(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if strings.TrimSpace(c.RefreshToken) == "" {
		return fmt.Errorf("access token expired and no refresh_token is available; login again")
	}

	p, err := c.refresh(ctx)
	if err != nil {
		// If refresh fails, surface a clear message.
		return fmt.Errorf("access token expired and refresh failed: %w (login again)", err)
	}

	if p.AccessToken != "" {
		c.Token = p.AccessToken
	}
	if p.RefreshToken != "" {
		c.RefreshToken = p.RefreshToken
	}
	return nil
}

func (c *APIClient) doRequestRaw(
	ctx context.Context,
	method, url string,
	body any,
	timeout time.Duration,
) ([]byte, int, error) {
	return c.doRequestRawWithHeaders(ctx, method, url, body, timeout, nil)
}

func (c *APIClient) doRequestRawWithHeaders(
	ctx context.Context,
	method, url string,
	body any,
	timeout time.Duration,
	extraHeaders map[string]string,
) ([]byte, int, error) {
	var payload []byte
	var err error
	if body != nil {
		if b, ok := body.([]byte); ok {
			payload = b
		} else {
			payload, err = json.Marshal(body)
		}
		if err != nil {
			return nil, 0, err
		}
	}

	for attempt := 0; attempt < 2; attempt++ {
		var reader io.Reader
		if payload != nil {
			reader = bytes.NewBuffer(payload)
		}

		req, err := http.NewRequestWithContext(ctx, method, url, reader)
		if err != nil {
			return nil, 0, err
		}

		if err := c.setCommonHeaders(ctx, req); err != nil {
			return nil, 0, err
		}
		for k, v := range extraHeaders {
			if strings.TrimSpace(k) == "" || strings.TrimSpace(v) == "" {
				continue
			}
			req.Header.Set(k, v)
		}

		transport := http.DefaultTransport.(*http.Transport).Clone()
		if c.InsecureTLS {
			if transport.TLSClientConfig == nil {
				transport.TLSClientConfig = &tls.Config{}
			}
			transport.TLSClientConfig.InsecureSkipVerify = true
		}
		client := &http.Client{Timeout: timeout, Transport: transport}
		resp, err := client.Do(req)
		if err != nil {
			return nil, 0, wrapConnectivityError(url, err)
		}
		respBody, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		// Retry once on auth failure by refreshing access token.
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			if err := c.refreshTokenOnce(ctx); err == nil {
				continue
			}
		}

		return respBody, resp.StatusCode, nil
	}

	return nil, 0, fmt.Errorf("request failed after auth refresh retry")
}

func wrapConnectivityError(requestURL string, err error) error {
	if err == nil {
		return nil
	}

	// Timeout / context deadline
	if errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf(
			"unable to connect to Fabric API endpoint (%s). Please verify the endpoint URL and network connectivity (endpoint down, wrong IP/port, firewall/DNS). Original error: %w",
			requestURL,
			err,
		)
	}

	// net.Error timeouts
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return fmt.Errorf(
			"unable to connect to Fabric API endpoint (%s). Please verify the endpoint URL and network connectivity (endpoint down, wrong IP/port, firewall/DNS). Original error: %w",
			requestURL,
			err,
		)
	}

	// Connection refused / reset
	if errors.Is(err, syscall.ECONNREFUSED) || errors.Is(err, syscall.ECONNRESET) {
		return fmt.Errorf(
			"unable to connect to Fabric API endpoint (%s). Connection was refused/reset. Check the service is running and reachable (IP/port, firewall rules). Original error: %w",
			requestURL,
			err,
		)
	}

	// DNS / no such host
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return fmt.Errorf(
			"unable to connect to Fabric API endpoint (%s). DNS lookup failed. Check the hostname and DNS/network settings. Original error: %w",
			requestURL,
			err,
		)
	}

	return err
}

type tokenPair struct {
	AccessToken  string
	RefreshToken string
	TokenType    string
	ExpiresIn    int64
}

func extractTokenPairFromResponse(m map[string]any) (tokenPair, bool) {
	getStr := func(key string) string {
		if v, ok := m[key]; ok {
			if s, ok := v.(string); ok {
				return strings.TrimSpace(s)
			}
		}
		return ""
	}
	getInt := func(key string) int64 {
		if v, ok := m[key]; ok {
			switch t := v.(type) {
			case float64:
				return int64(t)
			case int64:
				return t
			case int:
				return int64(t)
			}
		}
		return 0
	}

	p := tokenPair{
		AccessToken:  getStr("access_token"),
		RefreshToken: getStr("refresh_token"),
		TokenType:    getStr("token_type"),
		ExpiresIn:    getInt("expires_in"),
	}
	if p.TokenType == "" {
		p.TokenType = "Bearer"
	}
	if p.AccessToken != "" {
		return p, true
	}

	for _, k := range []string{"data", "result", "auth"} {
		if v, ok := m[k]; ok {
			if mm, ok := v.(map[string]any); ok {
				if p2, ok := extractTokenPairFromResponse(mm); ok {
					return p2, true
				}
			}
		}
	}

	// Backwards-compatible: accept "token" / "jwt" as access token.
	for _, k := range []string{"token", "jwt", "accessToken", "id_token", "idToken"} {
		if s := getStr(k); s != "" {
			return tokenPair{AccessToken: s, TokenType: "Bearer"}, true
		}
	}

	return tokenPair{}, false
}

func (c *APIClient) login(ctx context.Context, username, password string) (tokenPair, error) {
	base := strings.TrimRight(c.AuthEndpoint, "/")
	if base == "" {
		base = strings.TrimRight(c.Endpoint, "/")
	}
	u := base + "/login"

	payload := loginRequest{Username: username, Password: password}
	b, err := json.Marshal(payload)
	if err != nil {
		return tokenPair{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewBuffer(b))
	if err != nil {
		return tokenPair{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if c.InsecureTLS {
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{}
		}
		transport.TLSClientConfig.InsecureSkipVerify = true
	}
	client := &http.Client{Timeout: 2 * time.Minute, Transport: transport}
	resp, err := client.Do(req)
	if err != nil {
		return tokenPair{}, wrapConnectivityError(u, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return tokenPair{}, fmt.Errorf("login failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var m map[string]any
	if err := json.Unmarshal(respBody, &m); err != nil {
		s := strings.TrimSpace(string(respBody))
		if s != "" && !strings.HasPrefix(s, "{") {
			return tokenPair{AccessToken: s, TokenType: "Bearer"}, nil
		}
		return tokenPair{}, fmt.Errorf("login response parse error: %w", err)
	}

	if p, ok := extractTokenPairFromResponse(m); ok {
		return p, nil
	}
	return tokenPair{}, fmt.Errorf("login succeeded but no token field found in response")
}

type refreshRequest struct {
	RefreshToken string `json:"refresh_token"`
}

func (c *APIClient) refresh(ctx context.Context) (tokenPair, error) {
	base := strings.TrimRight(c.AuthEndpoint, "/")
	if base == "" {
		base = strings.TrimRight(c.Endpoint, "/")
	}
	u := base + "/refresh"

	payload := refreshRequest{RefreshToken: c.RefreshToken}
	b, err := json.Marshal(payload)
	if err != nil {
		return tokenPair{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewBuffer(b))
	if err != nil {
		return tokenPair{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if c.InsecureTLS {
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{}
		}
		transport.TLSClientConfig.InsecureSkipVerify = true
	}
	client := &http.Client{Timeout: 2 * time.Minute, Transport: transport}
	resp, err := client.Do(req)
	if err != nil {
		return tokenPair{}, wrapConnectivityError(u, err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return tokenPair{}, fmt.Errorf("refresh failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var m map[string]any
	if err := json.Unmarshal(respBody, &m); err != nil {
		return tokenPair{}, fmt.Errorf("refresh response parse error: %w", err)
	}
	if p, ok := extractTokenPairFromResponse(m); ok {
		return p, nil
	}
	return tokenPair{}, fmt.Errorf("refresh succeeded but no token field found in response")
}

// Logout calls POST {auth_endpoint}/api/auth/logout with JSON body {"refresh_token":"..."}.
// It does not use ensureToken (so a refresh-only revoke works). We intentionally do NOT
// send Authorization here because some backends reject logout when the access token is
// expired/invalid, even though refresh-token revocation should still be allowed.
// On success, in-memory Token and RefreshToken are cleared.
func (c *APIClient) Logout(ctx context.Context, refreshTokenOverride string) error {
	rt := strings.TrimSpace(refreshTokenOverride)
	if rt == "" {
		c.mu.Lock()
		rt = strings.TrimSpace(c.RefreshToken)
		c.mu.Unlock()
	}
	if rt == "" {
		return fmt.Errorf("logout requires refresh_token (set provider refresh_token or fabricapi_auth_logout.refresh_token)")
	}

	base := strings.TrimRight(c.AuthEndpoint, "/")
	if base == "" {
		base = strings.TrimRight(c.Endpoint, "/")
	}
	u := base + "/api/auth/logout"

	payload, err := json.Marshal(map[string]string{"refresh_token": rt})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewBuffer(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	transport := http.DefaultTransport.(*http.Transport).Clone()
	if c.InsecureTLS {
		if transport.TLSClientConfig == nil {
			transport.TLSClientConfig = &tls.Config{}
		}
		transport.TLSClientConfig.InsecureSkipVerify = true
	}
	client := &http.Client{Timeout: 2 * time.Minute, Transport: transport}
	resp, err := client.Do(req)
	if err != nil {
		return wrapConnectivityError(u, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("logout failed (%d): %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	c.mu.Lock()
	c.Token = ""
	c.RefreshToken = ""
	c.loginAttempted = false
	c.mu.Unlock()
	return nil
}

func (c *APIClient) ensureToken(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if strings.TrimSpace(c.Token) != "" {
		return nil
	}
	if c.loginAttempted {
		return fmt.Errorf("no token configured and login did not yield a token")
	}
	c.loginAttempted = true

	if strings.TrimSpace(c.Username) == "" || strings.TrimSpace(c.Password) == "" {
		return fmt.Errorf("missing auth: set access_token or username/password to authenticate")
	}

	p, err := c.login(ctx, c.Username, c.Password)
	if err != nil {
		return err
	}
	c.Token = p.AccessToken
	if p.RefreshToken != "" {
		c.RefreshToken = p.RefreshToken
	}
	return nil
}

func (c *APIClient) setCommonHeaders(ctx context.Context, req *http.Request) error {
	req.Header.Set("Content-Type", "application/json")
	// Always require login-based token acquisition for authenticated APIs.
	if err := c.ensureToken(ctx); err != nil {
		return err
	}
	if h := c.authHeader(); h != "" {
		req.Header.Set("Authorization", h)
	}
	return nil
}

func preferHeaderValue(prefer string) string {
	switch strings.ToLower(strings.TrimSpace(prefer)) {
	case "", "respond_sync", "respond-sync":
		return "respond-sync"
	case "respond_async", "respond-async":
		return "respond-async"
	default:
		// Be permissive: send whatever user set (best-effort).
		return prefer
	}
}

func maybeWebhookBody(opts *requestOptions) map[string]any {
	if opts == nil {
		return nil
	}
	if strings.EqualFold(preferHeaderValue(opts.Prefer), "respond-async") && opts.WebhooksEnabled {
		out := map[string]any{
			"enableWebhook": true,
			"webhookUrl":    strings.TrimSpace(opts.WebhookURL),
			"webhookEvents": opts.WebhookEvents,
		}
		return out
	}
	return nil
}

func extractOperationID(respBody []byte) (string, bool) {
	s := strings.TrimSpace(string(respBody))
	if s == "" {
		return "", false
	}

	// Some backends may return plain text operation id.
	if !strings.HasPrefix(s, "{") && !strings.HasPrefix(s, "[") {
		return s, true
	}

	var m map[string]any
	if err := json.Unmarshal(respBody, &m); err != nil {
		return "", false
	}
	getStr := func(key string) string {
		if v, ok := m[key]; ok {
			if ss, ok := v.(string); ok {
				return strings.TrimSpace(ss)
			}
		}
		return ""
	}

	for _, k := range []string{"operationId", "operation_id", "jobId", "job_id", "id"} {
		if v := getStr(k); v != "" {
			return v, true
		}
	}

	// Nested common shapes: {"data":{"operationId":"..."}} etc.
	for _, k := range []string{"data", "result", "operation"} {
		if v, ok := m[k]; ok {
			if mm, ok := v.(map[string]any); ok {
				b, _ := json.Marshal(mm)
				if id, ok := extractOperationID(b); ok {
					return id, true
				}
			}
		}
	}

	return "", false
}

type operationStatus struct {
	Status  string
	Done    *bool
	Message string
}

func parseOperationStatus(respBody []byte) operationStatus {
	var m map[string]any
	if err := json.Unmarshal(respBody, &m); err != nil {
		// Unknown; keep polling unless caller decides otherwise.
		return operationStatus{Status: ""}
	}
	getStr := func(key string) string {
		if v, ok := m[key]; ok {
			if ss, ok := v.(string); ok {
				return strings.TrimSpace(ss)
			}
		}
		return ""
	}
	getBoolPtr := func(key string) *bool {
		if v, ok := m[key]; ok {
			if bb, ok := v.(bool); ok {
				return &bb
			}
		}
		return nil
	}

	st := operationStatus{
		Status:  getStr("status"),
		Message: firstNonEmpty(getStr("message"), getStr("error"), getStr("detail")),
		Done:    getBoolPtr("done"),
	}
	if st.Status == "" {
		st.Status = getStr("state")
	}
	if st.Message == "" {
		// some APIs wrap error as object
		if v, ok := m["error"]; ok {
			if mm, ok := v.(map[string]any); ok {
				if s, ok := mm["message"].(string); ok {
					st.Message = strings.TrimSpace(s)
				}
			}
		}
	}
	return st
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

func isOperationDone(st operationStatus) (done bool, success bool) {
	if st.Done != nil {
		if *st.Done {
			// If done is true and status indicates failure, treat as failed.
			s := strings.ToUpper(strings.TrimSpace(st.Status))
			if s == "FAILED" || s == "ERROR" {
				return true, false
			}
			return true, true
		}
		return false, false
	}
	s := strings.ToUpper(strings.TrimSpace(st.Status))
	switch s {
	case "PENDING", "RUNNING":
		return false, false
	case "DONE", "COMPLETED", "SUCCESS", "SUCCEEDED":
		return true, true
	case "FAILED", "ERROR":
		return true, false
	default:
		return false, false
	}
}

func (c *APIClient) GetOperation(ctx context.Context, operationID string) (operationStatus, error) {
	u := fmt.Sprintf("%s/operations/%s", strings.TrimRight(c.Endpoint, "/"), url.PathEscape(operationID))
	body, status, err := c.doRequestRaw(ctx, http.MethodGet, u, nil, 60*time.Minute)
	if err != nil {
		return operationStatus{}, err
	}
	if status < 200 || status >= 300 {
		return operationStatus{}, fmt.Errorf("operation status returned %d: %s", status, strings.TrimSpace(string(body)))
	}
	return parseOperationStatus(body), nil
}

func (c *APIClient) WaitForOperationDone(ctx context.Context, operationID string, timeout time.Duration) error {
	if strings.TrimSpace(operationID) == "" {
		return fmt.Errorf("missing operation id")
	}
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timeout waiting for operation %s", operationID)
		}
		st, err := c.GetOperation(ctx, operationID)
		if err != nil {
			return err
		}
		if done, ok := isOperationDone(st); done {
			if ok {
				return nil
			}
			if st.Message != "" {
				return fmt.Errorf("operation %s failed: %s", operationID, st.Message)
			}
			return fmt.Errorf("operation %s failed (status=%s)", operationID, st.Status)
		}
		time.Sleep(2 * time.Second)
	}
}

type TenantRequest struct {
	TenantName     string
	Description    string
	MaxGpusAllowed int
}

// tenantCreateAsyncFlatBody matches the async curl contract:
// tenantName, description, maxGpusAllowed, shared (flat only).
func tenantCreateAsyncFlatBody(t TenantRequest) map[string]any {
	return map[string]any{
		"tenantName":     t.TenantName,
		"description":    t.Description,
		"maxGpusAllowed": t.MaxGpusAllowed,
		"shared":         false,
	}
}

// MarshalJSON encodes tenant create as the Fabric controller expects. Java/Jackson
// commonly uses maxGPUsAllowed (capital "GPUs"). We also send maxGpusAllowed for
// older/mock backends that only read the lowercase form.
func (t TenantRequest) MarshalJSON() ([]byte, error) {
	type body struct {
		// Some controllers expect the request nested under "tenant", mirroring the response shape.
		// Others accept a flat body. To stay compatible, we include both.
		Tenant *struct {
			TenantName     string `json:"tenantName"`
			Description    string `json:"description"`
			MaxGPUsAllowed int    `json:"maxGPUsAllowed"`
			MaxGpusAllowed int    `json:"maxGpusAllowed"`
			Shared         bool   `json:"shared"`
		} `json:"tenant,omitempty"`

		TenantName     string `json:"tenantName"`
		Description    string `json:"description"`
		MaxGPUsAllowed int    `json:"maxGPUsAllowed"`
		MaxGpusAllowed int    `json:"maxGpusAllowed"`
		// Backend sample payloads include shared; async handlers may validate incorrectly if omitted.
		Shared bool `json:"shared"`
	}

	nested := &struct {
		TenantName     string `json:"tenantName"`
		Description    string `json:"description"`
		MaxGPUsAllowed int    `json:"maxGPUsAllowed"`
		MaxGpusAllowed int    `json:"maxGpusAllowed"`
		Shared         bool   `json:"shared"`
	}{
		TenantName:     t.TenantName,
		Description:    t.Description,
		MaxGPUsAllowed: t.MaxGpusAllowed,
		MaxGpusAllowed: t.MaxGpusAllowed,
		Shared:         false,
	}

	return json.Marshal(body{
		Tenant:         nested,
		TenantName:     t.TenantName,
		Description:    t.Description,
		MaxGPUsAllowed: t.MaxGpusAllowed,
		MaxGpusAllowed: t.MaxGpusAllowed,
		Shared:         false,
	})
}

type TenantServersRequest struct {
	Operation string               `json:"operation"`
	Servers   []TenantServerUpdate `json:"servers"`
}

// TenantServersDeallocateRequest matches APIs that expect a plain server name list for DELETE.
type TenantServersDeallocateRequest struct {
	Operation string   `json:"operation"`
	Servers   []string `json:"servers"`
}

type TenantServerUpdate struct {
	ServerName string `json:"serverName"`
	Shared     *bool  `json:"shared,omitempty"`
}

// API Response structure - nested under "tenant" key
type TenantAPIResponse struct {
	Tenant TenantData `json:"tenant"`
}

type TenantData struct {
	ID             int         `json:"id"`
	Name           string      `json:"name"`
	Description    string      `json:"description"`
	MaxGpusAllowed int         `json:"maxGpusAllowed"`
	GpusAllocated  int         `json:"gpusAllocated"`
	AllotedGpus    string      `json:"allotedGpus"` // Comma-separated server names
	FabricName     string      `json:"fabricName"`
	Vnets          TenantVnets `json:"vnets"`
}

func (t *TenantData) UnmarshalJSON(data []byte) error {
	var aux struct {
		ID            int         `json:"id"`
		Name          string      `json:"name"`
		Description   string      `json:"description"`
		MaxGpusLower  int         `json:"maxGpusAllowed"`
		MaxGpusJava   int         `json:"maxGPUsAllowed"`
		GpusAllocated int         `json:"gpusAllocated"`
		AllotedGpus   string      `json:"allotedGpus"`
		FabricName    string      `json:"fabricName"`
		Vnets         TenantVnets `json:"vnets"`
	}
	if err := json.Unmarshal(data, &aux); err != nil {
		return err
	}
	max := aux.MaxGpusJava
	if max == 0 {
		max = aux.MaxGpusLower
	}
	*t = TenantData{
		ID:             aux.ID,
		Name:           aux.Name,
		Description:    aux.Description,
		MaxGpusAllowed: max,
		GpusAllocated:  aux.GpusAllocated,
		AllotedGpus:    aux.AllotedGpus,
		FabricName:     aux.FabricName,
		Vnets:          aux.Vnets,
	}
	return nil
}

type TenantVnets struct {
	Name string `json:"name"`
}

type TenantResponse struct {
	TenantName     string   `json:"tenantName"`
	Description    string   `json:"description"`
	MaxGpusAllowed int      `json:"maxGpusAllowed"`
	GpusAllocated  int      `json:"gpusAllocated,omitempty"`
	Servers        []string `json:"servers,omitempty"`
	AllotedGpus    string   `json:"allotedGpus"`
	VnetsName      string   `json:"vnetsName,omitempty"`
}

type FabricsAPIResponse struct {
	Fabrics []FabricData `json:"fabrics"`
}

type FabricData struct {
	FabricName         string `json:"fabricName"`
	DefaultStorageName string `json:"defaultStorageName"`
}

type VpcPeeringRequest struct {
	Name        string `json:"name"`
	VpcName     string `json:"vpcname"`
	PeerVpcName string `json:"peervpcname"`
}

// DeviceGpus is the per-server GPU list in a gpuAllocations request.
type DeviceGpus struct {
	Gpus []string `json:"gpus"`
}

// GpuAllocationsRequest matches POST .../gpuAllocations (curl uses "operation").
// suid is SU id (string key) → hostname → { gpus: ["G0", ...] }.
type GpuAllocationsRequest struct {
	Operation string                           `json:"operation"`
	Suid      map[string]map[string]DeviceGpus `json:"suid"`
}

// CreateTenantWithFabric creates a tenant in the specified fabric
func (c *APIClient) CreateTenantWithFabric(fabricName string, tenant TenantRequest) (*TenantResponse, error) {
	resp, _, err := c.CreateTenantWithFabricWithOptions(context.Background(), fabricName, tenant, nil)
	return resp, err
}

// CreateTenantWithFabricWithOptions supports Prefer + webhook fields for async mode.
// Returns (tenantResponse, operationID, error). operationID is only set for async (202) responses.
func (c *APIClient) CreateTenantWithFabricWithOptions(
	ctx context.Context,
	fabricName string,
	tenant TenantRequest,
	opts *requestOptions,
) (*TenantResponse, string, error) {
	u := fmt.Sprintf("%s/fabrics/%s/tenants", c.Endpoint, fabricName)

	prefer := ""
	if opts != nil {
		prefer = opts.Prefer
	}
	headers := map[string]string{
		"Prefer": preferHeaderValue(prefer),
	}

	// Sync: use TenantRequest.MarshalJSON (nested+flat, dual maxGPU keys).
	// Async: match the backend curl contract exactly (flat body only, single maxGpusAllowed),
	// then optionally merge webhook fields when enabled.
	var bodyAny any
	if strings.EqualFold(preferHeaderValue(prefer), "respond-async") {
		m := tenantCreateAsyncFlatBody(tenant)
		if wh := maybeWebhookBody(opts); wh != nil {
			for k, v := range wh {
				m[k] = v
			}
		}
		bodyAny = m
	} else {
		bodyAny = tenant
	}

	respBody, status, err := c.doRequestRawWithHeaders(ctx, http.MethodPost, u, bodyAny, 60*time.Minute, headers)
	if err != nil {
		return nil, "", err
	}

	if status == http.StatusAccepted {
		if opID, ok := extractOperationID(respBody); ok {
			return &TenantResponse{
				TenantName:     tenant.TenantName,
				Description:    tenant.Description,
				MaxGpusAllowed: tenant.MaxGpusAllowed,
			}, opID, nil
		}
		return &TenantResponse{
			TenantName:     tenant.TenantName,
			Description:    tenant.Description,
			MaxGpusAllowed: tenant.MaxGpusAllowed,
		}, "", nil
	}

	if status != http.StatusOK && status != http.StatusCreated {
		reqPreview := ""
		if b, e := json.Marshal(bodyAny); e == nil {
			reqPreview = string(b)
			if len(reqPreview) > 2048 {
				reqPreview = reqPreview[:2048] + "...(truncated)"
			}
		}
		errMsg := fmt.Sprintf(
			"API returned status %d: %s (tenant create request JSON: %s)",
			status, string(respBody), reqPreview,
		)
		// Same JSON as respond-sync; only Prefer differs. Controllers that still return
		// MAX_GPUS_INVALID need a server-side fix or tenant create must use respond-sync.
		if status == http.StatusBadRequest &&
			strings.Contains(string(respBody), "MAX_GPUS_INVALID") &&
			strings.EqualFold(preferHeaderValue(prefer), "respond-async") {
			errMsg += " Hint: If the same max_gpus_allowed works with prefer=respond-sync, the async tenant endpoint is rejecting a valid body—use respond-sync for tenant creation, or have the API team fix async validation."
		}
		return nil, "", fmt.Errorf("%s", errMsg)
	}

	var apiResponse TenantAPIResponse
	if err := json.Unmarshal(respBody, &apiResponse); err != nil {
		return &TenantResponse{
			TenantName:     tenant.TenantName,
			Description:    tenant.Description,
			MaxGpusAllowed: tenant.MaxGpusAllowed,
		}, "", nil
	}

	result := &TenantResponse{
		TenantName:     apiResponse.Tenant.Name,
		Description:    apiResponse.Tenant.Description,
		MaxGpusAllowed: apiResponse.Tenant.MaxGpusAllowed,
	}
	if result.TenantName == "" {
		result.TenantName = tenant.TenantName
	}
	if result.Description == "" {
		result.Description = tenant.Description
	}
	if result.MaxGpusAllowed == 0 {
		result.MaxGpusAllowed = tenant.MaxGpusAllowed
	}

	return result, "", nil
}

// CreateTenant uses the default fabric from the client
func (c *APIClient) CreateTenant(tenant TenantRequest) (*TenantResponse, error) {
	return c.CreateTenantWithFabric(c.Fabric, tenant)
}

// GetTenantWithFabric retrieves tenant information from the specified fabric
func (c *APIClient) GetTenantWithFabric(fabricName string, tenantName string) (*TenantResponse, error) {
	url := fmt.Sprintf("%s/fabrics/%s/tenants/%s", c.Endpoint, fabricName, tenantName)

	body, status, err := c.doRequestRaw(context.Background(), http.MethodGet, url, nil, 60*time.Minute)
	if err != nil {
		return nil, err
	}

	if status == http.StatusNotFound {
		return nil, nil
	}

	if status != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", status, string(body))
	}

	// Parse the nested API response
	var apiResponse TenantAPIResponse
	if err := json.Unmarshal(body, &apiResponse); err != nil {
		return nil, fmt.Errorf("failed to parse API response: %v", err)
	}

	// Map API response to our response structure
	result := &TenantResponse{
		TenantName:     apiResponse.Tenant.Name, // Map "name" to "tenantName"
		Description:    apiResponse.Tenant.Description,
		MaxGpusAllowed: apiResponse.Tenant.MaxGpusAllowed,
		GpusAllocated:  apiResponse.Tenant.GpusAllocated,
		VnetsName:      apiResponse.Tenant.Vnets.Name,
	}

	// Parse comma-separated server names from allotedGpus
	if apiResponse.Tenant.AllotedGpus != "" {
		servers := []string{}
		for _, server := range bytes.Split([]byte(apiResponse.Tenant.AllotedGpus), []byte(",")) {
			serverName := string(bytes.TrimSpace(server))
			if serverName != "" {
				servers = append(servers, serverName)
			}
		}
		result.Servers = servers
	}

	return result, nil
}

// GetTenant uses the default fabric from the client
func (c *APIClient) GetTenant(tenantName string) (*TenantResponse, error) {
	return c.GetTenantWithFabric(c.Fabric, tenantName)
}

// ListTenants returns all tenants in a fabric
func (c *APIClient) ListTenants(
	ctx context.Context,
	fabricName string,
) ([]TenantResponse, error) {

	url := fmt.Sprintf("%s/fabrics/%s/tenants", c.Endpoint, fabricName)

	var raw struct {
		Tenants []TenantData `json:"tenants"`
	}

	if err := c.doRequest(ctx, http.MethodGet, url, nil, &raw); err != nil {
		// Some mock/testing environments may not have fabric seeded yet. Optionally
		// treat "404 Fabric does not exist" as an empty list to allow `terraform plan`
		// to run end-to-end against lightweight backends.
		allow404Empty := strings.ToLower(os.Getenv("FABRICAPI_ALLOW_FABRIC_404_EMPTY_LIST"))
		if allow404Empty == "1" || allow404Empty == "true" || allow404Empty == "yes" {
			msg := strings.ToLower(err.Error())
			// Match typical 404 bodies: "Fabric does not exist", "INVALID_FABRIC", etc.
			if strings.Contains(msg, "404") && strings.Contains(msg, "fabric") {
				return []TenantResponse{}, nil
			}
		}
		return nil, err
	}

	result := make([]TenantResponse, 0, len(raw.Tenants))

	for _, t := range raw.Tenants {
		result = append(result, TenantResponse{
			TenantName:     t.Name,
			Description:    t.Description,
			MaxGpusAllowed: t.MaxGpusAllowed,
			GpusAllocated:  t.GpusAllocated,
			AllotedGpus:    t.AllotedGpus,
		})
	}

	return result, nil
}

func (c *APIClient) doRequest(
	ctx context.Context,
	method, url string,
	body any,
	out any,
) error {
	respBody, status, err := c.doRequestRaw(ctx, method, url, body, 60*time.Minute)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("API returned %d: %s", status, string(respBody))
	}

	if out != nil {
		if len(respBody) == 0 {
			return nil
		}
		if err := json.Unmarshal(respBody, out); err != nil {
			return err
		}
	}

	return nil
}

// DeleteTenantWithFabric deletes a tenant from the specified fabric
func (c *APIClient) DeleteTenantWithFabric(fabricName string, tenantName string) error {
	_, err := c.DeleteTenantWithFabricWithOptions(context.Background(), fabricName, tenantName, nil)
	return err
}

// DeleteTenantWithFabricWithOptions supports Prefer + webhook fields for async mode.
// Returns operationID only for async (202) responses.
func (c *APIClient) DeleteTenantWithFabricWithOptions(
	ctx context.Context,
	fabricName string,
	tenantName string,
	opts *requestOptions,
) (string, error) {
	u := fmt.Sprintf("%s/fabrics/%s/tenants/%s", c.Endpoint, fabricName, tenantName)

	headers := map[string]string{
		"Prefer": preferHeaderValue(func() string {
			if opts == nil {
				return ""
			}
			return opts.Prefer
		}()),
	}

	var bodyAny any
	if wh := maybeWebhookBody(opts); wh != nil {
		bodyAny = wh
	}

	respBody, status, err := c.doRequestRawWithHeaders(ctx, http.MethodDelete, u, bodyAny, 60*time.Minute, headers)
	if err != nil {
		return "", err
	}
	if status == http.StatusAccepted {
		if opID, ok := extractOperationID(respBody); ok {
			return opID, nil
		}
		return "", nil
	}
	if status != http.StatusOK && status != http.StatusNoContent {
		return "", fmt.Errorf("API returned status %d: %s", status, string(respBody))
	}
	return "", nil
}

// DeleteTenant uses the default fabric from the client
func (c *APIClient) DeleteTenant(tenantName string) error {
	return c.DeleteTenantWithFabric(c.Fabric, tenantName)
}

// UpdateTenantServers PATCHes the tenant using the provider default fabric.
func (c *APIClient) UpdateTenantServers(tenantName string, operation string, servers []string, shared *bool) error {
	return c.UpdateTenantServersWithFabric(c.Fabric, tenantName, operation, servers, shared)
}

// UpdateTenantServersWithFabric PATCHes /fabrics/{fabric}/tenants/{tenant}.
// ADD uses server objects with serverName + optional shared; DELETE uses a plain string array.
func (c *APIClient) UpdateTenantServersWithFabric(fabricName string, tenantName string, operation string, servers []string, shared *bool) error {
	_, err := c.UpdateTenantServersWithFabricWithOptions(context.Background(), fabricName, tenantName, operation, servers, shared, nil)
	return err
}

// UpdateTenantServersWithFabricWithOptions supports Prefer + webhook fields for async mode.
// Returns operationID only for async (202) responses.
func (c *APIClient) UpdateTenantServersWithFabricWithOptions(
	ctx context.Context,
	fabricName string,
	tenantName string,
	operation string,
	servers []string,
	shared *bool,
	opts *requestOptions,
) (string, error) {
	// Normalize operation: support both DELETE and REMOVE
	operation = strings.ToUpper(strings.TrimSpace(operation))
	if operation == "REMOVE" {
		operation = "DELETE"
	}

	u := fmt.Sprintf("%s/fabrics/%s/tenants/%s", c.Endpoint, fabricName, tenantName)

	// Build base request map to easily append webhook fields.
	reqMap := map[string]any{
		"operation": operation,
	}

	if operation == "DELETE" {
		// Deallocate: {"operation":"DELETE","servers":["host1","host2"]}
		reqMap["servers"] = servers
	} else {
		serverUpdates := make([]TenantServerUpdate, 0, len(servers))
		for _, server := range servers {
			serverUpdates = append(serverUpdates, TenantServerUpdate{
				ServerName: server,
				Shared:     shared,
			})
		}
		reqMap["servers"] = serverUpdates
	}

	if wh := maybeWebhookBody(opts); wh != nil {
		for k, v := range wh {
			reqMap[k] = v
		}
	}

	headers := map[string]string{
		"Prefer": preferHeaderValue(func() string {
			if opts == nil {
				return ""
			}
			return opts.Prefer
		}()),
	}

	respBody, status, err := c.doRequestRawWithHeaders(ctx, http.MethodPatch, u, reqMap, 60*time.Minute, headers)
	if err != nil {
		return "", err
	}

	if status == http.StatusAccepted {
		if opID, ok := extractOperationID(respBody); ok {
			return opID, nil
		}
		return "", nil
	}

	if status != http.StatusOK && status != http.StatusNoContent && status != http.StatusCreated {
		return "", fmt.Errorf("API returned status %d: %s", status, string(respBody))
	}

	return "", nil
}

// ServersForDeallocation returns host names to send in a DELETE PATCH, using parsed
// servers from GET tenant when present, otherwise comma-split allotedGpus.
func ServersForDeallocation(t *TenantResponse) []string {
	if t == nil {
		return nil
	}
	if len(t.Servers) > 0 {
		return t.Servers
	}
	if t.AllotedGpus == "" {
		return nil
	}
	var out []string
	for _, s := range strings.Split(t.AllotedGpus, ",") {
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (c *APIClient) GetAllocatedServers(ctx context.Context, fabric string) (map[string]string, error) {
	tenants, err := c.ListTenants(ctx, fabric)
	if err != nil {
		return nil, err
	}

	allocated := make(map[string]string)

	for _, t := range tenants {
		if t.AllotedGpus == "" {
			continue
		}

		servers := strings.Split(t.AllotedGpus, ",")
		for _, s := range servers {
			allocated[strings.TrimSpace(s)] = t.TenantName
		}
	}

	return allocated, nil
}

// GetFabrics fetches the /fabrics list (used to resolve defaultStorageName).
func (c *APIClient) GetFabrics(ctx context.Context) ([]FabricData, error) {
	url := fmt.Sprintf("%s/fabrics", c.Endpoint)

	var raw FabricsAPIResponse
	if err := c.doRequest(ctx, http.MethodGet, url, nil, &raw); err != nil {
		return nil, err
	}

	return raw.Fabrics, nil
}

// AvailableServersResponse matches GET /fabrics/{fabric}/available_servers.
type AvailableServersResponse struct {
	AvailableGPUs []string `json:"availableGPUs"`
}

// GetAvailableServers lists free GPU server hostnames for a fabric.
func (c *APIClient) GetAvailableServers(ctx context.Context, fabricName string) ([]string, error) {
	u := fmt.Sprintf(
		"%s/fabrics/%s/available_servers",
		strings.TrimRight(c.Endpoint, "/"),
		url.PathEscape(fabricName),
	)

	var raw AvailableServersResponse
	if err := c.doRequest(ctx, http.MethodGet, u, nil, &raw); err != nil {
		return nil, err
	}

	out := make([]string, 0, len(raw.AvailableGPUs))
	seen := make(map[string]struct{}, len(raw.AvailableGPUs))
	for _, s := range raw.AvailableGPUs {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out, nil
}

// VfInterfacesResponse matches GET /fabrics/{fabric}/servers/{server}/vf-interfaces.
type VfInterfacesResponse struct {
	ServerName string        `json:"serverName"`
	FabricName string        `json:"fabricName"`
	DpuCount   int           `json:"dpuCount"`
	Dpus       []VfDpuEntry  `json:"dpus"`
}

// VfDpuEntry is one DPU and its VF interfaces on a GPU server.
type VfDpuEntry struct {
	DpuName      string         `json:"dpuName"`
	VfInterfaces []VfInterface  `json:"vfInterfaces"`
}

// VfInterface is a single VF row (DPU if_name and/or host server_if).
type VfInterface struct {
	IfName     string `json:"ifName"`
	ServerIf   string `json:"serverIf"`
	Status     string `json:"status"`
	TenantName string `json:"tenantName,omitempty"`
}

// VfAssignRequest matches POST/DELETE .../vf-interfaces/{vfId}/assign body.
type VfAssignRequest struct {
	TenantName string `json:"tenantName"`
}

// VfAssignResponse matches POST .../vf-interfaces/{vfId}/assign success body.
type VfAssignResponse struct {
	VfID          string `json:"vfId"`
	ServerName    string `json:"serverName"`
	FabricName    string `json:"fabricName"`
	TenantName    string `json:"tenantName"`
	Status        string `json:"status"`
	VlanID        *int   `json:"vlanId"`
	VniID         *int   `json:"vniId"`
	ProvisionedAt string `json:"provisionedAt"`
}

// GetVfInterfaces lists VF interfaces for a GPU server (HBN / DPU offload fabrics).
func (c *APIClient) GetVfInterfaces(ctx context.Context, fabricName, serverName string) (*VfInterfacesResponse, error) {
	fabricName = strings.TrimSpace(fabricName)
	serverName = strings.TrimSpace(serverName)
	if fabricName == "" {
		return nil, fmt.Errorf("fabric name is required")
	}
	if serverName == "" {
		return nil, fmt.Errorf("server name is required")
	}

	u := fmt.Sprintf(
		"%s/fabrics/%s/servers/%s/vf-interfaces",
		strings.TrimRight(c.Endpoint, "/"),
		url.PathEscape(fabricName),
		url.PathEscape(serverName),
	)

	var raw VfInterfacesResponse
	if err := c.doRequest(ctx, http.MethodGet, u, nil, &raw); err != nil {
		return nil, err
	}
	return &raw, nil
}

// FindVfInterface locates a VF by path id (DPU if_name or host server_if) within a list response.
func FindVfInterface(resp *VfInterfacesResponse, vfID string) (dpuName string, vf VfInterface, ok bool) {
	vfID = strings.TrimSpace(vfID)
	if resp == nil || vfID == "" {
		return "", VfInterface{}, false
	}
	for _, dpu := range resp.Dpus {
		for _, entry := range dpu.VfInterfaces {
			if strings.EqualFold(strings.TrimSpace(entry.IfName), vfID) ||
				strings.EqualFold(strings.TrimSpace(entry.ServerIf), vfID) {
				return dpu.DpuName, entry, true
			}
		}
	}
	return "", VfInterface{}, false
}

// AssignVf binds a VF interface to a tenant VLAN (POST .../vf-interfaces/{vfId}/assign).
// prefer may be empty (defaults to respond-sync).
func (c *APIClient) AssignVf(ctx context.Context, fabricName, serverName, vfID, tenantName, prefer string) (*VfAssignResponse, error) {
	fabricName = strings.TrimSpace(fabricName)
	serverName = strings.TrimSpace(serverName)
	vfID = strings.TrimSpace(vfID)
	tenantName = strings.TrimSpace(tenantName)
	if fabricName == "" || serverName == "" || vfID == "" || tenantName == "" {
		return nil, fmt.Errorf("fabric, server, vf id, and tenant name are required")
	}

	u := fmt.Sprintf(
		"%s/fabrics/%s/servers/%s/vf-interfaces/%s/assign",
		strings.TrimRight(c.Endpoint, "/"),
		url.PathEscape(fabricName),
		url.PathEscape(serverName),
		url.PathEscape(vfID),
	)

	headers := map[string]string{
		"Prefer": preferHeaderValue(prefer),
	}
	body := VfAssignRequest{TenantName: tenantName}

	respBody, status, err := c.doRequestRawWithHeaders(ctx, http.MethodPost, u, body, 60*time.Minute, headers)
	if err != nil {
		return nil, err
	}
	if status < 200 || status >= 300 {
		return nil, fmt.Errorf("API returned %d: %s", status, strings.TrimSpace(string(respBody)))
	}

	var out VfAssignResponse
	if len(respBody) > 0 {
		if err := json.Unmarshal(respBody, &out); err != nil {
			return nil, fmt.Errorf("decode assign response: %w (body=%s)", err, strings.TrimSpace(string(respBody)))
		}
	}
	return &out, nil
}

// UnassignVf unbinds a VF from its tenant VLAN (DELETE .../vf-interfaces/{vfId}/assign).
// The request body includes tenantName to match the documented Fabric API sample.
// prefer may be empty (defaults to respond-sync).
func (c *APIClient) UnassignVf(ctx context.Context, fabricName, serverName, vfID, tenantName, prefer string) error {
	fabricName = strings.TrimSpace(fabricName)
	serverName = strings.TrimSpace(serverName)
	vfID = strings.TrimSpace(vfID)
	tenantName = strings.TrimSpace(tenantName)
	if fabricName == "" || serverName == "" || vfID == "" || tenantName == "" {
		return fmt.Errorf("fabric, server, vf id, and tenant name are required")
	}

	u := fmt.Sprintf(
		"%s/fabrics/%s/servers/%s/vf-interfaces/%s/assign",
		strings.TrimRight(c.Endpoint, "/"),
		url.PathEscape(fabricName),
		url.PathEscape(serverName),
		url.PathEscape(vfID),
	)

	headers := map[string]string{
		"Prefer": preferHeaderValue(prefer),
	}
	body := VfAssignRequest{TenantName: tenantName}

	respBody, status, err := c.doRequestRawWithHeaders(ctx, http.MethodDelete, u, body, 60*time.Minute, headers)
	if err != nil {
		return err
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("API returned %d: %s", status, strings.TrimSpace(string(respBody)))
	}
	return nil
}

// CreateVpcPeering creates a VPC peering on the target fabric.
func (c *APIClient) CreateVpcPeering(ctx context.Context, targetFabric string, req VpcPeeringRequest) error {
	_, err := c.CreateVpcPeeringWithResponse(ctx, targetFabric, req)
	return err
}

// CreateVpcPeeringWithResponse creates VPC peering and returns the raw response body (if any).
// Some backends return a human-readable success message in the response body; surfacing it makes
// `terraform apply` logs match what users see with curl.
func (c *APIClient) CreateVpcPeeringWithResponse(ctx context.Context, targetFabric string, reqBody VpcPeeringRequest) (string, error) {
	bodyStr, _, err := c.CreateVpcPeeringWithResponseAndOptions(ctx, targetFabric, reqBody, nil)
	return bodyStr, err
}

// CreateVpcPeeringWithResponseAndOptions creates VPC peering with Prefer + optional webhook fields.
// Returns (response body text, operation id for 202 async, error).
func (c *APIClient) CreateVpcPeeringWithResponseAndOptions(
	ctx context.Context,
	targetFabric string,
	reqBody VpcPeeringRequest,
	opts *requestOptions,
) (string, string, error) {
	u := fmt.Sprintf("%s/fabrics/%s/vpcpeering", strings.TrimRight(c.Endpoint, "/"), url.PathEscape(targetFabric))

	prefer := ""
	if opts != nil {
		prefer = opts.Prefer
	}
	headers := map[string]string{
		"Prefer": preferHeaderValue(prefer),
	}

	var bodyAny any = reqBody
	if wh := maybeWebhookBody(opts); wh != nil {
		b, err := json.Marshal(reqBody)
		if err != nil {
			return "", "", err
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			return "", "", err
		}
		for k, v := range wh {
			m[k] = v
		}
		bodyAny = m
	}

	respBody, status, err := c.doRequestRawWithHeaders(ctx, http.MethodPost, u, bodyAny, 60*time.Minute, headers)
	if err != nil {
		return "", "", err
	}
	bodyStr := strings.TrimSpace(string(respBody))

	if status == http.StatusAccepted {
		opID, _ := extractOperationID(respBody)
		return bodyStr, opID, nil
	}

	if status < 200 || status >= 300 {
		if bodyStr == "" {
			return "", "", fmt.Errorf("API returned %d", status)
		}
		return "", "", fmt.Errorf("API returned %d: %s", status, bodyStr)
	}

	return bodyStr, "", nil
}

// CreateGpuAllocationsWithOptions POSTs /fabrics/{fabric}/tenants/{tenant}/gpuAllocations.
// Body matches the FM curl contract: {"operation":"ADD|DELETE","suid":{...}} plus optional webhooks.
// Returns operationID only for async (202) responses.
func (c *APIClient) CreateGpuAllocationsWithOptions(
	ctx context.Context,
	fabricName string,
	tenantName string,
	reqBody GpuAllocationsRequest,
	opts *requestOptions,
) (string, error) {
	u := fmt.Sprintf(
		"%s/fabrics/%s/tenants/%s/gpuAllocations",
		strings.TrimRight(c.Endpoint, "/"),
		url.PathEscape(fabricName),
		url.PathEscape(tenantName),
	)

	prefer := ""
	if opts != nil {
		prefer = opts.Prefer
	}
	headers := map[string]string{
		"Prefer": preferHeaderValue(prefer),
	}

	reqMap := map[string]any{
		"operation": strings.ToUpper(strings.TrimSpace(reqBody.Operation)),
		"suid":      reqBody.Suid,
	}
	if wh := maybeWebhookBody(opts); wh != nil {
		for k, v := range wh {
			reqMap[k] = v
		}
	}

	respBody, status, err := c.doRequestRawWithHeaders(ctx, http.MethodPost, u, reqMap, 60*time.Minute, headers)
	if err != nil {
		return "", err
	}

	if status == http.StatusAccepted {
		if opID, ok := extractOperationID(respBody); ok {
			return opID, nil
		}
		return "", nil
	}

	if status != http.StatusOK && status != http.StatusNoContent && status != http.StatusCreated {
		return "", fmt.Errorf("API returned status %d: %s", status, string(respBody))
	}

	return "", nil
}

// FabricDataRequest matches the ONES UI "config" service contract:
// POST {config_endpoint}/api/config/addFabricData.
type FabricDataRequest struct {
	Name              string            `json:"name"`
	Type              string            `json:"type"`
	Status            string            `json:"status"`
	Description       string            `json:"description"`
	NumOfSus          int               `json:"numOfSus"`
	MaxNumOfSus       int               `json:"maxNumOfSus"`
	HostMap           map[string]string `json:"hostMap"`
	StartingSubnetGpu string            `json:"startingSubnetGpu"`
	SimulationID      int               `json:"simulationId"`
	EnableEW          bool              `json:"enableEW"`
	SuHostCnt         string            `json:"suHostCnt"`
	Tenant            string            `json:"tenant"`
	Instance          string            `json:"instance"`
}

// rawTokenHeaderValue strips any "Bearer "/"bearer " prefix so the token is sent
// exactly as-is. Unlike the Fabric API (Endpoint), the config service's
// addFabricData endpoint expects the raw JWT with no "Bearer " prefix.
func rawTokenHeaderValue(token string) string {
	tok := strings.TrimSpace(token)
	if strings.HasPrefix(strings.ToLower(tok), "bearer ") {
		return strings.TrimSpace(tok[len("bearer "):])
	}
	return tok
}

// CreateFabricData POSTs {config_endpoint}/api/config/addFabricData, used by the
// ONES UI to create/generate a fabric. It reuses the provider's login/token flow
// (via ensureToken) but sends the raw token in Authorization, without the "Bearer "
// prefix used by every other Fabric API call in this client.
func (c *APIClient) CreateFabricData(ctx context.Context, reqBody FabricDataRequest) (string, error) {
	base := strings.TrimRight(c.ConfigEndpoint, "/")
	if base == "" {
		return "", fmt.Errorf(
			"config endpoint not configured; set provider attribute `config_endpoint` or environment variable `FABRIC_API_CONFIG_ENDPOINT`",
		)
	}
	u := base + "/api/config/addFabricData"

	if err := c.ensureToken(ctx); err != nil {
		return "", err
	}

	headers := map[string]string{
		"Authorization": rawTokenHeaderValue(c.Token),
	}

	respBody, status, err := c.doRequestRawWithHeaders(ctx, http.MethodPost, u, reqBody, 60*time.Minute, headers)
	if err != nil {
		return "", err
	}

	bodyStr := strings.TrimSpace(string(respBody))
	if status < 200 || status >= 300 {
		if bodyStr == "" {
			return "", fmt.Errorf("API returned %d", status)
		}
		return "", fmt.Errorf("API returned %d: %s", status, bodyStr)
	}

	return bodyStr, nil
}

// DeleteFabricData DELETEs {config_endpoint}/api/config/deletefabricbyname/{name}, the
// same ONES UI config service used by CreateFabricData (same host, same raw-token
// Authorization header with no "Bearer " prefix).
func (c *APIClient) DeleteFabricData(ctx context.Context, name string) (string, error) {
	base := strings.TrimRight(c.ConfigEndpoint, "/")
	if base == "" {
		return "", fmt.Errorf(
			"config endpoint not configured; set provider attribute `config_endpoint` or environment variable `FABRIC_API_CONFIG_ENDPOINT`",
		)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("fabric name is required")
	}
	u := fmt.Sprintf("%s/api/config/deletefabricbyname/%s", base, url.PathEscape(name))

	if err := c.ensureToken(ctx); err != nil {
		return "", err
	}

	headers := map[string]string{
		"Authorization": rawTokenHeaderValue(c.Token),
	}

	respBody, status, err := c.doRequestRawWithHeaders(ctx, http.MethodDelete, u, nil, 60*time.Minute, headers)
	if err != nil {
		return "", err
	}

	bodyStr := strings.TrimSpace(string(respBody))
	if status < 200 || status >= 300 {
		if bodyStr == "" {
			return "", fmt.Errorf("API returned %d", status)
		}
		return "", fmt.Errorf("API returned %d: %s", status, bodyStr)
	}

	return bodyStr, nil
}

// GetFabricYaml fetches the raw generated fabric YAML: GET {config_endpoint}/fabrics/{name}.
// This is the same file the ONES UI fetches (and re-parses) right before pushing it via
// PushFabricConfig — it's written server-side by addFabricData's topology generation step,
// so the fabric must already exist (via CreateFabricData) before this call succeeds. Note
// this path is mounted at the config service's root, not under /api.
func (c *APIClient) GetFabricYaml(ctx context.Context, name string) (string, error) {
	base := strings.TrimRight(c.ConfigEndpoint, "/")
	if base == "" {
		return "", fmt.Errorf(
			"config endpoint not configured; set provider attribute `config_endpoint` or environment variable `FABRIC_API_CONFIG_ENDPOINT`",
		)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("fabric name is required")
	}
	u := fmt.Sprintf("%s/fabrics/%s", base, url.PathEscape(name))

	if err := c.ensureToken(ctx); err != nil {
		return "", err
	}

	headers := map[string]string{
		"Authorization": rawTokenHeaderValue(c.Token),
	}

	respBody, status, err := c.doRequestRawWithHeaders(ctx, http.MethodGet, u, nil, 60*time.Minute, headers)
	if err != nil {
		return "", err
	}

	if status < 200 || status >= 300 {
		body := strings.TrimSpace(string(respBody))
		if body == "" {
			return "", fmt.Errorf("API returned %d", status)
		}
		return "", fmt.Errorf("API returned %d: %s", status, body)
	}

	return string(respBody), nil
}

// PushFabricConfigRequest matches POST {config_endpoint}/api/config — step 1 of the ONES
// UI's "Deploy Fabric" action: pushing the fabric's generated config to the real switches.
// Data should be the parsed contents of GetFabricYaml's output (e.g. via yaml.Unmarshal into
// a generic map), matching what the UI does with js-yaml before sending this request.
type PushFabricConfigRequest struct {
	Data       any    `json:"data"`
	FabricName string `json:"fabricName"`
	Instance   string `json:"instance"`
}

// PushFabricConfig POSTs {config_endpoint}/api/config. The response body from this endpoint
// is a plain string/token from the downstream FM engine, not a JSON envelope, so it's
// returned as-is.
func (c *APIClient) PushFabricConfig(ctx context.Context, fabricName, instance string, data any) (string, error) {
	base := strings.TrimRight(c.ConfigEndpoint, "/")
	if base == "" {
		return "", fmt.Errorf(
			"config endpoint not configured; set provider attribute `config_endpoint` or environment variable `FABRIC_API_CONFIG_ENDPOINT`",
		)
	}
	fabricName = strings.TrimSpace(fabricName)
	if fabricName == "" {
		return "", fmt.Errorf("fabric name is required")
	}
	u := base + "/api/config"

	if err := c.ensureToken(ctx); err != nil {
		return "", err
	}

	headers := map[string]string{
		"Authorization": rawTokenHeaderValue(c.Token),
	}

	reqBody := PushFabricConfigRequest{
		Data:       data,
		FabricName: fabricName,
		Instance:   instance,
	}

	respBody, status, err := c.doRequestRawWithHeaders(ctx, http.MethodPost, u, reqBody, 60*time.Minute, headers)
	if err != nil {
		return "", err
	}

	bodyStr := strings.TrimSpace(string(respBody))
	if status < 200 || status >= 300 {
		if bodyStr == "" {
			return "", fmt.Errorf("API returned %d", status)
		}
		return "", fmt.Errorf("API returned %d: %s", status, bodyStr)
	}

	return bodyStr, nil
}

// UpdateFabricStatusRequest matches POST {config_endpoint}/api/config/updatefabricstatus —
// step 2 of the ONES UI's "Deploy Fabric" action: marking the fabric Deployed after
// PushFabricConfig succeeds. Status is normally the literal string "Deployed".
type UpdateFabricStatusRequest struct {
	Name           string `json:"name"`
	Status         string `json:"status"`
	Description    string `json:"description"`
	DeploymentType string `json:"deploymentType"`
}

// UpdateFabricStatus POSTs {config_endpoint}/api/config/updatefabricstatus.
func (c *APIClient) UpdateFabricStatus(ctx context.Context, reqBody UpdateFabricStatusRequest) (string, error) {
	base := strings.TrimRight(c.ConfigEndpoint, "/")
	if base == "" {
		return "", fmt.Errorf(
			"config endpoint not configured; set provider attribute `config_endpoint` or environment variable `FABRIC_API_CONFIG_ENDPOINT`",
		)
	}
	if strings.TrimSpace(reqBody.Name) == "" {
		return "", fmt.Errorf("fabric name is required")
	}
	u := base + "/api/config/updatefabricstatus"

	if err := c.ensureToken(ctx); err != nil {
		return "", err
	}

	headers := map[string]string{
		"Authorization": rawTokenHeaderValue(c.Token),
	}

	respBody, status, err := c.doRequestRawWithHeaders(ctx, http.MethodPost, u, reqBody, 60*time.Minute, headers)
	if err != nil {
		return "", err
	}

	bodyStr := strings.TrimSpace(string(respBody))
	if status < 200 || status >= 300 {
		if bodyStr == "" {
			return "", fmt.Errorf("API returned %d", status)
		}
		return "", fmt.Errorf("API returned %d: %s", status, bodyStr)
	}

	return bodyStr, nil
}

// FabricDeviceInput is one switch/server/DPU's connection info, shared across the
// uploadip, validateswitch, validateserver, and updateinventory calls in the fabric deploy
// sequence (see FabricDeployResource). Each endpoint wants a slightly different wire shape,
// built by the helper functions below.
type FabricDeviceInput struct {
	Hostname    string
	IP          string
	Username    string
	Password    string
	DeviceType  string // e.g. "switch", "server", "dpu"
	DeviceRole  string // e.g. "spine", "leaf", "host"
	ApplyConfig bool
}

// yesNo matches the "Yes"/"No" string convention used by the ONES UI's device
// import spreadsheet (applyConfig/configure columns), which the backend expects verbatim.
func yesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}

type uploadIPDeviceEntry struct {
	Hostname string `json:"hostname"`
	IP       string `json:"ip"`
	Username string `json:"username"`
	Password string `json:"password"`
	// ApplyConfig/Applyconfig: both casings are sent because the ONES UI's own request
	// body includes both (confirmed against its source) — the backend's exact usage of
	// each isn't documented, so both are set to the same value to match observed behavior.
	ApplyConfig string `json:"applyConfig"`
	Applyconfig string `json:"applyconfig"`
	Configure   string `json:"configure"`
}

// UploadFabricDeviceIPsRequest matches POST {config_endpoint}/api/config/uploadip.
type UploadFabricDeviceIPsRequest struct {
	UpdatedDevices []uploadIPDeviceEntry `json:"updatedDevices"`
	FabricName     string                `json:"fabricName"`
}

// UploadFabricDeviceIPs fills in real IP/username/password for a fabric's devices,
// patching them into the generated YAML on disk. This is step 1 of the deploy sequence,
// matching the ONES UI's device-import step on the fabric's Deploy tab.
func (c *APIClient) UploadFabricDeviceIPs(ctx context.Context, fabricName string, devices []FabricDeviceInput) (string, error) {
	base := strings.TrimRight(c.ConfigEndpoint, "/")
	if base == "" {
		return "", fmt.Errorf(
			"config endpoint not configured; set provider attribute `config_endpoint` or environment variable `FABRIC_API_CONFIG_ENDPOINT`",
		)
	}
	fabricName = strings.TrimSpace(fabricName)
	if fabricName == "" {
		return "", fmt.Errorf("fabric name is required")
	}
	u := base + "/api/config/uploadip"

	entries := make([]uploadIPDeviceEntry, 0, len(devices))
	for _, d := range devices {
		applyConfig := yesNo(d.ApplyConfig)
		entries = append(entries, uploadIPDeviceEntry{
			Hostname:    d.Hostname,
			IP:          d.IP,
			Username:    d.Username,
			Password:    d.Password,
			ApplyConfig: applyConfig,
			Applyconfig: applyConfig,
			Configure:   applyConfig,
		})
	}
	reqBody := UploadFabricDeviceIPsRequest{
		UpdatedDevices: entries,
		FabricName:     fabricName,
	}

	if err := c.ensureToken(ctx); err != nil {
		return "", err
	}

	headers := map[string]string{
		"Authorization": rawTokenHeaderValue(c.Token),
	}

	respBody, status, err := c.doRequestRawWithHeaders(ctx, http.MethodPost, u, reqBody, 60*time.Minute, headers)
	if err != nil {
		return "", err
	}

	bodyStr := strings.TrimSpace(string(respBody))
	if status < 200 || status >= 300 {
		if bodyStr == "" {
			return "", fmt.Errorf("API returned %d", status)
		}
		return "", fmt.Errorf("API returned %d: %s", status, bodyStr)
	}

	return bodyStr, nil
}

type validateDeviceEntry struct {
	Hostname    string `json:"hostname"`
	IP          string `json:"ip"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	DeviceType  string `json:"deviceType,omitempty"`
	DeviceRole  string `json:"deviceRole,omitempty"`
	ApplyConfig string `json:"applyConfig,omitempty"`
	FabricName  string `json:"fabricName,omitempty"`
}

// ValidateDeviceResult is one element of the validateswitch/validateserver response array.
// A successful switch check sets Build; a successful server check sets OS; a failed check
// sets Error.
type ValidateDeviceResult struct {
	Hostname string `json:"hostname"`
	IP       string `json:"ip"`
	Username string `json:"username"`
	Build    string `json:"build,omitempty"`
	OS       string `json:"os,omitempty"`
	Error    string `json:"error,omitempty"`
}

func buildValidateDeviceEntries(fabricName string, devices []FabricDeviceInput) []validateDeviceEntry {
	entries := make([]validateDeviceEntry, 0, len(devices))
	for _, d := range devices {
		entries = append(entries, validateDeviceEntry{
			Hostname:    d.Hostname,
			IP:          d.IP,
			Username:    d.Username,
			Password:    d.Password,
			DeviceType:  d.DeviceType,
			DeviceRole:  d.DeviceRole,
			ApplyConfig: yesNo(d.ApplyConfig),
			FabricName:  fabricName,
		})
	}
	return entries
}

func (c *APIClient) validateDevices(ctx context.Context, path string, entries []validateDeviceEntry) ([]ValidateDeviceResult, error) {
	base := strings.TrimRight(c.ConfigEndpoint, "/")
	if base == "" {
		return nil, fmt.Errorf(
			"config endpoint not configured; set provider attribute `config_endpoint` or environment variable `FABRIC_API_CONFIG_ENDPOINT`",
		)
	}
	u := base + path

	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}

	headers := map[string]string{
		"Authorization": rawTokenHeaderValue(c.Token),
	}

	respBody, status, err := c.doRequestRawWithHeaders(ctx, http.MethodPost, u, entries, 60*time.Minute, headers)
	if err != nil {
		return nil, err
	}

	if status < 200 || status >= 300 {
		body := strings.TrimSpace(string(respBody))
		if body == "" {
			return nil, fmt.Errorf("API returned %d", status)
		}
		return nil, fmt.Errorf("API returned %d: %s", status, body)
	}

	var results []ValidateDeviceResult
	if err := json.Unmarshal(respBody, &results); err != nil {
		return nil, fmt.Errorf("decode validate response: %w (body=%s)", err, strings.TrimSpace(string(respBody)))
	}
	return results, nil
}

// ValidateSwitches POSTs {config_endpoint}/api/config/validateswitch — SSHes into each
// spine/leaf device and checks reachability/build. A device without Build set (and no
// Error) should be treated as a validation failure by the caller.
func (c *APIClient) ValidateSwitches(ctx context.Context, fabricName string, devices []FabricDeviceInput) ([]ValidateDeviceResult, error) {
	return c.validateDevices(ctx, "/api/config/validateswitch", buildValidateDeviceEntries(fabricName, devices))
}

// ValidateServers POSTs {config_endpoint}/api/config/validateserver — SSHes into each
// host/DPU device and checks OS/container state. A device without OS set (and no Error)
// should be treated as a validation failure by the caller.
func (c *APIClient) ValidateServers(ctx context.Context, fabricName string, devices []FabricDeviceInput) ([]ValidateDeviceResult, error) {
	return c.validateDevices(ctx, "/api/config/validateserver", buildValidateDeviceEntries(fabricName, devices))
}

type updateInventoryEntry struct {
	FabricName string `json:"fabricName"`
	Hostname   string `json:"hostname"`
	IPAddress  string `json:"ipAddress"`
	Username   string `json:"username"`
	Password   string `json:"password"`
	Configure  string `json:"configure"`
}

// UpdateFabricInventory POSTs {config_endpoint}/api/config/updateinventory, pushing device
// credentials to the downstream FM engine's inventory. This runs after validation succeeds
// and just before the final config push.
func (c *APIClient) UpdateFabricInventory(ctx context.Context, fabricName string, devices []FabricDeviceInput) (string, error) {
	base := strings.TrimRight(c.ConfigEndpoint, "/")
	if base == "" {
		return "", fmt.Errorf(
			"config endpoint not configured; set provider attribute `config_endpoint` or environment variable `FABRIC_API_CONFIG_ENDPOINT`",
		)
	}
	fabricName = strings.TrimSpace(fabricName)
	if fabricName == "" {
		return "", fmt.Errorf("fabric name is required")
	}
	u := base + "/api/config/updateinventory"

	entries := make([]updateInventoryEntry, 0, len(devices))
	for _, d := range devices {
		entries = append(entries, updateInventoryEntry{
			FabricName: fabricName,
			Hostname:   d.Hostname,
			IPAddress:  d.IP,
			Username:   d.Username,
			Password:   d.Password,
			Configure:  yesNo(d.ApplyConfig),
		})
	}

	if err := c.ensureToken(ctx); err != nil {
		return "", err
	}

	headers := map[string]string{
		"Authorization": rawTokenHeaderValue(c.Token),
	}

	respBody, status, err := c.doRequestRawWithHeaders(ctx, http.MethodPost, u, entries, 60*time.Minute, headers)
	if err != nil {
		return "", err
	}

	bodyStr := strings.TrimSpace(string(respBody))
	if status < 200 || status >= 300 {
		if bodyStr == "" {
			return "", fmt.Errorf("API returned %d", status)
		}
		return "", fmt.Errorf("API returned %d: %s", status, bodyStr)
	}

	return bodyStr, nil
}

func (c *APIClient) WaitForTenantReady(
	ctx context.Context,
	fabric string,
	tenantName string,
	timeout time.Duration,
) error {

	start := time.Now()

	for {
		if time.Since(start) > timeout {
			return fmt.Errorf("timeout waiting for tenant %s to become ready", tenantName)
		}

		tenant, err := c.GetTenantWithFabric(fabric, tenantName)
		if err != nil {
			return err
		}

		// READY when config exists OR tenant readable
		if tenant != nil {
			return nil
		}

		time.Sleep(2 * time.Second)
	}
}
