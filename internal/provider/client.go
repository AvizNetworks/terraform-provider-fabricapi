package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
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
	Name           string   `json:"name"`           // API uses "name" not "tenantName"
	Description    string   `json:"description"`
	MaxGpusAllowed int      `json:"maxGpusAllowed"`
	GpusAllocated  int      `json:"gpusAllocated"`
	FabricName     string   `json:"fabricName"`
	Networks       []string `json:"networks"`
}

// Our response structure for Terraform
type TenantResponse struct {
	TenantName     string   `json:"tenantName"`
	Description    string   `json:"description"`
	MaxGpusAllowed int      `json:"maxGpusAllowed"`
	Servers        []string `json:"servers,omitempty"`
}

func (c *APIClient) CreateTenant(tenant TenantRequest) (*TenantResponse, error) {
	url := fmt.Sprintf("%s/fabrics/%s/tenants", c.Endpoint, c.Fabric)
	
	jsonData, err := json.Marshal(tenant)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	// Debug: Log what API returned
	fmt.Printf("[DEBUG] API Response Status: %d\n", resp.StatusCode)
	fmt.Printf("[DEBUG] API Response Body: %s\n", string(body))

	// Parse the nested API response
	var apiResponse TenantAPIResponse
	if err := json.Unmarshal(body, &apiResponse); err != nil {
		// If parsing fails, return request data as fallback
		fmt.Printf("[DEBUG] Failed to parse API response: %v\n", err)
		return &TenantResponse{
			TenantName:     tenant.TenantName,
			Description:    tenant.Description,
			MaxGpusAllowed: tenant.MaxGpusAllowed,
		}, nil
	}

	// Map API response to our response structure
	result := &TenantResponse{
		TenantName:     apiResponse.Tenant.Name, // Map "name" to "tenantName"
		Description:    apiResponse.Tenant.Description,
		MaxGpusAllowed: apiResponse.Tenant.MaxGpusAllowed,
	}

	// Fallback to request values if API didn't return them
	if result.TenantName == "" {
		result.TenantName = tenant.TenantName
	}
	if result.Description == "" {
		result.Description = tenant.Description
	}
	if result.MaxGpusAllowed == 0 {
		result.MaxGpusAllowed = tenant.MaxGpusAllowed
	}

	fmt.Printf("[DEBUG] Mapped TenantName: %s (from API field 'name')\n", result.TenantName)
	fmt.Printf("[DEBUG] Mapped Description: %s\n", result.Description)
	fmt.Printf("[DEBUG] Mapped MaxGpusAllowed: %d\n", result.MaxGpusAllowed)

	return result, nil
}

func (c *APIClient) GetTenant(tenantName string) (*TenantResponse, error) {
	url := fmt.Sprintf("%s/fabrics/%s/tenants/%s", c.Endpoint, c.Fabric, tenantName)
	
	resp, err := http.Get(url)
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
	}

	return result, nil
}

func (c *APIClient) DeleteTenant(tenantName string) error {
	url := fmt.Sprintf("%s/fabrics/%s/tenants/%s", c.Endpoint, c.Fabric, tenantName)
	
	req, err := http.NewRequest("DELETE", url, nil)
	if err != nil {
		return err
	}

	client := &http.Client{}
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

func (c *APIClient) UpdateTenantServers(tenantName string, operation string, servers []string) error {
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

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	
	fmt.Printf("[DEBUG] PATCH Response Status: %d\n", resp.StatusCode)
	fmt.Printf("[DEBUG] PATCH Response Body: %s\n", string(body))

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	return nil
}