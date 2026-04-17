package provider

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &VpcPeeringResource{}

func NewVpcPeeringResource() resource.Resource {
	return &VpcPeeringResource{}
}

type VpcPeeringResource struct {
	client *APIClient
}

type VpcPeeringResourceModel struct {
	TenantName   types.String `tfsdk:"tenant_name"`
	TargetFabric types.String `tfsdk:"target_fabric"`
	Name         types.String `tfsdk:"name"`

	// Optional override for the fabric where the tenant lives.
	// If unset, we use the provider's configured fabric.
	TenantFabric types.String `tfsdk:"tenant_fabric"`

	DeleteOnDestroy types.Bool `tfsdk:"delete_on_destroy"`

	Prefer          types.String `tfsdk:"prefer"`
	WebhooksEnabled types.Bool   `tfsdk:"webhooks_enabled"`
	WebhookURL      types.String `tfsdk:"webhook_url"`
	WebhookEvents   types.List   `tfsdk:"webhook_events"`
	OperationID     types.String `tfsdk:"operation_id"`

	// Resolved from GET /fabrics and GET /{fabric}/tenants/{tenant}.
	VpcName     types.String `tfsdk:"vpcname"`
	PeerVpcName types.String `tfsdk:"peervpcname"`

	ID types.String `tfsdk:"id"`
}

func (r *VpcPeeringResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vpcpeering"
}

