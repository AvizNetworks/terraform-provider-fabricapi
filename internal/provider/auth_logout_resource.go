package provider

import (
	"context"
	"crypto/sha256"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ resource.Resource = &AuthLogoutResource{}

func NewAuthLogoutResource() resource.Resource {
	return &AuthLogoutResource{}
}

type AuthLogoutResource struct {
	client *APIClient
}

type AuthLogoutResourceModel struct {
	RefreshToken types.String `tfsdk:"refresh_token"`
	ID           types.String `tfsdk:"id"`
}

func (r *AuthLogoutResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_auth_logout"
}

func (r *AuthLogoutResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Calls POST {auth_endpoint}/api/auth/logout with refresh_token to revoke the refresh token on the server. Run after other resources or in a separate apply so API calls are not cut off mid-run.",
		Attributes: map[string]schema.Attribute{
			"refresh_token": schema.StringAttribute{
				MarkdownDescription: "Optional override refresh token. If unset, uses refresh_token from the provider configuration.",
				Optional:            true,
				Sensitive:           true,
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

func (r *AuthLogoutResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *AuthLogoutResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data AuthLogoutResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rt := ""
	if !data.RefreshToken.IsNull() && data.RefreshToken.ValueString() != "" {
		rt = data.RefreshToken.ValueString()
	}
	idKey := rt
	if idKey == "" {
		idKey = r.client.RefreshToken
	}

	if err := r.client.Logout(ctx, rt); err != nil {
		resp.Diagnostics.AddError("Logout failed", err.Error())
		return
	}

	sum := sha256.Sum256([]byte(idKey + "|fabricapi_auth_logout"))
	data.ID = types.StringValue(fmt.Sprintf("logout_%x", sum[:8]))
	data.RefreshToken = types.StringNull()

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *AuthLogoutResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data AuthLogoutResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *AuthLogoutResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Update not supported", "Destroy and re-apply this resource to call logout again.")
}

func (r *AuthLogoutResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.State.RemoveResource(ctx)
}
