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

var _ resource.Resource = &GPUAllocationsResource{}

func NewGPUAllocationsResource() resource.Resource {
	return &GPUAllocationsResource{}
}

// GPUAllocationsResource manages POST /fabrics/{fabric}/tenants/{tenant}/gpuAllocations.
type GPUAllocationsResource struct {
	client *APIClient
}

// GPUAllocationsResourceModel is the Terraform state model.
type GPUAllocationsResourceModel struct {
	TenantName types.String `tfsdk:"tenant_name"`
	FabricName types.String `tfsdk:"fabric_name"`
	Operation  types.String `tfsdk:"operation"`
	// servers is a list of objects: [{index, hostname, gpus}]
	Servers types.List   `tfsdk:"servers"`
	ID      types.String `tfsdk:"id"`
}

func (r *GPUAllocationsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_gpu_allocations"
}

func (r *GPUAllocationsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages partial GPU allocations for a tenant via `POST /fabrics/{fabric}/tenants/{tenant}/gpuAllocations`.",

		Attributes: map[string]schema.Attribute{
			"tenant_name": schema.StringAttribute{
				MarkdownDescription: "Name of the tenant.",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"fabric_name": schema.StringAttribute{
				MarkdownDescription: "Fabric name. If unset, uses the provider-level fabric.",
				Optional:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"operation": schema.StringAttribute{
				MarkdownDescription: "ADD or DELETE.",
				Required:            true,
			},
			"servers": schema.ListNestedAttribute{
				MarkdownDescription: "List of server GPU entries. Each entry specifies a server index, hostname, and GPU list.",
				Required:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"index": schema.StringAttribute{
							MarkdownDescription: "Server index key (e.g. \"0\", \"1\").",
							Required:            true,
						},
						"hostname": schema.StringAttribute{
							MarkdownDescription: "Compute node hostname (e.g. \"hgx-su00-h01\").",
							Required:            true,
						},
						"gpus": schema.ListAttribute{
							MarkdownDescription: "GPU identifiers on this node (e.g. [\"G0\",\"G1\"]).",
							Required:            true,
							ElementType:         types.StringType,
						},
					},
				},
			},
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Resource identifier (fabric:tenant).",
			},
		},
	}
}

func (r *GPUAllocationsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// gpuServerEntry is the in-memory representation of one list element.
type gpuServerEntry struct {
	Index    string
	Hostname string
	GPUs     []string
}

func readServerEntries(ctx context.Context, list types.List) ([]gpuServerEntry, error) {
	if list.IsNull() || list.IsUnknown() {
		return nil, nil
	}
	var raw []struct {
		Index    types.String `tfsdk:"index"`
		Hostname types.String `tfsdk:"hostname"`
		GPUs     types.List   `tfsdk:"gpus"`
	}
	diags := list.ElementsAs(ctx, &raw, false)
	if diags.HasError() {
		return nil, fmt.Errorf("failed to decode servers list")
	}
	entries := make([]gpuServerEntry, 0, len(raw))
	for _, r := range raw {
		var gpus []string
		d := r.GPUs.ElementsAs(ctx, &gpus, false)
		if d.HasError() {
			return nil, fmt.Errorf("failed to decode gpus for %s", r.Hostname.ValueString())
		}
		entries = append(entries, gpuServerEntry{
			Index:    r.Index.ValueString(),
			Hostname: r.Hostname.ValueString(),
			GPUs:     gpus,
		})
	}
	return entries, nil
}

func buildSuid(entries []gpuServerEntry) map[string]map[string]ServerGPUs {
	suid := make(map[string]map[string]ServerGPUs, len(entries))
	for _, e := range entries {
		if suid[e.Index] == nil {
			suid[e.Index] = make(map[string]ServerGPUs)
		}
		suid[e.Index][e.Hostname] = ServerGPUs{GPUs: e.GPUs}
	}
	return suid
}

func (r *GPUAllocationsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data GPUAllocationsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fabricName := resolveFabricName(r.client.Fabric, data.FabricName)
	tenantName := data.TenantName.ValueString()
	operation := strings.ToUpper(strings.TrimSpace(data.Operation.ValueString()))

	if operation != "ADD" && operation != "DELETE" {
		resp.Diagnostics.AddError("Invalid Operation", fmt.Sprintf("operation must be ADD or DELETE, got: %s", operation))
		return
	}

	entries, err := readServerEntries(ctx, data.Servers)
	if err != nil {
		resp.Diagnostics.AddError("Invalid servers", err.Error())
		return
	}
	if len(entries) == 0 {
		resp.Diagnostics.AddError("No servers", "At least one server entry is required.")
		return
	}

	_, apiErr := r.client.ModifyGPUAllocations(ctx, fabricName, tenantName, GPUAllocationRequest{
		Operation: GPUOperation(operation),
		Suid:      buildSuid(entries),
	})
	if apiErr != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to modify GPU allocations: %s", apiErr))
		return
	}

	data.Operation = types.StringValue(operation)
	data.ID = types.StringValue(fmt.Sprintf("%s:%s", fabricName, tenantName))
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *GPUAllocationsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// The gpuAllocations endpoint is write-only (POST); there is no GET.
	// We keep state as-is so Terraform does not show spurious diffs.
	var data GPUAllocationsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *GPUAllocationsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data GPUAllocationsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fabricName := resolveFabricName(r.client.Fabric, data.FabricName)
	tenantName := data.TenantName.ValueString()
	operation := strings.ToUpper(strings.TrimSpace(data.Operation.ValueString()))

	if operation != "ADD" && operation != "DELETE" {
		resp.Diagnostics.AddError("Invalid Operation", fmt.Sprintf("operation must be ADD or DELETE, got: %s", operation))
		return
	}

	entries, err := readServerEntries(ctx, data.Servers)
	if err != nil {
		resp.Diagnostics.AddError("Invalid servers", err.Error())
		return
	}
	if len(entries) == 0 {
		resp.Diagnostics.AddError("No servers", "At least one server entry is required.")
		return
	}

	_, apiErr := r.client.ModifyGPUAllocations(ctx, fabricName, tenantName, GPUAllocationRequest{
		Operation: GPUOperation(operation),
		Suid:      buildSuid(entries),
	})
	if apiErr != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to modify GPU allocations: %s", apiErr))
		return
	}

	data.Operation = types.StringValue(operation)
	data.ID = types.StringValue(fmt.Sprintf("%s:%s", fabricName, tenantName))
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *GPUAllocationsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data GPUAllocationsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fabricName := resolveFabricName(r.client.Fabric, data.FabricName)
	tenantName := data.TenantName.ValueString()

	entries, err := readServerEntries(ctx, data.Servers)
	if err != nil {
		resp.Diagnostics.AddError("Invalid servers", err.Error())
		return
	}
	if len(entries) == 0 {
		// Nothing to deallocate; just remove from state.
		resp.State.RemoveResource(ctx)
		return
	}

	_, apiErr := r.client.ModifyGPUAllocations(ctx, fabricName, tenantName, GPUAllocationRequest{
		Operation: GPUOperationDelete,
		Suid:      buildSuid(entries),
	})
	if apiErr != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to deallocate GPUs: %s", apiErr))
		return
	}

	resp.State.RemoveResource(ctx)
}
