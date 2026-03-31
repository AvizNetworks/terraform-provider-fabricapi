package provider

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
)

var _ resource.Resource = &TenantResource{}
var _ resource.ResourceWithImportState = &TenantResource{}

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
	FabricName     types.String `tfsdk:"fabric_name"`
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
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
				Validators: []validator.String{
					stringvalidator.RegexMatches(
						regexp.MustCompile(`^[a-zA-Z0-9-_]{3,32}$`),
						"Tenant name must be 3–32 chars: letters, numbers, - or _",
					),
				},
			},

			"description": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},

			"max_gpus_allowed": schema.Int64Attribute{
				Required: true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},

			"fabric_name": schema.StringAttribute{
				MarkdownDescription: "Fabric name (overrides provider-level fabric if specified)",
				Optional:            true,
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

	// Get fabric name - use resource override if provided, otherwise use client default
	fabricName := r.client.Fabric
	if !data.FabricName.IsNull() && data.FabricName.ValueString() != "" {
		fabricName = data.FabricName.ValueString()
	}

	tenantReq := TenantRequest{
		TenantName:     data.TenantName.ValueString(),
		Description:    data.Description.ValueString(),
		MaxGpusAllowed: int(data.MaxGpusAllowed.ValueInt64()),
	}

	_, err := r.client.CreateTenantWithFabric(fabricName, tenantReq)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create tenant: %s", err))
		return
	}

	// --- WAIT FOR TENANT READINESS (prevents backend NullPointerException) ---
	err = r.client.WaitForTenantReady(ctx, fabricName, data.TenantName.ValueString(), 60*time.Second)
	if err != nil {
		resp.Diagnostics.AddError(
			"Tenant provisioning not completed",
			fmt.Sprintf("Tenant %s was created but controller did not finish provisioning: %s",
				data.TenantName.ValueString(), err),
		)
		return
	}

	// State must match the plan exactly (avoids "inconsistent result" / phantom attributes).
	plannedName := data.TenantName.ValueString()
	data.ID = types.StringValue(plannedName)
	data.TenantName = types.StringValue(plannedName)
	data.Description = types.StringValue(data.Description.ValueString())
	data.MaxGpusAllowed = types.Int64Value(data.MaxGpusAllowed.ValueInt64())
	if data.FabricName.IsNull() || data.FabricName.ValueString() == "" {
		data.FabricName = types.StringNull()
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *TenantResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data TenantResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Get fabric name - use resource override if provided, otherwise use client default
	fabricName := r.client.Fabric
	if !data.FabricName.IsNull() && data.FabricName.ValueString() != "" {
		fabricName = data.FabricName.ValueString()
	}

	result, err := r.client.GetTenantWithFabric(fabricName, data.TenantName.ValueString())
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
	resp.Diagnostics.AddError(
		"Tenant update not supported",
		"The Fabric API does not support updating tenant properties. "+
			"Changing description or max_gpus_allowed requires recreating the tenant.",
	)
}

func (r *TenantResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data TenantResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	tenantName := data.TenantName.ValueString()

	// Get fabric name - use resource override if provided, otherwise use client default
	fabricName := r.client.Fabric
	if !data.FabricName.IsNull() && data.FabricName.ValueString() != "" {
		fabricName = data.FabricName.ValueString()
	}

	// Safe delete workflow:
	// Always query backend tenant state first (never trust Terraform state for allocations).
	// If GPUs are still allocated, deallocate them before deleting the tenant.
	tenantInfo, err := r.client.GetTenantWithFabric(fabricName, tenantName)
	if err != nil {
		resp.Diagnostics.AddWarning(
			"Unable to verify GPU allocation status",
			fmt.Sprintf("Could not check allocated GPUs before tenant deletion: %s. Proceeding with delete request.", err),
		)
	} else if tenantInfo != nil {
		toFree := ServersForDeallocation(tenantInfo)
		if len(toFree) > 0 {
			resp.Diagnostics.AddWarning(
				"Deallocating tenant servers before delete",
				fmt.Sprintf("Tenant %s has allocated servers: %v. Sending DELETE server deallocation first.", tenantName, toFree),
			)

			err = r.client.UpdateTenantServersWithFabric(fabricName, tenantName, "DELETE", toFree, nil)
			if err != nil {
				resp.Diagnostics.AddError(
					"Failed to deallocate servers",
					fmt.Sprintf("Unable to deallocate tenant servers before delete: %s", err),
				)
				return
			}
		}
	}

	err = r.client.DeleteTenantWithFabric(fabricName, tenantName)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete tenant: %s", err))
		return
	}
}

func (r *TenantResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// The import ID is the tenant name, optionally with fabric: "fabric_name:tenant_name" or just "tenant_name"
	importID := req.ID
	var tenantName, fabricName string

	// Check if import ID contains fabric override
	if len(importID) > 0 && importID[0] != ':' {
		// Format: "fabric_name:tenant_name" or just "tenant_name"
		parts := splitImportID(importID)
		if len(parts) == 2 {
			fabricName = parts[0]
			tenantName = parts[1]
		} else {
			fabricName = r.client.Fabric
			tenantName = importID
		}
	} else {
		fabricName = r.client.Fabric
		tenantName = importID
	}

	// Fetch tenant data from API
	tenantInfo, err := r.client.GetTenantWithFabric(fabricName, tenantName)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read tenant during import: %s", err))
		return
	}

	if tenantInfo == nil {
		resp.Diagnostics.AddError("Tenant Not Found", fmt.Sprintf("Tenant '%s' does not exist", tenantName))
		return
	}

	// Set state with imported data
	state := TenantResourceModel{
		TenantName:     types.StringValue(tenantInfo.TenantName),
		Description:    types.StringValue(tenantInfo.Description),
		MaxGpusAllowed: types.Int64Value(int64(tenantInfo.MaxGpusAllowed)),
		ID:             types.StringValue(tenantInfo.TenantName),
	}

	if fabricName != r.client.Fabric {
		state.FabricName = types.StringValue(fabricName)
	} else {
		state.FabricName = types.StringNull()
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// splitImportID splits an import ID in the format "fabric_name:tenant_name"
func splitImportID(id string) []string {
	for i, c := range id {
		if c == ':' {
			return []string{id[:i], id[i+1:]}
		}
	}
	return []string{id}
}
