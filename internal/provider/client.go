package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type APIClient struct {
	Endpoint string
	Fabric   string
}

type TenantRequest struct {
	TenantName     string
	Description    string
	MaxGpusAllowed int
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
		} `json:"tenant,omitempty"`

		TenantName     string `json:"tenantName"`
		Description    string `json:"description"`
		MaxGPUsAllowed int    `json:"maxGPUsAllowed"`
		MaxGpusAllowed int    `json:"maxGpusAllowed"`
	}

	nested := &struct {
		TenantName     string `json:"tenantName"`
		Description    string `json:"description"`
		MaxGPUsAllowed int    `json:"maxGPUsAllowed"`
		MaxGpusAllowed int    `json:"maxGpusAllowed"`
	}{
		TenantName:     t.TenantName,
		Description:    t.Description,
		MaxGPUsAllowed: t.MaxGpusAllowed,
		MaxGpusAllowed: t.MaxGpusAllowed,
	}

	return json.Marshal(body{
		Tenant:         nested,
		TenantName:     t.TenantName,
		Description:    t.Description,
		MaxGPUsAllowed: t.MaxGpusAllowed,
		MaxGpusAllowed: t.MaxGpusAllowed,
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

// CreateTenantWithFabric creates a tenant in the specified fabric
func (c *APIClient) CreateTenantWithFabric(fabricName string, tenant TenantRequest) (*TenantResponse, error) {
	url := fmt.Sprintf("%s/fabrics/%s/tenants", c.Endpoint, fabricName)

	jsonData, err := json.Marshal(tenant)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{
		Timeout: 60 * time.Minute,
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var apiResponse TenantAPIResponse
	if err := json.Unmarshal(body, &apiResponse); err != nil {
		return &TenantResponse{
			TenantName:     tenant.TenantName,
			Description:    tenant.Description,
			MaxGpusAllowed: tenant.MaxGpusAllowed,
		}, nil
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

	return result, nil
}

// CreateTenant uses the default fabric from the client
func (c *APIClient) CreateTenant(tenant TenantRequest) (*TenantResponse, error) {
	return c.CreateTenantWithFabric(c.Fabric, tenant)
}

// GetTenantWithFabric retrieves tenant information from the specified fabric
func (c *APIClient) GetTenantWithFabric(fabricName string, tenantName string) (*TenantResponse, error) {
	url := fmt.Sprintf("%s/fabrics/%s/tenants/%s", c.Endpoint, fabricName, tenantName)

	client := &http.Client{
		Timeout: 60 * time.Minute,
	}
	resp, err := client.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
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

	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewBuffer(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Minute}

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("API returned %d: %s", resp.StatusCode, string(respBody))
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
	url := fmt.Sprintf("%s/fabrics/%s/tenants/%s", c.Endpoint, fabricName, tenantName)

	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return err
	}

	client := &http.Client{
		Timeout: 60 * time.Minute,
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
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
	// Normalize operation: support both DELETE and REMOVE
	if operation == "REMOVE" {
		operation = "DELETE"
	}

	url := fmt.Sprintf("%s/fabrics/%s/tenants/%s", c.Endpoint, fabricName, tenantName)

	var jsonData []byte
	var err error

	if operation == "DELETE" {
		// Deallocate: {"operation":"DELETE","servers":["host1","host2"]}
		dealloc := TenantServersDeallocateRequest{
			Operation: operation,
			Servers:   servers,
		}
		jsonData, err = json.Marshal(dealloc)
	} else {
		serverUpdates := make([]TenantServerUpdate, 0, len(servers))
		for _, server := range servers {
			serverUpdates = append(serverUpdates, TenantServerUpdate{
				ServerName: server,
				Shared:     shared,
			})
		}
		request := TenantServersRequest{
			Operation: operation,
			Servers:   serverUpdates,
		}
		jsonData, err = json.Marshal(request)
	}
	if err != nil {
		return err
	}

	req, err := http.NewRequest("PATCH", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{
		Timeout: 60 * time.Minute, // GPU allocation/deallocation can be slow on the real API
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
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

// CreateVpcPeering creates a VPC peering on the target fabric.
func (c *APIClient) CreateVpcPeering(ctx context.Context, targetFabric string, req VpcPeeringRequest) error {
	_, err := c.CreateVpcPeeringWithResponse(ctx, targetFabric, req)
	return err
}

// CreateVpcPeeringWithResponse creates VPC peering and returns the raw response body (if any).
// Some backends return a human-readable success message in the response body; surfacing it makes
// `terraform apply` logs match what users see with curl.
func (c *APIClient) CreateVpcPeeringWithResponse(ctx context.Context, targetFabric string, reqBody VpcPeeringRequest) (string, error) {
	u := fmt.Sprintf("%s/fabrics/%s/vpcpeering", strings.TrimRight(c.Endpoint, "/"), url.PathEscape(targetFabric))

	data, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewBuffer(data))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	bodyStr := strings.TrimSpace(string(respBody))

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Preserve body in the error for troubleshooting.
		if bodyStr == "" {
			return "", fmt.Errorf("API returned %d", resp.StatusCode)
		}
		return "", fmt.Errorf("API returned %d: %s", resp.StatusCode, bodyStr)
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
