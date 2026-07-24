package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &VfAssignResource{}
var _ resource.ResourceWithImportState = &VfAssignResource{}

func NewVfAssignResource() resource.Resource {
	return &VfAssignResource{}
}

type VfAssignResource struct {
	client *APIClient
}

type VfAssignResourceModel struct {
	FabricName    types.String `tfsdk:"fabric_name"`
	ServerName    types.String `tfsdk:"server_name"`
	VfID          types.String `tfsdk:"vf_id"`
	TenantName    types.String `tfsdk:"tenant_name"`
	Prefer        types.String `tfsdk:"prefer"`
	Status        types.String `tfsdk:"status"`
	VlanID        types.Int64  `tfsdk:"vlan_id"`
	VniID         types.Int64  `tfsdk:"vni_id"`
	ProvisionedAt types.String `tfsdk:"provisioned_at"`
	DpuName       types.String `tfsdk:"dpu_name"`
	IfName        types.String `tfsdk:"if_name"`
	ServerIf      types.String `tfsdk:"server_if"`
	ID            types.String `tfsdk:"id"`
}

func (r *VfAssignResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vf_assign"
}

func (r *VfAssignResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Assigns an HBN VF interface on a GPU server to a tenant VLAN via " +
			"POST `/fabrics/{fabric}/servers/{server}/vf-interfaces/{vfId}/assign` with body " +
			"`{\"tenantName\":\"...\"}`. Destroy unbinds via DELETE on the same path (also sends " +
			"`tenantName` in the body, matching the Fabric API sample).\n\n" +
			"Prerequisites: tenant exists, server is attached to the tenant, VF is free, " +
			"fabric is DPU/HBN offload.",

		Attributes: map[string]schema.Attribute{
			"fabric_name": schema.StringAttribute{
				MarkdownDescription: "Fabric name. If unset, uses provider-level fabric (FABRIC_NAME).",
				Optional:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"server_name": schema.StringAttribute{
				MarkdownDescription: "GPU server hostname that owns the VF.",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"vf_id": schema.StringAttribute{
				MarkdownDescription: "VF path id — host `server_if` (e.g. vf4) or DPU `if_name` (e.g. pf1vf0_if).",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"tenant_name": schema.StringAttribute{
				MarkdownDescription: "Tenant to bind (POST/DELETE body field tenantName).",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"prefer": schema.StringAttribute{
				MarkdownDescription: "Prefer mode: respond-sync (default). Async is disabled in the current release.",
				Optional:            true,
			},
			"status": schema.StringAttribute{
				MarkdownDescription: "VF status after assign (e.g. provisioned).",
				Computed:            true,
			},
			"vlan_id": schema.Int64Attribute{
				MarkdownDescription: "Tenant VLAN id applied to the VF.",
				Computed:            true,
			},
			"vni_id": schema.Int64Attribute{
				MarkdownDescription: "Tenant VNI id applied to the VF.",
				Computed:            true,
			},
			"provisioned_at": schema.StringAttribute{
				MarkdownDescription: "Provision timestamp from the assign API response.",
				Computed:            true,
			},
			"dpu_name": schema.StringAttribute{
				MarkdownDescription: "DPU hostname that owns this VF (from GET vf-interfaces).",
				Computed:            true,
			},
			"if_name": schema.StringAttribute{
				MarkdownDescription: "Resolved DPU-side interface name.",
				Computed:            true,
			},
			"server_if": schema.StringAttribute{
				MarkdownDescription: "Resolved host-side VF name.",
				Computed:            true,
			},
			"id": schema.StringAttribute{
				MarkdownDescription: "Resource id: fabric:server:vf_id:tenant.",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *VfAssignResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *VfAssignResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data VfAssignResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fabricName, serverName, vfID, tenantName, err := vfAssignIdentity(r.client.Fabric, data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid configuration", err.Error())
		return
	}

	prefer := ""
	if !data.Prefer.IsNull() {
		prefer = data.Prefer.ValueString()
	}
	if strings.EqualFold(preferHeaderValue(prefer), "respond-async") && !asyncEnabled() {
		resp.Diagnostics.AddError(
			"Async not supported",
			"Async operations are currently disabled for this release. Use prefer=respond-sync (default).",
		)
		return
	}

	assignResp, err := r.client.AssignVf(ctx, fabricName, serverName, vfID, tenantName, prefer)
	if err != nil {
		resp.Diagnostics.AddError("VF assign failed", err.Error())
		return
	}

	applyVfAssignResponse(&data, fabricName, serverName, vfID, tenantName, assignResp)

	// Enrich with DPU / if_name / server_if from lookup when available.
	if list, err := r.client.GetVfInterfaces(ctx, fabricName, serverName); err == nil {
		if dpuName, vf, ok := FindVfInterface(list, vfID); ok {
			data.DpuName = types.StringValue(dpuName)
			data.IfName = types.StringValue(vf.IfName)
			data.ServerIf = types.StringValue(vf.ServerIf)
			if strings.TrimSpace(vf.Status) != "" {
				data.Status = types.StringValue(vf.Status)
			}
		}
	}

	data.ID = types.StringValue(stableVfAssignID(fabricName, serverName, vfID, tenantName))
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *VfAssignResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data VfAssignResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fabricName, serverName, vfID, tenantName, err := vfAssignIdentity(r.client.Fabric, data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid state", err.Error())
		return
	}

	list, err := r.client.GetVfInterfaces(ctx, fabricName, serverName)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read VF interfaces", err.Error())
		return
	}

	dpuName, vf, ok := FindVfInterface(list, vfID)
	if !ok {
		resp.State.RemoveResource(ctx)
		return
	}

	status := strings.ToLower(strings.TrimSpace(vf.Status))
	if status == "free" || status == "" {
		resp.State.RemoveResource(ctx)
		return
	}

	apiTenant := strings.TrimSpace(vf.TenantName)
	if apiTenant != "" && !strings.EqualFold(apiTenant, tenantName) {
		resp.Diagnostics.AddWarning(
			"VF tenant mismatch",
			fmt.Sprintf(
				"VF %q on server %q is provisioned to tenant %q, but Terraform state has tenant %q. "+
					"Refresh or re-import if the assignment was changed outside Terraform.",
				vfID, serverName, apiTenant, tenantName,
			),
		)
	}

	data.FabricName = types.StringValue(fabricName)
	data.ServerName = types.StringValue(serverName)
	data.VfID = types.StringValue(vfID)
	data.TenantName = types.StringValue(tenantName)
	data.Status = types.StringValue(vf.Status)
	data.DpuName = types.StringValue(dpuName)
	data.IfName = types.StringValue(vf.IfName)
	data.ServerIf = types.StringValue(vf.ServerIf)
	data.ID = types.StringValue(stableVfAssignID(fabricName, serverName, vfID, tenantName))

	// vlan/vni/provisioned_at are not always on GET; keep state values when present.
	if data.VlanID.IsNull() {
		data.VlanID = types.Int64Null()
	}
	if data.VniID.IsNull() {
		data.VniID = types.Int64Null()
	}
	if data.ProvisionedAt.IsNull() {
		data.ProvisionedAt = types.StringNull()
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *VfAssignResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// All identity fields RequireReplace; only optional prefer may change without replace.
	var plan VfAssignResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state VfAssignResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state.Prefer = plan.Prefer
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *VfAssignResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data VfAssignResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fabricName, serverName, vfID, tenantName, err := vfAssignIdentity(r.client.Fabric, data)
	if err != nil {
		resp.Diagnostics.AddError("Invalid state", err.Error())
		return
	}

	prefer := ""
	if !data.Prefer.IsNull() {
		prefer = data.Prefer.ValueString()
	}

	// Best-effort consistency check: confirm VF is still bound to this tenant before unbind.
	if list, err := r.client.GetVfInterfaces(ctx, fabricName, serverName); err == nil {
		if _, vf, ok := FindVfInterface(list, vfID); ok {
			status := strings.ToLower(strings.TrimSpace(vf.Status))
			if status == "free" || status == "" {
				resp.State.RemoveResource(ctx)
				return
			}
			apiTenant := strings.TrimSpace(vf.TenantName)
			if apiTenant != "" && !strings.EqualFold(apiTenant, tenantName) {
				resp.Diagnostics.AddError(
					"VF tenant mismatch on destroy",
					fmt.Sprintf(
						"Refusing to unbind VF %q: API reports tenant %q but Terraform state has %q. "+
							"Fix state or unbind manually.",
						vfID, apiTenant, tenantName,
					),
				)
				return
			}
		}
	}

	if err := r.client.UnassignVf(ctx, fabricName, serverName, vfID, tenantName, prefer); err != nil {
		// Already free is effectively success for destroy.
		if strings.Contains(strings.ToUpper(err.Error()), "VF_ALREADY_FREE") {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("VF unassign failed", err.Error())
		return
	}

	resp.State.RemoveResource(ctx)
}

func (r *VfAssignResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Expected: fabric:server:vf_id:tenant
	parts := strings.Split(req.ID, ":")
	if len(parts) != 4 {
		resp.Diagnostics.AddError(
			"Invalid import id",
			"Expected import id format: fabric:server:vf_id:tenant",
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("fabric_name"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("server_name"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("vf_id"), parts[2])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("tenant_name"), parts[3])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

func vfAssignIdentity(providerFabric string, data VfAssignResourceModel) (fabric, server, vfID, tenant string, err error) {
	fabric = strings.TrimSpace(resolveFabricName(providerFabric, data.FabricName))
	server = strings.TrimSpace(data.ServerName.ValueString())
	vfID = strings.TrimSpace(data.VfID.ValueString())
	tenant = strings.TrimSpace(data.TenantName.ValueString())
	if fabric == "" {
		return "", "", "", "", fmt.Errorf("fabric_name is required (or set FABRIC_NAME / provider fabric)")
	}
	if server == "" {
		return "", "", "", "", fmt.Errorf("server_name is required")
	}
	if vfID == "" {
		return "", "", "", "", fmt.Errorf("vf_id is required")
	}
	if tenant == "" {
		return "", "", "", "", fmt.Errorf("tenant_name is required")
	}
	return fabric, server, vfID, tenant, nil
}

func stableVfAssignID(fabric, server, vfID, tenant string) string {
	return fmt.Sprintf("%s:%s:%s:%s", fabric, server, vfID, tenant)
}

func applyVfAssignResponse(data *VfAssignResourceModel, fabric, server, vfID, tenant string, assignResp *VfAssignResponse) {
	data.FabricName = types.StringValue(fabric)
	data.ServerName = types.StringValue(server)
	data.VfID = types.StringValue(vfID)
	data.TenantName = types.StringValue(tenant)

	if assignResp == nil {
		data.Status = types.StringNull()
		data.VlanID = types.Int64Null()
		data.VniID = types.Int64Null()
		data.ProvisionedAt = types.StringNull()
		return
	}

	if strings.TrimSpace(assignResp.Status) != "" {
		data.Status = types.StringValue(assignResp.Status)
	} else {
		data.Status = types.StringNull()
	}
	if assignResp.VlanID != nil {
		data.VlanID = types.Int64Value(int64(*assignResp.VlanID))
	} else {
		data.VlanID = types.Int64Null()
	}
	if assignResp.VniID != nil {
		data.VniID = types.Int64Value(int64(*assignResp.VniID))
	} else {
		data.VniID = types.Int64Null()
	}
	if strings.TrimSpace(assignResp.ProvisionedAt) != "" {
		data.ProvisionedAt = types.StringValue(assignResp.ProvisionedAt)
	} else {
		data.ProvisionedAt = types.StringNull()
	}
}
