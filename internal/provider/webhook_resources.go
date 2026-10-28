package provider

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// webhookURLPattern is a light client-side check; Fabric Manager does the full
// URL validation (scheme, loopback/private address rules, port range).
var webhookURLPattern = regexp.MustCompile(`^https?://[^/\s]+`)

// Canonical webhook event types. FM also accepts fabric.tenant_* aliases but
// stores them under these names, so only the canonical names are allowed here
// (an alias would read back differently and show a permanent diff).
var (
	tenantWebhookEvents = []string{"tenant.create", "tenant.delete", "tenant.allocate", "tenant.deallocate", "tenant.vpcpeering"}
	fabricWebhookEvents = []string{"fabric.deploy", "fabric.delete"}
	allWebhookEvents    = append(append([]string{}, tenantWebhookEvents...), fabricWebhookEvents...)
)

func stringSetOrNull(ctx context.Context, items []string) (types.Set, diag.Diagnostics) {
	if len(items) == 0 {
		return types.SetNull(types.StringType), nil
	}
	return types.SetValueFrom(ctx, types.StringType, items)
}

func stringSetOrEmpty(ctx context.Context, items []string) (types.Set, diag.Diagnostics) {
	if items == nil {
		items = []string{}
	}
	return types.SetValueFrom(ctx, types.StringType, items)
}

// setToSlice returns a set's strings, sorted, as a non-nil slice (so JSON sends
// [] rather than null — FM reads null as "unchanged" on update).
func setToSlice(ctx context.Context, s types.Set) ([]string, diag.Diagnostics) {
	out := []string{}
	if s.IsNull() || s.IsUnknown() {
		return out, nil
	}
	diags := s.ElementsAs(ctx, &out, false)
	sort.Strings(out)
	return out, diags
}

