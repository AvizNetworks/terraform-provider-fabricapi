package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                   = &IpamConfigResource{}
	_ resource.ResourceWithImportState    = &IpamConfigResource{}
	_ resource.ResourceWithValidateConfig = &IpamConfigResource{}
)

const (
	ipamModeStatic = "static"
	ipamModeDhcp   = "dhcp"

	maxDhcpLeaseTimeSeconds = 2592000 // 30 days, FM's limit
)

func NewIpamConfigResource() resource.Resource {
	return &IpamConfigResource{}
}

// IpamConfigResource turns IPAM on for a fabric in static or DHCP mode
// (PATCH /fabrics/{fabric}/ipam/config). FM has one config per fabric and no
// delete API, so destroy turns IPAM off. FM refuses to switch an enabled fabric
// directly between static and DHCP, so changing `mode` replaces the resource:
// IPAM is turned off, then on in the new mode.
type IpamConfigResource struct {
	client *APIClient
}

type IpamConfigResourceModel struct {
	FabricName       types.String `tfsdk:"fabric_name"`
	Mode             types.String `tfsdk:"mode"`
	EnableIPAssign   types.Bool   `tfsdk:"enable_ip_assign"`
	LeaseTimeSeconds types.Int64  `tfsdk:"lease_time_seconds"`
	Force            types.Bool   `tfsdk:"force"`
	ID               types.String `tfsdk:"id"`
}

func (r *IpamConfigResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ipam_config"
}

