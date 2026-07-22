package provider

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &GpuAllocationsResource{}

func NewGpuAllocationsResource() resource.Resource {
	return &GpuAllocationsResource{}
}

type GpuAllocationsResource struct {
	client *APIClient
}

type GpuAllocationsResourceModel struct {
	TenantName      types.String `tfsdk:"tenant_name"`
	FabricName      types.String `tfsdk:"fabric_name"`
	Operation       types.String `tfsdk:"operation"`
	Allocations     types.List   `tfsdk:"allocations"`
	Prefer          types.String `tfsdk:"prefer"`
	WebhooksEnabled types.Bool   `tfsdk:"webhooks_enabled"`
	WebhookURL      types.String `tfsdk:"webhook_url"`
	WebhookEvents   types.List   `tfsdk:"webhook_events"`
	OperationID     types.String `tfsdk:"operation_id"`
	ID              types.String `tfsdk:"id"`
}

type gpuAllocationEntry struct {
	Suid   int64
	Server string
	Gpus   []string
}

func (r *GpuAllocationsResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gpu_allocations"
}

func (r *GpuAllocationsResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fabric API per-GPU allocations resource. Maps or unmaps logical GPUs " +
			"(e.g. G0–G7) on servers already attached to a tenant via POST " +
			"`/fabrics/{fabric}/tenants/{tenant}/gpuAllocations`.",

		Attributes: map[string]schema.Attribute{
			"tenant_name": schema.StringAttribute{
				MarkdownDescription: "Name of the tenant",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"fabric_name": schema.StringAttribute{
				MarkdownDescription: "Fabric name for the backend URL. If unset, uses provider-level fabric.",
				Optional:            true,
			},
			"operation": schema.StringAttribute{
				MarkdownDescription: "ADD or DELETE (REMOVE aliases DELETE). Sent as `operation` in the request body.",
				Required:            true,
			},
			"allocations": schema.ListNestedAttribute{
				MarkdownDescription: "Per-server GPU lists. Flattened to the API `suid` map: " +
					"`suid -> hostname -> { gpus: [...] }`. GPU count is fabric-defined (often 8: G0–G7).",
				Required: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"suid": schema.Int64Attribute{
							MarkdownDescription: "Scalable Unit (SU) id used as the outer key in `suid` (e.g. 0).",
							Required:            true,
						},
						"server": schema.StringAttribute{
							MarkdownDescription: "Server hostname already attached to the tenant (e.g. su00-node00).",
							Required:            true,
						},
						"gpus": schema.ListAttribute{
							MarkdownDescription: "Logical GPU ids on the server (e.g. [\"G6\", \"G7\"] or full G0–G7).",
							Required:            true,
							ElementType:         types.StringType,
						},
					},
				},
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
				MarkdownDescription: "Webhook receiver URL (when prefer is respond-async and webhooks_enabled=true).",
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

func (r *GpuAllocationsResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *GpuAllocationsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data GpuAllocationsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fabricName := resolveFabricName(r.client.Fabric, data.FabricName)
	tenantName := data.TenantName.ValueString()
	operation, err := normalizeGpuAllocOperation(data.Operation.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid Operation", err.Error())
		return
	}

	entries, err := parseGpuAllocationEntries(ctx, data.Allocations)
	if err != nil {
		resp.Diagnostics.AddError("Invalid allocations", err.Error())
		return
	}
	if len(entries) == 0 {
		resp.Diagnostics.AddError("Invalid allocations", "allocations must contain at least one server GPU list")
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
			fmt.Sprintf("Tenant %q does not exist in fabric %q. Create the tenant and attach servers first.", tenantName, fabricName),
		)
		return
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

	opts, err := buildRequestOptionsFromModel(ctx, data.Prefer, data.WebhooksEnabled, data.WebhookURL, data.WebhookEvents)
	if err != nil {
		resp.Diagnostics.AddError("Invalid request options", err.Error())
		return
	}
	if strings.EqualFold(preferHeaderValue(opts.Prefer), "respond-async") && !asyncEnabled() {
		resp.Diagnostics.AddError(
			"Async not supported",
			"Async operations are currently disabled for this release. Use prefer=respond-sync (default).",
		)
		return
	}

	opID, err := r.client.CreateGpuAllocationsWithOptions(ctx, fabricName, tenantName, GpuAllocationsRequest{
		Operation: operation,
		Suid:      buildSuidMap(entries),
	}, opts)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to apply GPU allocations: %s", err))
		return
	}

	if strings.EqualFold(preferHeaderValue(opts.Prefer), "respond-async") && !opts.WebhooksEnabled && strings.TrimSpace(opID) != "" {
		if err := r.client.WaitForOperationDone(ctx, opID, 60*time.Minute); err != nil {
			resp.Diagnostics.AddError("Async operation failed", err.Error())
			return
		}
	}

	if err := setGpuAllocationsState(ctx, &data, fabricName, tenantName, operation, entries, opts, opID); err != nil {
		resp.Diagnostics.AddError("State Error", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *GpuAllocationsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data GpuAllocationsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fabricName := resolveFabricName(r.client.Fabric, data.FabricName)
	tenantName := data.TenantName.ValueString()

	tenantInfo, err := r.client.GetTenantWithFabric(fabricName, tenantName)
	if err != nil {
		resp.Diagnostics.AddWarning(
			"GPU allocations read skipped",
			fmt.Sprintf("Could not verify tenant %q in fabric %q: %s. Terraform state left unchanged.", tenantName, fabricName, err),
		)
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}
	if tenantInfo == nil {
		resp.Diagnostics.AddWarning(
			"Tenant not found; removing from state",
			fmt.Sprintf(
				"Tenant %q does not exist in fabric %q. Removing fabricapi_gpu_allocations from Terraform state.",
				tenantName,
				fabricName,
			),
		)
		resp.State.RemoveResource(ctx)
		return
	}

	data.ID = types.StringValue(stableGpuAllocationsID(fabricName, tenantName))
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *GpuAllocationsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan GpuAllocationsResourceModel
	var state GpuAllocationsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fabricName := resolveFabricName(r.client.Fabric, plan.FabricName)
	tenantName := plan.TenantName.ValueString()
	operation, err := normalizeGpuAllocOperation(plan.Operation.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Invalid Operation", err.Error())
		return
	}

	desired, err := parseGpuAllocationEntries(ctx, plan.Allocations)
	if err != nil {
		resp.Diagnostics.AddError("Invalid allocations", err.Error())
		return
	}
	if len(desired) == 0 {
		resp.Diagnostics.AddError("Invalid allocations", "allocations must contain at least one server GPU list")
		return
	}

	current, err := parseGpuAllocationEntries(ctx, state.Allocations)
	if err != nil {
		resp.Diagnostics.AddError("Invalid state allocations", err.Error())
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
			fmt.Sprintf("Tenant %q does not exist in fabric %q.", tenantName, fabricName),
		)
		return
	}

	opts, err := buildRequestOptionsFromModel(ctx, plan.Prefer, plan.WebhooksEnabled, plan.WebhookURL, plan.WebhookEvents)
	if err != nil {
		resp.Diagnostics.AddError("Invalid request options", err.Error())
		return
	}
	if strings.EqualFold(preferHeaderValue(opts.Prefer), "respond-async") && !asyncEnabled() {
		resp.Diagnostics.AddError(
			"Async not supported",
			"Async operations are currently disabled for this release. Use prefer=respond-sync (default).",
		)
		return
	}

	var mutationOpID string

	// If previous apply was ADD and the desired set shrinks/changes, release removed GPUs first.
	if strings.EqualFold(state.Operation.ValueString(), "ADD") || strings.EqualFold(state.Operation.ValueString(), "") {
		toDelete := diffGpuAllocationsToDelete(current, desired)
		if len(toDelete) > 0 {
			opID, err := r.client.CreateGpuAllocationsWithOptions(ctx, fabricName, tenantName, GpuAllocationsRequest{
				Operation: "DELETE",
				Suid:      buildSuidMap(toDelete),
			}, opts)
			if err != nil {
				resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to deallocate GPUs during update: %s", err))
				return
			}
			if strings.EqualFold(preferHeaderValue(opts.Prefer), "respond-async") && !opts.WebhooksEnabled && strings.TrimSpace(opID) != "" {
				if err := r.client.WaitForOperationDone(ctx, opID, 60*time.Minute); err != nil {
					resp.Diagnostics.AddError("Async operation failed", err.Error())
					return
				}
			}
			mutationOpID = opID
		}
	}

	if operation == "ADD" {
		toAdd := diffGpuAllocationsToAdd(current, desired)
		if len(toAdd) == 0 {
			// Operation-only or identical set: still POST desired when operation is ADD so
			// re-apply is idempotent toward the planned mapping.
			toAdd = desired
			if gpuAllocationsEqual(current, desired) && strings.EqualFold(state.Operation.ValueString(), "ADD") {
				toAdd = nil
			}
		}
		if len(toAdd) > 0 {
			if err := r.client.WaitForTenantReady(ctx, fabricName, tenantName, 60*time.Second); err != nil {
				resp.Diagnostics.AddError(
					"Tenant not ready",
					fmt.Sprintf("Cannot allocate GPUs until tenant %s is readable: %s", tenantName, err),
				)
				return
			}
			opID, err := r.client.CreateGpuAllocationsWithOptions(ctx, fabricName, tenantName, GpuAllocationsRequest{
				Operation: "ADD",
				Suid:      buildSuidMap(toAdd),
			}, opts)
			if err != nil {
				resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to allocate GPUs during update: %s", err))
				return
			}
			if strings.EqualFold(preferHeaderValue(opts.Prefer), "respond-async") && !opts.WebhooksEnabled && strings.TrimSpace(opID) != "" {
				if err := r.client.WaitForOperationDone(ctx, opID, 60*time.Minute); err != nil {
					resp.Diagnostics.AddError("Async operation failed", err.Error())
					return
				}
			}
			mutationOpID = opID
		}
	} else if operation == "DELETE" {
		opID, err := r.client.CreateGpuAllocationsWithOptions(ctx, fabricName, tenantName, GpuAllocationsRequest{
			Operation: "DELETE",
			Suid:      buildSuidMap(desired),
		}, opts)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to deallocate GPUs during update: %s", err))
			return
		}
		if strings.EqualFold(preferHeaderValue(opts.Prefer), "respond-async") && !opts.WebhooksEnabled && strings.TrimSpace(opID) != "" {
			if err := r.client.WaitForOperationDone(ctx, opID, 60*time.Minute); err != nil {
				resp.Diagnostics.AddError("Async operation failed", err.Error())
				return
			}
		}
		mutationOpID = opID
	}

	if err := setGpuAllocationsState(ctx, &plan, fabricName, tenantName, operation, desired, opts, mutationOpID); err != nil {
		resp.Diagnostics.AddError("State Error", err.Error())
		return
	}
	if mutationOpID == "" && !state.OperationID.IsNull() && !state.OperationID.IsUnknown() {
		plan.OperationID = state.OperationID
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *GpuAllocationsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data GpuAllocationsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fabricName := resolveFabricName(r.client.Fabric, data.FabricName)
	tenantName := data.TenantName.ValueString()

	// If create was already a DELETE, nothing to clean up.
	if op, _ := normalizeGpuAllocOperation(data.Operation.ValueString()); op == "DELETE" {
		resp.State.RemoveResource(ctx)
		return
	}

	entries, err := parseGpuAllocationEntries(ctx, data.Allocations)
	if err != nil || len(entries) == 0 {
		resp.State.RemoveResource(ctx)
		return
	}

	tenantInfo, err := r.client.GetTenantWithFabric(fabricName, tenantName)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read tenant: %s", err))
		return
	}
	if tenantInfo == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	opts, err := buildRequestOptionsFromModel(ctx, data.Prefer, data.WebhooksEnabled, data.WebhookURL, data.WebhookEvents)
	if err != nil {
		resp.Diagnostics.AddError("Invalid request options", err.Error())
		return
	}
	if strings.EqualFold(preferHeaderValue(opts.Prefer), "respond-async") && !asyncEnabled() {
		resp.Diagnostics.AddError(
			"Async not supported",
			"Async operations are currently disabled for this release. Use prefer=respond-sync (default).",
		)
		return
	}

	opID, err := r.client.CreateGpuAllocationsWithOptions(ctx, fabricName, tenantName, GpuAllocationsRequest{
		Operation: "DELETE",
		Suid:      buildSuidMap(entries),
	}, opts)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to deallocate GPUs: %s", err))
		return
	}
	if strings.EqualFold(preferHeaderValue(opts.Prefer), "respond-async") && !opts.WebhooksEnabled && strings.TrimSpace(opID) != "" {
		if err := r.client.WaitForOperationDone(ctx, opID, 60*time.Minute); err != nil {
			resp.Diagnostics.AddError("Async operation failed", err.Error())
			return
		}
	}

	resp.State.RemoveResource(ctx)
}

