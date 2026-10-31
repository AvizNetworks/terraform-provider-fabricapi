package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Data sources for the read-only Fabric Manager 4.4 APIs: network policy and
// webhook listings, IPAM config and address queries, tenant DHCP and rail
// CIDRPools. They share fmDataSource for Configure and fabric resolution.

type fmDataSource struct {
	client *APIClient
}

func (d *fmDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	client, ok := req.ProviderData.(*APIClient)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Data Source Configure Type", fmt.Sprintf("Expected *APIClient, got: %T", req.ProviderData))
		return
	}
	d.client = client
}

func (d *fmDataSource) fabric(override types.String) (string, diag.Diagnostics) {
	var diags diag.Diagnostics
	name := resolveFabricName(d.client.Fabric, override)
	if strings.TrimSpace(name) == "" {
		diags.AddError("Missing fabric", "Set `fabric_name` on the data source or `fabric` on the provider (FABRIC_NAME).")
	}
	return name, diags
}

func fabricNameDSAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "Fabric to query. If unset, uses the provider-level fabric.",
		Optional:            true,
		Computed:            true,
	}
}

func requiredDSString(desc string) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: desc, Required: true}
}

func computedString(desc string) schema.StringAttribute {
	return schema.StringAttribute{MarkdownDescription: desc, Computed: true}
}

func computedInt(desc string) schema.Int64Attribute {
	return schema.Int64Attribute{MarkdownDescription: desc, Computed: true}
}

func computedStrings(desc string) schema.ListAttribute {
	return schema.ListAttribute{MarkdownDescription: desc, Computed: true, ElementType: types.StringType}
}

func strOrNull(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

func ptrStrOrNull(s *string) types.String {
	if s == nil {
		return types.StringNull()
	}
	return types.StringValue(*s)
}

func stringList(items []string) []types.String {
	out := make([]types.String, 0, len(items))
	for _, s := range items {
		out = append(out, types.StringValue(s))
	}
	return out
}

// ====================================================== network policies ===

var _ datasource.DataSource = &NetworkPoliciesDataSource{}

func NewNetworkPoliciesDataSource() datasource.DataSource {
	return &NetworkPoliciesDataSource{}
}

type NetworkPoliciesDataSource struct{ fmDataSource }

type networkPolicySideDS struct {
	Type        types.String   `tfsdk:"type"`
	TenantName  types.String   `tfsdk:"tenant_name"`
	ServiceName types.String   `tfsdk:"service_name"`
	Endpoints   []types.String `tfsdk:"endpoints"`
}

type networkPolicyDS struct {
	Name        types.String        `tfsdk:"name"`
	Source      networkPolicySideDS `tfsdk:"source"`
	Destination networkPolicySideDS `tfsdk:"destination"`
}

type NetworkPoliciesDataSourceModel struct {
	FabricName types.String      `tfsdk:"fabric_name"`
	Policies   []networkPolicyDS `tfsdk:"policies"`
	ID         types.String      `tfsdk:"id"`
}

func (d *NetworkPoliciesDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_network_policies"
}

func networkPolicySideDSSchema(which string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		MarkdownDescription: "The policy's " + which + ".",
		Computed:            true,
		Attributes: map[string]schema.Attribute{
			"type":         computedString("`tenant` or `service`."),
			"tenant_name":  computedString("Tenant this side belongs to."),
			"service_name": computedString("Service label, if any."),
			"endpoints":    computedStrings("Endpoint addresses, in the order Fabric Manager applied them. Empty on a tenant side."),
		},
	}
}

func (d *NetworkPoliciesDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists every network policy on a fabric (GET `/fabrics/{fabric}/networkpolicy`).",
		Attributes: map[string]schema.Attribute{
			"fabric_name": fabricNameDSAttribute(),
			"policies": schema.ListNestedAttribute{
				MarkdownDescription: "The fabric's network policies.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name":        computedString("Policy name."),
						"source":      networkPolicySideDSSchema("source"),
						"destination": networkPolicySideDSSchema("destination"),
					},
				},
			},
			"id": computedString("The fabric name."),
		},
	}
}

