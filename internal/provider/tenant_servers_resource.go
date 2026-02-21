package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &TenantServersResource{}

func NewTenantServersResource() resource.Resource {
	return &TenantServersResource{}
}

type TenantServersResource struct {
	client *APIClient
}

type TenantServersResourceModel struct {
	TenantName types.String `tfsdk:"tenant_name"`
	Operation  types.String `tfsdk:"operation"`
	Servers    types.List   `tfsdk:"servers"`
	ID         types.String `tfsdk:"id"`
}

func (r *TenantServersResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tenant_servers"
}

func (r *TenantServersResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fabric API Tenant Servers Resource - Manages server assignments",

		Attributes: map[string]schema.Attribute{
			"tenant_name": schema.StringAttribute{
				MarkdownDescription: "Name of the tenant",
				Required:            true,
			},
			"operation": schema.StringAttribute{
				MarkdownDescription: "Operation to perform: ADD, DELETE, or REMOVE (REMOVE is alias for DELETE)",
				Required:            true,
			},
			"servers": schema.ListAttribute{
				MarkdownDescription: "List of server names",
				Required:            true,
				ElementType:         types.StringType,
			},
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource identifier",
			},
		},
	}
}

func (r *TenantServersResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *TenantServersResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data TenantServersResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	var servers []string
	resp.Diagnostics.Append(data.Servers.ElementsAs(ctx, &servers, false)...)

	if resp.Diagnostics.HasError() {
		return
	}

	operation := data.Operation.ValueString()
	if operation != "ADD" && operation != "DELETE" && operation != "REMOVE" {
		resp.Diagnostics.AddError(
			"Invalid Operation",
			fmt.Sprintf("Operation must be 'ADD', 'DELETE', or 'REMOVE', got: %s", operation),
		)
		return
	}

	tenantName := data.TenantName.ValueString()
	// --- PRE-ALLOCATION CONFLICT CHECK ---
	if operation == "ADD" {
		allocated, err := r.client.GetAllocatedServers(ctx, r.client.Fabric)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", err.Error())
			return
		}

		for _, s := range servers {
			if owner, exists := allocated[s]; exists && owner != tenantName {
				resp.Diagnostics.AddError(
					"Server already allocated",
					fmt.Sprintf("Server %s is already allocated to tenant %s", s, owner),
				)
				return
			}
		}
	}
	err := r.client.UpdateTenantServers(tenantName, operation, servers)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update tenant servers: %s", err))
		return
	}

	data.ID = types.StringValue(fmt.Sprintf("%s-%s-%d", tenantName, operation, len(servers)))

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TenantServersResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data TenantServersResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TenantServersResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data TenantServersResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	var servers []string
	resp.Diagnostics.Append(data.Servers.ElementsAs(ctx, &servers, false)...)

	if resp.Diagnostics.HasError() {
		return
	}

	operation := data.Operation.ValueString()
	
	// --- PRE-ALLOCATION CONFLICT CHECK ---
	if operation == "ADD" {
		allocated, err := r.client.GetAllocatedServers(ctx, r.client.Fabric)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", err.Error())
			return
		}

		tenantName := data.TenantName.ValueString()
		for _, s := range servers {
			if owner, exists := allocated[s]; exists && owner != tenantName {
				resp.Diagnostics.AddError(
					"Server already allocated",
					fmt.Sprintf("Server %s is already allocated to tenant %s", s, owner),
				)
				return
			}
		}
	}
	
	err := r.client.UpdateTenantServers(data.TenantName.ValueString(), operation, servers)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update tenant servers: %s", err))
		return
	}

	data.ID = types.StringValue(fmt.Sprintf("%s-%s-%d", data.TenantName.ValueString(), operation, len(servers)))

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TenantServersResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data TenantServersResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	var servers []string
	resp.Diagnostics.Append(data.Servers.ElementsAs(ctx, &servers, false)...)

	if resp.Diagnostics.HasError() {
		return
	}

	err := r.client.UpdateTenantServers(data.TenantName.ValueString(), "DELETE", servers)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete tenant servers: %s", err))
		return
	}
}