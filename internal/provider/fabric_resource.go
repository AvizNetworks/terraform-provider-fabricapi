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
	// RackHostMap is the gb200/gb300-only host layout: SU index -> {rack name ->
	// host count}, matching the ONES UI's rack/node picker for those node types
	// (FabricSuConfig.jsx). Ignored for every other node_type. See the schema
	// description for why this can't just be host_map with a looser element type.
	RackHostMap types.Map `tfsdk:"rack_host_map"`
	// TenantCtrl is the addFabricData `tenant` field (always effectively
	// "ones" in practice) — NOT the ONES UI's separate "Tenant control"
	// ONES/External radio (isOnesControlled), which is TenantControl below.
	TenantCtrl types.String `tfsdk:"tenant_ctrl"`
	// TenantControl is the ONES UI's "Tenant control" ONES/External radio
	// (FabricSuConfig.jsx), sent as isOnesControlled.
	TenantControl types.String `tfsdk:"tenant_control"`
	SimulationID  types.Int64  `tfsdk:"simulation_id"`
}

// FabricNetworkConfigModel groups east-west and north-south networking
// configuration under a single nested attribute.
type FabricNetworkConfigModel struct {
	EnableEastWestNetworking   types.Bool   `tfsdk:"enable_east_west_networking"`
	NetworkType                types.String `tfsdk:"network_type"`
	StartingSubnetGpu          types.String `tfsdk:"starting_subnet_gpu"`
	EwTenantAware              types.Bool   `tfsdk:"ew_tenant_aware"`
	StartingSubnetTenants      types.String `tfsdk:"starting_subnet_tenants"`
	EnableNorthSouthNetworking types.Bool   `tfsdk:"enable_north_south_networking"`
	NsOperatingSystem          types.String `tfsdk:"ns_operating_system"`
	DedicatedStorage           types.Bool   `tfsdk:"dedicated_storage"`
	StartingSubnetCpu          types.String `tfsdk:"starting_subnet_cpu"`
	StartingSubnetStorage      types.String `tfsdk:"starting_subnet_storage"`
	DpuHbn                     types.Bool   `tfsdk:"dpu_hbn"`
	IsVxlanFabric              types.Bool   `tfsdk:"is_vxlan_fabric"`
	StorageTenantAware         types.Bool   `tfsdk:"storage_tenant_aware"`
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
						MarkdownDescription: "SU index -> host count, matching the addFabricData `hostMap` field (e.g. {\"0\" = \"1\"}). Optional: if unset, it is derived automatically from `hosts_per_su` (e.g. \"{0:1}\" -> {\"0\" = \"1\"}), since the API requires both fields to carry the same data. Ignored when `rack_host_map` is set (gb200/gb300 only).",
						Optional:            true,
						Computed:            true,
						ElementType:         types.StringType,
						PlanModifiers: []planmodifier.Map{
							mapplanmodifier.UseStateForUnknown(),
						},
					},
					"rack_host_map": schema.MapAttribute{
						MarkdownDescription: "**gb200/gb300 only.** SU index -> {rack name -> host count}, e.g. " +
							"`{\"0\" = {\"rack1\" = \"5\", \"rack2\" = \"6\", \"rack3\" = \"4\"}}` for a gb300 SU with 3 " +
							"racks. The addFabricData `hostMap` field needs this nested shape for `node_type` " +
							"\"gb200\"/\"gb300\" to get the right rack-based hosts (`su00-rack00-node00`, ...), and is " +
							"**required** for those node types: a flat count would be applied to every rack, " +
							"multiplying the hosts. Rack keys must be exactly " +
							"\"rack1\"/\"rack2\"/\"rack3\" (gb300: 3 racks; gb200: 2), max 18 nodes/rack, matching " +
							"the ONES UI's rack/node picker. When set, takes precedence over `host_map`/`hosts_per_su` " +
							"for the request's `hostMap` field — but `hosts_per_su` is still required and must be " +
							"set to the same data as raw JSON (e.g. `\"{\\\"0\\\":{\\\"rack1\\\":\\\"5\\\",\\\"rack2\\\":\\\"6\\\",\\\"rack3\\\":\\\"4\\\"}}\"`), matching how the ONES UI round-trips `suHostCnt` for these node types.",
						Optional:    true,
						ElementType: types.MapType{ElemType: types.StringType},
					},
					"tenant_ctrl": schema.StringAttribute{
						MarkdownDescription: "Tenant context for the addFabricData call (`tenant`), e.g. \"ones\". Unrelated to the ONES UI's ONES/External \"Tenant control\" radio — that is `tenant_control`.",
						Required:            true,
					},
					"tenant_control": schema.StringAttribute{
						MarkdownDescription: "The ONES UI's \"Tenant control\" radio: \"ones\" (tenants managed in ONES, the default) or \"external\" (tenants managed by an external integrated service). Sent as `isOnesControlled` (true for \"ones\", false for \"external\"). With \"external\", `network_config.starting_subnet_tenants` is required when north-south networking is enabled, and `network_config.ew_tenant_aware` must be false.",
						Optional:            true,
						Computed:            true,
						Validators: []validator.String{
							stringvalidator.OneOf(tenantControlOnes, tenantControlExternal),
						},
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.UseStateForUnknown(),
						},
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
					"network_type": schema.StringAttribute{
						MarkdownDescription: "E-W technology: \"ethernet\" or \"infiniband\", matching the ONES UI's \"Technology\" radio. Only valid when `enable_east_west_networking` is true. Defaults to \"infiniband\" for node_type \"gb200\" (the UI forces it) and \"ethernet\" otherwise. NVIDIA SpX RA 2.1 only supports \"ethernet\"; gb200 only supports \"infiniband\". The provider derives the fabric's `gpuScaleMode` from this and `node_type` the same way the UI does (e.g. SCALE_OUT_ETH, SCALE_OUT_IB, SCALE_OUT_SCALE_UP_IB for GB200/GB300 NVLink scale-up). With \"infiniband\", no GPU subnet is used and the request is sent with `enableEW = false`, as the UI does (InfiniBand is UFM-managed, so no E-W switches are generated).",
						Optional:            true,
						Computed:            true,
						Validators: []validator.String{
							stringvalidator.OneOf(networkTypeEthernet, networkTypeInfiniband),
						},
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.UseStateForUnknown(),
						},
					},
					"starting_subnet_gpu": schema.StringAttribute{
						MarkdownDescription: "Starting GPU subnet first octet: \"10\", \"172\" or \"192\". Required when `enable_east_west_networking` is true with Ethernet; must be unset for InfiniBand. For NVIDIA SpX RA 2.1 it is fixed to \"172\" (sent automatically when unset).",
						Optional:            true,
					},
					"ew_tenant_aware": schema.BoolAttribute{
						MarkdownDescription: "The ONES UI's E-W \"Tenant aware\" Yes/No: whether the GPU (E-W) network is isolated per tenant. Only for Ethernet E-W, and must be false when `su_config.tenant_control` is \"external\". Defaults to false.",
						Optional:            true,
						Computed:            true,
						PlanModifiers: []planmodifier.Bool{
							boolplanmodifier.UseStateForUnknown(),
						},
					},
					"starting_subnet_tenants": schema.StringAttribute{
						MarkdownDescription: "The ONES UI's \"Tenant Compute IP Pool\": first octet \"10\", \"172\" or \"192\", sent as \"X.0\". Only used (and then required, when north-south networking is enabled) with `su_config.tenant_control = \"external\"`. Must not overlap the GPU subnet.",
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
					"ns_operating_system": schema.StringAttribute{
						MarkdownDescription: "Operating system for the north-south (front-end/storage) switches — \"cumulus\" or \"sonic\", matching the ONES UI's OS selector in the N-S section. Only meaningful when `enable_north_south_networking` is true; a mismatch against the real devices' actual OS fails switch validation (\"Device type mismatch\"). Defaults to \"cumulus\" if unset. East-west switches are always treated as Cumulus by the API and aren't configurable here.",
						Optional:            true,
						Computed:            true,
						Validators: []validator.String{
							stringvalidator.OneOf("cumulus", "sonic"),
						},
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.UseStateForUnknown(),
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
					"dpu_hbn": schema.BoolAttribute{
						MarkdownDescription: "Enable DPU/HBN offload for the east-west network — GPU hosts connect through a BlueField DPU running HBN instead of directly to the E-W leaf, matching the ONES UI's HBN toggle. Only meaningful when `enable_east_west_networking` is true. Defaults to false if unset. Devices discovered on an HBN fabric are read with `fabricapi_vf_interfaces` and bound to a tenant with `fabricapi_vf_assign`, not through the whole-server/per-GPU allocation resources. The ONES UI forces `is_vxlan_fabric` to VXLAN whenever this is true; so does the provider (it is set to true when unset, and `is_vxlan_fabric = false` is rejected).",
						Optional:            true,
						Computed:            true,
						PlanModifiers: []planmodifier.Bool{
							boolplanmodifier.UseStateForUnknown(),
						},
					},
					"is_vxlan_fabric": schema.BoolAttribute{
						MarkdownDescription: "Tenant segmentation mechanism: `true` for VXLAN (overlay), `false` for VLAN — matching the ONES UI's \"Tenant Segmentation\" radio in the N-S network step. A fabric-wide choice applied to every tenant's generated config (Fabrics.isVxlanFabric on the backend, consumed by SpectrumMultiTenancyConfig/SpectrumVrfLiteConfig), not a per-tenant setting. Defaults to `false` (VLAN) if unset, matching the UI's default for a new fabric — except where the UI forces VXLAN: `dpu_hbn = true`, or SONiC N-S on fabric types other than Aviz RA 1.0.",
						Optional:            true,
						Computed:            true,
						PlanModifiers: []planmodifier.Bool{
							boolplanmodifier.UseStateForUnknown(),
						},
					},
					"storage_tenant_aware": schema.BoolAttribute{
						MarkdownDescription: "Whether storage NICs participate in tenant isolation, matching the ONES UI's \"Tenant aware (Storage)\" Yes/No toggle in the N-S network step. Defaults to `false` if unset, matching the UI's default — except SONiC N-S on fabric types other than Aviz RA 1.0, where the UI forces it to true.",
						Optional:            true,
						Computed:            true,
						PlanModifiers: []planmodifier.Bool{
							boolplanmodifier.UseStateForUnknown(),
						},
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
	nsOperatingSystem := "cumulus"
	if !data.NetworkConfig.NsOperatingSystem.IsNull() && !data.NetworkConfig.NsOperatingSystem.IsUnknown() && strings.TrimSpace(data.NetworkConfig.NsOperatingSystem.ValueString()) != "" {
		nsOperatingSystem = data.NetworkConfig.NsOperatingSystem.ValueString()
	}
	dpuHbn := false
	if !data.NetworkConfig.DpuHbn.IsNull() && !data.NetworkConfig.DpuHbn.IsUnknown() {
		dpuHbn = data.NetworkConfig.DpuHbn.ValueBool()
	}
	// Defaults match the ONES UI for a new fabric (FabricNetwork.jsx formData):
	// VLAN, storage not tenant aware. applyForcedDefaults later overrides the
	// unset ones where the UI forces a value (SONiC on SpX, DPU HBN).
	isVxlanFabric := false
	isVxlanFabricSet := !data.NetworkConfig.IsVxlanFabric.IsNull() && !data.NetworkConfig.IsVxlanFabric.IsUnknown()
	if isVxlanFabricSet {
		isVxlanFabric = data.NetworkConfig.IsVxlanFabric.ValueBool()
	}
	storageTenantAware := false
	storageTenantAwareSet := !data.NetworkConfig.StorageTenantAware.IsNull() && !data.NetworkConfig.StorageTenantAware.IsUnknown()
	if storageTenantAwareSet {
		storageTenantAware = data.NetworkConfig.StorageTenantAware.ValueBool()
	}
	tenantControl := tenantControlOnes
	if !data.SuConfig.TenantControl.IsNull() && !data.SuConfig.TenantControl.IsUnknown() && strings.TrimSpace(data.SuConfig.TenantControl.ValueString()) != "" {
		tenantControl = data.SuConfig.TenantControl.ValueString()
	}
	networkType := ""
	networkTypeSet := !data.NetworkConfig.NetworkType.IsNull() && !data.NetworkConfig.NetworkType.IsUnknown() && strings.TrimSpace(data.NetworkConfig.NetworkType.ValueString()) != ""
	if networkTypeSet {
		networkType = data.NetworkConfig.NetworkType.ValueString()
	}
	ewTenantAware := false
	if !data.NetworkConfig.EwTenantAware.IsNull() && !data.NetworkConfig.EwTenantAware.IsUnknown() {
		ewTenantAware = data.NetworkConfig.EwTenantAware.ValueBool()
	}
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
	startingSubnetTenants := data.NetworkConfig.StartingSubnetTenants.ValueString()
	hostMap := map[string]any{}
	usingRackHostMap := !data.SuConfig.RackHostMap.IsNull() && !data.SuConfig.RackHostMap.IsUnknown() && len(data.SuConfig.RackHostMap.Elements()) > 0
	var flatHostMap map[string]string
	var rackHostMap map[string]map[string]string
	if usingRackHostMap {
		// gb200/gb300 rack layout — nested per-SU object, never flattened. Takes
		// precedence over host_map/hosts_per_su-derivation; see the schema
		// description for why the API needs this exact shape for rack naming.
		rackHostMap = map[string]map[string]string{}
		resp.Diagnostics.Append(data.SuConfig.RackHostMap.ElementsAs(ctx, &rackHostMap, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		for su, racks := range rackHostMap {
			hostMap[su] = racks
		}
	} else {
		flatHostMap = map[string]string{}
		if !data.SuConfig.HostMap.IsNull() && !data.SuConfig.HostMap.IsUnknown() {
			resp.Diagnostics.Append(data.SuConfig.HostMap.ElementsAs(ctx, &flatHostMap, false)...)
			if resp.Diagnostics.HasError() {
				return
			}
		}
		if len(flatHostMap) == 0 {
			derived, err := hostMapFromHostsPerSu(data.SuConfig.HostsPerSU.ValueString())
			if err != nil && isRackNodeType(nodeType) {
				// gb200/gb300 need rack_host_map; validate reports that clearly.
				derived, err = map[string]string{}, nil
			}
			if err != nil {
				resp.Diagnostics.AddError(
					"Invalid su_config.hosts_per_su",
					fmt.Sprintf("Unable to derive host_map from hosts_per_su %q: %s", data.SuConfig.HostsPerSU.ValueString(), err),
				)
				return
			}
			flatHostMap = derived
		}
		for su, count := range flatHostMap {
			hostMap[su] = count
		}
	}

	in := fabricInput{
		Type:                  data.Type.ValueString(),
		NodeType:              nodeType,
		NumOfSUs:              int(data.SuConfig.NumberOfSUs.ValueInt64()),
		MaxNumOfSUs:           int(data.SuConfig.MaxNumberOfSUs.ValueInt64()),
		FlatHostMap:           flatHostMap,
		RackHostMap:           rackHostMap,
		TenantControl:         tenantControl,
		EnableEW:              enableEW,
		NetworkType:           networkType,
		NetworkTypeSet:        networkTypeSet,
		StartingSubnetGpu:     startingSubnetGpu,
		EwTenantAware:         ewTenantAware,
		EnableNS:              enableNS,
		NsOperatingSystem:     nsOperatingSystem,
		DedicatedStorage:      dedicatedStorage,
		StartingSubnetCpu:     startingSubnetCpu,
		StartingSubnetStorage: startingSubnetStorage,
		StartingSubnetTenants: startingSubnetTenants,
		DpuHbn:                dpuHbn,
		IsVxlanFabric:         isVxlanFabric,
		IsVxlanFabricSet:      isVxlanFabricSet,
		StorageTenantAware:    storageTenantAware,
		StorageTenantAwareSet: storageTenantAwareSet,
	}
	in.applyForcedDefaults()
	// Error summaries match the ONES UI's own validation messages
	// (FabricSuConfig.jsx, FabricNetwork.jsx, NewHostPerSu.jsx) where an
	// equivalent check exists, so the same failure reads the same way whether
	// it's hit from the UI or from this provider.
	if issues := in.validate(); len(issues) > 0 {
		for _, issue := range issues {
			resp.Diagnostics.AddError(issue.Summary, issue.Detail)
		}
		return
	}

	gpuScaleMode, scaleUpNvLink := gpuScaleModeFor(in.Type, in.NodeType, in.NetworkType, in.EnableEW)
	// The UI sends enableEW=false for InfiniBand (payloadToSend): UFM manages
	// that network, so no E-W switches are generated.
	sendEnableEW := in.EnableEW && in.NetworkType != networkTypeInfiniband
	sendGpuSubnet := ""
	if sendEnableEW {
		sendGpuSubnet = in.StartingSubnetGpu
	}
	sendTenants := ""
	if in.EnableNS && in.TenantControl == tenantControlExternal && strings.TrimSpace(in.StartingSubnetTenants) != "" {
		sendTenants = strings.TrimSpace(in.StartingSubnetTenants) + ".0"
	}
	// Tenant aware (E-W) is forced off when tenants are managed externally.
	sendEwTenantAware := in.EwTenantAware && in.TenantControl != tenantControlExternal

	reqBody := FabricDataRequest{
		Name:              data.Name.ValueString(),
		Type:              data.Type.ValueString(),
		Status:            status,
		Description:       data.Description.ValueString(),
		NumOfSUs:          int(data.SuConfig.NumberOfSUs.ValueInt64()),
		MaxNumOfSUs:       int(data.SuConfig.MaxNumberOfSUs.ValueInt64()),
		HostMap:           hostMap,
		StartingSubnetGPU: sendGpuSubnet,
		SimulationID:      int(simulationID),
		EnableEW:          sendEnableEW,
		SUHostCount:       data.SuConfig.HostsPerSU.ValueString(),
		Tenant:            data.SuConfig.TenantCtrl.ValueString(),
		Instance:          instance,
		NodeType:          nodeType,

		EnableNS:              enableNS,
		IsONESControlled:      in.TenantControl != tenantControlExternal,
		UserAndStorage:        userAndStorage,
		DedicatedStorage:      dedicatedStorage,
		FrontendStorage:       frontendStorage,
		StartingSubnetCPU:     startingSubnetCpu,
		StartingSubnetStorage: startingSubnetStorage,
		StartingSubnetTenants: sendTenants,

		// East-west is always Cumulus in the ONES UI (not user-selectable there);
		// front-end/storage (north-south) OS is user-selectable and must match
		// the real devices' actual OS or switch validation fails.
		OperatingSystemE: "cumulus",
		OperatingSystemF: nsOperatingSystem,
		OperatingSystemS: nsOperatingSystem,

		DpuHbn:             dpuHbn,
		IsVxlanFabric:      in.IsVxlanFabric,
		StorageTenantAware: in.StorageTenantAware,

		GpuScaleMode:   gpuScaleMode,
		NetworkType:    wireNetworkType(in.NetworkType),
		ScaleUpNvLink:  scaleUpNvLink,
		SuSize:         suSizeFor(nodeType),
		Hybrid:         enableNS && nsOperatingSystem == "sonic",
		Dedicated:      enableNS && dedicatedStorage,
		EwTenantAware:  sendEwTenantAware,
		DeploymentType: "DEFAULT",
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
	data.NetworkConfig.NsOperatingSystem = types.StringValue(nsOperatingSystem)
	data.NetworkConfig.DedicatedStorage = types.BoolValue(dedicatedStorage)
	data.NetworkConfig.DpuHbn = types.BoolValue(dpuHbn)
	data.NetworkConfig.IsVxlanFabric = types.BoolValue(in.IsVxlanFabric)
	data.NetworkConfig.StorageTenantAware = types.BoolValue(in.StorageTenantAware)
	data.NetworkConfig.EwTenantAware = types.BoolValue(sendEwTenantAware)
	data.SuConfig.TenantControl = types.StringValue(in.TenantControl)
	if in.EnableEW {
		data.NetworkConfig.NetworkType = types.StringValue(in.NetworkType)
	} else {
		data.NetworkConfig.NetworkType = types.StringNull()
	}
	data.ID = types.StringValue(data.Name.ValueString())

	if usingRackHostMap {
		// host_map carries flat string values only (see schema); it genuinely
		// doesn't apply in rack mode, so write back null rather than attempting
		// to force the nested rack data into that shape.
		data.SuConfig.HostMap = types.MapNull(types.StringType)
	} else {
		hostMapValue, diags := types.MapValueFrom(ctx, types.StringType, flatHostMap)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		data.SuConfig.HostMap = hostMapValue
	}

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
