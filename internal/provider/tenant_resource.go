package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &TenantResource{}

func NewTenantResource() resource.Resource {
	return &TenantResource{}
}

type TenantResource struct {
	client *APIClient
}

type TenantResourceModel struct {
	TenantName     types.String `tfsdk:"tenant_name"`
	Description    types.String `tfsdk:"description"`
	MaxGpusAllowed types.Int64  `tfsdk:"max_gpus_allowed"`
	ID             types.String `tfsdk:"id"`
}

func (r *TenantResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tenant"
}

func (r *TenantResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fabric API Tenant Resource",

		Attributes: map[string]schema.Attribute{
			"tenant_name": schema.StringAttribute{
				MarkdownDescription: "Name of the tenant",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Description of the tenant",
				Required:            true,
			},
			"max_gpus_allowed": schema.Int64Attribute{
				MarkdownDescription: "Maximum number of GPUs allowed",
				Required:            true,
			},
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Tenant identifier",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *TenantResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *TenantResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data TenantResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	tenantReq := TenantRequest{
		TenantName:     data.TenantName.ValueString(),
		Description:    data.Description.ValueString(),
		MaxGpusAllowed: int(data.MaxGpusAllowed.ValueInt64()),
	}

	result, err := r.client.CreateTenant(tenantReq)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create tenant: %s", err))
		return
	}

	// IMPORTANT: Always use the planned values if API returns empty
	// This prevents the "inconsistent result" error
	if result.TenantName == "" {
		result.TenantName = data.TenantName.ValueString()
	}
	if result.Description == "" {
		result.Description = data.Description.ValueString()
	}
	if result.MaxGpusAllowed == 0 {
		result.MaxGpusAllowed = int(data.MaxGpusAllowed.ValueInt64())
	}

	// Set all fields from the result
	data.ID = types.StringValue(result.TenantName)
	data.TenantName = types.StringValue(result.TenantName)
	data.Description = types.StringValue(result.Description)
	data.MaxGpusAllowed = types.Int64Value(int64(result.MaxGpusAllowed))

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TenantResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data TenantResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	result, err := r.client.GetTenant(data.TenantName.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read tenant: %s", err))
		return
	}

	if result == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	// Preserve existing values if API returns empty
	if result.TenantName == "" {
		result.TenantName = data.TenantName.ValueString()
	}
	if result.Description == "" {
		result.Description = data.Description.ValueString()
	}
	if result.MaxGpusAllowed == 0 {
		result.MaxGpusAllowed = int(data.MaxGpusAllowed.ValueInt64())
	}

	data.TenantName = types.StringValue(result.TenantName)
	data.Description = types.StringValue(result.Description)
	data.MaxGpusAllowed = types.Int64Value(int64(result.MaxGpusAllowed))

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TenantResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data TenantResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.AddWarning(
		"Update Not Fully Supported",
		"The API does not support updating tenant properties. Terraform will track the new values but the API may not reflect them.",
	)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TenantResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data TenantResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.DeleteTenant(data.TenantName.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete tenant: %s", err))
		return
	}
}