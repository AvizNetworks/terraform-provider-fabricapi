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

// FabricResource manages a fabric via the ONES UI config service:
// POST /api/config/addFabricData to create (the same call the UI makes to
// generate/create a fabric) and DELETE /api/config/deletefabricbyname/{name}
// to delete — always called on destroy, unconditionally, matching how
// TenantResource's Delete works. There is no known GET endpoint for this
// object, so Read is a best-effort no-op (see VpcPeeringResource).
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
	HostsPerSu        types.String `tfsdk:"hosts_per_su"`
	TenantCtrl        types.String `tfsdk:"tenant_ctrl"`
	Instance          types.String `tfsdk:"instance"`

	// North-South (front-end user/storage) networking — see FabricDataRequest
	// in client.go for how these map onto the addFabricData API fields.
	EnableNS              types.Bool   `tfsdk:"enable_ns"`
	DedicatedStorage      types.Bool   `tfsdk:"dedicated_storage"`
	StartingSubnetCpu     types.String `tfsdk:"starting_subnet_cpu"`
	StartingSubnetStorage types.String `tfsdk:"starting_subnet_storage"`
	StartingSubnetTenants types.String `tfsdk:"starting_subnet_tenants"`

	ID types.String `tfsdk:"id"`
}

func (r *FabricResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_fabric"
}

