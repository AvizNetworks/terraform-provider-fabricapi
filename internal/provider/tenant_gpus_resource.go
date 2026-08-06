package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &TenantGpusResource{}

func NewTenantGpusResource() resource.Resource {
	return &TenantGpusResource{}
}

// TenantGpusResource manages POST /fabrics/{fabric}/tenants/{tenant}/gpus (assign/remove GPU
// ports on externally-managed UFM/NMX-C fabrics), separate from the PATCH /tenants/{tenantName}
// endpoint used by fabricapi_tenant_servers. Always synchronous: no Prefer/webhooks/operationId.
type TenantGpusResource struct {
	client *APIClient
}

type TenantGpusResourceModel struct {
	TenantName  types.String `tfsdk:"tenant_name"`
	FabricName  types.String `tfsdk:"fabric_name"`
	Operation   types.String `tfsdk:"operation"`
	ServerNames types.List   `tfsdk:"server_names"`
	GpuIds      types.List   `tfsdk:"gpu_ids"`
	Membership  types.String `tfsdk:"membership"`
	ID          types.String `tfsdk:"id"`
}

func (r *TenantGpusResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tenant_gpus"
}

func (r *TenantGpusResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fabric API Tenant GPU Resource - assigns or removes InfiniBand GPU ports for a tenant on an externally-managed fabric (POST /fabrics/{fabric}/tenants/{tenant}/gpus; UFM or NMX-C performs the actual port/PKey assignment). This is separate from the FM-native tenant lifecycle endpoint (/tenants/{tenantName}) used by fabricapi_tenant_servers. The tenant must already exist (see fabricapi_tenant). This call is always synchronous; it has no Prefer/async, webhook, or operation_id support. `tenant_name`, `fabric_name`, `server_names`, `gpu_ids`, and `membership` identify what is being targeted, so changing any of them replaces the resource. `operation` is the exception: flipping it between ADD and DELETE on an otherwise-unchanged resource updates in place (calls AssignPorts/UnassignPorts directly, no destroy), the same way changing `operation` on fabricapi_tenant_servers does.",

		Attributes: map[string]schema.Attribute{
			"tenant_name": schema.StringAttribute{
				MarkdownDescription: "Name of the tenant.",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"fabric_name": schema.StringAttribute{
				MarkdownDescription: "Fabric name for the backend URL /fabrics/{fabric}/tenants/{tenant}/gpus. If unset, uses provider-level fabric.",
				Optional:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"operation": schema.StringAttribute{
				MarkdownDescription: "ADD or DELETE. REMOVE is accepted client-side and aliased to DELETE (the server's GpuAction enum only has ADD/DELETE). Unlike the other attributes, changing only this one updates in place instead of replacing the resource - e.g. apply with ADD, then re-apply with DELETE (same tenant_name/server_names/gpu_ids/membership) to release without a destroy.",
				Required:            true,
			},
			"server_names": schema.ListAttribute{
				MarkdownDescription: "Server names to assign/remove GPU ports on (maps to `serverNames`).",
				Required:            true,
				ElementType:         types.StringType,
				PlanModifiers: []planmodifier.List{
					listplanmodifier.RequiresReplace(),
				},
			},
			"gpu_ids": schema.ListAttribute{
				MarkdownDescription: "Optional 1-based GPU port indices on server_names (maps to `gpuIds`). Omit or leave empty to act on all GPUs on each server.",
				Optional:            true,
				ElementType:         types.Int64Type,
				PlanModifiers: []planmodifier.List{
					listplanmodifier.RequiresReplace(),
				},
			},
			"membership": schema.StringAttribute{
				MarkdownDescription: "UFM-only PKey partition membership: \"full\" or \"limited\". Omit for NMXC fabrics or to use the backend default.",
				Optional:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource identifier",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *TenantGpusResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*APIClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *APIClient, got: %T", req.ProviderData),
		)
		return
	}

	r.client = client
}

// normalizeGpuMembership validates the optional membership value and returns it lowercased,
// or "" (and ok=false with a diagnostic added) if invalid.
func normalizeGpuMembership(v types.String, diags *[]string) (string, bool) {
	if v.IsNull() || v.IsUnknown() {
		return "", true
	}
	m := strings.ToLower(strings.TrimSpace(v.ValueString()))
	if m == "" {
		return "", true
	}
	if m != "full" && m != "limited" {
		*diags = append(*diags, fmt.Sprintf("`membership` must be \"full\" or \"limited\" (UFM-only), got: %s", v.ValueString()))
		return "", false
	}
	return m, true
}

// gpuIdsFromList extracts gpu_ids from plan/state. The API distinguishes "gpuIds omitted"
// (act on the whole server) from "gpuIds present" (act on specific GPUs): server_names alone is
// always sufficient for whole-server allocation, so gpu_ids may be omitted/null entirely, but if
// set it must not be an empty list — that's ambiguous input, not "omitted".
func gpuIdsFromList(ctx context.Context, v types.List, diags *diag.Diagnostics) ([]int64, bool) {
	if v.IsNull() || v.IsUnknown() {
		return nil, true
	}
	var gpuIds []int64
	diags.Append(v.ElementsAs(ctx, &gpuIds, false)...)
	if diags.HasError() {
		return nil, false
	}
	if len(gpuIds) == 0 {
		diags.AddError(
			"Invalid gpu_ids",
			"`gpu_ids` must not be an empty list. Omit `gpu_ids` (or set it to null) to act on the whole server; provide at least one GPU id to act on specific GPUs.",
		)
		return nil, false
	}
	return gpuIds, true
}

// assignGpuPorts runs the pre-allocation conflict check, waits for the tenant to be readable,
// then calls AssignPorts. Shared by Create's ADD path and Update's ADD transition (DELETE -> ADD).
func (r *TenantGpusResource) assignGpuPorts(
	ctx context.Context,
	fabricName, tenantName string,
	serverNames []string,
	gpuIds []int64,
	membership string,
	diags *diag.Diagnostics,
) bool {
	if len(gpuIds) == 0 {
		// Fast-fail pre-check, whole-server requests only: partial gpu_ids requests can validly
		// share a server across tenants, so a server-level check would misfire there. The real
		// API call below is the authoritative conflict check either way (also blind on
		// EW-IBOnly fabrics, since allotedGpus is empty there too).
		allocated, err := r.client.GetAllocatedServers(ctx, fabricName)
		if err != nil {
			diags.AddError("Client Error", err.Error())
			return false
		}
		for _, s := range serverNames {
			if owner, exists := allocated[s]; exists && owner != tenantName {
				diags.AddError(
					"Server already allocated",
					fmt.Sprintf("Server %s is already allocated to tenant %s", s, owner),
				)
				return false
			}
		}
	}

	if err := r.client.WaitForTenantReady(ctx, fabricName, tenantName, 60*time.Second); err != nil {
		diags.AddError(
			"Tenant not ready",
			fmt.Sprintf("Cannot assign GPU ports until tenant %s is readable: %s", tenantName, err),
		)
		return false
	}

	if err := r.client.AssignPorts(ctx, fabricName, tenantName, serverNames, gpuIds, membership); err != nil {
		diags.AddError("Client Error", fmt.Sprintf("Unable to assign tenant GPU ports: %s", err))
		return false
	}
	return true
}

func (r *TenantGpusResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data TenantGpusResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fabricName := resolveFabricName(r.client.Fabric, data.FabricName)
	tenantName := data.TenantName.ValueString()

	operation := strings.ToUpper(strings.TrimSpace(data.Operation.ValueString()))
	if operation != "ADD" && operation != "DELETE" && operation != "REMOVE" {
		resp.Diagnostics.AddError(
			"Invalid Operation",
			fmt.Sprintf("Operation must be 'ADD', 'DELETE', or 'REMOVE', got: %s", operation),
		)
		return
	}
	if operation == "REMOVE" {
		operation = "DELETE"
	}

	var serverNames []string
	resp.Diagnostics.Append(data.ServerNames.ElementsAs(ctx, &serverNames, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	serverNames = normalizeServerList(serverNames)
	if len(serverNames) == 0 {
		resp.Diagnostics.AddError(
			"Missing required attribute",
			"`server_names` must contain at least one server name.",
		)
		return
	}

	gpuIds, ok := gpuIdsFromList(ctx, data.GpuIds, &resp.Diagnostics)
	if !ok {
		return
	}

	var membershipErrs []string
	membership, ok := normalizeGpuMembership(data.Membership, &membershipErrs)
	if !ok {
		resp.Diagnostics.AddError("Invalid membership", membershipErrs[0])
		return
	}

	tenantInfo, err := r.client.GetTenantWithFabric(fabricName, tenantName)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read tenant: %s", err))
		return
	}
	if tenantInfo == nil {
		resp.Diagnostics.AddError(
			"Tenant not found",
			fmt.Sprintf("Tenant %q does not exist in fabric %q. Create the tenant first.", tenantName, fabricName),
		)
		return
	}

	if operation == "ADD" {
		if !r.assignGpuPorts(ctx, fabricName, tenantName, serverNames, gpuIds, membership, &resp.Diagnostics) {
			return
		}
	} else {
		// Same "missing" check as fabricapi_tenant_servers' Create() DELETE branch, so a
		// never-allocated server name gets the same "Server not found for deletion" message on
		// both resources instead of a raw backend error.
		currentServers := normalizedServersFromTenant(tenantInfo, serverNames)
		curSet := make(map[string]struct{}, len(currentServers))
		for _, s := range currentServers {
			curSet[s] = struct{}{}
		}
		missing := make([]string, 0)
		for _, s := range serverNames {
			if _, ok := curSet[s]; !ok {
				missing = append(missing, s)
			}
		}
		if len(missing) > 0 {
			resp.Diagnostics.AddError(
				"Server not found for deletion",
				fmt.Sprintf("Cannot deallocate servers not currently allocated to tenant %q: %v", tenantName, missing),
			)
			return
		}

		if err := r.client.UnassignPorts(ctx, fabricName, tenantName, serverNames, gpuIds, membership); err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to remove tenant GPU ports: %s", err))
			return
		}
	}

	serverList, diagsList := types.ListValueFrom(ctx, types.StringType, serverNames)
	resp.Diagnostics.Append(diagsList...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.ServerNames = serverList

	if len(gpuIds) > 0 {
		gpuList, diagsGpu := types.ListValueFrom(ctx, types.Int64Type, gpuIds)
		resp.Diagnostics.Append(diagsGpu...)
		if resp.Diagnostics.HasError() {
			return
		}
		data.GpuIds = gpuList
	} else {
		data.GpuIds = types.ListNull(types.Int64Type)
	}

	data.TenantName = types.StringValue(tenantName)
	data.ID = types.StringValue(stableTenantGpusID(fabricName, tenantName, operation, serverNames))

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TenantGpusResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data TenantGpusResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fabricName := resolveFabricName(r.client.Fabric, data.FabricName)
	tenantName := data.TenantName.ValueString()

	// The FM API does not expose a per-gpuId read-back that matches this resource's request
	// shape, so Read only confirms the tenant still exists; it does not reconcile server_names
	// or gpu_ids drift (unlike fabricapi_tenant_servers, which can diff against GET tenant).
	tenantInfo, err := r.client.GetTenantWithFabric(fabricName, tenantName)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read tenant: %s", err))
		return
	}
	if tenantInfo == nil {
		resp.Diagnostics.AddWarning(
			"Tenant not found; removing from state",
			fmt.Sprintf(
				"Tenant %q does not exist in fabric %q. Removing fabricapi_tenant_gpus from Terraform state.",
				tenantName,
				fabricName,
			),
		)
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Update only ever runs when `operation` itself changes: every other attribute
// (tenant_name/fabric_name/server_names/gpu_ids/membership) still has RequiresReplace, so Terraform
// guarantees those are identical between plan and state by the time Update is called - no GET-based
// diffing needed, just do what the plan's new operation says with the (unchanged) target.
func (r *TenantGpusResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan TenantGpusResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fabricName := resolveFabricName(r.client.Fabric, plan.FabricName)
	tenantName := plan.TenantName.ValueString()

	operation := strings.ToUpper(strings.TrimSpace(plan.Operation.ValueString()))
	if operation != "ADD" && operation != "DELETE" && operation != "REMOVE" {
		resp.Diagnostics.AddError(
			"Invalid Operation",
			fmt.Sprintf("Operation must be 'ADD', 'DELETE', or 'REMOVE', got: %s", operation),
		)
		return
	}
	if operation == "REMOVE" {
		operation = "DELETE"
	}

	var serverNames []string
	resp.Diagnostics.Append(plan.ServerNames.ElementsAs(ctx, &serverNames, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	serverNames = normalizeServerList(serverNames)

	gpuIds, ok := gpuIdsFromList(ctx, plan.GpuIds, &resp.Diagnostics)
	if !ok {
		return
	}

	var membershipErrs []string
	membership, ok := normalizeGpuMembership(plan.Membership, &membershipErrs)
	if !ok {
		resp.Diagnostics.AddError("Invalid membership", membershipErrs[0])
		return
	}

	if operation == "ADD" {
		if !r.assignGpuPorts(ctx, fabricName, tenantName, serverNames, gpuIds, membership, &resp.Diagnostics) {
			return
		}
	} else {
		// Same as Delete(): confirm something is really still allocated (so a repeat
		// operation=DELETE no-ops instead of erroring), and prefer the live server list over
		// the plan's when the API can report one (empty on EW-IBOnly fabrics).
		tenantInfo, err := r.client.GetTenantWithFabric(fabricName, tenantName)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read tenant: %s", err))
			return
		}
		if hasUnreportedAllocation(tenantInfo) {
			releaseServers := normalizedServersFromTenant(tenantInfo, serverNames)
			if err := r.client.UnassignPorts(ctx, fabricName, tenantName, releaseServers, gpuIds, membership); err != nil {
				resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to release tenant GPU ports: %s", err))
				return
			}
		}
	}

	// id is Computed with UseStateForUnknown: since it isn't marked unknown in the plan, Terraform
	// already fixed it to the prior state's value, and Update must return that same value or the
	// framework reports "provider produced inconsistent result after apply". Leave plan.ID as-is
	// (it still identifies this resource; only its operation attribute reflects the new value).
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *TenantGpusResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data TenantGpusResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fabricName := resolveFabricName(r.client.Fabric, data.FabricName)
	tenantName := data.TenantName.ValueString()

	// Same concept as fabricapi_tenant_servers: ask the API whether anything is really still
	// allocated before attempting to release it, instead of blindly trusting the operation this
	// resource was created with. This stops Delete from either skipping a release that's
	// actually needed or re-issuing one that's already done.
	tenantInfo, err := r.client.GetTenantWithFabric(fabricName, tenantName)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read tenant: %s", err))
		return
	}
	if tenantInfo == nil {
		// Tenant already gone; nothing to release.
		resp.State.RemoveResource(ctx)
		return
	}
	if !hasUnreportedAllocation(tenantInfo) {
		// Nothing allocated to this tenant right now.
		resp.State.RemoveResource(ctx)
		return
	}

	var serverNames []string
	resp.Diagnostics.Append(data.ServerNames.ElementsAs(ctx, &serverNames, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// allotedGpus is empty on EW-IBOnly fabrics (like fabricapi_tenant_servers), so this falls
	// back to state there. Assumes one fabricapi_tenant_gpus resource per tenant: GET returns
	// the tenant's whole server set, not per-resource, so multiple resources per tenant would
	// pick up each other's servers here.
	serverNames = normalizedServersFromTenant(tenantInfo, serverNames)

	gpuIds, ok := gpuIdsFromList(ctx, data.GpuIds, &resp.Diagnostics)
	if !ok {
		return
	}

	var membershipErrs []string
	membership, ok := normalizeGpuMembership(data.Membership, &membershipErrs)
	if !ok {
		resp.Diagnostics.AddError("Invalid membership", membershipErrs[0])
		return
	}

	err = r.client.UnassignPorts(ctx, fabricName, tenantName, serverNames, gpuIds, membership)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to release tenant GPU ports: %s", err))
		return
	}

	resp.State.RemoveResource(ctx)
}

func stableTenantGpusID(fabricName, tenantName,_ string , serverNames []string) string {
	return fmt.Sprintf("%s:%s:%s", fabricName, tenantName, strings.Join(serverNames, ","))
}
