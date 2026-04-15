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
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"name": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"tenant_fabric": schema.StringAttribute{
				Optional: true,
			},
			"delete_on_destroy": schema.BoolAttribute{
				Optional: true,
			},
			"prefer": schema.StringAttribute{
				MarkdownDescription: "Prefer mode: respond-sync (default) or respond-async (HTTP Prefer). Underscore forms are accepted and normalized.",
				Optional:            true,
			},
			"webhooks_enabled": schema.BoolAttribute{
				MarkdownDescription: "When prefer is respond-async, set true to include enableWebhook, webhookUrl, webhookEvents in the request body. If unset, false is used.",
				Optional:            true,
			},
			"webhook_url": schema.StringAttribute{
				MarkdownDescription: "Webhook receiver URL (when prefer is respond-async and webhooks_enabled=true). If unset, defaults to http://localhost:8787/test/webhook-receiver in the client when needed.",
				Optional:            true,
			},
			"webhook_events": schema.ListAttribute{
				MarkdownDescription: "Webhook events (required when prefer is respond-async and webhooks_enabled=true).",
				Optional:            true,
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

	tenantFabric := r.client.Fabric
	if !data.TenantFabric.IsNull() && data.TenantFabric.ValueString() != "" {
		tenantFabric = data.TenantFabric.ValueString()
	}

	// Peer/storage VPC name: use explicit Terraform value when set; otherwise resolve from
	// GET /fabrics → defaultStorageName for target_fabric (real simulator), with legacy fallback.
	storageVPC := "Storage-VPC"
	if !data.PeerVpcName.IsNull() && !data.PeerVpcName.IsUnknown() && data.PeerVpcName.ValueString() != "" {
		storageVPC = data.PeerVpcName.ValueString()
	} else {
		targetFab := data.TargetFabric.ValueString()
		if fabrics, err := r.client.GetFabrics(ctx); err == nil {
			for _, f := range fabrics {
				if f.FabricName == targetFab && f.DefaultStorageName != "" {
					storageVPC = f.DefaultStorageName
					break
				}
			}
		}
	}

	// Resolve tenant.vnets.name from GET /fabrics/{fabric}/tenants/{tenant}.
	tenant, err := r.client.GetTenantWithFabric(tenantFabric, data.TenantName.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to get tenant: %s", err))
		return
	}
	if tenant == nil || tenant.VnetsName == "" {
		resp.Diagnostics.AddError(
			"Missing tenant vnets name",
			fmt.Sprintf("Could not find tenant.vnets.name for tenant %s in fabric %s", data.TenantName.ValueString(), tenantFabric),
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

	opts := &requestOptions{
		Prefer:          prefer,
		WebhooksEnabled: webhooksEnabled,
		WebhookURL:      webhookURL,
		WebhookEvents:   webhookEvents,
	}

	respBody, opID, err := r.client.CreateVpcPeeringWithResponseAndOptions(ctx, data.TargetFabric.ValueString(), reqBody, opts)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create vpcpeering: %s", err))
		return
	}

	if strings.EqualFold(preferHeaderValue(prefer), "respond-async") && !webhooksEnabled && strings.TrimSpace(opID) != "" {
		if err := r.client.WaitForOperationDone(ctx, opID, 60*time.Minute); err != nil {
			resp.Diagnostics.AddError("Async operation failed", err.Error())
			return
		}
	}

	// Visible during terraform apply / pytest integration (provider stderr).
	fmt.Fprintf(os.Stderr,
		"[fabricapi] VPC peering done: GET tenant + POST vpcpeering succeeded (name=%s target_fabric=%s vpcname=%s peervpcname=%s)\n",
		reqBody.Name, data.TargetFabric.ValueString(), reqBody.VpcName, reqBody.PeerVpcName,
	)
	if respBody != "" {
		fmt.Fprintf(os.Stderr, "[fabricapi] vpcpeering backend response: %s\n", respBody)
		// Warnings appear in normal `terraform apply` output; provider stderr is often easy to miss.
		resp.Diagnostics.AddWarning("VPC peering created", respBody)
	}

	data.VpcName = types.StringValue(reqBody.VpcName)
	data.PeerVpcName = types.StringValue(reqBody.PeerVpcName)
	data.ID = types.StringValue(data.Name.ValueString())
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

func (r *VpcPeeringResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// For now, keep state as-is. If API returns non-trivial identifiers,
	// we can implement a GET later.
	var data VpcPeeringResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
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
