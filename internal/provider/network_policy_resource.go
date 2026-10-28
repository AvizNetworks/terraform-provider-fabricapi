package provider

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var (
	_ resource.Resource                   = &NetworkPolicyResource{}
	_ resource.ResourceWithImportState    = &NetworkPolicyResource{}
	_ resource.ResourceWithModifyPlan     = &NetworkPolicyResource{}
	_ resource.ResourceWithValidateConfig = &NetworkPolicyResource{}
)

func NewNetworkPolicyResource() resource.Resource {
	return &NetworkPolicyResource{}
}

const (
	networkPolicyTypeTenant  = "tenant"
	networkPolicyTypeService = "service"

	// maxNetworkPolicyNameLength is FM's limit: the name is embedded in the
	// prefix-list/route-map names pushed to the devices.
	maxNetworkPolicyNameLength = 24
)

// NetworkPolicyResource manages a Fabric Manager network policy:
// POST/PATCH/DELETE /fabrics/{fabric}/networkpolicy[/{name}], read back by
// filtering GET /fabrics/{fabric}/networkpolicy (FM has no single-policy GET).
//
// FM's PATCH can only add endpoints. ModifyPlan therefore plans an in-place
// update only when the new endpoint sets are supersets of the old ones and FM
// will accept the patch; anything else (removing an endpoint, changing a side's
// type/tenant/service) is planned as a replacement.
type NetworkPolicyResource struct {
	client *APIClient
}

type NetworkPolicySideModel struct {
	Type        types.String `tfsdk:"type"`
	TenantName  types.String `tfsdk:"tenant_name"`
	ServiceName types.String `tfsdk:"service_name"`
	Endpoints   types.Set    `tfsdk:"endpoints"`
}

type NetworkPolicyResourceModel struct {
	FabricName  types.String           `tfsdk:"fabric_name"`
	Name        types.String           `tfsdk:"name"`
	Source      NetworkPolicySideModel `tfsdk:"source"`
	Destination NetworkPolicySideModel `tfsdk:"destination"`
	ID          types.String           `tfsdk:"id"`
}

var networkPolicySideAttrTypes = map[string]attr.Type{
	"type":         types.StringType,
	"tenant_name":  types.StringType,
	"service_name": types.StringType,
	"endpoints":    types.SetType{ElemType: types.StringType},
}

func (r *NetworkPolicyResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_network_policy"
}