func sideToDS(s NetworkPolicySide) networkPolicySideDS {
	return networkPolicySideDS{
		Type:        types.StringValue(strings.ToLower(s.Type)),
		TenantName:  types.StringValue(s.TenantName),
		ServiceName: strOrNull(s.ServiceName),
		Endpoints:   stringList(s.Endpoints),
	}
}

func (d *NetworkPoliciesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data NetworkPoliciesDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	fabric, diags := d.fabric(data.FabricName)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	policies, err := d.client.ListNetworkPolicies(ctx, fabric)
	if err != nil {
		resp.Diagnostics.AddError("Unable to list network policies", err.Error())
		return
	}
	data.Policies = make([]networkPolicyDS, 0, len(policies))
	for _, p := range policies {
		data.Policies = append(data.Policies, networkPolicyDS{
			Name:        types.StringValue(p.Policy.Name),
			Source:      sideToDS(p.Spec.Source),
			Destination: sideToDS(p.Spec.Destination),
		})
	}
	data.FabricName = types.StringValue(fabric)
	data.ID = types.StringValue(fabric)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// ============================================================= webhooks ===

var _ datasource.DataSource = &WebhooksDataSource{}

func NewWebhooksDataSource() datasource.DataSource {
	return &WebhooksDataSource{}
}

type WebhooksDataSource struct{ fmDataSource }

type webhookDS struct {
	WebhookID         types.String   `tfsdk:"webhook_id"`
	CallbackURL       types.String   `tfsdk:"callback_url"`
	Enabled           types.Bool     `tfsdk:"enabled"`
	EventTypes        []types.String `tfsdk:"event_types"`
	FilterFabricNames []types.String `tfsdk:"filter_fabric_names"`
	FilterTenantNames []types.String `tfsdk:"filter_tenant_names"`
	CreatedAt         types.String   `tfsdk:"created_at"`
	UpdatedAt         types.String   `tfsdk:"updated_at"`
}

type WebhooksDataSourceModel struct {
	Webhooks []webhookDS  `tfsdk:"webhooks"`
	ID       types.String `tfsdk:"id"`
}

func (d *WebhooksDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_webhooks"
}

func (d *WebhooksDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists every global webhook registration, newest first (GET `/webhooks`). Fabric- and tenant-scoped registrations aren't included; read those with the `fabricapi_fabric_webhook`/`fabricapi_tenant_webhook` resources.",
		Attributes: map[string]schema.Attribute{
			"webhooks": schema.ListNestedAttribute{
				MarkdownDescription: "Global webhook registrations.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"webhook_id":          computedString("Registration id (UUID)."),
						"callback_url":        computedString("URL events are delivered to."),
						"enabled":             schema.BoolAttribute{MarkdownDescription: "Whether events are delivered.", Computed: true},
						"event_types":         computedStrings("Subscribed event types."),
						"filter_fabric_names": computedStrings("Fabric filter; empty means every fabric."),
						"filter_tenant_names": computedStrings("Tenant filter; empty means every tenant."),
						"created_at":          computedString("Creation time."),
						"updated_at":          computedString("Last change time."),
					},
				},
			},
			"id": computedString("Constant `webhooks`."),
		},
	}
}

