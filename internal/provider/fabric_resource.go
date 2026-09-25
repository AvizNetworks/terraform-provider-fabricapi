package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
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

// FabricSuConfigModel groups the fabric's scale-unit/tenant configuration —
// the ONES UI's own SU-config step — under a single nested attribute rather
// than flat top-level attributes.
type FabricSuConfigModel struct {
	NodeType       types.String `tfsdk:"node_type"`
	NumberOfSUs    types.Int64  `tfsdk:"number_of_sus"`
	MaxNumberOfSUs types.Int64  `tfsdk:"max_number_of_sus"`
	HostsPerSU     types.String `tfsdk:"hosts_per_su"`
	HostMap        types.Map    `tfsdk:"host_map"`
	// TenantCtrl is the addFabricData `tenant` field (always effectively
	// "ones" in practice) — NOT the ONES UI's separate "Tenant control"
	// ONES/External radio (isOnesControlled), which this provider doesn't
	// expose since external-tenant mode isn't implemented. Kept as
	// `tenant_ctrl` rather than a "tenant_control"-style name specifically to
	// avoid that mix-up.
	TenantCtrl   types.String `tfsdk:"tenant_ctrl"`
	SimulationID types.Int64  `tfsdk:"simulation_id"`
}

// FabricNetworkConfigModel groups east-west and north-south networking
// configuration under a single nested attribute.
type FabricNetworkConfigModel struct {
	EnableEastWestNetworking   types.Bool   `tfsdk:"enable_east_west_networking"`
	StartingSubnetGpu          types.String `tfsdk:"starting_subnet_gpu"`
	EnableNorthSouthNetworking types.Bool   `tfsdk:"enable_north_south_networking"`
	DedicatedStorage           types.Bool   `tfsdk:"dedicated_storage"`
	StartingSubnetCpu          types.String `tfsdk:"starting_subnet_cpu"`
	StartingSubnetStorage      types.String `tfsdk:"starting_subnet_storage"`
}

type FabricResourceModel struct {
	Name          types.String             `tfsdk:"name"`
	Type          types.String             `tfsdk:"type"`
	Status        types.String             `tfsdk:"status"`
	Description   types.String             `tfsdk:"description"`
	Instance      types.String             `tfsdk:"instance"`
	SuConfig      FabricSuConfigModel      `tfsdk:"su_config"`
	NetworkConfig FabricNetworkConfigModel `tfsdk:"network_config"`

	ID types.String `tfsdk:"id"`
}

func (r *FabricResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_fabric"
}

