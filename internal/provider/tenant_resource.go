package provider

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
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
var _ resource.ResourceWithModifyPlan = &TenantResource{}

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
	Prefer         types.String `tfsdk:"prefer"`
	WebhooksEnabled types.Bool  `tfsdk:"webhooks_enabled"`
	WebhookURL     types.String `tfsdk:"webhook_url"`
	WebhookEvents  types.List   `tfsdk:"webhook_events"`
	OperationID    types.String `tfsdk:"operation_id"`
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
			"prefer": schema.StringAttribute{
				MarkdownDescription: "Prefer mode for this operation. Use respond-sync (default) or respond-async; these match the HTTP Prefer header. Underscore forms respond_sync / respond_async are accepted and normalized. If tenant create returns MAX_GPUS_INVALID only with respond-async while the same max_gpus_allowed succeeds with respond-sync, the Fabric API async handler is at fault (the provider sends the same JSON body); use respond-sync for tenant creation until the API is fixed.",
				Optional:            true,
			},
			"webhooks_enabled": schema.BoolAttribute{
				MarkdownDescription: "Enable webhook callback payload for async operations (Prefer: respond-async). If unset, false is used.",
				Optional:            true,
			},
			"webhook_url": schema.StringAttribute{
				MarkdownDescription: "Webhook receiver URL (used when prefer is respond-async and webhooks_enabled=true). The API rejects localhost and loopback URLs (SSRF prevention); use a reachable host/IP from the controller. If unset, defaults to http://localhost:8787/test/webhook-receiver (only valid when webhooks are disabled).",
				Optional:            true,
			},
			"webhook_events": schema.ListAttribute{
				MarkdownDescription: "Webhook event list (used only when prefer is respond-async and webhooks_enabled=true).",
				Optional:            true,
				ElementType:         types.StringType,
			},
			"operation_id": schema.StringAttribute{
				MarkdownDescription: "Operation/job id for async requests (202). Used for polling when webhooks are disabled.",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
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

// ModifyPlan prevents accidental destroy+recreate when users change immutable fields.
// QA expectation: changing max_gpus_allowed/description should not replace automatically;
// the apply should fail with a clear message so the existing tenant is left untouched.
func (r *TenantResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	// If state is null, this is a create plan; nothing to enforce.
	if req.State.Raw.IsNull() {
		return
	}
	// If plan is null, this is a destroy plan; do not attempt to read/validate values.
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan TenantResourceModel
	var state TenantResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// If the resource already exists in state and the user re-applies the same configuration,
	// Terraform will report "No changes". Add a friendly hint explaining why.
	// (Terraform does not call Create again in this case.)
	if !plan.TenantName.IsUnknown() && !state.TenantName.IsUnknown() &&
		!plan.TenantName.IsNull() && !state.TenantName.IsNull() &&
		plan.TenantName.ValueString() == state.TenantName.ValueString() &&
		!plan.Description.IsUnknown() && !state.Description.IsUnknown() &&
		!plan.Description.IsNull() && !state.Description.IsNull() &&
		plan.Description.ValueString() == state.Description.ValueString() &&
		!plan.MaxGpusAllowed.IsUnknown() && !state.MaxGpusAllowed.IsUnknown() &&
		!plan.MaxGpusAllowed.IsNull() && !state.MaxGpusAllowed.IsNull() &&
		plan.MaxGpusAllowed.ValueInt64() == state.MaxGpusAllowed.ValueInt64() {
		resp.Diagnostics.AddWarning(
			"Tenant already managed",
			fmt.Sprintf(
				"Tenant %q already exists and is already managed in this Terraform state. "+
					"Re-running apply with the same configuration will result in no changes.\n\n"+
					"If the tenant exists in the backend but is not in this state (created via UI or a different state), import it instead:\n"+
					"  terraform import fabricapi_tenant.<name> %s",
				plan.TenantName.ValueString(),
				plan.TenantName.ValueString(),
			),
		)
	}

	// Only block when the resource already exists and user is trying to change immutable fields.
	if !plan.Description.IsUnknown() && !state.Description.IsUnknown() &&
		!plan.Description.IsNull() && !state.Description.IsNull() &&
		plan.Description.ValueString() != state.Description.ValueString() {
		resp.Diagnostics.AddError(
			"Unsupported update for tenant resource",
			"The attribute `description` cannot be modified for an existing tenant.\n\n"+
				"If you need to change this value, the tenant must be recreated:\n\n"+
				"Destroy the existing tenant:\n"+
				"  terraform destroy -target=fabricapi_tenant.<name>\n\n"+
				"Then apply:\n"+
				"  terraform apply\n\n"+
				"Note: This will result in resource re-creation.",
		)
	}

	if !plan.MaxGpusAllowed.IsUnknown() && !state.MaxGpusAllowed.IsUnknown() &&
		!plan.MaxGpusAllowed.IsNull() && !state.MaxGpusAllowed.IsNull() &&
		plan.MaxGpusAllowed.ValueInt64() != state.MaxGpusAllowed.ValueInt64() {
		resp.Diagnostics.AddError(
			"Unsupported update for tenant resource",
			"The attribute `max_gpus_allowed` cannot be modified for an existing tenant.\n\n"+
				"If you need to change this value, the tenant must be recreated:\n\n"+
				"Destroy the existing tenant:\n"+
				"  terraform destroy -target=fabricapi_tenant.<name>\n\n"+
				"Then apply:\n"+
				"  terraform apply\n\n"+
				"Note: This will result in resource re-creation.",
		)
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

	// If the tenant already exists (created via UI or managed by another Terraform state),
	// return a clear error prompting the user to import instead of attempting create.
	existing, err := r.client.GetTenantWithFabric(fabricName, data.TenantName.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to verify tenant existence before create: %s", err))
		return
	}
	if existing != nil {
		resp.Diagnostics.AddError(
			"Tenant already exists",
			fmt.Sprintf(
				"Tenant %q already exists in fabric %q. Import it into Terraform state instead of creating it again.\n\n"+
					"Example:\n"+
					"  terraform import fabricapi_tenant.<name> %s\n\n"+
					"If the tenant belongs to a different fabric than your provider default, use:\n"+
					"  terraform import fabricapi_tenant.<name> %s:%s",
				data.TenantName.ValueString(),
				fabricName,
				data.TenantName.ValueString(),
				fabricName,
				data.TenantName.ValueString(),
			),
		)
		return
	}

	tenantReq := TenantRequest{
		TenantName:     data.TenantName.ValueString(),
		Description:    data.Description.ValueString(),
		MaxGpusAllowed: int(data.MaxGpusAllowed.ValueInt64()),
	}

	prefer := "respond-sync"
	if !data.Prefer.IsNull() && strings.TrimSpace(data.Prefer.ValueString()) != "" {
		prefer = data.Prefer.ValueString()
	}
	webhooksEnabled := false
	if !data.WebhooksEnabled.IsNull() && !data.WebhooksEnabled.IsUnknown() {
		webhooksEnabled = data.WebhooksEnabled.ValueBool()
	}
	webhookURL := "http://localhost:8787/test/webhook-receiver"
	if !data.WebhookURL.IsNull() && strings.TrimSpace(data.WebhookURL.ValueString()) != "" {
		webhookURL = data.WebhookURL.ValueString()
	}
	var webhookEvents []string
	if !data.WebhookEvents.IsNull() && !data.WebhookEvents.IsUnknown() {
		resp.Diagnostics.Append(data.WebhookEvents.ElementsAs(ctx, &webhookEvents, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	// Validate webhook inputs only for async+enabled.
	if strings.EqualFold(preferHeaderValue(prefer), "respond-async") && webhooksEnabled {
		if strings.TrimSpace(webhookURL) == "" || len(webhookEvents) == 0 {
			resp.Diagnostics.AddError(
				"Missing webhook configuration",
				"When prefer is respond-async and webhooks_enabled is true, both webhook_url and webhook_events must be provided.",
			)
			return
		}
		if isWebhookURLLoopback(webhookURL) {
			resp.Diagnostics.AddError(
				"Invalid webhook_url",
				"The Fabric API rejects webhook URLs that use localhost or loopback addresses (SSRF prevention). Set webhook_url to a hostname or IP reachable from the controller (for example the fabric API host), not localhost or 127.0.0.1.",
			)
			return
		}
	}

	_, opID, err := r.client.CreateTenantWithFabricWithOptions(ctx, fabricName, tenantReq, &requestOptions{
		Prefer:          prefer,
		WebhooksEnabled: webhooksEnabled,
		WebhookURL:      webhookURL,
		WebhookEvents:   webhookEvents,
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create tenant: %s", err))
		return
	}

	// Async polling only when webhooks are disabled.
	if strings.EqualFold(preferHeaderValue(prefer), "respond-async") && !webhooksEnabled && strings.TrimSpace(opID) != "" {
		if err := r.client.WaitForOperationDone(ctx, opID, 60*time.Minute); err != nil {
			resp.Diagnostics.AddError("Async operation failed", err.Error())
			return
		}
	}

	// Preserve previous behavior (readiness wait) only when we are not relying on webhook async.
	if !(strings.EqualFold(preferHeaderValue(prefer), "respond-async") && webhooksEnabled) {
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
	if strings.TrimSpace(opID) == "" {
		data.OperationID = types.StringNull()
	} else {
		data.OperationID = types.StringValue(opID)
	}

	data.Prefer = types.StringValue(prefer)
	data.WebhooksEnabled = types.BoolValue(webhooksEnabled)
	data.WebhookURL = types.StringValue(webhookURL)
	evList, diags := types.ListValueFrom(ctx, types.StringType, webhookEvents)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.WebhookEvents = evList

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

	prefer := "respond-sync"
	if !data.Prefer.IsNull() && strings.TrimSpace(data.Prefer.ValueString()) != "" {
		prefer = data.Prefer.ValueString()
	}
	webhooksEnabled := false
	if !data.WebhooksEnabled.IsNull() && !data.WebhooksEnabled.IsUnknown() {
		webhooksEnabled = data.WebhooksEnabled.ValueBool()
	}
	webhookURL := "http://localhost:8787/test/webhook-receiver"
	if !data.WebhookURL.IsNull() && strings.TrimSpace(data.WebhookURL.ValueString()) != "" {
		webhookURL = data.WebhookURL.ValueString()
	}
	var webhookEvents []string
	if !data.WebhookEvents.IsNull() && !data.WebhookEvents.IsUnknown() {
		resp.Diagnostics.Append(data.WebhookEvents.ElementsAs(ctx, &webhookEvents, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	if strings.EqualFold(preferHeaderValue(prefer), "respond-async") && webhooksEnabled {
		if strings.TrimSpace(webhookURL) == "" || len(webhookEvents) == 0 {
			resp.Diagnostics.AddError(
				"Missing webhook configuration",
				"When prefer is respond-async and webhooks_enabled is true, both webhook_url and webhook_events must be provided.",
			)
			return
		}
		// Backend requires delete-specific event for delete operations.
		// Tenants may have been created with webhook_events=["tenant.create"], which will be rejected on delete.
		// Always send tenant.delete for tenant deletion.
		webhookEvents = []string{"tenant.delete"}
	}

	opID, err := r.client.DeleteTenantWithFabricWithOptions(ctx, fabricName, tenantName, &requestOptions{
		Prefer:          prefer,
		WebhooksEnabled: webhooksEnabled,
		WebhookURL:      webhookURL,
		WebhookEvents:   webhookEvents,
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete tenant: %s", err))
		return
	}

	if strings.EqualFold(preferHeaderValue(prefer), "respond-async") && !webhooksEnabled && strings.TrimSpace(opID) != "" {
		if err := r.client.WaitForOperationDone(ctx, opID, 60*time.Minute); err != nil {
			resp.Diagnostics.AddError("Async operation failed", err.Error())
			return
		}
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

// isWebhookURLLoopback matches Fabric API SSRF rules (localhost / loopback IPs).
func isWebhookURLLoopback(raw string) bool {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return false
	}
	host := u.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
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