func (d *WebhooksDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	regs, err := d.client.ListGlobalWebhooks(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Unable to list webhooks", err.Error())
		return
	}
	data := WebhooksDataSourceModel{Webhooks: make([]webhookDS, 0, len(regs)), ID: types.StringValue("webhooks")}
	for _, r := range regs {
		var fabrics, tenants []string
		if r.Filters != nil {
			fabrics, tenants = r.Filters.FabricNames, r.Filters.TenantNames
		}
		data.Webhooks = append(data.Webhooks, webhookDS{
			WebhookID:         types.StringValue(r.WebhookID),
			CallbackURL:       types.StringValue(r.CallbackURL),
			Enabled:           types.BoolValue(r.Enabled),
			EventTypes:        stringList(r.EventTypes),
			FilterFabricNames: stringList(fabrics),
			FilterTenantNames: stringList(tenants),
			CreatedAt:         strOrNull(r.CreatedAt),
			UpdatedAt:         strOrNull(r.UpdatedAt),
		})
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// =========================================================== IPAM config ===

var _ datasource.DataSource = &IpamConfigDataSource{}

func NewIpamConfigDataSource() datasource.DataSource {
	return &IpamConfigDataSource{}
}

type IpamConfigDataSource struct{ fmDataSource }

type IpamConfigDataSourceModel struct {
	FabricName       types.String `tfsdk:"fabric_name"`
	EnableIpam       types.Bool   `tfsdk:"enable_ipam"`
	Mode             types.String `tfsdk:"mode"`
	EnableIPAssign   types.Bool   `tfsdk:"enable_ip_assign"`
	LeaseTimeSeconds types.Int64  `tfsdk:"lease_time_seconds"`
	ID               types.String `tfsdk:"id"`
}

func (d *IpamConfigDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_ipam_config"
}

func (d *IpamConfigDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads a fabric's IPAM configuration (GET `/fabrics/{fabric}/ipam/config`). A fabric that was never configured reports IPAM disabled.",
		Attributes: map[string]schema.Attribute{
			"fabric_name":        fabricNameDSAttribute(),
			"enable_ipam":        schema.BoolAttribute{MarkdownDescription: "Whether IPAM is on.", Computed: true},
			"mode":               computedString("`static`, `dhcp`, or `disabled`."),
			"enable_ip_assign":   schema.BoolAttribute{MarkdownDescription: "Static mode: whether addresses are pushed to servers over SSH. Null in other modes.", Computed: true},
			"lease_time_seconds": computedInt("DHCP mode: lease time in seconds. Null in other modes."),
			"id":                 computedString("The fabric name."),
		},
	}
}

func (d *IpamConfigDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data IpamConfigDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	fabric, diags := d.fabric(data.FabricName)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	cfg, err := d.client.GetIpamConfig(ctx, fabric)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read IPAM configuration", err.Error())
		return
	}
	data.FabricName = types.StringValue(fabric)
	data.ID = types.StringValue(fabric)
	data.EnableIpam = types.BoolValue(cfg.EnableIpam)
	data.Mode = types.StringValue("disabled")
	data.EnableIPAssign = types.BoolNull()
	data.LeaseTimeSeconds = types.Int64Null()
	switch {
	case cfg.EnableIpam && cfg.Static != nil && cfg.Static.Enabled:
		data.Mode = types.StringValue(ipamModeStatic)
		data.EnableIPAssign = types.BoolValue(cfg.Static.EnableIPAssign != nil && *cfg.Static.EnableIPAssign)
	case cfg.EnableIpam && cfg.Dhcp != nil && cfg.Dhcp.Enabled:
		data.Mode = types.StringValue(ipamModeDhcp)
		if cfg.Dhcp.LeaseTimeSeconds != nil {
			data.LeaseTimeSeconds = types.Int64Value(*cfg.Dhcp.LeaseTimeSeconds)
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// ======================================================= IPAM addresses ===

type ipamAllocationDS struct {
	ID            types.Int64  `tfsdk:"id"`
	PoolID        types.Int64  `tfsdk:"pool_id"`
	TenantName    types.String `tfsdk:"tenant_name"`
	TenantID      types.Int64  `tfsdk:"tenant_id"`
	ServerName    types.String `tfsdk:"server_name"`
	IPAddress     types.String `tfsdk:"ip_address"`
	PrefixLength  types.Int64  `tfsdk:"prefix_length"`
	AssignStatus  types.String `tfsdk:"assign_status"`
	CleanupStatus types.String `tfsdk:"cleanup_status"`
	AllocatedAt   types.String `tfsdk:"allocated_at"`
	InterfaceName types.String `tfsdk:"interface_name"`
}

func ipamAllocationAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id":             computedInt("Allocation id."),
		"pool_id":        computedInt("IPAM pool the address came from."),
		"tenant_name":    computedString("Tenant the address belongs to."),
		"tenant_id":      computedInt("Tenant id, if known."),
		"server_name":    computedString("Compute server holding the address."),
		"ip_address":     computedString("Allocated address."),
		"prefix_length":  computedInt("Prefix length of the address."),
		"assign_status":  computedString("`ALLOCATED_ONLY`, `ASSIGNED` or `IP_ASSIGN_FAILED`."),
		"cleanup_status": computedString("`NONE` or `PENDING`."),
		"allocated_at":   computedString("When the address was allocated."),
		"interface_name": computedString("Host-side HBN VF interface the address is on; null for the north-south NIC."),
	}
}

func allocationToDS(a IpamAllocation) ipamAllocationDS {
	tenantID := types.Int64Null()
	if a.TenantID != nil {
		tenantID = types.Int64Value(*a.TenantID)
	}
	return ipamAllocationDS{
		ID:            types.Int64Value(a.ID),
		PoolID:        types.Int64Value(a.PoolID),
		TenantName:    types.StringValue(a.TenantName),
		TenantID:      tenantID,
		ServerName:    types.StringValue(a.ServerName),
		IPAddress:     types.StringValue(a.IPAddress),
		PrefixLength:  types.Int64Value(a.PrefixLength),
		AssignStatus:  types.StringValue(a.AssignStatus),
		CleanupStatus: types.StringValue(a.CleanupStatus),
		AllocatedAt:   strOrNull(a.AllocatedAt),
		InterfaceName: ptrStrOrNull(a.InterfaceName),
	}
}

func allocationsToDS(in []IpamAllocation) []ipamAllocationDS {
	out := make([]ipamAllocationDS, 0, len(in))
	for _, a := range in {
		out = append(out, allocationToDS(a))
	}
	return out
}

// --- server ---

var _ datasource.DataSource = &ServerIPAddressesDataSource{}

func NewServerIPAddressesDataSource() datasource.DataSource {
	return &ServerIPAddressesDataSource{}
}

type ServerIPAddressesDataSource struct{ fmDataSource }

type ServerIPAddressesDataSourceModel struct {
	FabricName  types.String       `tfsdk:"fabric_name"`
	ServerName  types.String       `tfsdk:"server_name"`
	Allocations []ipamAllocationDS `tfsdk:"allocations"`
	ID          types.String       `tfsdk:"id"`
}

func (d *ServerIPAddressesDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_server_ip_addresses"
}

func (d *ServerIPAddressesDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Every address IPAM has allocated to one compute server, across all its tenants (GET `/fabrics/{fabric}/computes/{server}/ip-addresses`). Fails with 404 SERVER_NOT_FOUND if the server isn't in the fabric inventory.",
		Attributes: map[string]schema.Attribute{
			"fabric_name": fabricNameDSAttribute(),
			"server_name": requiredDSString("Compute server hostname."),
			"allocations": schema.ListNestedAttribute{
				MarkdownDescription: "The server's allocations, ordered by tenant name. Empty when it holds none.",
				Computed:            true,
				NestedObject:        schema.NestedAttributeObject{Attributes: ipamAllocationAttributes()},
			},
			"id": computedString("`<fabric>/<server>`."),
		},
	}
}

