package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)


type APIClient struct {
	Endpoint string
	Fabric   string
}

type TenantRequest struct {
	TenantName     string `json:"tenantName"`
	Description    string `json:"description"`
	MaxGpusAllowed int    `json:"maxGpusAllowed"`
}

type TenantServersRequest struct {
	Operation string   `json:"operation"`
	Servers   []string `json:"servers"`
}

// API Response structure - nested under "tenant" key
type TenantAPIResponse struct {
	Tenant TenantData `json:"tenant"`
}

type TenantData struct {
	ID             int      `json:"id"`
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	MaxGpusAllowed int      `json:"maxGpusAllowed"`
	GpusAllocated  int      `json:"gpusAllocated"`
	AllotedGpus    string   `json:"allotedGpus"`   // Comma-separated server names
	FabricName     string   `json:"fabricName"`
}


type TenantResponse struct {
	TenantName     string   `json:"tenantName"`
	Description    string   `json:"description"`
	MaxGpusAllowed int      `json:"maxGpusAllowed"`
	GpusAllocated  int      `json:"gpusAllocated,omitempty"`
	Servers        []string `json:"servers,omitempty"`
	AllotedGpus    string   `json:"allotedGpus"` 
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
		Timeout: 6 * time.Minute,
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
		Timeout: 6 * time.Minute,
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

	client := &http.Client{Timeout: 6 * time.Minute}

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
		Timeout: 5 * time.Minute,
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

func (c *APIClient) UpdateTenantServers(tenantName string, operation string, servers []string) error {
	// Normalize operation: support both DELETE and REMOVE
	if operation == "REMOVE" {
		operation = "DELETE"
	}

	// Tenant name goes in the URL path, not the body
	url := fmt.Sprintf("%s/fabrics/%s/tenants/%s", c.Endpoint, c.Fabric, tenantName)
	
	request := TenantServersRequest{
		Operation: operation,
		Servers:   servers,
	}

	jsonData, err := json.Marshal(request)
	if err != nil {
		return err
	}
	fmt.Printf("[DEBUG] PATCH Request URL: %s\n", url)
	fmt.Printf("[DEBUG] PATCH Request Body: %s\n", string(jsonData))

	req, err := http.NewRequest("PATCH", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{
		Timeout: 10 * time.Minute, // Long timeout for GPU allocation/deallocation operations
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
