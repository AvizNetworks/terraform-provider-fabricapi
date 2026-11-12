package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &TopologyAuditResource{}

func NewTopologyAuditResource() resource.Resource {
	return &TopologyAuditResource{}
}

// TopologyAuditResource is an action-style resource (like fabricapi_inventory_sync
// and fabricapi_dhcp_reconcile): Create triggers an on-demand LLDP-vs-intent
// Topology Audit for a fabric (POST /fabrics/{fabric}/topology-audit), polls
// the async job to completion (GET /operations/{id}), and records the
// outcome. It runs again whenever `triggers` (or `fabric_name`) changes;
// Read/refresh never re-triggers it, and destroy only forgets it (there is
// nothing remote to undo - an audit is a read-only check).
//
// There is no "fabric id" in this API (nor anywhere else in this provider) -
// `fabric_name` is the sole identifier, matching every other resource here.
type TopologyAuditResource struct {
	client *APIClient
}

type TopologyAuditResourceModel struct {
	FabricName    types.String `tfsdk:"fabric_name"`
	Triggers      types.Map    `tfsdk:"triggers"`
	Timeout       types.String `tfsdk:"timeout"`
	FailOnNonPass types.Bool   `tfsdk:"fail_on_non_pass"`

	OperationID   types.String `tfsdk:"operation_id"`
	JobStatus     types.String `tfsdk:"job_status"`
	OverallStatus types.String `tfsdk:"overall_status"`
	ExecutedAt    types.String `tfsdk:"executed_at"`
	Discrepancies types.String `tfsdk:"discrepancies"`

	ID types.String `tfsdk:"id"`
}

func (r *TopologyAuditResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_topology_audit"
}