func (r *IpamConfigResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Enables IPAM on a fabric (`PATCH /fabrics/{fabric}/ipam/config`), so tenants created afterwards get an address pool and their servers get addresses. " +
			"Only fabrics with External tenant control can be changed. Destroying the resource turns IPAM off, which Fabric Manager refuses while any server still holds an address. " +
			"Changing `mode` replaces the resource (IPAM off, then on in the new mode), since Fabric Manager won't switch an enabled fabric between static and DHCP directly.\n\n" +
			"DHCP mode needs a DHCP (Kea) server configured for the fabric and a `dhcp-host-NN` node in its intent, and a VXLAN fabric. The config only affects tenants created later; existing tenants are not re-addressed.",
		Attributes: map[string]schema.Attribute{
			"fabric_name": schema.StringAttribute{
				MarkdownDescription: "Fabric to configure. If unset, uses the provider-level fabric.",
				Optional:            true,
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"mode": schema.StringAttribute{
				MarkdownDescription: "`static` (Fabric Manager picks each server's address) or `dhcp` (servers lease addresses from the fabric's Kea server). Changing it replaces the resource.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf(ipamModeStatic, ipamModeDhcp)},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"enable_ip_assign": schema.BoolAttribute{
				MarkdownDescription: "Static mode only: also push each allocated address to the server over SSH. Defaults to false.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"lease_time_seconds": schema.Int64Attribute{
				MarkdownDescription: fmt.Sprintf("DHCP mode only: lease time, 1 to %d seconds (30 days). Renew and rebind follow at 50%% and 87.5%%. If unset, Fabric Manager keeps the fabric's saved value (60 on a new fabric). Takes effect on the next tenant create/delete or DHCP reconcile.", maxDhcpLeaseTimeSeconds),
				Optional:            true,
				Computed:            true,
				Validators:          []validator.Int64{int64validator.Between(1, maxDhcpLeaseTimeSeconds)},
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"force": schema.BoolAttribute{
				MarkdownDescription: "Sent as `?force=true` on every change. When turning DHCP on it skips Fabric Manager's DHCP server reachability check; when turning IPAM off (destroy or mode change) it lets Fabric Manager disable unused tenant pools. It never overrides the checks for allocated addresses or a missing DHCP node. Defaults to false.",
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
			},
			"id": schema.StringAttribute{
				MarkdownDescription: "The fabric name.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *IpamConfigResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*APIClient)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected *APIClient, got: %T", req.ProviderData))
		return
	}
	r.client = client
}

func (r *IpamConfigResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data IpamConfigResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() || data.Mode.IsUnknown() || data.Mode.IsNull() {
		return
	}
	mode := data.Mode.ValueString()
	if mode == ipamModeDhcp && !data.EnableIPAssign.IsNull() && !data.EnableIPAssign.IsUnknown() && data.EnableIPAssign.ValueBool() {
		resp.Diagnostics.AddAttributeError(path.Root("enable_ip_assign"), "Setting not used in DHCP mode",
			"enable_ip_assign only applies with mode = \"static\".")
	}
	if mode == ipamModeStatic && !data.LeaseTimeSeconds.IsNull() && !data.LeaseTimeSeconds.IsUnknown() {
		resp.Diagnostics.AddAttributeError(path.Root("lease_time_seconds"), "Setting not used in static mode",
			"lease_time_seconds only applies with mode = \"dhcp\".")
	}
}

func (r *IpamConfigResource) fabricName(data IpamConfigResourceModel) (string, diag.Diagnostics) {
	var diags diag.Diagnostics
	name := resolveFabricName(r.client.Fabric, data.FabricName)
	if strings.TrimSpace(name) == "" {
		diags.AddError("Missing fabric", "Set `fabric_name` on this resource or `fabric` on the provider.")
	}
	return name, diags
}

// body is the PATCH body for the planned mode. Only the enabled mode is sent;
// FM treats the other as disabled.
func (m IpamConfigResourceModel) body() IpamConfig {
	cfg := IpamConfig{EnableIpam: true}
	if m.Mode.ValueString() == ipamModeDhcp {
		cfg.Dhcp = &IpamDhcpConfig{Enabled: true}
		if !m.LeaseTimeSeconds.IsNull() && !m.LeaseTimeSeconds.IsUnknown() {
			v := m.LeaseTimeSeconds.ValueInt64()
			cfg.Dhcp.LeaseTimeSeconds = &v
		}
		return cfg
	}
	assign := m.EnableIPAssign.ValueBool()
	cfg.Static = &IpamStaticConfig{Enabled: true, EnableIPAssign: &assign}
	return cfg
}

// fromAPI copies the server's config into the model. It reports false when
// IPAM is off, i.e. the resource no longer exists.
func (m *IpamConfigResourceModel) fromAPI(cfg *IpamConfig) bool {
	switch {
	case !cfg.EnableIpam:
		return false
	case cfg.Dhcp != nil && cfg.Dhcp.Enabled:
		m.Mode = types.StringValue(ipamModeDhcp)
		m.EnableIPAssign = types.BoolValue(false)
		m.LeaseTimeSeconds = types.Int64Null()
		if cfg.Dhcp.LeaseTimeSeconds != nil {
			m.LeaseTimeSeconds = types.Int64Value(*cfg.Dhcp.LeaseTimeSeconds)
		}
	case cfg.Static != nil && cfg.Static.Enabled:
		m.Mode = types.StringValue(ipamModeStatic)
		m.EnableIPAssign = types.BoolValue(cfg.Static.EnableIPAssign != nil && *cfg.Static.EnableIPAssign)
		m.LeaseTimeSeconds = types.Int64Null()
	default:
		return false
	}
	return true
}

func (r *IpamConfigResource) apply(ctx context.Context, data *IpamConfigResourceModel, diags *diag.Diagnostics, action string) {
	fabricName, d := r.fabricName(*data)
	diags.Append(d...)
	if diags.HasError() {
		return
	}
	cfg, err := r.client.UpdateIpamConfig(ctx, fabricName, data.body(), data.Force.ValueBool())
	if err != nil {
		diags.AddError("Unable to "+action+" IPAM configuration", err.Error())
		return
	}
	if !data.fromAPI(cfg) {
		diags.AddError("Unexpected IPAM configuration",
			fmt.Sprintf("Fabric Manager accepted the change but reports IPAM disabled for fabric %q.", fabricName))
		return
	}
	data.FabricName = types.StringValue(fabricName)
	data.ID = types.StringValue(fabricName)
}

func (r *IpamConfigResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data IpamConfigResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &data, &resp.Diagnostics, "set")
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *IpamConfigResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data IpamConfigResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	fabricName, diags := r.fabricName(data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	cfg, err := r.client.GetIpamConfig(ctx, fabricName)
	if err != nil {
		if isFMNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to read IPAM configuration", err.Error())
		return
	}
	if !data.fromAPI(cfg) {
		resp.State.RemoveResource(ctx)
		return
	}
	data.FabricName = types.StringValue(fabricName)
	data.ID = types.StringValue(fabricName)
	if data.Force.IsNull() {
		data.Force = types.BoolValue(false)
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *IpamConfigResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data IpamConfigResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.apply(ctx, &data, &resp.Diagnostics, "update")
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *IpamConfigResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data IpamConfigResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	fabricName, diags := r.fabricName(data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	_, err := r.client.UpdateIpamConfig(ctx, fabricName, IpamConfig{EnableIpam: false}, data.Force.ValueBool())
	if err != nil && !isFMNotFound(err) {
		resp.Diagnostics.AddError("Unable to turn IPAM off", err.Error())
		return
	}
	resp.State.RemoveResource(ctx)
}

// ImportState accepts the fabric name.
func (r *IpamConfigResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("fabric_name"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("force"), false)...)
}
