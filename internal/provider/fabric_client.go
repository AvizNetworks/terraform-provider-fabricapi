package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// This file holds every client method for the ONES UI "config" service
// (addFabricData, deploy sequence, etc.) — split out from client.go, which
// otherwise grew past 1700 lines mixing this with the unrelated main Fabric
// API client methods.

const maxAPIErrorBodyLength = 8 * 1024

// apiErrorBody returns a bounded response body suitable for diagnostic messages.
// Error responses can originate from an upstream proxy and may contain a large
// HTML or diagnostic page, so the complete body is never included in Terraform
// logs/diagnostics.
func apiErrorBody(body []byte) string {
	const truncatedSuffix = "...(truncated)"

	message := strings.TrimSpace(string(body))
	if len(message) <= maxAPIErrorBodyLength {
		return message
	}

	limit := maxAPIErrorBodyLength - len(truncatedSuffix)
	return message[:limit] + truncatedSuffix
}

// doConfigRequest consolidates the repeated config-endpoint-check + token +
// raw-Authorization-header pattern shared by every ONES UI config-service call.
func (c *APIClient) doConfigRequest(ctx context.Context, method, path string, body any) ([]byte, int, error) {
	base := strings.TrimRight(c.ConfigEndpoint, "/")
	if base == "" {
		return nil, 0, fmt.Errorf(
			"config endpoint not configured; set provider attribute `config_endpoint` or environment variable `FABRIC_API_CONFIG_ENDPOINT`",
		)
	}
	if err := c.ensureToken(ctx); err != nil {
		return nil, 0, err
	}

	headers := map[string]string{
		"Authorization": rawTokenHeaderValue(c.Token),
	}

	return c.doRequestRawWithHeaders(ctx, method, base+path, body, 60*time.Minute, headers)
}

// configRequestString runs doConfigRequest and reduces the result to the (string,
// error) shape most config-service calls return, treating any non-2xx status as
// an error.
func (c *APIClient) configRequestString(ctx context.Context, method, path string, body any) (string, error) {
	respBody, status, err := c.doConfigRequest(ctx, method, path, body)
	if err != nil {
		return "", err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		bodyStr := apiErrorBody(respBody)
		if bodyStr == "" {
			return "", fmt.Errorf("API returned %d", status)
		}
		return "", fmt.Errorf("API returned %d: %s", status, bodyStr)
	}
	return strings.TrimSpace(string(respBody)), nil
}

// FabricDataRequest matches the ONES UI "config" service contract:
// POST {config_endpoint}/api/config/addFabricData.
type FabricDataRequest struct {
	Name              string            `json:"name"`
	Type              string            `json:"type"`
	Status            string            `json:"status"`
	Description       string            `json:"description"`
	NumOfSUs          int               `json:"numOfSus"`
	MaxNumOfSUs       int               `json:"maxNumOfSus"`
	HostMap           map[string]string `json:"hostMap"`
	StartingSubnetGPU string            `json:"startingSubnetGpu"`
	SimulationID      int               `json:"simulationId"`
	EnableEW          bool              `json:"enableEW"`
	SUHostCount       string            `json:"suHostCnt"`
	Tenant            string            `json:"tenant"`
	Instance          string            `json:"instance"`

	// North-South (front-end user/storage) networking — mirrors the ONES UI's
	// "N-S (Front-End) Network" section in FabricNetwork.jsx. IsONESControlled
	// is always true here (matching the UI's default for RA/DNO fabrics), which
	// is why StartingSubnetTenants is optional rather than required.
	EnableNS              bool   `json:"enableNS"`
	IsONESControlled      bool   `json:"isOnesControlled"`
	UserAndStorage        bool   `json:"userandstorage"`
	DedicatedStorage      bool   `json:"dedicatedStorage"`
	FrontendStorage       bool   `json:"frontendStorage"`
	StartingSubnetCPU     string `json:"startingSubnetCpu,omitempty"`
	StartingSubnetStorage string `json:"startingSubnetStorage,omitempty"`
	StartingSubnetTenants string `json:"startingSubnetTenants,omitempty"`

	// NodeType is the GPU node hardware (e.g. "dgx", "gb200", "gb300"),
	// matching the ONES UI's nodeType field. Defaults to "dgx".
	NodeType string `json:"nodeType"`

	// Operating system per network leg, matching the ONES UI's OSRadioGroup
	// (FabricNetwork.jsx) — "cumulus" or "sonic". OperatingSystemE (east-west)
	// is always "cumulus" in the UI (not user-selectable there, so not exposed
	// here either). OperatingSystemF/OperatingSystemS (front-end/storage, i.e.
	// the north-south leaf/spine roles from StorageFrontendStrategy) are set
	// together from a single UI toggle, since validateswitch checks each
	// device's actual OS against whichever value was sent here — a mismatch
	// (e.g. real SONiC switches with this left at the "cumulus" default)
	// fails validation with "Device type mismatch: Expected Cumulus but got
	// non-Cumulus".
	OperatingSystemE string `json:"operatingSystemE"`
	OperatingSystemF string `json:"operatingSystemF"`
	OperatingSystemS string `json:"operatingSystemS"`
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
	return c.configRequestString(ctx, http.MethodPost, "/api/config/addFabricData", reqBody)
}

