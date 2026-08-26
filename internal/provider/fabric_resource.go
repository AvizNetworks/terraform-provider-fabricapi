package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &FabricResource{}

func NewFabricResource() resource.Resource {
	return &FabricResource{}
}

// FabricResource manages a fabric via the ONES UI config service:
// POST /api/config/addFabricData to create (the same call the UI makes to
// generate/create a fabric) and DELETE /api/config/deletefabricbyname/{name}
// to delete. There is no known GET endpoint for this object, so Read is a
// best-effort no-op (see VpcPeeringResource for the same pattern), and actual
// deletion on destroy is opt-in via delete_on_destroy (default: state-only
// removal, matching the pre-delete-support behavior).
type FabricResource struct {
	client *APIClient
}

type FabricResourceModel struct {
	Name              types.String `tfsdk:"name"`
	Type              types.String `tfsdk:"type"`
	Status            types.String `tfsdk:"status"`
	Description       types.String `tfsdk:"description"`
	NumOfSus          types.Int64  `tfsdk:"num_of_sus"`
	MaxNumOfSus       types.Int64  `tfsdk:"max_num_of_sus"`
	HostMap           types.Map    `tfsdk:"host_map"`
	StartingSubnetGpu types.String `tfsdk:"starting_subnet_gpu"`
	SimulationID      types.Int64  `tfsdk:"simulation_id"`
	EnableEW          types.Bool   `tfsdk:"enable_ew"`
	SuHostCnt         types.String `tfsdk:"su_host_cnt"`
	Tenant            types.String `tfsdk:"tenant"`
	Instance          types.String `tfsdk:"instance"`

	DeleteOnDestroy types.Bool `tfsdk:"delete_on_destroy"`

	ID types.String `tfsdk:"id"`
}

func (r *FabricResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_fabric"
}

