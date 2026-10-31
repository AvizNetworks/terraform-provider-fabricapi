package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &DhcpActionResource{}

// NewDhcpReconcileResource runs POST /fabrics/{fabric}/dhcp/reconcile.
func NewDhcpReconcileResource() resource.Resource {
	return &DhcpActionResource{tenantScoped: false}
}

// NewTenantDhcpRetryResource runs POST /fabrics/{fabric}/tenants/{tenant}/dhcp/retry.
func NewTenantDhcpRetryResource() resource.Resource {
	return &DhcpActionResource{tenantScoped: true}
}

// DhcpActionResource is an action-style resource (like fabricapi_inventory_sync):
// Create re-applies the tenant DHCP subnets to the fabric's Kea server and
// records Fabric Manager's summary. It runs again whenever `triggers` (or the
// fabric/tenant) changes. Delete only forgets it.
type DhcpActionResource struct {
	client       *APIClient
	tenantScoped bool
}

type DhcpReconcileResourceModel struct {
	FabricName types.String `tfsdk:"fabric_name"`
	Triggers   types.Map    `tfsdk:"triggers"`
	Summary    types.String `tfsdk:"summary"`
	ID         types.String `tfsdk:"id"`
}

type TenantDhcpRetryResourceModel struct {
	FabricName types.String `tfsdk:"fabric_name"`
	TenantName types.String `tfsdk:"tenant_name"`
	Triggers   types.Map    `tfsdk:"triggers"`
	Summary    types.String `tfsdk:"summary"`
	ID         types.String `tfsdk:"id"`
}

func (r *DhcpActionResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	if r.tenantScoped {
		resp.TypeName = req.ProviderTypeName + "_tenant_dhcp_retry"
	} else {
		resp.TypeName = req.ProviderTypeName + "_dhcp_reconcile"
	}
}

func (r *DhcpActionResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		"fabric_name": schema.StringAttribute{
			MarkdownDescription: "Fabric to act on. If unset, uses the provider-level fabric.",
			Optional:            true,
			Computed:            true,
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.RequiresReplace(),
				stringplanmodifier.UseStateForUnknown(),
			},
		},
		"triggers": schema.MapAttribute{
			MarkdownDescription: "Arbitrary values; changing any of them runs the action again (e.g. `{ at = timestamp() }` to run on every apply).",
			Optional:            true,
			ElementType:         types.StringType,
			PlanModifiers:       []planmodifier.Map{mapplanmodifier.RequiresReplace()},
		},
		"summary": schema.StringAttribute{
			MarkdownDescription: "Fabric Manager's summary of what it applied to Kea, e.g. `fabric1: CONFIG_TEST OK; fabric1: CONFIG_SET OK; fabric1: CONFIG_WRITE OK`.",
			Computed:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"id": schema.StringAttribute{
			Computed:      true,
			PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
	}
	description := "Re-applies every tenant DHCP subnet so the fabric's Kea server matches Fabric Manager's records (`POST /fabrics/{fabric}/dhcp/reconcile`). " +
		"Only the tenant-owned part of the Kea config is rewritten. Fabric Manager already does this before every tenant create and delete; use this to repair drift in between. " +
		"Action-style resource: runs on create and whenever `triggers` changes; destroy only removes it from state."
	if r.tenantScoped {
		attrs["tenant_name"] = schema.StringAttribute{
			MarkdownDescription: "Tenant whose DHCP provisioning to retry. DHCP must apply to it (IPAM in DHCP mode, tenant has a VLAN and address pool), or Fabric Manager answers 404 DHCP_SUBNET_NOT_FOUND.",
			Required:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
		}
		description = "Retries a tenant's DHCP provisioning by re-applying the desired DHCP state to the fabric's Kea server (`POST /fabrics/{fabric}/tenants/{tenant}/dhcp/retry`). " +
			"Fabric Manager re-applies the whole fabric's tenant subnets, gated on DHCP applying to this tenant. " +
			"Action-style resource: runs on create and whenever `triggers` changes; destroy only removes it from state."
	}
	resp.Schema = schema.Schema{MarkdownDescription: description, Attributes: attrs}
}

func (r *DhcpActionResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *DhcpActionResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var (
		fabricOverride types.String
		tenant         string
		reconcile      DhcpReconcileResourceModel
		retry          TenantDhcpRetryResourceModel
		diags          diag.Diagnostics
	)
	if r.tenantScoped {
		diags = req.Plan.Get(ctx, &retry)
		fabricOverride, tenant = retry.FabricName, retry.TenantName.ValueString()
	} else {
		diags = req.Plan.Get(ctx, &reconcile)
		fabricOverride = reconcile.FabricName
	}
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	fabricName := resolveFabricName(r.client.Fabric, fabricOverride)
	if strings.TrimSpace(fabricName) == "" {
		resp.Diagnostics.AddError("Missing fabric", "Set `fabric_name` on this resource or `fabric` on the provider.")
		return
	}

	if r.tenantScoped {
		summary, err := r.client.RetryTenantDhcp(ctx, fabricName, tenant)
		if err != nil {
			resp.Diagnostics.AddError("Tenant DHCP retry failed", err.Error())
			return
		}
		retry.FabricName = types.StringValue(fabricName)
		retry.Summary = types.StringValue(summary)
		retry.ID = types.StringValue(fmt.Sprintf("dhcpretry:%s/%s", fabricName, tenant))
		resp.Diagnostics.Append(resp.State.Set(ctx, &retry)...)
		return
	}

	summary, err := r.client.ReconcileFabricDhcp(ctx, fabricName)
	if err != nil {
		resp.Diagnostics.AddError("DHCP reconcile failed", err.Error())
		return
	}
	reconcile.FabricName = types.StringValue(fabricName)
	reconcile.Summary = types.StringValue(summary)
	reconcile.ID = types.StringValue(fmt.Sprintf("dhcpreconcile:%s", fabricName))
	resp.Diagnostics.Append(resp.State.Set(ctx, &reconcile)...)
}

func (r *DhcpActionResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// Nothing to read back: the action's result is what Create recorded.
	resp.State.Raw = req.State.Raw
}

func (r *DhcpActionResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	// Every input forces replacement, so Update is never planned.
	resp.Diagnostics.AddError("Update not supported", "Change `triggers` to run the action again.")
}

func (r *DhcpActionResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.State.RemoveResource(ctx)
}
