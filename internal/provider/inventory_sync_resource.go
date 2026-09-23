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

var _ resource.Resource = &InventorySyncResource{}

func NewInventorySyncResource() resource.Resource {
	return &InventorySyncResource{}
}

// InventorySyncResource is an action-style resource (like fabricapi_auth_logout): its Create
// performs POST /fabrics/{fabric}/inventorySync to force an immediate UFM inventory sync,
// bypassing the scheduled interval. Read/Update/Delete are trivial. To sync again, replace or
// re-apply the resource (terraform apply -replace=..., or destroy + apply).
type InventorySyncResource struct {
	client *APIClient
}

type InventorySyncResourceModel struct {
	Fabric  types.String `tfsdk:"fabric"`
	Message types.String `tfsdk:"message"`
	ID      types.String `tfsdk:"id"`
}

func (r *InventorySyncResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_inventory_sync"
}

func (r *InventorySyncResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Forces an immediate UFM inventory sync (POST /fabrics/{fabric}/inventorySync), reconciling FM's hosts/ports/PKey bookkeeping with the UFM appliance. Useful when FM and UFM state have diverged (e.g. a stuck/orphaned PKey blocks tenant onboarding). Action-style resource: the sync runs on create; to run it again, replace or re-apply.",
		Attributes: map[string]schema.Attribute{
			"fabric": schema.StringAttribute{
				MarkdownDescription: "Fabric to sync. If unset, uses the provider-level fabric.",
				Optional:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"message": schema.StringAttribute{
				MarkdownDescription: "Message returned by the inventory sync.",
				Computed:            true,
			},
			"id": schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *InventorySyncResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *InventorySyncResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data InventorySyncResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fabricName := resolveFabricName(r.client.Fabric, data.Fabric)
	if fabricName == "" {
		resp.Diagnostics.AddError(
			"Missing fabric",
			"Set `fabric` on this resource or `fabric` on the provider.",
		)
		return
	}

	res, err := r.client.InventorySync(ctx, fabricName)
	if err != nil {
		resp.Diagnostics.AddError("Inventory sync failed", err.Error())
		return
	}

	if res != nil {
		data.Message = types.StringValue(res.Message)
	} else {
		data.Message = types.StringNull()
	}
	data.ID = types.StringValue(fmt.Sprintf("inventorysync:%s", fabricName))

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *InventorySyncResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data InventorySyncResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *InventorySyncResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Update not supported", "Replace or re-apply this resource to run the inventory sync again.")
}

func (r *InventorySyncResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.State.RemoveResource(ctx)
}
