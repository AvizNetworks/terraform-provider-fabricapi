package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

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
	FabricName types.String `tfsdk:"fabric_name"`
	Operation  types.String `tfsdk:"operation"`
	Servers    types.List   `tfsdk:"servers"`
	Shared     types.Bool   `tfsdk:"shared"`
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
			"fabric_name": schema.StringAttribute{
				MarkdownDescription: "Fabric name for the backend URL /fabrics/{fabric}/tenants/{tenant}. If unset, uses provider-level fabric.",
				Optional:            true,
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
			"shared": schema.BoolAttribute{
				MarkdownDescription: "Optional shared GPU allocation flag. When set, value is sent in PATCH payload for every server.",
				Optional:            true,
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

	fabricName := resolveFabricName(r.client.Fabric, data.FabricName)
	tenantName := data.TenantName.ValueString()

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

	// --- PRE-ALLOCATION CONFLICT CHECK ---
	if operation == "ADD" {
		allocated, err := r.client.GetAllocatedServers(ctx, fabricName)
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
	if operation == "ADD" {
		if err := r.client.WaitForTenantReady(ctx, fabricName, tenantName, 60*time.Second); err != nil {
			resp.Diagnostics.AddError(
				"Tenant not ready",
				fmt.Sprintf("Cannot allocate GPUs until tenant %s is readable: %s", tenantName, err),
			)
			return
		}
	}
	var shared *bool
	if !data.Shared.IsNull() && !data.Shared.IsUnknown() {
		v := data.Shared.ValueBool()
		shared = &v
	}

	err = r.client.UpdateTenantServersWithFabric(fabricName, tenantName, operation, servers, shared)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update tenant servers: %s", err))
		return
	}

	// Read back from API so state reflects the backend instead of only the request.
	refreshed, err := r.client.GetTenantWithFabric(fabricName, tenantName)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read tenant after update: %s", err))
		return
	}
	if refreshed == nil {
		resp.Diagnostics.AddError("Tenant not found", fmt.Sprintf("Tenant %q disappeared after update.", tenantName))
		return
	}

	serverList, diags := types.ListValueFrom(ctx, types.StringType, normalizedServersFromTenant(refreshed))
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Servers = serverList
	data.Operation = types.StringValue(operation)
	data.ID = types.StringValue(stableTenantServersID(fabricName, tenantName))

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TenantServersResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data TenantServersResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	fabricName := resolveFabricName(r.client.Fabric, data.FabricName)
	tenantName := data.TenantName.ValueString()

	tenantInfo, err := r.client.GetTenantWithFabric(fabricName, tenantName)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read tenant: %s", err))
		return
	}
	if tenantInfo == nil {
		resp.Diagnostics.Append(resp.State.RemoveResource(ctx)...)
		return
	}

	serverList, diags := types.ListValueFrom(ctx, types.StringType, normalizedServersFromTenant(tenantInfo))
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Servers = serverList
	data.ID = types.StringValue(stableTenantServersID(fabricName, tenantName))

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TenantServersResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan TenantServersResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fabricName := resolveFabricName(r.client.Fabric, plan.FabricName)
	tenantName := plan.TenantName.ValueString()

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

	var desiredServers []string
	resp.Diagnostics.Append(plan.Servers.ElementsAs(ctx, &desiredServers, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	desiredServers = normalizeServerList(desiredServers)
	currentServers := normalizedServersFromTenant(tenantInfo)

	toAdd, toDelete := diffServers(currentServers, desiredServers)

	if len(toDelete) > 0 {
		if err := r.client.UpdateTenantServersWithFabric(fabricName, tenantName, "DELETE", toDelete, nil); err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to deallocate tenant servers: %s", err))
			return
		}
	}

	if len(toAdd) > 0 {
		allocated, err := r.client.GetAllocatedServers(ctx, fabricName)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", err.Error())
			return
		}
		for _, s := range toAdd {
			if owner, exists := allocated[s]; exists && owner != tenantName {
				resp.Diagnostics.AddError(
					"Server already allocated",
					fmt.Sprintf("Server %s is already allocated to tenant %s", s, owner),
				)
				return
			}
		}

		if err := r.client.WaitForTenantReady(ctx, fabricName, tenantName, 60*time.Second); err != nil {
			resp.Diagnostics.AddError(
				"Tenant not ready",
				fmt.Sprintf("Cannot allocate GPUs until tenant %s is readable: %s", tenantName, err),
			)
			return
		}

		var shared *bool
		if !plan.Shared.IsNull() && !plan.Shared.IsUnknown() {
			v := plan.Shared.ValueBool()
			shared = &v
		}
		if err := r.client.UpdateTenantServersWithFabric(fabricName, tenantName, "ADD", toAdd, shared); err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to allocate tenant servers: %s", err))
			return
		}
	}

	refreshed, err := r.client.GetTenantWithFabric(fabricName, tenantName)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read tenant after update: %s", err))
		return
	}
	if refreshed == nil {
		resp.Diagnostics.AddError("Tenant not found", fmt.Sprintf("Tenant %q disappeared after update.", tenantName))
		return
	}

	serverList, diags := types.ListValueFrom(ctx, types.StringType, normalizedServersFromTenant(refreshed))
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.Servers = serverList
	plan.ID = types.StringValue(stableTenantServersID(fabricName, tenantName))

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *TenantServersResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data TenantServersResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	fabricName := r.client.Fabric
	if !data.FabricName.IsNull() && data.FabricName.ValueString() != "" {
		fabricName = data.FabricName.ValueString()
	}

	tenantName := data.TenantName.ValueString()

	tenantInfo, err := r.client.GetTenantWithFabric(fabricName, tenantName)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read tenant: %s", err))
		return
	}
	if tenantInfo == nil {
		// Tenant already gone; nothing to deallocate.
		resp.Diagnostics.Append(resp.State.RemoveResource(ctx)...)
		return
	}

	toFree := normalizedServersFromTenant(tenantInfo)
	if len(toFree) == 0 {
		resp.Diagnostics.Append(resp.State.RemoveResource(ctx)...)
		return
	}

	err = r.client.UpdateTenantServersWithFabric(fabricName, tenantName, "DELETE", toFree, nil)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to deallocate tenant servers: %s", err))
		return
	}

	// Deallocation completed; clear resource from Terraform state so destroy completes cleanly.
	resp.Diagnostics.Append(resp.State.RemoveResource(ctx)...)
}

func resolveFabricName(providerFabric string, override types.String) string {
	if !override.IsNull() && override.ValueString() != "" {
		return override.ValueString()
	}
	return providerFabric
}

func stableTenantServersID(fabricName, tenantName string) string {
	return fmt.Sprintf("%s:%s", fabricName, tenantName)
}

func normalizeServerList(in []string) []string {
	set := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, exists := set[s]; exists {
			continue
		}
		set[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func normalizedServersFromTenant(t *TenantResponse) []string {
	return normalizeServerList(ServersForDeallocation(t))
}

func diffServers(current, desired []string) (toAdd, toDelete []string) {
	currentSet := make(map[string]struct{}, len(current))
	desiredSet := make(map[string]struct{}, len(desired))
	for _, s := range current {
		currentSet[s] = struct{}{}
	}
	for _, s := range desired {
		desiredSet[s] = struct{}{}
	}
	for _, s := range desired {
		if _, ok := currentSet[s]; !ok {
			toAdd = append(toAdd, s)
		}
	}
	for _, s := range current {
		if _, ok := desiredSet[s]; !ok {
			toDelete = append(toDelete, s)
		}
	}
	return toAdd, toDelete
}