func (r *VpcPeeringResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fabric API VPC Peering Resource",
		Attributes: map[string]schema.Attribute{
			"tenant_name": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					// Tenant name change requires a new peering.
					stringplanmodifier.RequiresReplace(),
				},
			},
			"target_fabric": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Fabric passed to the VPC peering API. Omit to use provider fabric (FABRIC_NAME / provider \"fabric\"); stored after apply.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					// When omitted, keep prior resolved value so repeated applies don't
					// force replacement due to unknown planned values.
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"tenant_fabric": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Fabric for tenant lookup. Omit to use provider fabric; stored after apply.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"delete_on_destroy": schema.BoolAttribute{
				Optional: true,
			},

			// Kept for forward compatibility (async/webhooks), but current Create path
			// works fine even when these are omitted (defaults are stored in state).
			"prefer": schema.StringAttribute{
				MarkdownDescription: "Prefer mode: respond-sync (default) or respond-async (HTTP Prefer). Underscore forms are accepted and normalized.",
				Optional:            true,
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"webhooks_enabled": schema.BoolAttribute{
				MarkdownDescription: "When prefer is respond-async, set true to include enableWebhook, webhookUrl, webhookEvents in the request body. If unset, false is used.",
				Optional:            true,
				Computed:            true,
			},
			"webhook_url": schema.StringAttribute{
				MarkdownDescription: "Webhook receiver URL (when prefer is respond-async and webhooks_enabled=true). If unset, defaults to http://localhost:8787/test/webhook-receiver in the client when needed.",
				Optional:            true,
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"webhook_events": schema.ListAttribute{
				MarkdownDescription: "Webhook events (required when prefer is respond-async and webhooks_enabled=true).",
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
			},
			"operation_id": schema.StringAttribute{
				MarkdownDescription: "Async operation id when the API returns 202 (useful for GET /operations/{id} when webhooks_enabled=false).",
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},

			"vpcname": schema.StringAttribute{
				Computed: true,
			},
			"peervpcname": schema.StringAttribute{
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					// When omitted, keep computed value from state to avoid replacement.
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"id": schema.StringAttribute{
				Computed: true,
			},
		},
	}
}

func (r *VpcPeeringResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *VpcPeeringResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data VpcPeeringResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tenantFabric := resolveVpcPeeringFabric(r.client.Fabric, data.TenantFabric)
	targetFab := resolveVpcPeeringFabric(r.client.Fabric, data.TargetFabric)
	if tenantFabric == "" || targetFab == "" {
		resp.Diagnostics.AddError(
			"Missing fabric",
			"Set target_fabric and/or tenant_fabric, or configure provider fabric via FABRIC_NAME / provider \"fabric\".",
		)
		return
	}

	// --- PRE-VALIDATION: FABRICS MUST EXIST ---
	// QA expectation: fail fast with clear message if the fabric(s) don't exist,
	// instead of timing out or failing later on missing tenant networking data.
	fabrics, err := r.client.GetFabrics(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list fabrics: %s", err))
		return
	}
	fabricExists := func(name string) bool {
		for _, f := range fabrics {
			if f.FabricName == name {
				return true
			}
		}
		return false
	}
	missingFabrics := make([]string, 0, 2)
	if !fabricExists(tenantFabric) {
		missingFabrics = append(missingFabrics, tenantFabric)
	}
	if targetFab != tenantFabric && !fabricExists(targetFab) {
		missingFabrics = append(missingFabrics, targetFab)
	}
	if len(missingFabrics) > 0 {
		resp.Diagnostics.AddError(
			"Fabric not found",
			fmt.Sprintf(
				"VPC peering cannot be created because the fabric(s) do not exist: %v. "+
					"Verify FABRIC_NAME/provider fabric and the target_fabric/tenant_fabric values.",
				missingFabrics,
			),
		)
		return
	}

	// Peer/storage VPC name: use explicit Terraform value when set; otherwise resolve from
	// GET /fabrics → defaultStorageName for target_fabric (real simulator), with legacy fallback.
	storageVPC := "Storage-VPC"
	if !data.PeerVpcName.IsNull() && !data.PeerVpcName.IsUnknown() && data.PeerVpcName.ValueString() != "" {
		storageVPC = data.PeerVpcName.ValueString()
	} else {
		if fabrics, err := r.client.GetFabrics(ctx); err == nil {
			for _, f := range fabrics {
				if f.FabricName == targetFab && f.DefaultStorageName != "" {
					storageVPC = f.DefaultStorageName
					break
				}
			}
		}
	}

	// --- PRE-VALIDATION: TENANT MUST EXIST ---
	// Resolve tenant.vnets.name from GET /fabrics/{fabric}/tenants/{tenant}.
	tenantName := data.TenantName.ValueString()
	tenant, err := r.client.GetTenantWithFabric(tenantFabric, tenantName)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get tenant: %s", err))
		return
	}
	if tenant == nil {
		resp.Diagnostics.AddError(
			"Tenant not found",
			fmt.Sprintf("VPC peering cannot be created because tenant %q does not exist in fabric %q.", tenantName, tenantFabric),
		)
		return
	}

	// If tenant exists but networking isn't ready yet, wait briefly and retry once.
	if tenant.VnetsName == "" {
		if err := r.client.WaitForTenantReady(ctx, tenantFabric, tenantName, 60*time.Second); err != nil {
			resp.Diagnostics.AddError(
				"Tenant not ready",
				fmt.Sprintf("Tenant %q exists in fabric %q but is not readable/ready yet (needed for VPC peering): %s", tenantName, tenantFabric, err),
			)
			return
		}
		tenant, err = r.client.GetTenantWithFabric(tenantFabric, tenantName)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get tenant after readiness wait: %s", err))
			return
		}
	}
	if tenant == nil || tenant.VnetsName == "" {
		resp.Diagnostics.AddError(
			"Missing tenant vnets name",
			fmt.Sprintf(
				"VPC peering cannot be created: required tenant VNet (tenant.vnets.name) not found for tenant %q in fabric %q. "+
					"Ensure tenant networking/VNet provisioning is completed before creating VPC peering.",
				tenantName, tenantFabric,
			),
		)
		return
	}

	vpcName := tenant.VnetsName
	// Real APIs return the exact vnet name the controller expects; do not rewrite it by default.
	// Mock backends that omit the "-north-south" suffix can opt in:
	//   FABRICAPI_VPC_APPEND_NORTH_SOUTH=1
	appendNorthSouth := strings.ToLower(os.Getenv("FABRICAPI_VPC_APPEND_NORTH_SOUTH"))
	if appendNorthSouth == "1" || appendNorthSouth == "true" || appendNorthSouth == "yes" {
		if vpcName != "" && !strings.HasSuffix(vpcName, "-north-south") {
			vpcName = vpcName + "-north-south"
		}
	}

	reqBody := VpcPeeringRequest{
		Name:        data.Name.ValueString(),
		VpcName:     vpcName,
		PeerVpcName: storageVPC,
	}

	// Current backend: VPC peering does NOT integrate webhooks. Use sync API call.
	respBody, err := r.client.CreateVpcPeeringWithResponse(ctx, targetFab, reqBody)
	if err != nil {
		if vpcPeeringErrMeansAlreadyExists(err) {
			fmt.Fprintf(os.Stderr,
				"[fabricapi] VPC peering: POST returned already-exists/conflict; treating as success (idempotent). %s\n",
				err.Error(),
			)
			resp.Diagnostics.AddWarning("VPC peering already exists", err.Error())
		} else {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create vpcpeering: %s", err))
			return
		}
	} else {
		fmt.Fprintf(os.Stderr,
			"[fabricapi] VPC peering done: GET tenant + POST vpcpeering succeeded (name=%s target_fabric=%s vpcname=%s peervpcname=%s)\n",
			reqBody.Name, targetFab, reqBody.VpcName, reqBody.PeerVpcName,
		)
		if respBody != "" {
			fmt.Fprintf(os.Stderr, "[fabricapi] vpcpeering backend response: %s\n", respBody)
			resp.Diagnostics.AddWarning("VPC peering created", respBody)
		}
	}

	// Store computed/resolved fields in state.
	data.VpcName = types.StringValue(reqBody.VpcName)
	data.PeerVpcName = types.StringValue(reqBody.PeerVpcName)
	data.ID = types.StringValue(data.Name.ValueString())

	// Persist effective fabrics so state matches API usage.
	data.TargetFabric = types.StringValue(targetFab)
	data.TenantFabric = types.StringValue(tenantFabric)

	// Persist defaults for forward-compat fields so state is stable.
	if data.Prefer.IsNull() || strings.TrimSpace(data.Prefer.ValueString()) == "" {
		data.Prefer = types.StringValue("respond-sync")
	}
	if data.WebhooksEnabled.IsNull() || data.WebhooksEnabled.IsUnknown() {
		data.WebhooksEnabled = types.BoolValue(false)
	}
	if data.WebhookURL.IsNull() || strings.TrimSpace(data.WebhookURL.ValueString()) == "" {
		data.WebhookURL = types.StringValue("http://localhost:8787/test/webhook-receiver")
	}
	if data.WebhookEvents.IsNull() || data.WebhookEvents.IsUnknown() {
		evList, diags := types.ListValueFrom(ctx, types.StringType, []string{})
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		data.WebhookEvents = evList
	}
	data.OperationID = types.StringNull()

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// vpcPeeringErrMeansAlreadyExists treats common duplicate / idempotent responses as success so
// apply does not fail when peering (or same name) already exists on the API but Terraform is creating again.
func vpcPeeringErrMeansAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "409") ||
		strings.Contains(msg, "already exist") ||
		strings.Contains(msg, "duplicate") ||
		strings.Contains(msg, "conflict") ||
		strings.Contains(msg, "identical") ||
		strings.Contains(msg, "not modified")
}

