package provider

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestStartTopologyAudit_SuccessAndUnknownFabric(t *testing.T) {
	f := newFakeFM(t)
	c := testClient(f)

	opID, err := c.StartTopologyAudit(context.Background(), "fab1")
	if err != nil || opID == "" {
		t.Fatalf("StartTopologyAudit(fab1) = %q, %v", opID, err)
	}
	if req := f.lastCall(http.MethodPost, "/fabrics/fab1/topology-audit"); req.Method != http.MethodPost {
		t.Fatal("expected a POST to be recorded")
	}

	_, err = c.StartTopologyAudit(context.Background(), "nope")
	if err == nil || !strings.Contains(err.Error(), "INVALID_FABRIC") {
		t.Fatalf("expected INVALID_FABRIC for an unknown fabric, got %v", err)
	}
}

func TestPollOperationUntilTerminal_WaitsThroughPending(t *testing.T) {
	f := newFakeFM(t)
	c := testClient(f)
	f.queueTopologyAudit("fab1",
		fakeOperationResponse{Status: "PENDING"},
		fakeOperationResponse{Status: "RUNNING"},
		fakeOperationResponse{Status: "SUCCESS", Result: `{"fabricName":"fab1","overallStatus":"PASS","links":[],"deviceIpMap":{}}`},
	)
	opID, err := c.StartTopologyAudit(context.Background(), "fab1")
	if err != nil {
		t.Fatal(err)
	}

	detail, err := c.PollOperationUntilTerminal(context.Background(), opID, 30*time.Second)
	if err != nil {
		t.Fatalf("PollOperationUntilTerminal: %v", err)
	}
	if detail.Status != "SUCCESS" {
		t.Fatalf("final status = %q, want SUCCESS", detail.Status)
	}
	if n := len(f.calls(http.MethodGet, "/operations/"+opID)); n != 3 {
		t.Fatalf("expected 3 polls (PENDING, RUNNING, SUCCESS), got %d", n)
	}
}

func TestPollOperationUntilTerminal_FailedIsTerminalNotAnError(t *testing.T) {
	// A FAILED job is a terminal state the caller must see and decide what to
	// do with - PollOperationUntilTerminal (unlike WaitForOperationDone) must
	// not turn it into a Go error itself.
	f := newFakeFM(t)
	c := testClient(f)
	f.queueTopologyAudit("fab1", fakeOperationResponse{
		Status:       "FAILED",
		ErrorMessage: "No intent found for fabric 'fab1'.",
	})
	opID, err := c.StartTopologyAudit(context.Background(), "fab1")
	if err != nil {
		t.Fatal(err)
	}
	detail, err := c.PollOperationUntilTerminal(context.Background(), opID, 5*time.Second)
	if err != nil {
		t.Fatalf("expected no transport error, got %v", err)
	}
	if detail.Status != "FAILED" || detail.ErrorMessage != "No intent found for fabric 'fab1'." {
		t.Fatalf("detail = %+v", detail)
	}
}

func TestPollOperationUntilTerminal_UnknownOperationID(t *testing.T) {
	f := newFakeFM(t)
	c := testClient(f)
	if _, err := c.PollOperationUntilTerminal(context.Background(), "does-not-exist", 5*time.Second); err == nil {
		t.Fatal("expected an error for an unknown operation id")
	}
}

func TestParseTopologyAuditResult_DecodesLinksAndOutcome(t *testing.T) {
	result, err := ParseTopologyAuditResult(
		`{"fabricName":"fab1","intentVersion":"1:abc","overallStatus":"FAIL","links":[` +
			`{"device":"spine1","localInterface":"Ethernet1","remoteDevice":"leaf1","remoteInterface":"Ethernet1",` +
			`"observedRemoteDevice":"","observedRemoteInterface":"","status":"MISSING_LINK","message":"no neighbor seen"}` +
			`],"deviceIpMap":{"spine1":"10.0.0.1"}}`,
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.OverallStatus != "FAIL" || len(result.Links) != 1 {
		t.Fatalf("result = %+v", result)
	}
	link := result.Links[0]
	if link.Device != "spine1" || link.Status != "MISSING_LINK" || link.Message != "no neighbor seen" {
		t.Fatalf("link = %+v", link)
	}
	if result.DeviceIPMap["spine1"] != "10.0.0.1" {
		t.Fatalf("deviceIpMap = %+v", result.DeviceIPMap)
	}
}

func TestParseTopologyAuditResult_EmptyOrInvalid(t *testing.T) {
	if _, err := ParseTopologyAuditResult(""); err == nil {
		t.Fatal("expected an error for an empty result")
	}
	if _, err := ParseTopologyAuditResult("not json"); err == nil {
		t.Fatal("expected an error for invalid JSON")
	}
}