func normalizeGpuAllocOperation(raw string) (string, error) {
	op := strings.ToUpper(strings.TrimSpace(raw))
	switch op {
	case "ADD":
		return "ADD", nil
	case "DELETE", "REMOVE":
		return "DELETE", nil
	default:
		return "", fmt.Errorf("operation must be 'ADD', 'DELETE', or 'REMOVE', got: %s", raw)
	}
}

func parseGpuAllocationEntries(ctx context.Context, list types.List) ([]gpuAllocationEntry, error) {
	if list.IsNull() || list.IsUnknown() {
		return nil, fmt.Errorf("allocations is required")
	}

	var objs []struct {
		Suid   types.Int64  `tfsdk:"suid"`
		Server types.String `tfsdk:"server"`
		Gpus   types.List   `tfsdk:"gpus"`
	}
	diags := list.ElementsAs(ctx, &objs, false)
	if diags.HasError() {
		return nil, fmt.Errorf("%s", diags.Errors()[0].Detail())
	}

	out := make([]gpuAllocationEntry, 0, len(objs))
	for i, o := range objs {
		server := strings.TrimSpace(o.Server.ValueString())
		if server == "" {
			return nil, fmt.Errorf("allocations[%d].server must be non-empty", i)
		}
		var gpus []string
		diags := o.Gpus.ElementsAs(ctx, &gpus, false)
		if diags.HasError() {
			return nil, fmt.Errorf("allocations[%d].gpus: %s", i, diags.Errors()[0].Detail())
		}
		gpus = normalizeGpuList(gpus)
		if len(gpus) == 0 {
			return nil, fmt.Errorf("allocations[%d].gpus must contain at least one GPU id", i)
		}
		out = append(out, gpuAllocationEntry{
			Suid:   o.Suid.ValueInt64(),
			Server: server,
			Gpus:   gpus,
		})
	}
	return mergeGpuAllocationEntries(out), nil
}

