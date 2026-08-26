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

// FabricResource manages a fabric via the ONES UI config service
// (POST /api/config/addFabricData) — the same call the UI makes to
// generate/create a fabric. There is no known GET/DELETE endpoint for this
// object, so this resource is create-only (see VpcPeeringResource for the
// same pattern): destroy only removes it from Terraform state.
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
		MarkdownDescription: "Creates a fabric via the ONES UI config service (POST /api/config/addFabricData) — the same call the ONES UI makes to generate a fabric. Requires provider attribute `config_endpoint` (or env `FABRIC_API_CONFIG_ENDPOINT`) pointing at the UI/config backend, which is a different host from `endpoint`. Create-only: there is no known API to read back or delete a fabric, so destroy only removes it from Terraform state.",
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
				MarkdownDescription: "Reserved for forward compatibility. There is currently no known delete API for a fabric; destroy always just removes it from Terraform state.",
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

func (r *FabricResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError(
		"Update not supported",
		"Changing fabric inputs requires recreating the resource.",
	)
}

func (r *FabricResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data FabricResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !data.DeleteOnDestroy.IsNull() && data.DeleteOnDestroy.ValueBool() {
		resp.Diagnostics.AddWarning(
			"Delete on destroy not implemented",
			"This provider currently creates fabric data but does not delete it (no known delete API).",
		)
	}

	// Even though we don't delete the remote resource, remove it from Terraform state
	// so that destroy completes cleanly and future plans are accurate.
	resp.State.RemoveResource(ctx)
}