func (r *FabricResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Creates and deletes a fabric via the ONES UI config service: POST /api/config/addFabricData and DELETE /api/config/deletefabricbyname/{name} — the same calls the ONES UI makes. Requires provider attribute `config_endpoint` (or env `FABRIC_API_CONFIG_ENDPOINT`) pointing at the UI/config backend, which is a different host from `endpoint`. There is no known API to read back a fabric, so Read is a best-effort no-op. `terraform destroy` always calls the delete API. Inputs are grouped into `su_config` (scale-unit/tenant) and `network_config` (east-west/north-south), mirroring the ONES UI's own fabric-creation steps.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"type": schema.StringAttribute{
				MarkdownDescription: "Fabric type. One of \"NVIDIA SpX RA 1.3\", \"NVIDIA SpX RA 2.1\", \"Aviz RA 1.0\" (matching the ONES UI's fabric type list).",
				Required:            true,
				Validators: []validator.String{
					stringvalidator.OneOf("NVIDIA SpX RA 1.3", "NVIDIA SpX RA 2.1", "Aviz RA 1.0"),
				},
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
			"instance": schema.StringAttribute{
				MarkdownDescription: "Target instance, e.g. \"fm\". Defaults to \"fm\" if unset.",
				Optional:            true,
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"su_config": schema.SingleNestedAttribute{
				MarkdownDescription: "Scale-unit and tenant configuration — mirrors the ONES UI's SU-config step.",
				Required:            true,
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.RequiresReplace(),
				},
				Attributes: map[string]schema.Attribute{
					"node_type": schema.StringAttribute{
						MarkdownDescription: "GPU node hardware, e.g. \"gb200\", \"gb300\", \"b300_32\", \"b300_64\", \"rtxpro_4\", \"rtxpro_8\". Leave unset for the default (\"dgx\") — dgx is not itself a value you need to set.",
						Optional:            true,
						Computed:            true,
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.UseStateForUnknown(),
						},
					},
					"number_of_sus": schema.Int64Attribute{
						Required: true,
					},
					"max_number_of_sus": schema.Int64Attribute{
						Required: true,
					},
					"hosts_per_su": schema.StringAttribute{
						MarkdownDescription: "Raw `suHostCnt` value expected by the API, e.g. \"{0:1}\". Sent exactly as provided, and also used to derive `host_map` when that attribute is left unset.",
						Required:            true,
					},
					"host_map": schema.MapAttribute{
						MarkdownDescription: "SU index -> host count, matching the addFabricData `hostMap` field (e.g. {\"0\" = \"1\"}). Optional: if unset, it is derived automatically from `hosts_per_su` (e.g. \"{0:1}\" -> {\"0\" = \"1\"}), since the API requires both fields to carry the same data.",
						Optional:            true,
						Computed:            true,
						ElementType:         types.StringType,
						PlanModifiers: []planmodifier.Map{
							mapplanmodifier.UseStateForUnknown(),
						},
					},
					"tenant_ctrl": schema.StringAttribute{
						MarkdownDescription: "Tenant context for the addFabricData call, e.g. \"ones\". Unrelated to the ONES UI's ONES/External \"Tenant control\" radio, which this provider doesn't expose.",
						Required:            true,
					},
					"simulation_id": schema.Int64Attribute{
						MarkdownDescription: "Raw `simulationId` value expected by the API. Defaults to 1 if unset.",
						Optional:            true,
						Computed:            true,
						PlanModifiers: []planmodifier.Int64{
							int64planmodifier.UseStateForUnknown(),
						},
					},
				},
			},
			"network_config": schema.SingleNestedAttribute{
				MarkdownDescription: "East-west and north-south networking configuration.",
				Required:            true,
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.RequiresReplace(),
				},
				Attributes: map[string]schema.Attribute{
					"enable_east_west_networking": schema.BoolAttribute{
						MarkdownDescription: "Enable east-west networking. Defaults to false if unset.",
						Optional:            true,
						Computed:            true,
						PlanModifiers: []planmodifier.Bool{
							boolplanmodifier.UseStateForUnknown(),
						},
					},
					"starting_subnet_gpu": schema.StringAttribute{
						MarkdownDescription: "Starting GPU subnet, e.g. \"192\". Required when `enable_east_west_networking` is true; not needed otherwise.",
						Optional:            true,
					},
					"enable_north_south_networking": schema.BoolAttribute{
						MarkdownDescription: "Enable north-south (front-end user/storage) networking, matching the ONES UI's \"N-S (Front-End) Network\" section. When true, `starting_subnet_cpu` is required. Defaults to false if unset.",
						Optional:            true,
						Computed:            true,
						PlanModifiers: []planmodifier.Bool{
							boolplanmodifier.UseStateForUnknown(),
						},
					},
					"dedicated_storage": schema.BoolAttribute{
						MarkdownDescription: "Use a separate subnet for storage NICs instead of sharing the user/storage subnet (the ONES UI's \"Dedicated Storage Network\" switch). Only meaningful when `enable_north_south_networking` is true. Requires `starting_subnet_storage`. Defaults to false if unset.",
						Optional:            true,
						Computed:            true,
						PlanModifiers: []planmodifier.Bool{
							boolplanmodifier.UseStateForUnknown(),
						},
					},
					"starting_subnet_cpu": schema.StringAttribute{
						MarkdownDescription: "Starting subnet for CPU NICs, e.g. \"10.2\". Required when `enable_north_south_networking` is true — used as the shared user/storage subnet when `dedicated_storage` is false, or as the dedicated CPU subnet when it's true.",
						Optional:            true,
					},
					"starting_subnet_storage": schema.StringAttribute{
						MarkdownDescription: "Starting subnet for storage NICs, e.g. \"10.3\". Required when `dedicated_storage` is true.",
						Optional:            true,
					},
				},
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
	nodeType := "dgx"
	if !data.SuConfig.NodeType.IsNull() && !data.SuConfig.NodeType.IsUnknown() && strings.TrimSpace(data.SuConfig.NodeType.ValueString()) != "" {
		nodeType = data.SuConfig.NodeType.ValueString()
	}
	enableEW := false
	if !data.NetworkConfig.EnableEastWestNetworking.IsNull() && !data.NetworkConfig.EnableEastWestNetworking.IsUnknown() {
		enableEW = data.NetworkConfig.EnableEastWestNetworking.ValueBool()
	}
	simulationID := int64(1)
	if !data.SuConfig.SimulationID.IsNull() && !data.SuConfig.SimulationID.IsUnknown() {
		simulationID = data.SuConfig.SimulationID.ValueInt64()
	}
	enableNS := false
	if !data.NetworkConfig.EnableNorthSouthNetworking.IsNull() && !data.NetworkConfig.EnableNorthSouthNetworking.IsUnknown() {
		enableNS = data.NetworkConfig.EnableNorthSouthNetworking.ValueBool()
	}
	// Always ONES-managed — not exposed as an input. The ONES UI's "Tenant
	// control" radio (isOnesControlled=false, "External") is what unlocks the
	// "Tenant Compute IP Pool" field (startingSubnetTenants); since that mode
	// isn't supported here, startingSubnetTenants is never applicable either.
	const isOnesControlled = true
	dedicatedStorage := false
	if !data.NetworkConfig.DedicatedStorage.IsNull() && !data.NetworkConfig.DedicatedStorage.IsUnknown() {
		dedicatedStorage = data.NetworkConfig.DedicatedStorage.ValueBool()
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
	startingSubnetGpu := data.NetworkConfig.StartingSubnetGpu.ValueString()
	startingSubnetCpu := data.NetworkConfig.StartingSubnetCpu.ValueString()
	startingSubnetStorage := data.NetworkConfig.StartingSubnetStorage.ValueString()
	// Error text below matches the ONES UI's own validation messages
	// (selfcare/src/ui-v2/pages/inventory/FabricNetwork.jsx handleSubmit)
	// where an equivalent check exists, so the same failure reads the same
	// way whether it's hit from the UI or from this provider.
	if enableEW && strings.TrimSpace(startingSubnetGpu) == "" {
		resp.Diagnostics.AddError(
			"Please enter a valid GPU Subnet",
			"network_config.starting_subnet_gpu is required when enable_east_west_networking is true.",
		)
		return
	}
	if enableNS && strings.TrimSpace(startingSubnetCpu) == "" {
		resp.Diagnostics.AddError(
			"Please enter a valid CPU Subnet",
			"network_config.starting_subnet_cpu is required when enable_north_south_networking is true (used as the shared user/storage subnet, or the CPU subnet when dedicated_storage is also true).",
		)
		return
	}
	if dedicatedStorage && strings.TrimSpace(startingSubnetStorage) == "" {
		resp.Diagnostics.AddError(
			"Please enter a valid Storage Subnet",
			"network_config.starting_subnet_storage is required when dedicated_storage is true.",
		)
		return
	}
	hostMap := map[string]string{}
	if !data.SuConfig.HostMap.IsNull() && !data.SuConfig.HostMap.IsUnknown() {
		resp.Diagnostics.Append(data.SuConfig.HostMap.ElementsAs(ctx, &hostMap, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	if len(hostMap) == 0 {
		derived, err := hostMapFromHostsPerSu(data.SuConfig.HostsPerSU.ValueString())
		if err != nil {
			resp.Diagnostics.AddError(
				"Invalid su_config.hosts_per_su",
				fmt.Sprintf("Unable to derive host_map from hosts_per_su %q: %s", data.SuConfig.HostsPerSU.ValueString(), err),
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
		NumOfSUs:          int(data.SuConfig.NumberOfSUs.ValueInt64()),
		MaxNumOfSUs:       int(data.SuConfig.MaxNumberOfSUs.ValueInt64()),
		HostMap:           hostMap,
		StartingSubnetGPU: startingSubnetGpu,
		SimulationID:      int(simulationID),
		EnableEW:          enableEW,
		SUHostCount:       data.SuConfig.HostsPerSU.ValueString(),
		Tenant:            data.SuConfig.TenantCtrl.ValueString(),
		Instance:          instance,
		NodeType:          nodeType,

		EnableNS:              enableNS,
		IsONESControlled:      isOnesControlled,
		UserAndStorage:        userAndStorage,
		DedicatedStorage:      dedicatedStorage,
		FrontendStorage:       frontendStorage,
		StartingSubnetCPU:     startingSubnetCpu,
		StartingSubnetStorage: startingSubnetStorage,
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
	data.SuConfig.NodeType = types.StringValue(nodeType)
	data.SuConfig.SimulationID = types.Int64Value(simulationID)
	data.NetworkConfig.EnableEastWestNetworking = types.BoolValue(enableEW)
	data.NetworkConfig.EnableNorthSouthNetworking = types.BoolValue(enableNS)
	data.NetworkConfig.DedicatedStorage = types.BoolValue(dedicatedStorage)
	data.ID = types.StringValue(data.Name.ValueString())

	hostMapValue, diags := types.MapValueFrom(ctx, types.StringType, hostMap)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.SuConfig.HostMap = hostMapValue

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// hostMapFromHostsPerSu parses the API's "{0:1,1:2}" suHostCnt shorthand
// (Terraform-facing as su_config.hosts_per_su) into the hostMap field shape
// the same addFabricData call also expects (both fields carry the same
// SU-index -> host-count data, in two formats).
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
