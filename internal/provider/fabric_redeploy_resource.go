package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"gopkg.in/yaml.v3"
)

var _ resource.Resource = &FabricRedeployResource{}

func NewFabricRedeployResource() resource.Resource {
	return &FabricRedeployResource{}
}

// FabricRedeployResource is an action-style resource (like fabricapi_inventory_sync): its
// Create re-pushes an already-Deployed fabric's current saved config, matching the ONES UI's
// Re-Deploy button (RedeployFabricButton.jsx) — recovering devices that failed config or
// operational validation without redoing the full Deploy Fabric sequence.
//
// Unlike fabricapi_fabric_deploy, this takes only fabric_name: no devices, no custom_yaml.
// Re-deploy never uploads new credentials — it SSHes into every device in the fabric's last
// saved YAML using whatever is already stored server-side, then re-pushes that same YAML
// as-is. If you need to change devices or push a different YAML, that's a new deploy
// (fabricapi_fabric_deploy), not a redeploy.
//
// Sequence:
//  1. POST {config_endpoint}/api/config/redeployprecheck -> SSH-check every device in the
//     fabric's last saved YAML with its already-stored credentials
//  2. GET  {config_endpoint}/fabrics/{name}               -> fetch that same saved YAML
//     POST {config_endpoint}/api/config with redeploy=true -> re-push it
//
// A device whose stored login now fails always blocks the redeploy (credential problems can't
// be fixed by redeploying); an unreachable-but-not-auth-failed device only blocks when
// allow_unreachable is false. To redeploy again, replace or re-apply this resource.
type FabricRedeployResource struct {
	client *APIClient
}

type FabricRedeployResourceModel struct {
	FabricName       types.String `tfsdk:"fabric_name"`
	Instance         types.String `tfsdk:"instance"`
	AllowUnreachable types.Bool   `tfsdk:"allow_unreachable"`
	Message          types.String `tfsdk:"message"`
	ID               types.String `tfsdk:"id"`
}

func (r *FabricRedeployResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_fabric_redeploy"
}

func (r *FabricRedeployResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Re-pushes an already-Deployed fabric's current saved config, matching the ONES UI's Re-Deploy button — recovers devices that failed config/operational validation without a full redeploy (no devices, no custom_yaml; it reuses whatever is already stored server-side from the last real deploy). Requires provider `config_endpoint`. Action-style resource: the redeploy runs on create; to run it again, replace or re-apply (e.g. `terraform apply -replace=...`).",
		Attributes: map[string]schema.Attribute{
			"fabric_name": schema.StringAttribute{
				MarkdownDescription: "Name of an already-Deployed fabric to redeploy (e.g. fabricapi_fabric.this.name).",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"instance": schema.StringAttribute{
				MarkdownDescription: "FM instance used for the config push (POST /api/config `instance` field). Should match the `instance` used for the original deploy. Defaults to \"fm\" if unset.",
				Optional:            true,
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"allow_unreachable": schema.BoolAttribute{
				MarkdownDescription: "When the precheck finds devices that are unreachable but not an outright login failure, proceed anyway (those specific devices will fail again; others may still succeed) — matching the UI's \"Re-Deploy Anyway\". Never bypasses an actual credential/login failure. Defaults to false.",
				Optional:            true,
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.RequiresReplace(),
				},
			},
			"message": schema.StringAttribute{
				MarkdownDescription: "Response message from the config push.",
				Computed:            true,
			},
			"id": schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *FabricRedeployResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *FabricRedeployResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data FabricRedeployResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fabricName := strings.TrimSpace(data.FabricName.ValueString())
	if fabricName == "" {
		resp.Diagnostics.AddError("Missing fabric_name", "Set `fabric_name` to an already-Deployed fabric.")
		return
	}
	instance := "fm"
	if !data.Instance.IsNull() && !data.Instance.IsUnknown() && strings.TrimSpace(data.Instance.ValueString()) != "" {
		instance = data.Instance.ValueString()
	}
	allowUnreachable := !data.AllowUnreachable.IsNull() && !data.AllowUnreachable.IsUnknown() && data.AllowUnreachable.ValueBool()

	// Pre-check: SSH into every device in the fabric's last saved YAML with its already-
	// stored credentials. Matches the ONES UI's Re-Deploy button exactly, including which
	// failures block outright.
	precheck, err := r.client.RedeployPrecheck(ctx, fabricName)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Redeploy precheck failed for fabric %q: %s", fabricName, err))
		return
	}
	var blocking, unreachable []string
	for _, d := range precheck {
		if d.OK {
			continue
		}
		line := fmt.Sprintf("%s (%s, %s): %s", d.Hostname, d.IP, d.Role, d.Error)
		if d.Reason == "unreachable" {
			unreachable = append(unreachable, line)
		} else {
			blocking = append(blocking, line)
		}
	}
	if len(blocking) > 0 {
		resp.Diagnostics.AddError(
			"Redeploy cannot proceed",
			fmt.Sprintf("Login failed or credentials are missing for one or more devices in fabric %q — a redeploy re-pushes the existing config and never uploads new credentials, so this must be fixed via fabricapi_fabric_deploy with corrected `devices`, not by retrying the redeploy:\n- %s", fabricName, strings.Join(blocking, "\n- ")),
		)
		return
	}
	if len(unreachable) > 0 && !allowUnreachable {
		resp.Diagnostics.AddError(
			"Redeploy precheck found unreachable devices",
			fmt.Sprintf("One or more devices in fabric %q are not reachable (these will fail again if pushed):\n- %s\n\nSet allow_unreachable = true to redeploy the rest anyway, matching the ONES UI's \"Re-Deploy Anyway\".", fabricName, strings.Join(unreachable, "\n- ")),
		)
		return
	}
	if len(unreachable) > 0 {
		resp.Diagnostics.AddWarning(
			"Redeploying with unreachable devices",
			fmt.Sprintf("Proceeding past unreachable devices in fabric %q (allow_unreachable = true) — these will fail again:\n- %s", fabricName, strings.Join(unreachable, "\n- ")),
		)
	}

	// Re-push the fabric's current saved YAML as-is — no devices to inject credentials
	// from, unlike fabricapi_fabric_deploy: redeploy only ever reuses what's already stored.
	rawYAML, err := r.client.GetFabricYaml(ctx, fabricName)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to fetch saved fabric YAML for %q: %s", fabricName, err))
		return
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(rawYAML), &doc); err != nil {
		resp.Diagnostics.AddError("YAML Parse Error", fmt.Sprintf("Unable to parse saved fabric YAML for %q: %s", fabricName, err))
		return
	}
	orderedJSON, err := yamlNodeToOrderedJSON(&doc)
	if err != nil {
		resp.Diagnostics.AddError("YAML Parse Error", fmt.Sprintf("Unable to convert saved fabric YAML for %q to JSON: %s", fabricName, err))
		return
	}

	pushResp, err := r.client.PushFabricConfig(ctx, fabricName, instance, orderedJSON, true)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to redeploy config for fabric %q: %s", fabricName, err))
		return
	}

	data.Instance = types.StringValue(instance)
	if pushResp != "" {
		data.Message = types.StringValue(pushResp)
	} else {
		data.Message = types.StringNull()
	}
	data.ID = types.StringValue(fmt.Sprintf("fabricredeploy:%s", fabricName))

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *FabricRedeployResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data FabricRedeployResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *FabricRedeployResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Update not supported", "Replace or re-apply this resource to redeploy again.")
}

func (r *FabricRedeployResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.State.RemoveResource(ctx)
}
