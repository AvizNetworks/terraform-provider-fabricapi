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
var _ resource.ResourceWithModifyPlan = &TenantServersResource{}

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
	Prefer         types.String `tfsdk:"prefer"`
	WebhooksEnabled types.Bool  `tfsdk:"webhooks_enabled"`
	WebhookURL     types.String `tfsdk:"webhook_url"`
	WebhookEvents  types.List   `tfsdk:"webhook_events"`
	OperationID    types.String `tfsdk:"operation_id"`
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
				MarkdownDescription: "ADD, DELETE, or REMOVE (REMOVE aliases DELETE). On **create**, this is sent to the API with `servers`. On **update**, allocation changes are driven by diffing **servers** (desired set) against the live tenant from the API: servers removed from the list are deallocated (DELETE); new names are allocated (ADD). Changing only `operation` without changing `servers` may perform no API call if the set already matches the backend.",
				Required:            true,
			},
			"servers": schema.ListAttribute{
				MarkdownDescription: "Server names. On **update**, this is the **target** set: use `[]` to deallocate all, or omit servers you want removed (the provider DELETEs servers that exist on the tenant but are not in this list). It is not an imperative “only these servers for DELETE” list unless that matches the declarative diff.",
				Required:            true,
				ElementType:         types.StringType,
			},
			"shared": schema.BoolAttribute{
				MarkdownDescription: "Optional shared GPU allocation flag. When set, value is sent in PATCH payload for every server.",
				Optional:            true,
			},
			"prefer": schema.StringAttribute{
				MarkdownDescription: "Prefer mode: respond-sync (default) or respond-async (HTTP Prefer). Underscore forms are accepted and normalized.",
				Optional:            true,
			},
			"webhooks_enabled": schema.BoolAttribute{
				MarkdownDescription: "Enable webhook callback payload for async operations (Prefer: respond-async). If unset, false is used.",
				Optional:            true,
			},
			"webhook_url": schema.StringAttribute{
				MarkdownDescription: "Webhook receiver URL (when prefer is respond-async and webhooks_enabled=true). If unset, defaults to http://localhost:8787/test/webhook-receiver in the client when needed.",
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

// ModifyPlan normalizes inputs that users commonly pass in imperative workflows.
// In particular, it converts servers=[""] (or any list containing empty/whitespace-only
// entries) into servers=[], so Terraform's plan matches the state produced after apply.
func (r *TenantServersResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	// If there's no plan (e.g. destroy), nothing to do.
	if req.Plan.Raw.IsNull() {
		return
	}

	var plan TenantServersResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Normalize servers list (drop blanks, dedupe, sort).
	if !plan.Servers.IsNull() && !plan.Servers.IsUnknown() {
		var desired []string
		resp.Diagnostics.Append(plan.Servers.ElementsAs(ctx, &desired, false)...)
		if resp.Diagnostics.HasError() {
			return
		}

		norm := normalizeServerList(desired)
		serverList, diags := types.ListValueFrom(ctx, types.StringType, norm)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		plan.Servers = serverList
	}

	resp.Diagnostics.Append(resp.Plan.Set(ctx, &plan)...)
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
	operation = strings.ToUpper(strings.TrimSpace(operation))
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

	// Normalize operation (support REMOVE alias).
	if operation == "REMOVE" {
		operation = "DELETE"
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

	// DELETE semantics (QA): treat servers as the deletion list (imperative), not as the desired final set.
	// - servers=[] means "deallocate all currently allocated servers"
	// - servers=[x] means "deallocate x", and error if x isn't currently allocated
	if operation == "DELETE" {
		currentServers := normalizedServersFromTenant(tenantInfo)
		deleteList := normalizeServerList(servers)
		if len(deleteList) == 0 {
			deleteList = currentServers
		} else {
			curSet := make(map[string]struct{}, len(currentServers))
			for _, s := range currentServers {
				curSet[s] = struct{}{}
			}
			missing := make([]string, 0)
			for _, s := range deleteList {
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
		}

		// Nothing to delete: no-op without calling backend.
		if len(deleteList) == 0 {
			serverList, diags := types.ListValueFrom(ctx, types.StringType, []string{})
			resp.Diagnostics.Append(diags...)
			if resp.Diagnostics.HasError() {
				return
			}
			data.Servers = serverList
			data.Operation = types.StringValue(operation)
			data.ID = types.StringValue(stableTenantServersID(fabricName, tenantName))
			data.OperationID = types.StringNull()
			resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
			return
		}

		// Override request server list to the computed delete list.
		servers = deleteList
	}

	var shared *bool
	if !data.Shared.IsNull() && !data.Shared.IsUnknown() {
		v := data.Shared.ValueBool()
		shared = &v
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
	}

	opID, err := r.client.UpdateTenantServersWithFabricWithOptions(ctx, fabricName, tenantName, operation, servers, shared, &requestOptions{
		Prefer:          prefer,
		WebhooksEnabled: webhooksEnabled,
		WebhookURL:      webhookURL,
		WebhookEvents:   webhookEvents,
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update tenant servers: %s", err))
		return
	}

	if strings.EqualFold(preferHeaderValue(prefer), "respond-async") && !webhooksEnabled && strings.TrimSpace(opID) != "" {
		if err := r.client.WaitForOperationDone(ctx, opID, 60*time.Minute); err != nil {
			resp.Diagnostics.AddError("Async operation failed", err.Error())
			return
		}
	}

	// State semantics:
	// - For ADD: servers represents the allocated set, so we read back from API (unless async+webhook).
	// - For DELETE: servers represents the deletion request list (imperative), not the final allocation set.
	if operation == "DELETE" {
		serverList, diags := types.ListValueFrom(ctx, types.StringType, normalizeServerList(servers))
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		data.Servers = serverList
	} else if !(strings.EqualFold(preferHeaderValue(prefer), "respond-async") && webhooksEnabled) {
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
	} else {
		// Async+webhook: do not block; keep planned servers list.
		serverList, diags := types.ListValueFrom(ctx, types.StringType, normalizeServerList(servers))
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		data.Servers = serverList
	}
	data.Operation = types.StringValue(operation)
	data.ID = types.StringValue(stableTenantServersID(fabricName, tenantName))
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
		resp.State.RemoveResource(ctx)
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
	var state TenantServersResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var didAllocMutation bool
	var mutationOpID string

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

	operation := plan.Operation.ValueString()
	operation = strings.ToUpper(strings.TrimSpace(operation))
	if operation == "REMOVE" {
		operation = "DELETE"
	}

	// DELETE semantics (QA): imperative deletion.
	// - servers=[] => delete all currently allocated servers
	// - servers=[x] => delete x; error if x not currently allocated
	var toAdd, toDelete []string
	if operation == "DELETE" {
		if len(desiredServers) == 0 {
			toDelete = currentServers
		} else {
			curSet := make(map[string]struct{}, len(currentServers))
			for _, s := range currentServers {
				curSet[s] = struct{}{}
			}
			missing := make([]string, 0)
			for _, s := range desiredServers {
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
			toDelete = desiredServers
		}
		toAdd = nil
	} else {
		toAdd, toDelete = diffServers(currentServers, desiredServers)
	}

	if len(toDelete) > 0 {
		prefer := "respond-sync"
		if !plan.Prefer.IsNull() && strings.TrimSpace(plan.Prefer.ValueString()) != "" {
			prefer = plan.Prefer.ValueString()
		}
		webhooksEnabled := false
		if !plan.WebhooksEnabled.IsNull() && !plan.WebhooksEnabled.IsUnknown() {
			webhooksEnabled = plan.WebhooksEnabled.ValueBool()
		}
		webhookURL := "http://localhost:8787/test/webhook-receiver"
		if !plan.WebhookURL.IsNull() && strings.TrimSpace(plan.WebhookURL.ValueString()) != "" {
			webhookURL = plan.WebhookURL.ValueString()
		}
		var webhookEvents []string
		if !plan.WebhookEvents.IsNull() && !plan.WebhookEvents.IsUnknown() {
			resp.Diagnostics.Append(plan.WebhookEvents.ElementsAs(ctx, &webhookEvents, false)...)
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
		}

		opID, err := r.client.UpdateTenantServersWithFabricWithOptions(ctx, fabricName, tenantName, "DELETE", toDelete, nil, &requestOptions{
			Prefer:          prefer,
			WebhooksEnabled: webhooksEnabled,
			WebhookURL:      webhookURL,
			WebhookEvents:   webhookEvents,
		})
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to deallocate tenant servers: %s", err))
			return
		}
		if strings.EqualFold(preferHeaderValue(prefer), "respond-async") && !webhooksEnabled && strings.TrimSpace(opID) != "" {
			if err := r.client.WaitForOperationDone(ctx, opID, 60*time.Minute); err != nil {
				resp.Diagnostics.AddError("Async operation failed", err.Error())
				return
			}
		}
		didAllocMutation = true
		mutationOpID = opID
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
		prefer := "respond-sync"
		if !plan.Prefer.IsNull() && strings.TrimSpace(plan.Prefer.ValueString()) != "" {
			prefer = plan.Prefer.ValueString()
		}
		webhooksEnabled := false
		if !plan.WebhooksEnabled.IsNull() && !plan.WebhooksEnabled.IsUnknown() {
			webhooksEnabled = plan.WebhooksEnabled.ValueBool()
		}
		webhookURL := "http://localhost:8787/test/webhook-receiver"
		if !plan.WebhookURL.IsNull() && strings.TrimSpace(plan.WebhookURL.ValueString()) != "" {
			webhookURL = plan.WebhookURL.ValueString()
		}
		var webhookEvents []string
		if !plan.WebhookEvents.IsNull() && !plan.WebhookEvents.IsUnknown() {
			resp.Diagnostics.Append(plan.WebhookEvents.ElementsAs(ctx, &webhookEvents, false)...)
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
		}

		opID, err := r.client.UpdateTenantServersWithFabricWithOptions(ctx, fabricName, tenantName, "ADD", toAdd, shared, &requestOptions{
			Prefer:          prefer,
			WebhooksEnabled: webhooksEnabled,
			WebhookURL:      webhookURL,
			WebhookEvents:   webhookEvents,
		})
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to allocate tenant servers: %s", err))
			return
		}
		if strings.EqualFold(preferHeaderValue(prefer), "respond-async") && !webhooksEnabled && strings.TrimSpace(opID) != "" {
			if err := r.client.WaitForOperationDone(ctx, opID, 60*time.Minute); err != nil {
				resp.Diagnostics.AddError("Async operation failed", err.Error())
				return
			}
		}
		didAllocMutation = true
		mutationOpID = opID
	}

	// If async+webhook, do not block on backend updates; keep desired plan servers.
	preferFinal := "respond-sync"
	if !plan.Prefer.IsNull() && strings.TrimSpace(plan.Prefer.ValueString()) != "" {
		preferFinal = plan.Prefer.ValueString()
	}
	webhooksEnabledFinal := false
	if !plan.WebhooksEnabled.IsNull() && !plan.WebhooksEnabled.IsUnknown() {
		webhooksEnabledFinal = plan.WebhooksEnabled.ValueBool()
	}
	if operation == "DELETE" {
		// For DELETE, keep the deletion request list in state to match the plan (avoids "element vanished").
		serverList, diags := types.ListValueFrom(ctx, types.StringType, desiredServers)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		plan.Servers = serverList
	} else if !(strings.EqualFold(preferHeaderValue(preferFinal), "respond-async") && webhooksEnabledFinal) {
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
	} else {
		// Async+webhook: do not block.
		// Keep the requested list in state until the async operation completes.
		// Note: when operation==DELETE, desiredServers is the deletion request list (imperative),
		// not the post-delete allocation set.
		serverList, diags := types.ListValueFrom(ctx, types.StringType, desiredServers)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		plan.Servers = serverList
	}
	plan.ID = types.StringValue(stableTenantServersID(fabricName, tenantName))

	webhookURLSt := "http://localhost:8787/test/webhook-receiver"
	if !plan.WebhookURL.IsNull() && strings.TrimSpace(plan.WebhookURL.ValueString()) != "" {
		webhookURLSt = plan.WebhookURL.ValueString()
	}
	var evFinal []string
	if !plan.WebhookEvents.IsNull() && !plan.WebhookEvents.IsUnknown() {
		resp.Diagnostics.Append(plan.WebhookEvents.ElementsAs(ctx, &evFinal, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	plan.Prefer = types.StringValue(preferFinal)
	plan.WebhooksEnabled = types.BoolValue(webhooksEnabledFinal)
	plan.WebhookURL = types.StringValue(webhookURLSt)
	evList, diags := types.ListValueFrom(ctx, types.StringType, evFinal)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	plan.WebhookEvents = evList

	if didAllocMutation {
		if strings.TrimSpace(mutationOpID) != "" {
			plan.OperationID = types.StringValue(strings.TrimSpace(mutationOpID))
		} else {
			plan.OperationID = types.StringNull()
		}
	} else if plan.OperationID.IsUnknown() {
		if !state.OperationID.IsUnknown() {
			plan.OperationID = state.OperationID
		} else {
			plan.OperationID = types.StringNull()
		}
	}

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
		resp.State.RemoveResource(ctx)
		return
	}

	toFree := normalizedServersFromTenant(tenantInfo)
	if len(toFree) == 0 {
		resp.State.RemoveResource(ctx)
		return
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
	}

	opID, err := r.client.UpdateTenantServersWithFabricWithOptions(ctx, fabricName, tenantName, "DELETE", toFree, nil, &requestOptions{
		Prefer:          prefer,
		WebhooksEnabled: webhooksEnabled,
		WebhookURL:      webhookURL,
		WebhookEvents:   webhookEvents,
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to deallocate tenant servers: %s", err))
		return
	}
	if strings.EqualFold(preferHeaderValue(prefer), "respond-async") && !webhooksEnabled && strings.TrimSpace(opID) != "" {
		if err := r.client.WaitForOperationDone(ctx, opID, 60*time.Minute); err != nil {
			resp.Diagnostics.AddError("Async operation failed", err.Error())
			return
		}
	}

	// Deallocation completed; clear resource from Terraform state so destroy completes cleanly.
	resp.State.RemoveResource(ctx)
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