func normalizeGpuList(in []string) []string {
	set := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, g := range in {
		g = strings.TrimSpace(g)
		if g == "" {
			continue
		}
		key := strings.ToUpper(g)
		if _, exists := set[key]; exists {
			continue
		}
		set[key] = struct{}{}
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func mergeGpuAllocationEntries(in []gpuAllocationEntry) []gpuAllocationEntry {
	type key struct {
		suid   int64
		server string
	}
	merged := make(map[key]map[string]struct{})
	order := make([]key, 0)
	for _, e := range in {
		k := key{suid: e.Suid, server: e.Server}
		if _, ok := merged[k]; !ok {
			merged[k] = make(map[string]struct{})
			order = append(order, k)
		}
		for _, g := range e.Gpus {
			merged[k][g] = struct{}{}
		}
	}
	out := make([]gpuAllocationEntry, 0, len(order))
	for _, k := range order {
		gpus := make([]string, 0, len(merged[k]))
		for g := range merged[k] {
			gpus = append(gpus, g)
		}
		sort.Strings(gpus)
		out = append(out, gpuAllocationEntry{Suid: k.suid, Server: k.server, Gpus: gpus})
	}
	return out
}

func buildSuidMap(entries []gpuAllocationEntry) map[string]map[string]DeviceGpus {
	suid := make(map[string]map[string]DeviceGpus)
	for _, e := range entries {
		suKey := strconv.FormatInt(e.Suid, 10)
		if _, ok := suid[suKey]; !ok {
			suid[suKey] = make(map[string]DeviceGpus)
		}
		// If the same server appears twice after merge it won't; still replace safely.
		existing := suid[suKey][e.Server]
		gpus := append([]string{}, existing.Gpus...)
		gpus = append(gpus, e.Gpus...)
		suid[suKey][e.Server] = DeviceGpus{Gpus: normalizeGpuList(gpus)}
	}
	return suid
}

func gpuAllocKey(e gpuAllocationEntry, gpu string) string {
	return fmt.Sprintf("%d|%s|%s", e.Suid, e.Server, gpu)
}

func expandGpuKeys(entries []gpuAllocationEntry) map[string]gpuAllocationEntry {
	out := make(map[string]gpuAllocationEntry)
	for _, e := range entries {
		for _, g := range e.Gpus {
			out[gpuAllocKey(e, g)] = gpuAllocationEntry{Suid: e.Suid, Server: e.Server, Gpus: []string{g}}
		}
	}
	return out
}

func collapseGpuKeys(m map[string]gpuAllocationEntry) []gpuAllocationEntry {
	tmp := make([]gpuAllocationEntry, 0, len(m))
	for _, e := range m {
		tmp = append(tmp, e)
	}
	return mergeGpuAllocationEntries(tmp)
}

func diffGpuAllocationsToDelete(current, desired []gpuAllocationEntry) []gpuAllocationEntry {
	cur := expandGpuKeys(current)
	des := expandGpuKeys(desired)
	del := make(map[string]gpuAllocationEntry)
	for k, e := range cur {
		if _, ok := des[k]; !ok {
			del[k] = e
		}
	}
	return collapseGpuKeys(del)
}

func diffGpuAllocationsToAdd(current, desired []gpuAllocationEntry) []gpuAllocationEntry {
	cur := expandGpuKeys(current)
	des := expandGpuKeys(desired)
	add := make(map[string]gpuAllocationEntry)
	for k, e := range des {
		if _, ok := cur[k]; !ok {
			add[k] = e
		}
	}
	return collapseGpuKeys(add)
}

func gpuAllocationsEqual(a, b []gpuAllocationEntry) bool {
	return len(diffGpuAllocationsToAdd(a, b)) == 0 && len(diffGpuAllocationsToDelete(a, b)) == 0
}

func allocationsListValue(ctx context.Context, entries []gpuAllocationEntry) (types.List, error) {
	objType := types.ObjectType{
		AttrTypes: map[string]attr.Type{
			"suid":   types.Int64Type,
			"server": types.StringType,
			"gpus":   types.ListType{ElemType: types.StringType},
		},
	}
	objs := make([]attr.Value, 0, len(entries))
	for _, e := range entries {
		gpus, diags := types.ListValueFrom(ctx, types.StringType, e.Gpus)
		if diags.HasError() {
			return types.ListNull(objType), fmt.Errorf("%s", diags.Errors()[0].Detail())
		}
		obj, diags := types.ObjectValue(objType.AttrTypes, map[string]attr.Value{
			"suid":   types.Int64Value(e.Suid),
			"server": types.StringValue(e.Server),
			"gpus":   gpus,
		})
		if diags.HasError() {
			return types.ListNull(objType), fmt.Errorf("%s", diags.Errors()[0].Detail())
		}
		objs = append(objs, obj)
	}
	list, diags := types.ListValue(objType, objs)
	if diags.HasError() {
		return types.ListNull(objType), fmt.Errorf("%s", diags.Errors()[0].Detail())
	}
	return list, nil
}

func buildRequestOptionsFromModel(
	ctx context.Context,
	preferAttr types.String,
	webhooksEnabledAttr types.Bool,
	webhookURLAttr types.String,
	webhookEventsAttr types.List,
) (*requestOptions, error) {
	prefer := "respond-sync"
	if !preferAttr.IsNull() && strings.TrimSpace(preferAttr.ValueString()) != "" {
		prefer = preferAttr.ValueString()
	}
	webhooksEnabled := false
	if !webhooksEnabledAttr.IsNull() && !webhooksEnabledAttr.IsUnknown() {
		webhooksEnabled = webhooksEnabledAttr.ValueBool()
	}
	webhookURL := "http://localhost:8787/test/webhook-receiver"
	if !webhookURLAttr.IsNull() && strings.TrimSpace(webhookURLAttr.ValueString()) != "" {
		webhookURL = webhookURLAttr.ValueString()
	}
	var webhookEvents []string
	if !webhookEventsAttr.IsNull() && !webhookEventsAttr.IsUnknown() {
		diags := webhookEventsAttr.ElementsAs(ctx, &webhookEvents, false)
		if diags.HasError() {
			return nil, fmt.Errorf("%s", diags.Errors()[0].Detail())
		}
	}
	if strings.EqualFold(preferHeaderValue(prefer), "respond-async") && webhooksEnabled {
		if strings.TrimSpace(webhookURL) == "" || len(webhookEvents) == 0 {
			return nil, fmt.Errorf("when prefer is respond-async and webhooks_enabled is true, both webhook_url and webhook_events must be provided")
		}
	}
	return &requestOptions{
		Prefer:          prefer,
		WebhooksEnabled: webhooksEnabled,
		WebhookURL:      webhookURL,
		WebhookEvents:   webhookEvents,
	}, nil
}

func setGpuAllocationsState(
	ctx context.Context,
	data *GpuAllocationsResourceModel,
	fabricName, tenantName, operation string,
	entries []gpuAllocationEntry,
	opts *requestOptions,
	opID string,
) error {
	list, err := allocationsListValue(ctx, entries)
	if err != nil {
		return err
	}
	data.Allocations = list
	data.Operation = types.StringValue(operation)
	data.ID = types.StringValue(stableGpuAllocationsID(fabricName, tenantName))
	if strings.TrimSpace(opID) == "" {
		data.OperationID = types.StringNull()
	} else {
		data.OperationID = types.StringValue(opID)
	}
	if opts != nil {
		data.Prefer = types.StringValue(opts.Prefer)
		data.WebhooksEnabled = types.BoolValue(opts.WebhooksEnabled)
		data.WebhookURL = types.StringValue(opts.WebhookURL)
		evList, diags := types.ListValueFrom(ctx, types.StringType, opts.WebhookEvents)
		if diags.HasError() {
			return fmt.Errorf("%s", diags.Errors()[0].Detail())
		}
		data.WebhookEvents = evList
	}
	return nil
}

func stableGpuAllocationsID(fabricName, tenantName string) string {
	return fmt.Sprintf("%s:%s:gpuAllocations", fabricName, tenantName)
}