func (r *FabricResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Creates and deletes a fabric via the ONES UI config service: POST /api/config/addFabricData and DELETE /api/config/deletefabricbyname/{name} — the same calls the ONES UI makes. Requires provider attribute `config_endpoint` (or env `FABRIC_API_CONFIG_ENDPOINT`) pointing at the UI/config backend, which is a different host from `endpoint`. There is no known API to read back a fabric, so Read is a best-effort no-op. `terraform destroy` always calls the delete API.",
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
				MarkdownDescription: "SU index -> host count, matching the addFabricData `hostMap` field (e.g. {\"0\" = \"1\"}). Optional: if unset, it is derived automatically from `hosts_per_su` (e.g. \"{0:1}\" -> {\"0\" = \"1\"}), since the API requires both fields to carry the same data.",
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
			},
			"starting_subnet_gpu": schema.StringAttribute{
				Required: true,
			},
			"simulation_id": schema.Int64Attribute{
				MarkdownDescription: "Raw `simulationId` value expected by the API. Defaults to 1 if unset.",
				Optional:            true,
				Computed:            true,
			},
			"enable_ew": schema.BoolAttribute{
				MarkdownDescription: "Enable east-west networking. Defaults to false if unset.",
				Optional:            true,
				Computed:            true,
			},
			"hosts_per_su": schema.StringAttribute{
				MarkdownDescription: "Raw `suHostCnt` value expected by the API, e.g. \"{0:1}\". Sent exactly as provided, and also used to derive `host_map` when that attribute is left unset.",
				Required:            true,
			},
			"tenant_ctrl": schema.StringAttribute{
				Required: true,
			},
			"instance": schema.StringAttribute{
				MarkdownDescription: "Target instance, e.g. \"fm\". Defaults to \"fm\" if unset.",
				Optional:            true,
				Computed:            true,
			},
			"enable_ns": schema.BoolAttribute{
				MarkdownDescription: "Enable north-south (front-end user/storage) networking, matching the ONES UI's \"N-S (Front-End) Network\" section. When true, `starting_subnet_cpu` is required. Defaults to false if unset.",
				Optional:            true,
				Computed:            true,
			},
			"dedicated_storage": schema.BoolAttribute{
				MarkdownDescription: "Use a separate subnet for storage NICs instead of sharing the user/storage subnet (the ONES UI's \"Dedicated Storage Network\" switch). Only meaningful when `enable_ns` is true. Requires `starting_subnet_storage`. Defaults to false if unset.",
				Optional:            true,
				Computed:            true,
			},
			"starting_subnet_cpu": schema.StringAttribute{
				MarkdownDescription: "Starting subnet for CPU NICs, e.g. \"10.2\". Required when `enable_ns` is true — used as the shared user/storage subnet when `dedicated_storage` is false, or as the dedicated CPU subnet when it's true.",
				Optional:            true,
			},
			"starting_subnet_storage": schema.StringAttribute{
				MarkdownDescription: "Starting subnet for storage NICs, e.g. \"10.3\". Required when `dedicated_storage` is true.",
				Optional:            true,
			},
			"starting_subnet_tenants": schema.StringAttribute{
				MarkdownDescription: "Starting subnet for the tenant (user/storage) IP pool, e.g. \"10.4\" — sent to the API with a trailing \".0\" octet appended (matching the ONES UI), so \"10.4\" becomes \"10.4.0\". Only meaningful when `enable_ns` is true.",
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
	simulationID := int64(1)
	if !data.SimulationID.IsNull() && !data.SimulationID.IsUnknown() {
		simulationID = data.SimulationID.ValueInt64()
	}
	enableNS := false
	if !data.EnableNS.IsNull() && !data.EnableNS.IsUnknown() {
		enableNS = data.EnableNS.ValueBool()
	}
	dedicatedStorage := false
	if !data.DedicatedStorage.IsNull() && !data.DedicatedStorage.IsUnknown() {
		dedicatedStorage = data.DedicatedStorage.ValueBool()
	}
	// The ONES UI's "Dedicated Storage Network" switch (FabricNetwork.jsx) always
	// toggles dedicatedStorage and frontendStorage together — they are never
	// independent — so frontendStorage is derived here rather than exposed as its
	// own attribute. userandstorage covers the "shared" case (NS on, dedicated
	// storage off): TopologyController.generateTopologyAndYaml forces
	// userandstorage=false whenever frontendStorage is true, and
	// StorageFrontendStrategy only builds the storage-leaf nodes (leaf-cn /
	// leaf-storage) from the userandstorage branch — so setting frontendStorage
	// unconditionally whenever enable_ns is true (an earlier version of this
	// resource did this) silently suppressed those leafs.
	frontendStorage := dedicatedStorage
	userAndStorage := enableNS && !dedicatedStorage
	startingSubnetGpu := data.StartingSubnetGpu.ValueString()
	startingSubnetCpu := data.StartingSubnetCpu.ValueString()
	startingSubnetStorage := data.StartingSubnetStorage.ValueString()
	// Error text below matches the ONES UI's own validation messages
	// (selfcare/src/ui-v2/pages/inventory/FabricNetwork.jsx handleSubmit)
	// where an equivalent check exists, so the same failure reads the same
	// way whether it's hit from the UI or from this provider.
	if enableEW && strings.TrimSpace(startingSubnetGpu) == "" {
		resp.Diagnostics.AddError(
			"Please enter a valid GPU Subnet",
			"starting_subnet_gpu is required when enable_ew is true.",
		)
		return
	}
	if enableNS && strings.TrimSpace(startingSubnetCpu) == "" {
		resp.Diagnostics.AddError(
			"Please enter a valid CPU Subnet",
			"starting_subnet_cpu is required when enable_ns is true (used as the shared user/storage subnet, or the CPU subnet when dedicated_storage is also true).",
		)
		return
	}
	if dedicatedStorage && strings.TrimSpace(startingSubnetStorage) == "" {
		resp.Diagnostics.AddError(
			"Please enter a valid Storage Subnet",
			"starting_subnet_storage is required when dedicated_storage is true.",
		)
		return
	}
	// Matches the ONES UI (FabricNetwork.jsx), which appends a trailing ".0"
	// octet to the tenant subnet before sending it to addFabricData.
	startingSubnetTenants := data.StartingSubnetTenants.ValueString()
	if strings.TrimSpace(startingSubnetTenants) != "" {
		startingSubnetTenants = startingSubnetTenants + ".0"
	}

	hostMap := map[string]string{}
	if !data.HostMap.IsNull() && !data.HostMap.IsUnknown() {
		resp.Diagnostics.Append(data.HostMap.ElementsAs(ctx, &hostMap, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	if len(hostMap) == 0 {
		derived, err := hostMapFromHostsPerSu(data.HostsPerSu.ValueString())
		if err != nil {
			resp.Diagnostics.AddError(
				"Invalid hosts_per_su",
				fmt.Sprintf("Unable to derive host_map from hosts_per_su %q: %s", data.HostsPerSu.ValueString(), err),
			)
			return
		}
		hostMap = derived
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
		SimulationID:      int(simulationID),
		EnableEW:          enableEW,
		SuHostCnt:         data.HostsPerSu.ValueString(),
		Tenant:            data.TenantCtrl.ValueString(),
		Instance:          instance,

		EnableNS:              enableNS,
		IsOnesControlled:      true,
		UserAndStorage:        userAndStorage,
		DedicatedStorage:      dedicatedStorage,
		FrontendStorage:       frontendStorage,
		StartingSubnetCpu:     startingSubnetCpu,
		StartingSubnetStorage: startingSubnetStorage,
		StartingSubnetTenants: startingSubnetTenants,
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
	data.SimulationID = types.Int64Value(simulationID)
	data.EnableNS = types.BoolValue(enableNS)
	data.DedicatedStorage = types.BoolValue(dedicatedStorage)
	data.ID = types.StringValue(data.Name.ValueString())

	hostMapValue, diags := types.MapValueFrom(ctx, types.StringType, hostMap)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.HostMap = hostMapValue

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// hostMapFromHostsPerSu parses the API's "{0:1,1:2}" suHostCnt shorthand
// (Terraform-facing as hosts_per_su) into the hostMap field shape the same
// addFabricData call also expects (both fields carry the same SU-index ->
// host-count data, in two formats).
func hostMapFromHostsPerSu(raw string) (map[string]string, error) {
	trimmed := strings.TrimSpace(raw)
	trimmed = strings.TrimPrefix(trimmed, "{")
	trimmed = strings.TrimSuffix(trimmed, "}")
	trimmed = strings.TrimSpace(trimmed)

	hostMap := map[string]string{}
	if trimmed == "" {
		return hostMap, nil
	}

	for _, pair := range strings.Split(trimmed, ",") {
		kv := strings.SplitN(pair, ":", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("expected \"su:count\" pairs like \"{0:1,1:2}\", got %q", pair)
		}
		key := strings.TrimSpace(kv[0])
		value := strings.TrimSpace(kv[1])
		if key == "" || value == "" {
			return nil, fmt.Errorf("expected \"su:count\" pairs like \"{0:1,1:2}\", got %q", pair)
		}
		hostMap[key] = value
	}

	return hostMap, nil
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

	respBody, err := r.client.DeleteFabricData(ctx, data.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete fabric: %s", err))
		return
	}
	if respBody != "" {
		resp.Diagnostics.AddWarning("Fabric deleted", respBody)
	}

	resp.State.RemoveResource(ctx)
}