func resolveVpcPeeringFabric(providerFabric string, override types.String) string {
	if override.IsNull() || override.IsUnknown() {
		return providerFabric
	}
	s := override.ValueString()
	if s == "" {
		return providerFabric
	}
	return s
}

func (r *VpcPeeringResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data VpcPeeringResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if r.client == nil {
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}

	tenantFabric := resolveVpcPeeringFabric(r.client.Fabric, data.TenantFabric)
	tenantName := data.TenantName.ValueString()
	if tenantName == "" {
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}

	tenant, err := r.client.GetTenantWithFabric(tenantFabric, tenantName)
	if err != nil {
		resp.Diagnostics.AddWarning(
			"VPC peering read skipped",
			fmt.Sprintf("Could not verify tenant %q in fabric %q: %s. Terraform state left unchanged.", tenantName, tenantFabric, err),
		)
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}
	if tenant == nil {
		// Tenant was deleted; drop peering from state so apply can create again.
		fmt.Fprintf(os.Stderr,
			"[fabricapi] vpcpeering Read: tenant %q not found in fabric %q (GET 404); removing fabricapi_vpcpeering from Terraform state\n",
			tenantName, tenantFabric,
		)
		resp.Diagnostics.AddWarning(
			"VPC peering removed from Terraform state",
			fmt.Sprintf("Tenant %q not found in fabric %q. Run apply when the tenant exists again to recreate peering.", tenantName, tenantFabric),
		)
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *VpcPeeringResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError(
		"Update not supported",
		"Changing VPC peering inputs requires recreating the resource.",
	)
}

func (r *VpcPeeringResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data VpcPeeringResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// For now we don't try to delete automatically because the API delete endpoint
	// isn't implemented in this provider yet. Keep destroy reliable.
	if !data.DeleteOnDestroy.IsNull() && data.DeleteOnDestroy.ValueBool() {
		resp.Diagnostics.AddWarning(
			"Delete on destroy not implemented",
			"This provider currently creates vpcpeering but does not delete it.",
		)
	}

	// Even though we don't delete the remote resource, remove it from Terraform state
	// so that destroy completes cleanly and future plans are accurate.
	resp.State.RemoveResource(ctx)
}