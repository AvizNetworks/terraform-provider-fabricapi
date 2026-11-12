package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// This file holds the client-side pieces for the on-demand Topology Audit
// (LLDP-vs-intent) trigger added to Fabric Manager in GFX-6219:
//
//	POST /fabrics/{fabricName}/topology-audit   -> 202 + {operationId, status}
//	GET  /operations/{operationId}              -> generic poll, reused as-is
//
// Unlike every other FM call in this provider, this one has no synchronous
// form at all - FM always answers 202 and the caller must poll - so these
// helpers always use PollOperationUntilTerminal regardless of asyncEnabled().

// topologyAuditStartResponse is the `data` payload of the 202 response.
type topologyAuditStartResponse struct {
	OperationID string `json:"operationId"`
	Status      string `json:"status"`
}

// StartTopologyAudit kicks off an async LLDP-vs-intent audit for fabricName
// and returns the operation id to poll. A non-existent fabric/intent comes
// back as a 404 INVALID_FABRIC FMAPIError.
func (c *APIClient) StartTopologyAudit(ctx context.Context, fabricName string) (string, error) {
	data, err := doFMData[topologyAuditStartResponse](
		ctx, c, http.MethodPost, fmPath("fabrics", fabricName, "topology-audit"), nil, nil,
	)
	if err != nil {
		return "", err
	}
	if data.OperationID == "" {
		return "", fmt.Errorf("topology audit accepted but response had no operationId")
	}
	return data.OperationID, nil
}

// LinkCheckResult mirrors SES_FM's
// com.ONES.FabricManager.dto.TopologyCheckResult.LinkCheckResult - one
// link's expected-vs-observed outcome.
type LinkCheckResult struct {
	Device                  string `json:"device"`
	LocalInterface          string `json:"localInterface"`
	RemoteDevice            string `json:"remoteDevice"`
	RemoteInterface         string `json:"remoteInterface"`
	ObservedRemoteDevice    string `json:"observedRemoteDevice"`
	ObservedRemoteInterface string `json:"observedRemoteInterface"`
	Status                  string `json:"status"`
	Message                 string `json:"message"`
}

// TopologyAuditResult is the decoded form of OperationStatus.result for a
// TOPOLOGY_AUDIT operation (see TopologyAuditService.buildReport).
// OverallStatus is one of PASS, FAIL, or INCOMPLETE (see TopologyAuditService.rollupStatus):
// INCOMPLETE wins over FAIL if any link is UNVERIFIABLE, otherwise FAIL wins if
// any link is MISSING_LINK/MISMATCH/UNEXPECTED_LINK, else PASS.
type TopologyAuditResult struct {
	FabricName    string            `json:"fabricName"`
	IntentVersion string            `json:"intentVersion"`
	OverallStatus string            `json:"overallStatus"`
	Links         []LinkCheckResult `json:"links"`
	DeviceIPMap   map[string]string `json:"deviceIpMap"`
}

// ParseTopologyAuditResult decodes the raw `result` JSON string a terminal
// TOPOLOGY_AUDIT operation carries. Returns an error if resultJSON is empty
// or not valid JSON - callers should only reach here once the job status is
// SUCCESS (a FAILED job never populates `result`).
func ParseTopologyAuditResult(resultJSON string) (*TopologyAuditResult, error) {
	if resultJSON == "" {
		return nil, fmt.Errorf("operation has no result payload")
	}
	var out TopologyAuditResult
	if err := json.Unmarshal([]byte(resultJSON), &out); err != nil {
		return nil, fmt.Errorf("decode topology audit result: %w (body=%s)", err, resultJSON)
	}
	return &out, nil
}