func (d *ServerIPAddressesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data ServerIPAddressesDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	fabric, diags := d.fabric(data.FabricName)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	res, err := d.client.GetServerIPAddresses(ctx, fabric, data.ServerName.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read server IP addresses", err.Error())
		return
	}
	data.FabricName = types.StringValue(fabric)
	data.Allocations = allocationsToDS(res.Allocations)
	data.ID = types.StringValue(fabric + "/" + data.ServerName.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- tenant compute ---

var _ datasource.DataSource = &TenantComputeIPDataSource{}

func NewTenantComputeIPDataSource() datasource.DataSource {
	return &TenantComputeIPDataSource{}
}

type TenantComputeIPDataSource struct{ fmDataSource }

type TenantComputeIPDataSourceModel struct {
	FabricName    types.String `tfsdk:"fabric_name"`
	TenantName    types.String `tfsdk:"tenant_name"`
	ServerName    types.String `tfsdk:"server_name"`
	IPAddress     types.String `tfsdk:"ip_address"`
	PrefixLength  types.Int64  `tfsdk:"prefix_length"`
	AssignStatus  types.String `tfsdk:"assign_status"`
	CleanupStatus types.String `tfsdk:"cleanup_status"`
	AllocatedAt   types.String `tfsdk:"allocated_at"`
	InterfaceName types.String `tfsdk:"interface_name"`
	AllocationID  types.Int64  `tfsdk:"allocation_id"`
	PoolID        types.Int64  `tfsdk:"pool_id"`
	ID            types.String `tfsdk:"id"`
}

func (d *TenantComputeIPDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tenant_compute_ip"
}

func (d *TenantComputeIPDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The address allocated to one server within one tenant (GET `/fabrics/{fabric}/tenants/{tenant}/computes/{server}/ip-address`). " +
			"Fails with 404 IPAM_NOT_CONFIGURED when the tenant has no IPAM pool (this is also what an unknown fabric or tenant returns), or SERVER_NOT_ALLOCATED_TO_TENANT when the server holds no address in it.",
		Attributes: map[string]schema.Attribute{
			"fabric_name":    fabricNameDSAttribute(),
			"tenant_name":    requiredDSString("Tenant name."),
			"server_name":    requiredDSString("Compute server hostname."),
			"ip_address":     computedString("Allocated address."),
			"prefix_length":  computedInt("Prefix length of the address."),
			"assign_status":  computedString("`ALLOCATED_ONLY`, `ASSIGNED` or `IP_ASSIGN_FAILED`."),
			"cleanup_status": computedString("`NONE` or `PENDING`."),
			"allocated_at":   computedString("When the address was allocated."),
			"interface_name": computedString("Host-side HBN VF interface the address is on; null for the north-south NIC."),
			"allocation_id":  computedInt("Allocation id."),
			"pool_id":        computedInt("IPAM pool the address came from."),
			"id":             computedString("`<fabric>/<tenant>/<server>`."),
		},
	}
}

func (d *TenantComputeIPDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data TenantComputeIPDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	fabric, diags := d.fabric(data.FabricName)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	a, err := d.client.GetTenantComputeIP(ctx, fabric, data.TenantName.ValueString(), data.ServerName.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read tenant compute IP", err.Error())
		return
	}
	v := allocationToDS(*a)
	data.FabricName = types.StringValue(fabric)
	data.IPAddress = v.IPAddress
	data.PrefixLength = v.PrefixLength
	data.AssignStatus = v.AssignStatus
	data.CleanupStatus = v.CleanupStatus
	data.AllocatedAt = v.AllocatedAt
	data.InterfaceName = v.InterfaceName
	data.AllocationID = v.ID
	data.PoolID = v.PoolID
	data.ID = types.StringValue(fabric + "/" + data.TenantName.ValueString() + "/" + data.ServerName.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- tenant pool ---

var _ datasource.DataSource = &TenantIPAddressesDataSource{}

func NewTenantIPAddressesDataSource() datasource.DataSource {
	return &TenantIPAddressesDataSource{}
}

type TenantIPAddressesDataSource struct{ fmDataSource }

type TenantIPAddressesDataSourceModel struct {
	FabricName              types.String       `tfsdk:"fabric_name"`
	TenantName              types.String       `tfsdk:"tenant_name"`
	Status                  types.String       `tfsdk:"status"`
	PoolCidr                types.String       `tfsdk:"pool_cidr"`
	AllocatableAddressCount types.Int64        `tfsdk:"allocatable_address_count"`
	AllocatedCount          types.Int64        `tfsdk:"allocated_count"`
	Allocations             []ipamAllocationDS `tfsdk:"allocations"`
	ID                      types.String       `tfsdk:"id"`
}

func (d *TenantIPAddressesDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tenant_ip_addresses"
}

func (d *TenantIPAddressesDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A tenant's IPAM pool and every address allocated from it (GET `/fabrics/{fabric}/tenants/{tenant}/ip-addresses`, all pages). Fails with 404 IPAM_NOT_CONFIGURED when the tenant has no pool.",
		Attributes: map[string]schema.Attribute{
			"fabric_name": fabricNameDSAttribute(),
			"tenant_name": requiredDSString("Tenant name."),
			"status": schema.StringAttribute{
				MarkdownDescription: "Only return allocations in this state: `ALLOCATED_ONLY`, `ASSIGNED` or `IP_ASSIGN_FAILED`.",
				Optional:            true,
				Validators:          []validator.String{stringvalidator.OneOf("ALLOCATED_ONLY", "ASSIGNED", "IP_ASSIGN_FAILED")},
			},
			"pool_cidr":                 computedString("The tenant pool's CIDR."),
			"allocatable_address_count": computedInt("Addresses the pool can hand out (network, broadcast and gateway excluded)."),
			"allocated_count":           computedInt("Addresses currently allocated from the pool."),
			"allocations": schema.ListNestedAttribute{
				MarkdownDescription: "The pool's allocations (filtered by `status` when set).",
				Computed:            true,
				NestedObject:        schema.NestedAttributeObject{Attributes: ipamAllocationAttributes()},
			},
			"id": computedString("`<fabric>/<tenant>`."),
		},
	}
}

func (d *TenantIPAddressesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data TenantIPAddressesDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	fabric, diags := d.fabric(data.FabricName)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	res, err := d.client.GetAllTenantIPAddresses(ctx, fabric, data.TenantName.ValueString(), data.Status.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read tenant IP addresses", err.Error())
		return
	}
	data.FabricName = types.StringValue(fabric)
	data.PoolCidr = types.StringValue(res.Pool.Cidr)
	data.AllocatableAddressCount = types.Int64Value(res.Pool.AllocatableAddressCount)
	data.AllocatedCount = types.Int64Value(res.Pool.AllocatedCount)
	data.Allocations = allocationsToDS(res.Allocations)
	data.ID = types.StringValue(fabric + "/" + data.TenantName.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// =========================================================== tenant DHCP ===

var _ datasource.DataSource = &TenantDhcpDataSource{}

func NewTenantDhcpDataSource() datasource.DataSource {
	return &TenantDhcpDataSource{}
}

type TenantDhcpDataSource struct{ fmDataSource }

type dhcpSubnetDS struct {
	VlanID      types.Int64  `tfsdk:"vlan_id"`
	SubnetID    types.Int64  `tfsdk:"subnet_id"`
	NetworkName types.String `tfsdk:"network_name"`
	SubnetCidr  types.String `tfsdk:"subnet_cidr"`
	GatewayIP   types.String `tfsdk:"gateway_ip"`
	PoolStart   types.String `tfsdk:"pool_start"`
	PoolEnd     types.String `tfsdk:"pool_end"`
}

type dhcpLeaseDS struct {
	IPAddress            types.String `tfsdk:"ip_address"`
	MacAddress           types.String `tfsdk:"mac_address"`
	Hostname             types.String `tfsdk:"hostname"`
	State                types.String `tfsdk:"state"`
	ValidLifetimeSeconds types.Int64  `tfsdk:"valid_lifetime_seconds"`
	LastRenewedAt        types.String `tfsdk:"last_renewed_at"`
	ExpiresAt            types.String `tfsdk:"expires_at"`
}

type TenantDhcpDataSourceModel struct {
	FabricName      types.String  `tfsdk:"fabric_name"`
	TenantName      types.String  `tfsdk:"tenant_name"`
	Subnet          *dhcpSubnetDS `tfsdk:"subnet"`
	LiveInKea       types.Bool    `tfsdk:"live_in_kea"`
	LiveCheckError  types.String  `tfsdk:"live_check_error"`
	Leases          []dhcpLeaseDS `tfsdk:"leases"`
	LeaseCheckError types.String  `tfsdk:"lease_check_error"`
	ID              types.String  `tfsdk:"id"`
}

func (d *TenantDhcpDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tenant_dhcp"
}

func (d *TenantDhcpDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A tenant's DHCP subnet, whether the fabric's Kea server is actually serving it, and every lease handed out in it (GET `/fabrics/{fabric}/tenants/{tenant}/dhcp`, all pages). " +
			"Fails with 404 DHCP_SUBNET_NOT_FOUND when DHCP doesn't apply to the tenant (fabric IPAM not in DHCP mode, tenant without a VLAN or pool, or an unknown fabric/tenant). " +
			"Kea being unreachable is not an error: `live_in_kea`/`leases` are then null and the `*_check_error` attributes say why. Comparing `live_in_kea` with what you expect is how you spot drift that `fabricapi_dhcp_reconcile` repairs.",
		Attributes: map[string]schema.Attribute{
			"fabric_name": fabricNameDSAttribute(),
			"tenant_name": requiredDSString("Tenant name."),
			"subnet": schema.SingleNestedAttribute{
				MarkdownDescription: "The tenant's DHCP subnet as Fabric Manager derives it.",
				Computed:            true,
				Attributes: map[string]schema.Attribute{
					"vlan_id":      computedInt("Tenant VLAN."),
					"subnet_id":    computedInt("Kea subnet id."),
					"network_name": computedString("Kea shared-network name."),
					"subnet_cidr":  computedString("Subnet CIDR (the tenant's IPAM pool)."),
					"gateway_ip":   computedString("Gateway address."),
					"pool_start":   computedString("First address Kea hands out."),
					"pool_end":     computedString("Last address Kea hands out."),
				},
			},
			"live_in_kea":      schema.BoolAttribute{MarkdownDescription: "Whether Kea is currently serving the subnet. Null if Kea couldn't be queried.", Computed: true},
			"live_check_error": computedString("Why `live_in_kea` couldn't be determined, if it couldn't."),
			"leases": schema.ListNestedAttribute{
				MarkdownDescription: "Leases in the subnet, ordered by address. Null if Kea couldn't be queried.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"ip_address":             computedString("Leased address."),
						"mac_address":            computedString("Client MAC address."),
						"hostname":               computedString("Client hostname, if sent."),
						"state":                  computedString("`ASSIGNED`, `DECLINED`, `EXPIRED_RECLAIMED`, or `STATE_<n>`."),
						"valid_lifetime_seconds": computedInt("Lease lifetime."),
						"last_renewed_at":        computedString("Last renewal time."),
						"expires_at":             computedString("Expiry time."),
					},
				},
			},
			"lease_check_error": computedString("Why `leases` couldn't be read, if they couldn't."),
			"id":                computedString("`<fabric>/<tenant>`."),
		},
	}
}

func (d *TenantDhcpDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data TenantDhcpDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	fabric, diags := d.fabric(data.FabricName)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	res, err := d.client.GetTenantDhcp(ctx, fabric, data.TenantName.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read tenant DHCP", err.Error())
		return
	}
	s := res.Subnet
	data.FabricName = types.StringValue(fabric)
	data.Subnet = &dhcpSubnetDS{
		VlanID:      types.Int64Value(s.VlanID),
		SubnetID:    types.Int64Value(s.SubnetID),
		NetworkName: types.StringValue(s.NetworkName),
		SubnetCidr:  types.StringValue(s.SubnetCidr),
		GatewayIP:   types.StringValue(s.GatewayIP),
		PoolStart:   types.StringValue(s.PoolStart),
		PoolEnd:     types.StringValue(s.PoolEnd),
	}
	data.LiveInKea = types.BoolNull()
	if res.LiveInKea != nil {
		data.LiveInKea = types.BoolValue(*res.LiveInKea)
	}
	data.LiveCheckError = strOrNull(res.LiveCheckError)
	data.LeaseCheckError = strOrNull(res.LeaseCheckError)
	data.Leases = nil
	if res.Leases != nil {
		data.Leases = make([]dhcpLeaseDS, 0, len(res.Leases))
		for _, l := range res.Leases {
			data.Leases = append(data.Leases, dhcpLeaseDS{
				IPAddress:            types.StringValue(l.IPAddress),
				MacAddress:           ptrStrOrNull(l.MacAddress),
				Hostname:             ptrStrOrNull(l.Hostname),
				State:                types.StringValue(l.State),
				ValidLifetimeSeconds: types.Int64Value(l.ValidLifetimeSeconds),
				LastRenewedAt:        strOrNull(l.LastRenewedAt),
				ExpiresAt:            strOrNull(l.ExpiresAt),
			})
		}
	}
	data.ID = types.StringValue(fabric + "/" + data.TenantName.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// ======================================================= rail CIDRPools ===

var (
	_ datasource.DataSource = &RailCidrPoolsDataSource{}
	_ datasource.DataSource = &RailCidrPoolDataSource{}
)

func NewRailCidrPoolsDataSource() datasource.DataSource {
	return &RailCidrPoolsDataSource{}
}

func NewRailCidrPoolDataSource() datasource.DataSource {
	return &RailCidrPoolDataSource{}
}

type RailCidrPoolsDataSource struct{ fmDataSource }

type RailCidrPoolsDataSourceModel struct {
	FabricName types.String   `tfsdk:"fabric_name"`
	Rails      []types.String `tfsdk:"rails"`
	YAML       types.String   `tfsdk:"yaml"`
	ID         types.String   `tfsdk:"id"`
}

func (d *RailCidrPoolsDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_rail_cidr_pools"
}

func (d *RailCidrPoolsDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The rail CIDRPool manifest for every East-West rail on a fabric (GET `/fabrics/{fabric}/rails/cidrpools`): a multi-document YAML of `CIDRPool` objects, ready to hand to the network operator. " +
			"Only SpectrumX fabrics with L3 East-West host links have rails; others fail with 404 RAIL_CIDRPOOL_NOT_FOUND.",
		Attributes: map[string]schema.Attribute{
			"fabric_name": fabricNameDSAttribute(),
			"rails":       computedStrings("Rail names in manifest order, e.g. `r0`…`r7`, or `p0-r0`… on multi-plane fabrics."),
			"yaml":        computedString("The full manifest."),
			"id":          computedString("The fabric name."),
		},
	}
}

func (d *RailCidrPoolsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data RailCidrPoolsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	fabric, diags := d.fabric(data.FabricName)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	res, err := d.client.GetRailCidrPools(ctx, fabric)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read rail CIDRPools", err.Error())
		return
	}
	data.FabricName = types.StringValue(fabric)
	data.Rails = stringList(res.Rails)
	data.YAML = types.StringValue(res.YAML)
	data.ID = types.StringValue(fabric)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

type RailCidrPoolDataSource struct{ fmDataSource }

type RailCidrPoolDataSourceModel struct {
	FabricName types.String `tfsdk:"fabric_name"`
	RailName   types.String `tfsdk:"rail_name"`
	YAML       types.String `tfsdk:"yaml"`
	ID         types.String `tfsdk:"id"`
}

func (d *RailCidrPoolDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_rail_cidr_pool"
}

func (d *RailCidrPoolDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The CIDRPool document for one East-West rail (GET `/fabrics/{fabric}/rails/{rail}/cidrpool`). Fails with 404 INVALID_RAIL when the fabric has no such rail.",
		Attributes: map[string]schema.Attribute{
			"fabric_name": fabricNameDSAttribute(),
			"rail_name":   requiredDSString("Rail name, as listed by `fabricapi_rail_cidr_pools` (e.g. `r0`)."),
			"yaml":        computedString("The rail's CIDRPool document."),
			"id":          computedString("`<fabric>/<rail>`."),
		},
	}
}

func (d *RailCidrPoolDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data RailCidrPoolDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	fabric, diags := d.fabric(data.FabricName)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	res, err := d.client.GetRailCidrPool(ctx, fabric, data.RailName.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Unable to read rail CIDRPool", err.Error())
		return
	}
	data.FabricName = types.StringValue(fabric)
	data.YAML = types.StringValue(res.YAML)
	data.ID = types.StringValue(fabric + "/" + data.RailName.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