func (r *TopologyAuditResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Triggers an on-demand Topology Audit (LLDP-vs-intent cabling validation) for a fabric " +
			"(`POST /fabrics/{fabric}/topology-audit`), waits for the async job to finish, and exposes the result. " +
			"This is the same audit the Rule Engine runs on a schedule; this resource runs it immediately instead of " +
			"waiting for the next scheduled interval. Action-style resource: it runs on create and whenever `triggers` " +
			"(or `fabric_name`) changes; Terraform refresh/plan never re-triggers it, and destroy only removes it from " +
			"state (an audit is a read-only check - there is nothing remote to undo).",
		Attributes: map[string]schema.Attribute{
			"fabric_name": schema.StringAttribute{
				MarkdownDescription: "Fabric to audit. If unset, uses the provider-level fabric.",
				Optional:            true,
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"triggers": schema.MapAttribute{
				MarkdownDescription: "Arbitrary values; changing any of them re-runs the audit (e.g. `{ at = timestamp() }` " +
					"to run on every apply). Without this, repeated applies never re-run an already-created audit.",
				Optional:      true,
				ElementType:   types.StringType,
				PlanModifiers: []planmodifier.Map{mapplanmodifier.RequiresReplace()},
			},
			"timeout": schema.StringAttribute{
				MarkdownDescription: "How long to wait for the audit job to finish, as a Go duration string (e.g. `10m`). Defaults to `10m` when unset.",
				Optional:            true,
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"fail_on_non_pass": schema.BoolAttribute{
				MarkdownDescription: "When true, a FAIL or INCOMPLETE audit outcome fails `terraform apply` (`Diagnostics.AddError`) " +
					"instead of the default of succeeding with a warning and recording the outcome in `overall_status`. " +
					"A job that errors before producing an outcome (bad fabric, FM exception, timeout) always fails apply, " +
					"regardless of this setting. Defaults to `false` when unset.",
				Optional: true,
				Computed: true,
			},
			"operation_id": schema.StringAttribute{
				MarkdownDescription: "Fabric Manager operation/job id for this audit run.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"job_status": schema.StringAttribute{
				MarkdownDescription: "Terminal job-execution status: `SUCCESS` (the audit ran to completion, regardless of outcome) or `FAILED` (the job itself errored, e.g. unknown fabric).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"overall_status": schema.StringAttribute{
				MarkdownDescription: "Audit outcome: `PASS`, `FAIL`, or `INCOMPLETE` (any link was unverifiable, e.g. unreachable device). Empty when `job_status` is `FAILED`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"executed_at": schema.StringAttribute{
				MarkdownDescription: "Timestamp the audit job completed, as reported by Fabric Manager.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"discrepancies": schema.StringAttribute{
				MarkdownDescription: "JSON-encoded per-link audit detail (the `links` array: `device`, `localInterface`, " +
					"`remoteDevice`, `remoteInterface`, `observedRemoteDevice`, `observedRemoteInterface`, `status`, `message`), " +
					"or the job's error message when `job_status` is `FAILED`.",
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"id": schema.StringAttribute{
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *TopologyAuditResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*APIClient)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *APIClient, got: %T", req.ProviderData))
		return
	}
	r.client = client
}

func (r *TopologyAuditResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data TopologyAuditResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fabricName := resolveFabricName(r.client.Fabric, data.FabricName)
	if strings.TrimSpace(fabricName) == "" {
		resp.Diagnostics.AddError("Missing fabric", "Set `fabric_name` on this resource or `fabric` on the provider.")
		return
	}

	timeoutStr := "10m"
	if !data.Timeout.IsNull() && !data.Timeout.IsUnknown() && strings.TrimSpace(data.Timeout.ValueString()) != "" {
		timeoutStr = data.Timeout.ValueString()
	}
	timeout, err := time.ParseDuration(timeoutStr)
	if err != nil {
		resp.Diagnostics.AddError("Invalid timeout", fmt.Sprintf("`timeout` must be a Go duration string (e.g. \"10m\"): %s", err))
		return
	}

	failOnNonPass := !data.FailOnNonPass.IsNull() && !data.FailOnNonPass.IsUnknown() && data.FailOnNonPass.ValueBool()

	operationID, err := r.client.StartTopologyAudit(ctx, fabricName)
	if err != nil {
		resp.Diagnostics.AddError(
			"Topology audit submission failed",
			fmt.Sprintf("Unable to start a topology audit for fabric %q: %s", fabricName, err),
		)
		return
	}

	detail, err := r.client.PollOperationUntilTerminal(ctx, operationID, timeout)
	if err != nil {
		resp.Diagnostics.AddError(
			"Topology audit did not complete",
			fmt.Sprintf("Fabric %q, operation %q: %s", fabricName, operationID, err),
		)
		return
	}

	data.FabricName = types.StringValue(fabricName)
	data.Timeout = types.StringValue(timeoutStr)
	data.FailOnNonPass = types.BoolValue(failOnNonPass)
	data.OperationID = types.StringValue(detail.ID)
	data.JobStatus = types.StringValue(strings.ToUpper(strings.TrimSpace(detail.Status)))
	data.ExecutedAt = types.StringValue(firstNonEmpty(detail.CompletedAt, detail.CreatedAt))
	data.ID = types.StringValue(fmt.Sprintf("topologyaudit:%s:%s", fabricName, detail.ID))

	if data.JobStatus.ValueString() != "SUCCESS" {
		// The job itself errored (bad fabric resolved after start, FM exception, etc.) -
		// this is always a hard failure, independent of fail_on_non_pass, since there is
		// no audit outcome to report at all.
		data.OverallStatus = types.StringNull()
		data.Discrepancies = types.StringValue(detail.ErrorMessage)
		resp.Diagnostics.AddError(
			"Topology audit job failed",
			fmt.Sprintf("Fabric %q, operation %q: %s", fabricName, operationID, firstNonEmpty(detail.ErrorMessage, "no error message returned")),
		)
		// Still record what we know so it isn't silently lost from state on a failed create.
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}

	result, err := ParseTopologyAuditResult(detail.Result)
	if err != nil {
		resp.Diagnostics.AddError(
			"Topology audit result could not be parsed",
			fmt.Sprintf("Fabric %q, operation %q: %s", fabricName, operationID, err),
		)
		return
	}

	linksJSON, err := json.Marshal(result.Links)
	if err != nil {
		resp.Diagnostics.AddError("Unable to encode discrepancies", err.Error())
		return
	}

	data.OverallStatus = types.StringValue(strings.ToUpper(strings.TrimSpace(result.OverallStatus)))
	data.Discrepancies = types.StringValue(string(linksJSON))

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	switch data.OverallStatus.ValueString() {
	case "PASS", "":
		// nothing to flag
	default:
		msg := fmt.Sprintf(
			"Fabric %q topology audit (operation %q) finished with overall_status=%s. See `discrepancies` for per-link detail.",
			fabricName, operationID, data.OverallStatus.ValueString(),
		)
		if failOnNonPass {
			resp.Diagnostics.AddError("Topology audit did not pass", msg)
		} else {
			resp.Diagnostics.AddWarning("Topology audit did not pass", msg)
		}
	}
}

func (r *TopologyAuditResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// Deliberately a no-op: refreshing/planning must never re-trigger an audit.
	// The recorded outcome is a point-in-time result, not live state to reconcile.
	var data TopologyAuditResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TopologyAuditResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError(
		"Update not supported",
		"Only `fabric_name` and `triggers` can change this resource, and both force replacement. "+
			"Change `triggers` (or use `terraform apply -replace=...`) to run the audit again.",
	)
}

func (r *TopologyAuditResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// An audit is a read-only check against Fabric Manager - there is nothing remote
	// to undo. Just drop it from state so destroy completes cleanly.
	resp.State.RemoveResource(ctx)
}