func (r *FabricResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Creates (and optionally deletes) a fabric via the ONES UI config service: POST /api/config/addFabricData and DELETE /api/config/deletefabricbyname/{name} — the same calls the ONES UI makes. Requires provider attribute `config_endpoint` (or env `FABRIC_API_CONFIG_ENDPOINT`) pointing at the UI/config backend, which is a different host from `endpoint`. There is no known API to read back a fabric, so Read is a best-effort no-op. By default `terraform destroy` only removes the resource from Terraform state (does not delete remotely); set `delete_on_destroy = true` to also call the delete API.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"type": schema.StringAttribute{
				MarkdownDescription: "Fabric type, e.g. \"Aviz RA\".",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"status": schema.StringAttribute{
				MarkdownDescription: "Fabric status. Defaults to \"Draft\" if unset.",
				Optional:            true,
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"description": schema.StringAttribute{
				Optional: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"num_of_sus": schema.Int64Attribute{
				Required: true,
			},
			"max_num_of_sus": schema.Int64Attribute{
				Required: true,
			},
			"host_map": schema.MapAttribute{
				MarkdownDescription: "SU index -> host count, matching the addFabricData `hostMap` field (e.g. {\"0\" = \"1\"}).",
				Required:            true,
				ElementType:         types.StringType,
			},
			"starting_subnet_gpu": schema.StringAttribute{
				Required: true,
			},
			"simulation_id": schema.Int64Attribute{
				Required: true,
			},
			"enable_ew": schema.BoolAttribute{
				MarkdownDescription: "Enable east-west networking. Defaults to false if unset.",
				Optional:            true,
				Computed:            true,
			},
			"su_host_cnt": schema.StringAttribute{
				MarkdownDescription: "Raw `suHostCnt` value expected by the API, e.g. \"{0:1}\". Sent exactly as provided.",
				Required:            true,
			},
			"tenant": schema.StringAttribute{
				Required: true,
			},
			"instance": schema.StringAttribute{
				MarkdownDescription: "Target instance, e.g. \"fm\". Defaults to \"fm\" if unset.",
				Optional:            true,
				Computed:            true,
			},
			"delete_on_destroy": schema.BoolAttribute{
				MarkdownDescription: "If true, `terraform destroy` calls DELETE /api/config/deletefabricbyname/{name} to actually delete the fabric before removing it from Terraform state. If false/unset (default), destroy only removes it from Terraform state — the fabric remains in ONES.",
				Optional:            true,
			},
			"id": schema.StringAttribute{
				Computed: true,
			},
		},
	}
}

func (r *FabricResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *FabricResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data FabricResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	status := "Draft"
	if !data.Status.IsNull() && !data.Status.IsUnknown() && strings.TrimSpace(data.Status.ValueString()) != "" {
		status = data.Status.ValueString()
	}
	instance := "fm"
	if !data.Instance.IsNull() && !data.Instance.IsUnknown() && strings.TrimSpace(data.Instance.ValueString()) != "" {
		instance = data.Instance.ValueString()
	}
	enableEW := false
	if !data.EnableEW.IsNull() && !data.EnableEW.IsUnknown() {
		enableEW = data.EnableEW.ValueBool()
	}

	hostMap := map[string]string{}
	if !data.HostMap.IsNull() && !data.HostMap.IsUnknown() {
		resp.Diagnostics.Append(data.HostMap.ElementsAs(ctx, &hostMap, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	reqBody := FabricDataRequest{
		Name:              data.Name.ValueString(),
		Type:              data.Type.ValueString(),
		Status:            status,
		Description:       data.Description.ValueString(),
		NumOfSus:          int(data.NumOfSus.ValueInt64()),
		MaxNumOfSus:       int(data.MaxNumOfSus.ValueInt64()),
		HostMap:           hostMap,
		StartingSubnetGpu: data.StartingSubnetGpu.ValueString(),
		SimulationID:      int(data.SimulationID.ValueInt64()),
		EnableEW:          enableEW,
		SuHostCnt:         data.SuHostCnt.ValueString(),
		Tenant:            data.Tenant.ValueString(),
		Instance:          instance,
	}

	respBody, err := r.client.CreateFabricData(ctx, reqBody)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create fabric: %s", err))
		return
	}
	if respBody != "" {
		resp.Diagnostics.AddWarning("Fabric created", respBody)
	}

	data.Status = types.StringValue(status)
	data.Instance = types.StringValue(instance)
	data.EnableEW = types.BoolValue(enableEW)
	data.ID = types.StringValue(data.Name.ValueString())

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *FabricResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// No known GET endpoint for fabric data; keep state as-is (best-effort no-op),
	// matching fabricapi_vpcpeering's create-only behavior.
	var data FabricResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Update allows `delete_on_destroy` to change in place — it is local Terraform
// bookkeeping only (no API call happens on apply), so a resource created without
// it can have it flipped on later to enable a real delete on a future destroy.
// Every other attribute maps to the addFabricData request, which has no update
// endpoint, so any other change is rejected.
func (r *FabricResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state FabricResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	onlyDeleteOnDestroyChanged := plan.Name.Equal(state.Name) &&
		plan.Type.Equal(state.Type) &&
		plan.Status.Equal(state.Status) &&
		plan.Description.Equal(state.Description) &&
		plan.NumOfSus.Equal(state.NumOfSus) &&
		plan.MaxNumOfSus.Equal(state.MaxNumOfSus) &&
		plan.HostMap.Equal(state.HostMap) &&
		plan.StartingSubnetGpu.Equal(state.StartingSubnetGpu) &&
		plan.SimulationID.Equal(state.SimulationID) &&
		plan.EnableEW.Equal(state.EnableEW) &&
		plan.SuHostCnt.Equal(state.SuHostCnt) &&
		plan.Tenant.Equal(state.Tenant) &&
		plan.Instance.Equal(state.Instance)

	if !onlyDeleteOnDestroyChanged {
		resp.Diagnostics.AddError(
			"Update not supported",
			"Only `delete_on_destroy` can be changed in place. Changing any other fabric attribute requires recreating the resource.",
		)
		return
	}

	plan.ID = state.ID
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *FabricResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data FabricResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !data.DeleteOnDestroy.IsNull() && data.DeleteOnDestroy.ValueBool() {
		respBody, err := r.client.DeleteFabricData(ctx, data.Name.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete fabric: %s", err))
			return
		}
		if respBody != "" {
			resp.Diagnostics.AddWarning("Fabric deleted", respBody)
		}
	}

	// Remove from Terraform state so destroy completes cleanly and future plans are
	// accurate. When delete_on_destroy is false/unset, this only drops Terraform
	// management — the fabric remains in ONES.
	resp.State.RemoveResource(ctx)
}