func networkPolicySideSchema(which string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		MarkdownDescription: fmt.Sprintf("The policy's %s.", which),
		Required:            true,
		Attributes: map[string]schema.Attribute{
			"type": schema.StringAttribute{
				MarkdownDescription: "`tenant` (a whole tenant) or `service` (specific endpoints inside a tenant). Changing it replaces the policy.",
				Required:            true,
				Validators:          []validator.String{stringvalidator.OneOf(networkPolicyTypeTenant, networkPolicyTypeService)},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"tenant_name": schema.StringAttribute{
				MarkdownDescription: "Tenant this side belongs to. Must exist in the fabric. Changing it replaces the policy.",
				Required:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"service_name": schema.StringAttribute{
				MarkdownDescription: "Optional free-text service label (for `service` sides). Changing it replaces the policy.",
				Optional:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"endpoints": schema.SetAttribute{
				MarkdownDescription: "Endpoint addresses (e.g. `192.2.127.129/32`). Required for a `service` destination; must be unset on a `tenant` side. " +
					"Adding endpoints updates the policy in place (PATCH); removing any replaces it, since Fabric Manager cannot remove endpoints from a policy.",
				Optional:    true,
				ElementType: types.StringType,
			},
		},
	}
}

func (r *NetworkPolicyResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a network policy: allows traffic from a source (tenant or service endpoints) to a destination " +
			"(POST/PATCH/DELETE `/fabrics/{fabric}/networkpolicy`). Fabric Manager pushes the config to the fabric's border/combined " +
			"leaves, DPUs or spines before answering. Only supported on fabrics with External tenant control.\n\n" +
			"Supported shapes: tenant → tenant (whole-tenant route leak, no endpoints), tenant → service, service → service. " +
			"A policy whose source and destination tenant are the same is recorded by Fabric Manager without pushing device config.",
		Attributes: map[string]schema.Attribute{
			"fabric_name": schema.StringAttribute{
				MarkdownDescription: "Fabric to create the policy in. If unset, uses the provider-level fabric.",
				Optional:            true,
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				MarkdownDescription: fmt.Sprintf("Policy name, unique within the fabric, at most %d characters.", maxNetworkPolicyNameLength),
				Required:            true,
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, maxNetworkPolicyNameLength),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"source":      networkPolicySideSchema("source"),
			"destination": networkPolicySideSchema("destination"),
			"id": schema.StringAttribute{
				MarkdownDescription: "`<fabric>/<name>`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *NetworkPolicyResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ValidateConfig enforces the policy shapes Fabric Manager accepts, so a bad
// combination fails at plan time instead of as an HTTP 500 after FM has
// started touching devices.
func (r *NetworkPolicyResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data NetworkPolicyResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	src, dst := data.Source, data.Destination
	if src.Type.IsUnknown() || dst.Type.IsUnknown() {
		return
	}
	if dst.Type.ValueString() == networkPolicyTypeTenant && src.Type.ValueString() != networkPolicyTypeTenant {
		resp.Diagnostics.AddAttributeError(path.Root("source").AtName("type"), "Unsupported network policy shape",
			fmt.Sprintf("A tenant destination requires a tenant source; got source type %q.", src.Type.ValueString()))
	}
	for _, side := range []struct {
		name string
		m    NetworkPolicySideModel
	}{{"source", src}, {"destination", dst}} {
		if side.m.Endpoints.IsUnknown() {
			continue
		}
		hasEndpoints := !side.m.Endpoints.IsNull() && len(side.m.Endpoints.Elements()) > 0
		if side.m.Type.ValueString() == networkPolicyTypeTenant && hasEndpoints {
			resp.Diagnostics.AddAttributeError(path.Root(side.name).AtName("endpoints"), "Endpoints not allowed on a tenant side",
				fmt.Sprintf("%s.endpoints must be unset when %s.type is \"tenant\": a tenant side covers the whole tenant.", side.name, side.name))
		}
		if side.name == "destination" && side.m.Type.ValueString() == networkPolicyTypeService && !hasEndpoints {
			resp.Diagnostics.AddAttributeError(path.Root("destination").AtName("endpoints"), "Missing destination endpoints",
				"destination.endpoints is required when destination.type is \"service\".")
		}
	}
}

// endpointSet returns a side's endpoints as a set of strings (nil-safe).
func endpointSet(ctx context.Context, s types.Set) (map[string]struct{}, diag.Diagnostics) {
	out := map[string]struct{}{}
	if s.IsNull() || s.IsUnknown() {
		return out, nil
	}
	var items []string
	diags := s.ElementsAs(ctx, &items, false)
	for _, it := range items {
		out[it] = struct{}{}
	}
	return out, diags
}

// addedEndpoints returns the elements of next missing from prev, sorted for a
// stable request body, and whether any element of prev is missing from next.
func addedEndpoints(prev, next map[string]struct{}) (added []string, removed bool) {
	for e := range prev {
		if _, ok := next[e]; !ok {
			removed = true
		}
	}
	for e := range next {
		if _, ok := prev[e]; !ok {
			added = append(added, e)
		}
	}
	sort.Strings(added)
	return added, removed
}

// ModifyPlan plans a replacement whenever FM's add-only PATCH can't express the
// change: an endpoint was removed, or the only additions are on the source of a
// service destination (FM's patch validation requires new destination
// endpoints in that case).
func (r *NetworkPolicyResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var state, plan NetworkPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() || plan.Source.Endpoints.IsUnknown() || plan.Destination.Endpoints.IsUnknown() {
		return
	}

	prevSrc, d := endpointSet(ctx, state.Source.Endpoints)
	resp.Diagnostics.Append(d...)
	nextSrc, d := endpointSet(ctx, plan.Source.Endpoints)
	resp.Diagnostics.Append(d...)
	prevDst, d := endpointSet(ctx, state.Destination.Endpoints)
	resp.Diagnostics.Append(d...)
	nextDst, d := endpointSet(ctx, plan.Destination.Endpoints)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	addedSrc, removedSrc := addedEndpoints(prevSrc, nextSrc)
	addedDst, removedDst := addedEndpoints(prevDst, nextDst)
	if removedSrc {
		resp.RequiresReplace = append(resp.RequiresReplace, path.Root("source").AtName("endpoints"))
	}
	if removedDst {
		resp.RequiresReplace = append(resp.RequiresReplace, path.Root("destination").AtName("endpoints"))
	}
	if !removedSrc && !removedDst && len(addedSrc) > 0 && len(addedDst) == 0 &&
		plan.Destination.Type.ValueString() != networkPolicyTypeTenant {
		resp.RequiresReplace = append(resp.RequiresReplace, path.Root("source").AtName("endpoints"))
	}
}

func (r *NetworkPolicyResource) fabricName(data NetworkPolicyResourceModel) (string, diag.Diagnostics) {
	var diags diag.Diagnostics
	name := resolveFabricName(r.client.Fabric, data.FabricName)
	if strings.TrimSpace(name) == "" {
		diags.AddError("Missing fabric", "Set `fabric_name` on this resource or `fabric` on the provider.")
	}
	return name, diags
}

func sideFromModel(ctx context.Context, m NetworkPolicySideModel, endpoints []string) NetworkPolicySide {
	return NetworkPolicySide{
		Type:        m.Type.ValueString(),
		TenantName:  m.TenantName.ValueString(),
		ServiceName: m.ServiceName.ValueString(),
		Endpoints:   endpoints,
	}
}

func setValues(ctx context.Context, s types.Set) ([]string, diag.Diagnostics) {
	if s.IsNull() || s.IsUnknown() {
		return nil, nil
	}
	var out []string
	diags := s.ElementsAs(ctx, &out, false)
	sort.Strings(out)
	return out, diags
}

func (r *NetworkPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data NetworkPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	fabricName, diags := r.fabricName(data)
	resp.Diagnostics.Append(diags...)
	srcEndpoints, d := setValues(ctx, data.Source.Endpoints)
	resp.Diagnostics.Append(d...)
	dstEndpoints, d := setValues(ctx, data.Destination.Endpoints)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}

	policy := newNetworkPolicy(data.Name.ValueString(),
		sideFromModel(ctx, data.Source, srcEndpoints),
		sideFromModel(ctx, data.Destination, dstEndpoints))
	if err := r.client.CreateNetworkPolicy(ctx, fabricName, policy); err != nil {
		resp.Diagnostics.AddError("Unable to create network policy", err.Error())
		return
	}

	data.FabricName = types.StringValue(fabricName)
	data.ID = types.StringValue(fabricName + "/" + data.Name.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// sideToModel converts FM's view of one side back into the model. prior is the
// side as previously known (config or state): when FM's endpoints match it as
// a set, prior's values are kept verbatim, so a server-side formatting change
// can't cause a perpetual diff.
func sideToModel(ctx context.Context, side NetworkPolicySide, prior NetworkPolicySideModel) (NetworkPolicySideModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	m := NetworkPolicySideModel{
		Type:        types.StringValue(strings.ToLower(side.Type)),
		TenantName:  types.StringValue(side.TenantName),
		ServiceName: types.StringNull(),
		Endpoints:   types.SetNull(types.StringType),
	}
	if side.ServiceName != "" {
		m.ServiceName = types.StringValue(side.ServiceName)
	}
	// FM matches tenant names case-insensitively; keep the configured casing.
	if !prior.TenantName.IsNull() && strings.EqualFold(prior.TenantName.ValueString(), side.TenantName) {
		m.TenantName = prior.TenantName
	}
	if len(side.Endpoints) > 0 {
		set, d := types.SetValueFrom(ctx, types.StringType, side.Endpoints)
		diags.Append(d...)
		m.Endpoints = set
	} else if !prior.Endpoints.IsNull() && !prior.Endpoints.IsUnknown() && len(prior.Endpoints.Elements()) == 0 {
		// Preserve an explicitly empty set from config.
		m.Endpoints = prior.Endpoints
	}
	return m, diags
}

func (r *NetworkPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data NetworkPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	fabricName, diags := r.fabricName(data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	policy, err := r.client.FindNetworkPolicy(ctx, fabricName, data.Name.ValueString())
	if err != nil {
		if isFMNotFound(err) {
			// The fabric itself is gone, and the policy with it.
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to read network policy", err.Error())
		return
	}
	if policy == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	src, d := sideToModel(ctx, policy.Spec.Source, data.Source)
	resp.Diagnostics.Append(d...)
	dst, d := sideToModel(ctx, policy.Spec.Destination, data.Destination)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Source = src
	data.Destination = dst
	data.FabricName = types.StringValue(fabricName)
	data.ID = types.StringValue(fabricName + "/" + data.Name.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *NetworkPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state NetworkPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	fabricName, diags := r.fabricName(state)
	resp.Diagnostics.Append(diags...)

	prevSrc, d := endpointSet(ctx, state.Source.Endpoints)
	resp.Diagnostics.Append(d...)
	nextSrc, d := endpointSet(ctx, plan.Source.Endpoints)
	resp.Diagnostics.Append(d...)
	prevDst, d := endpointSet(ctx, state.Destination.Endpoints)
	resp.Diagnostics.Append(d...)
	nextDst, d := endpointSet(ctx, plan.Destination.Endpoints)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	addedSrc, _ := addedEndpoints(prevSrc, nextSrc)
	addedDst, _ := addedEndpoints(prevDst, nextDst)

	// Nothing to send when only the endpoint sets' null/empty representation
	// changed.
	if len(addedSrc) > 0 || len(addedDst) > 0 {
		patch := newNetworkPolicy(plan.Name.ValueString(),
			sideFromModel(ctx, plan.Source, addedSrc),
			sideFromModel(ctx, plan.Destination, addedDst))
		if err := r.client.PatchNetworkPolicy(ctx, fabricName, patch); err != nil {
			resp.Diagnostics.AddError("Unable to update network policy", err.Error())
			return
		}
	}

	plan.FabricName = types.StringValue(fabricName)
	plan.ID = types.StringValue(fabricName + "/" + plan.Name.ValueString())
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *NetworkPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data NetworkPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	fabricName, diags := r.fabricName(data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if _, err := r.client.DeleteNetworkPolicy(ctx, fabricName, data.Name.ValueString()); err != nil {
		resp.Diagnostics.AddError("Unable to delete network policy", err.Error())
		return
	}
	resp.State.RemoveResource(ctx)
}

// ImportState accepts `<fabric>/<name>`. The policy is looked up here and the
// whole state written, since source/destination are required nested objects
// that Read can't start from empty.
func (r *NetworkPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	fabric, name, ok := strings.Cut(req.ID, "/")
	if !ok || fabric == "" || name == "" {
		resp.Diagnostics.AddError("Invalid import ID", "Expected `<fabric>/<policy name>`, got: "+req.ID)
		return
	}
	policy, err := r.client.FindNetworkPolicy(ctx, fabric, name)
	if err != nil {
		resp.Diagnostics.AddError("Unable to read network policy", err.Error())
		return
	}
	if policy == nil {
		resp.Diagnostics.AddError("Network policy not found", fmt.Sprintf("Fabric %q has no network policy named %q.", fabric, name))
		return
	}
	empty := NetworkPolicySideModel{TenantName: types.StringNull(), Endpoints: types.SetNull(types.StringType)}
	src, d := sideToModel(ctx, policy.Spec.Source, empty)
	resp.Diagnostics.Append(d...)
	dst, d := sideToModel(ctx, policy.Spec.Destination, empty)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	data := NetworkPolicyResourceModel{
		FabricName:  types.StringValue(fabric),
		Name:        types.StringValue(name),
		Source:      src,
		Destination: dst,
		ID:          types.StringValue(fabric + "/" + name),
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