func optionalTimestamp(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

// ============================================================== global ===

var (
	_ resource.Resource                = &WebhookResource{}
	_ resource.ResourceWithImportState = &WebhookResource{}
)

func NewWebhookResource() resource.Resource {
	return &WebhookResource{}
}

// WebhookResource manages a global webhook registration (/webhooks), which
// receives matching events from every fabric and tenant unless narrowed by the
// filter lists.
type WebhookResource struct {
	client *APIClient
}

type WebhookResourceModel struct {
	CallbackURL       types.String `tfsdk:"callback_url"`
	EventTypes        types.Set    `tfsdk:"event_types"`
	Enabled           types.Bool   `tfsdk:"enabled"`
	FilterFabricNames types.Set    `tfsdk:"filter_fabric_names"`
	FilterTenantNames types.Set    `tfsdk:"filter_tenant_names"`
	WebhookID         types.String `tfsdk:"webhook_id"`
	CreatedAt         types.String `tfsdk:"created_at"`
	UpdatedAt         types.String `tfsdk:"updated_at"`
	ID                types.String `tfsdk:"id"`
}

func (r *WebhookResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_webhook"
}

func callbackURLAttribute() schema.StringAttribute {
	return schema.StringAttribute{
		MarkdownDescription: "URL Fabric Manager POSTs events to. Must be http(s) and must not be a loopback address; Fabric Manager may also require HTTPS and a public address depending on its `webhook.allow-insecure-urls` setting. Deliveries are signed with HMAC-SHA256 in the `X-Webhook-Signature` header.",
		Required:            true,
		Validators: []validator.String{
			stringvalidator.RegexMatches(webhookURLPattern, "must start with http:// or https://"),
		},
	}
}

func enabledAttribute() schema.BoolAttribute {
	return schema.BoolAttribute{
		MarkdownDescription: "Whether Fabric Manager delivers events to this registration. Defaults to true.",
		Optional:            true,
		Computed:            true,
		Default:             booldefault.StaticBool(true),
	}
}

func timestampAttributes() (schema.StringAttribute, schema.StringAttribute) {
	return schema.StringAttribute{
			MarkdownDescription: "When Fabric Manager created the registration.",
			Computed:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		}, schema.StringAttribute{
			MarkdownDescription: "When Fabric Manager last changed the registration.",
			Computed:            true,
		}
}

func (r *WebhookResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	createdAt, updatedAt := timestampAttributes()
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a global webhook registration (`/webhooks`): Fabric Manager POSTs the selected events, from every fabric and tenant unless narrowed by the filters, to `callback_url`. " +
			"Fabric Manager keys global registrations on `callback_url`: creating one for a URL that is already registered adopts that registration (with a warning) instead of adding a duplicate.\n\n" +
			"Global, fabric (`fabricapi_fabric_webhook`) and tenant (`fabricapi_tenant_webhook`) registrations are independent: an event matching several is delivered to each.",
		Attributes: map[string]schema.Attribute{
			"callback_url": callbackURLAttribute(),
			"event_types": schema.SetAttribute{
				MarkdownDescription: "Events to deliver (at least one): " + strings.Join(allWebhookEvents, ", ") + ".",
				Required:            true,
				ElementType:         types.StringType,
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
					setvalidator.ValueStringsAre(stringvalidator.OneOf(allWebhookEvents...)),
				},
			},
			"enabled": enabledAttribute(),
			"filter_fabric_names": schema.SetAttribute{
				MarkdownDescription: "Only deliver events from these fabrics. Empty or unset means every fabric. Names match exactly (case-sensitive).",
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
				Default:             setdefault.StaticValue(types.SetValueMust(types.StringType, nil)),
			},
			"filter_tenant_names": schema.SetAttribute{
				MarkdownDescription: "Only deliver events for these tenants. Empty or unset means every tenant. Events without a tenant (`fabric.*`) are not affected by this filter.",
				Optional:            true,
				Computed:            true,
				ElementType:         types.StringType,
				Default:             setdefault.StaticValue(types.SetValueMust(types.StringType, nil)),
			},
			"webhook_id": schema.StringAttribute{
				MarkdownDescription: "Fabric Manager's id for the registration (a UUID).",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": createdAt,
			"updated_at": updatedAt,
			"id": schema.StringAttribute{
				MarkdownDescription: "Same as `webhook_id`.",
				Computed:            true,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *WebhookResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// request builds the full POST/PATCH body from the model. Every field is sent,
// with empty filter lists as [] so a PATCH clears them.
func (m WebhookResourceModel) request(ctx context.Context) (GlobalWebhookRequest, diag.Diagnostics) {
	var diags diag.Diagnostics
	url := m.CallbackURL.ValueString()
	enabled := m.Enabled.ValueBool()
	events, d := setToSlice(ctx, m.EventTypes)
	diags.Append(d...)
	fabrics, d := setToSlice(ctx, m.FilterFabricNames)
	diags.Append(d...)
	tenants, d := setToSlice(ctx, m.FilterTenantNames)
	diags.Append(d...)
	return GlobalWebhookRequest{
		CallbackURL: &url,
		Enabled:     &enabled,
		EventTypes:  events,
		Filters:     &WebhookFilters{FabricNames: fabrics, TenantNames: tenants},
	}, diags
}

func (m *WebhookResourceModel) fromAPI(ctx context.Context, reg *WebhookRegistration) diag.Diagnostics {
	var diags diag.Diagnostics
	var d diag.Diagnostics
	m.CallbackURL = types.StringValue(reg.CallbackURL)
	m.Enabled = types.BoolValue(reg.Enabled)
	m.EventTypes, d = stringSetOrEmpty(ctx, reg.EventTypes)
	diags.Append(d...)
	var fabrics, tenants []string
	if reg.Filters != nil {
		fabrics, tenants = reg.Filters.FabricNames, reg.Filters.TenantNames
	}
	m.FilterFabricNames, d = stringSetOrEmpty(ctx, fabrics)
	diags.Append(d...)
	m.FilterTenantNames, d = stringSetOrEmpty(ctx, tenants)
	diags.Append(d...)
	m.WebhookID = types.StringValue(reg.WebhookID)
	m.ID = types.StringValue(reg.WebhookID)
	m.CreatedAt = optionalTimestamp(reg.CreatedAt)
	m.UpdatedAt = optionalTimestamp(reg.UpdatedAt)
	return diags
}

func (r *WebhookResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data WebhookResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, diags := data.request(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// FM ignores `enabled` on register (a registration always starts enabled);
	// it is applied with a follow-up PATCH below.
	body.Enabled = nil

	reg, created, err := r.client.RegisterGlobalWebhook(ctx, body)
	if err != nil {
		resp.Diagnostics.AddError("Unable to register webhook", err.Error())
		return
	}
	if !created {
		resp.Diagnostics.AddWarning("Existing webhook registration adopted",
			fmt.Sprintf("A global webhook for %s was already registered (webhook_id %s). Fabric Manager updated it in place instead of creating a new one; Terraform now manages it, and destroying this resource deletes it.",
				data.CallbackURL.ValueString(), reg.WebhookID))
	}
	if !data.Enabled.ValueBool() {
		disabled := false
		reg, err = r.client.UpdateGlobalWebhook(ctx, reg.WebhookID, GlobalWebhookRequest{Enabled: &disabled})
		if err != nil {
			resp.Diagnostics.AddError("Webhook registered, but disabling it failed", err.Error())
			return
		}
	}
	// Re-read so timestamps carry the precision FM stores (write responses
	// report nanoseconds, reads after the save report microseconds).
	if stored, err := r.client.GetGlobalWebhook(ctx, reg.WebhookID); err == nil {
		reg = stored
	}
	resp.Diagnostics.Append(data.fromAPI(ctx, reg)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *WebhookResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data WebhookResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	reg, err := r.client.GetGlobalWebhook(ctx, data.WebhookID.ValueString())
	if err != nil {
		if isFMNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to read webhook", err.Error())
		return
	}
	resp.Diagnostics.Append(data.fromAPI(ctx, reg)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *WebhookResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state WebhookResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	body, diags := plan.request(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	reg, err := r.client.UpdateGlobalWebhook(ctx, state.WebhookID.ValueString(), body)
	if err != nil {
		resp.Diagnostics.AddError("Unable to update webhook", err.Error())
		return
	}
	if stored, err := r.client.GetGlobalWebhook(ctx, reg.WebhookID); err == nil {
		reg = stored
	}
	resp.Diagnostics.Append(plan.fromAPI(ctx, reg)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *WebhookResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data WebhookResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteGlobalWebhook(ctx, data.WebhookID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Unable to delete webhook", err.Error())
		return
	}
	resp.State.RemoveResource(ctx)
}

// ImportState accepts the webhook_id.
func (r *WebhookResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("webhook_id"), req.ID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// ====================================================== fabric / tenant ===

var (
	_ resource.Resource                = &ScopedWebhookResource{}
	_ resource.ResourceWithImportState = &ScopedWebhookResource{}
)

// NewFabricWebhookResource manages /fabrics/{fabric}/webhook.
func NewFabricWebhookResource() resource.Resource {
	return &ScopedWebhookResource{tenantScoped: false}
}

// NewTenantWebhookResource manages /fabrics/{fabric}/tenants/{tenant}/webhook.
func NewTenantWebhookResource() resource.Resource {
	return &ScopedWebhookResource{tenantScoped: true}
}

// ScopedWebhookResource manages the single webhook registration a fabric or a
// tenant can hold. Both scopes share one implementation; tenantScoped adds the
// tenant_name attribute and switches the path and allowed events.
type ScopedWebhookResource struct {
	client       *APIClient
	tenantScoped bool
}

// scopedWebhook is the common view of the fabric and tenant models.
type scopedWebhook struct {
	FabricName     types.String
	TenantName     types.String
	CallbackURL    types.String
	Enabled        types.Bool
	EventTypes     types.Set
	RegistrationID types.Int64
	CreatedAt      types.String
	UpdatedAt      types.String
	ID             types.String
}

type FabricWebhookResourceModel struct {
	FabricName     types.String `tfsdk:"fabric_name"`
	CallbackURL    types.String `tfsdk:"callback_url"`
	Enabled        types.Bool   `tfsdk:"enabled"`
	EventTypes     types.Set    `tfsdk:"event_types"`
	RegistrationID types.Int64  `tfsdk:"registration_id"`
	CreatedAt      types.String `tfsdk:"created_at"`
	UpdatedAt      types.String `tfsdk:"updated_at"`
	ID             types.String `tfsdk:"id"`
}

type TenantWebhookResourceModel struct {
	FabricName     types.String `tfsdk:"fabric_name"`
	TenantName     types.String `tfsdk:"tenant_name"`
	CallbackURL    types.String `tfsdk:"callback_url"`
	Enabled        types.Bool   `tfsdk:"enabled"`
	EventTypes     types.Set    `tfsdk:"event_types"`
	RegistrationID types.Int64  `tfsdk:"registration_id"`
	CreatedAt      types.String `tfsdk:"created_at"`
	UpdatedAt      types.String `tfsdk:"updated_at"`
	ID             types.String `tfsdk:"id"`
}

func (r *ScopedWebhookResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	if r.tenantScoped {
		resp.TypeName = req.ProviderTypeName + "_tenant_webhook"
	} else {
		resp.TypeName = req.ProviderTypeName + "_fabric_webhook"
	}
}

func (r *ScopedWebhookResource) events() []string {
	if r.tenantScoped {
		return tenantWebhookEvents
	}
	return fabricWebhookEvents
}

func (r *ScopedWebhookResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	createdAt, updatedAt := timestampAttributes()
	attrs := map[string]schema.Attribute{
		"fabric_name": schema.StringAttribute{
			MarkdownDescription: "Fabric the registration belongs to. If unset, uses the provider-level fabric.",
			Optional:            true,
			Computed:            true,
			PlanModifiers: []planmodifier.String{
				stringplanmodifier.RequiresReplace(),
				stringplanmodifier.UseStateForUnknown(),
			},
		},
		"callback_url": callbackURLAttribute(),
		"enabled":      enabledAttribute(),
		"event_types": schema.SetAttribute{
			MarkdownDescription: "Events to deliver: " + strings.Join(r.events(), ", ") + ". Unset means every event this scope receives.",
			Optional:            true,
			ElementType:         types.StringType,
			Validators: []validator.Set{
				setvalidator.SizeAtLeast(1),
				setvalidator.ValueStringsAre(stringvalidator.OneOf(r.events()...)),
			},
		},
		"registration_id": schema.Int64Attribute{
			MarkdownDescription: "Fabric Manager's numeric id for the registration.",
			Computed:            true,
			PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
		},
		"created_at": createdAt,
		"updated_at": updatedAt,
		"id": schema.StringAttribute{
			Computed:      true,
			PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
	}

	var description string
	if r.tenantScoped {
		attrs["tenant_name"] = schema.StringAttribute{
			MarkdownDescription: "Tenant whose events are delivered.",
			Required:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
		}
		attrs["id"] = schema.StringAttribute{
			MarkdownDescription: "`<fabric>/<tenant>`.",
			Computed:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		}
		description = "Manages a tenant's webhook registration (`/fabrics/{fabric}/tenants/{tenant}/webhook`). A tenant holds at most one; Fabric Manager delivers the tenant's `tenant.*` events to it.\n\n" +
			"Fabric Manager keys tenant registrations on the tenant name alone, and an asynchronous tenant operation sent with `webhooks_enabled = true` (e.g. on `fabricapi_tenant`) re-registers this same registration with that operation's URL and events. " +
			"Don't combine the two for one tenant, or each will keep overwriting the other. The registration is not removed when the tenant is deleted."
	} else {
		attrs["id"] = schema.StringAttribute{
			MarkdownDescription: "The fabric name.",
			Computed:            true,
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		}
		description = "Manages a fabric's webhook registration (`/fabrics/{fabric}/webhook`). A fabric holds at most one; Fabric Manager delivers the fabric's `fabric.deploy`/`fabric.delete` events to it (tenant events go to tenant and global registrations only)."
	}
	resp.Schema = schema.Schema{MarkdownDescription: description, Attributes: attrs}
}

func (r *ScopedWebhookResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

type tfGetter interface {
	Get(ctx context.Context, target interface{}) diag.Diagnostics
}

type tfSetter interface {
	Set(ctx context.Context, val interface{}) diag.Diagnostics
}

func (r *ScopedWebhookResource) load(ctx context.Context, src tfGetter) (scopedWebhook, diag.Diagnostics) {
	if r.tenantScoped {
		var m TenantWebhookResourceModel
		diags := src.Get(ctx, &m)
		return scopedWebhook{m.FabricName, m.TenantName, m.CallbackURL, m.Enabled, m.EventTypes, m.RegistrationID, m.CreatedAt, m.UpdatedAt, m.ID}, diags
	}
	var m FabricWebhookResourceModel
	diags := src.Get(ctx, &m)
	return scopedWebhook{m.FabricName, types.StringNull(), m.CallbackURL, m.Enabled, m.EventTypes, m.RegistrationID, m.CreatedAt, m.UpdatedAt, m.ID}, diags
}

func (r *ScopedWebhookResource) store(ctx context.Context, dst tfSetter, w scopedWebhook) diag.Diagnostics {
	if r.tenantScoped {
		return dst.Set(ctx, &TenantWebhookResourceModel{w.FabricName, w.TenantName, w.CallbackURL, w.Enabled, w.EventTypes, w.RegistrationID, w.CreatedAt, w.UpdatedAt, w.ID})
	}
	return dst.Set(ctx, &FabricWebhookResourceModel{w.FabricName, w.CallbackURL, w.Enabled, w.EventTypes, w.RegistrationID, w.CreatedAt, w.UpdatedAt, w.ID})
}

func (r *ScopedWebhookResource) names(w scopedWebhook) (fabric, tenant string, diags diag.Diagnostics) {
	fabric = resolveFabricName(r.client.Fabric, w.FabricName)
	if strings.TrimSpace(fabric) == "" {
		diags.AddError("Missing fabric", "Set `fabric_name` on this resource or `fabric` on the provider.")
	}
	if r.tenantScoped {
		tenant = w.TenantName.ValueString()
	}
	return fabric, tenant, diags
}

func (r *ScopedWebhookResource) scopeLabel(fabric, tenant string) string {
	if r.tenantScoped {
		return fmt.Sprintf("tenant %q in fabric %q", tenant, fabric)
	}
	return fmt.Sprintf("fabric %q", fabric)
}

func (r *ScopedWebhookResource) importID(fabric, tenant string) string {
	if r.tenantScoped {
		return fabric + "/" + tenant
	}
	return fabric
}

func (w *scopedWebhook) fromAPI(ctx context.Context, reg *WebhookRegistration, fabric, tenant string, id string) diag.Diagnostics {
	var diags diag.Diagnostics
	w.FabricName = types.StringValue(fabric)
	if tenant != "" {
		w.TenantName = types.StringValue(tenant)
	}
	w.CallbackURL = types.StringValue(reg.CallbackURL)
	w.Enabled = types.BoolValue(reg.Enabled)
	// FM omits eventTypes when the registration receives every event.
	set, d := stringSetOrNull(ctx, reg.EventTypes)
	diags.Append(d...)
	w.EventTypes = set
	w.RegistrationID = types.Int64Value(reg.ID)
	w.CreatedAt = optionalTimestamp(reg.CreatedAt)
	w.UpdatedAt = optionalTimestamp(reg.UpdatedAt)
	w.ID = types.StringValue(id)
	return diags
}

func (w scopedWebhook) request(ctx context.Context) (ScopedWebhookRequest, diag.Diagnostics) {
	events, diags := setToSlice(ctx, w.EventTypes)
	return ScopedWebhookRequest{
		CallbackURL: w.CallbackURL.ValueString(),
		Enabled:     w.Enabled.ValueBool(),
		EventTypes:  events,
	}, diags
}

func (r *ScopedWebhookResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	w, diags := r.load(ctx, req.Plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	fabric, tenant, diags := r.names(w)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// FM's register silently replaces an existing registration, so refuse to
	// take over one Terraform didn't create.
	existing, err := r.client.GetScopedWebhook(ctx, fabric, tenant)
	if err == nil {
		resp.Diagnostics.AddError("Webhook already registered",
			fmt.Sprintf("%s already has a webhook registration (callback %s). Import it instead: terraform import <address> %s",
				r.scopeLabel(fabric, tenant), existing.CallbackURL, r.importID(fabric, tenant)))
		return
	}
	if !isFMNotFound(err) {
		resp.Diagnostics.AddError("Unable to check for an existing webhook registration", err.Error())
		return
	}

	body, diags := w.request(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	reg, err := r.client.RegisterScopedWebhook(ctx, fabric, tenant, body)
	if err != nil {
		resp.Diagnostics.AddError("Unable to register webhook", err.Error())
		return
	}
	// FM always stores enabled=true on register.
	if !body.Enabled {
		reg, err = r.client.UpdateScopedWebhook(ctx, fabric, tenant, body)
		if err != nil {
			resp.Diagnostics.AddError("Webhook registered, but disabling it failed", err.Error())
			return
		}
	}
	// Re-read so timestamps carry the precision FM stores.
	if stored, err := r.client.GetScopedWebhook(ctx, fabric, tenant); err == nil {
		reg = stored
	}
	resp.Diagnostics.Append(w.fromAPI(ctx, reg, fabric, tenant, r.importID(fabric, tenant))...)
	resp.Diagnostics.Append(r.store(ctx, &resp.State, w)...)
}

func (r *ScopedWebhookResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	w, diags := r.load(ctx, req.State)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	fabric, tenant, diags := r.names(w)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	reg, err := r.client.GetScopedWebhook(ctx, fabric, tenant)
	if err != nil {
		if isFMNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Unable to read webhook", err.Error())
		return
	}
	resp.Diagnostics.Append(w.fromAPI(ctx, reg, fabric, tenant, r.importID(fabric, tenant))...)
	resp.Diagnostics.Append(r.store(ctx, &resp.State, w)...)
}

func (r *ScopedWebhookResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	w, diags := r.load(ctx, req.Plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	fabric, tenant, diags := r.names(w)
	resp.Diagnostics.Append(diags...)
	body, d := w.request(ctx)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	reg, err := r.client.UpdateScopedWebhook(ctx, fabric, tenant, body)
	if err != nil {
		resp.Diagnostics.AddError("Unable to update webhook", err.Error())
		return
	}
	if stored, err := r.client.GetScopedWebhook(ctx, fabric, tenant); err == nil {
		reg = stored
	}
	resp.Diagnostics.Append(w.fromAPI(ctx, reg, fabric, tenant, r.importID(fabric, tenant))...)
	resp.Diagnostics.Append(r.store(ctx, &resp.State, w)...)
}

func (r *ScopedWebhookResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	w, diags := r.load(ctx, req.State)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	fabric, tenant, diags := r.names(w)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteScopedWebhook(ctx, fabric, tenant); err != nil {
		resp.Diagnostics.AddError("Unable to delete webhook", err.Error())
		return
	}
	resp.State.RemoveResource(ctx)
}

// ImportState accepts `<fabric>` (fabric scope) or `<fabric>/<tenant>` (tenant
// scope).
func (r *ScopedWebhookResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	fabric, tenant := req.ID, ""
	if r.tenantScoped {
		var ok bool
		fabric, tenant, ok = strings.Cut(req.ID, "/")
		if !ok || fabric == "" || tenant == "" {
			resp.Diagnostics.AddError("Invalid import ID", "Expected `<fabric>/<tenant>`, got: "+req.ID)
			return
		}
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("tenant_name"), tenant)...)
	} else if fabric == "" || strings.Contains(fabric, "/") {
		resp.Diagnostics.AddError("Invalid import ID", "Expected `<fabric>`, got: "+req.ID)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("fabric_name"), fabric)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), r.importID(fabric, tenant))...)
}