// DeleteFabricData DELETEs {config_endpoint}/api/config/deletefabricbyname/{name}, the
// same ONES UI config service used by CreateFabricData (same host, same raw-token
// Authorization header with no "Bearer " prefix). A 404 is treated as success — the
// fabric is already gone, which is what a delete is trying to achieve — so destroy
// stays idempotent against a fabric that was already removed some other way.
func (c *APIClient) DeleteFabricData(ctx context.Context, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("fabric name is required")
	}

	respBody, status, err := c.doConfigRequest(ctx, http.MethodDelete, "/api/config/deletefabricbyname/"+url.PathEscape(name), nil)
	if err != nil {
		return "", err
	}
	if status == http.StatusNotFound {
		return strings.TrimSpace(string(respBody)), nil
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		bodyStr := apiErrorBody(respBody)
		if bodyStr == "" {
			return "", fmt.Errorf("API returned %d", status)
		}
		return "", fmt.Errorf("API returned %d: %s", status, bodyStr)
	}
	return strings.TrimSpace(string(respBody)), nil
}

// maxFabricYAMLSize caps how large a generated fabric YAML response is accepted
// before being treated as an error, rather than holding an unbounded amount of
// data from a misbehaving or compromised upstream. This is a post-read check —
// doRequestRawWithHeaders still buffers the full response first — not a true
// streaming limit.
const maxFabricYAMLSize = 4 * 1024 * 1024 // 4 MiB

// GetFabricYaml fetches the raw generated fabric YAML: GET {config_endpoint}/fabrics/{name}.
// This is the same file the ONES UI fetches (and re-parses) right before pushing it via
// PushFabricConfig — it's written server-side by addFabricData's topology generation step,
// so the fabric must already exist (via CreateFabricData) before this call succeeds. Note
// this path is mounted at the config service's root, not under /api.
func (c *APIClient) GetFabricYaml(ctx context.Context, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("fabric name is required")
	}

	respBody, status, err := c.doConfigRequest(ctx, http.MethodGet, "/fabrics/"+url.PathEscape(name), nil)
	if err != nil {
		return "", err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		bodyStr := apiErrorBody(respBody)
		if bodyStr == "" {
			return "", fmt.Errorf("API returned %d", status)
		}
		return "", fmt.Errorf("API returned %d: %s", status, bodyStr)
	}
	if len(respBody) > maxFabricYAMLSize {
		return "", fmt.Errorf("fabric YAML response exceeds %d bytes", maxFabricYAMLSize)
	}
	return string(respBody), nil
}

// PushFabricConfigRequest matches POST {config_endpoint}/api/config — step 1 of the ONES
// UI's "Deploy Fabric" action: pushing the fabric's generated config to the real switches.
// Data must be pre-encoded JSON (json.RawMessage), not a generic Go map/any — json.Marshal
// on a map[string]any always sorts keys alphabetically, which would silently reorder every
// key in the fabric's YAML on every deploy. Callers should build Data with an order-
// preserving conversion (see fabric_deploy_resource.go's yamlNodeToOrderedJSON) from the
// YAML fetched via GetFabricYaml, so only the fields that actually changed (e.g. device
// credentials from UploadFabricDeviceIPs) differ from the original document.
type PushFabricConfigRequest struct {
	Data       json.RawMessage `json:"data"`
	FabricName string          `json:"fabricName"`
	Instance   string          `json:"instance"`
}

// PushFabricConfig POSTs {config_endpoint}/api/config. The response body from this endpoint
// is a plain string/token from the downstream FM engine, not a JSON envelope, so it's
// returned as-is.
func (c *APIClient) PushFabricConfig(ctx context.Context, fabricName, instance string, data json.RawMessage) (string, error) {
	fabricName = strings.TrimSpace(fabricName)
	if fabricName == "" {
		return "", fmt.Errorf("fabric name is required")
	}

	reqBody := PushFabricConfigRequest{
		Data:       data,
		FabricName: fabricName,
		Instance:   instance,
	}
	return c.configRequestString(ctx, http.MethodPost, "/api/config", reqBody)
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
	if strings.TrimSpace(reqBody.Name) == "" {
		return "", fmt.Errorf("fabric name is required")
	}
	return c.configRequestString(ctx, http.MethodPost, "/api/config/updatefabricstatus", reqBody)
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
	fabricName = strings.TrimSpace(fabricName)
	if fabricName == "" {
		return "", fmt.Errorf("fabric name is required")
	}

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
	return c.configRequestString(ctx, http.MethodPost, "/api/config/uploadip", reqBody)
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
	respBody, status, err := c.doConfigRequest(ctx, http.MethodPost, path, entries)
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		bodyStr := apiErrorBody(respBody)
		if bodyStr == "" {
			return nil, fmt.Errorf("API returned %d", status)
		}
		return nil, fmt.Errorf("API returned %d: %s", status, bodyStr)
	}

	var results []ValidateDeviceResult
	if err := json.Unmarshal(respBody, &results); err != nil {
		return nil, fmt.Errorf("decode validate response: %w (body=%s)", err, apiErrorBody(respBody))
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
	fabricName = strings.TrimSpace(fabricName)
	if fabricName == "" {
		return "", fmt.Errorf("fabric name is required")
	}

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
	return c.configRequestString(ctx, http.MethodPost, "/api/config/updateinventory", entries)
}
