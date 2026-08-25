package provider

import (
	"context"
	"os"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ provider.Provider = &FabricAPIProvider{}

type FabricAPIProvider struct {
	version string
}

type FabricAPIProviderModel struct {
	Endpoint       types.String `tfsdk:"endpoint"`
	Fabric         types.String `tfsdk:"fabric"`
	AuthEndpoint   types.String `tfsdk:"auth_endpoint"`
	ConfigEndpoint types.String `tfsdk:"config_endpoint"`
	AccessToken    types.String `tfsdk:"access_token"`
	RefreshToken types.String `tfsdk:"refresh_token"`
	Username     types.String `tfsdk:"username"`
	Password     types.String `tfsdk:"password"`
	InsecureTLS  types.Bool   `tfsdk:"insecure_tls"`
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &FabricAPIProvider{
			version: version,
		}
	}
}

func (p *FabricAPIProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "fabricapi"
	resp.Version = p.version
}

func (p *FabricAPIProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"endpoint": schema.StringAttribute{
				MarkdownDescription: "API endpoint URL",
				Optional:            true,
			},
			"fabric": schema.StringAttribute{
				MarkdownDescription: "Fabric name",
				Optional:            true,
			},
			"auth_endpoint": schema.StringAttribute{
				MarkdownDescription: "Auth endpoint base URL (used for POST /login). If unset, defaults to endpoint.",
				Optional:            true,
			},
			"config_endpoint": schema.StringAttribute{
				MarkdownDescription: "Base URL for the ONES UI config service (used by fabricapi_fabric for POST /api/config/addFabricData). This is a separate backend/host from `endpoint`. Required only when using fabricapi_fabric.",
				Optional:            true,
			},
			"access_token": schema.StringAttribute{
				MarkdownDescription: "Bearer access token (JWT). If set, username/password login is skipped and this token is used for API Authorization.",
				Optional:            true,
				Sensitive:           true,
			},
			"refresh_token": schema.StringAttribute{
				MarkdownDescription: "Refresh token used to obtain a new access token via POST /refresh when the access token expires.",
				Optional:            true,
				Sensitive:           true,
			},
			"username": schema.StringAttribute{
				MarkdownDescription: "Username for login (POST /login). Used only if token is not set.",
				Optional:            true,
			},
			"password": schema.StringAttribute{
				MarkdownDescription: "Password for login (POST /login). Used only if token is not set.",
				Optional:            true,
				Sensitive:           true,
			},
			"insecure_tls": schema.BoolAttribute{
				MarkdownDescription: "If true, skip TLS certificate verification (use only for dev/testing with self-signed certs).",
				Optional:            true,
			},
		},
	}
}

func (p *FabricAPIProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var data FabricAPIProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	endpoint := os.Getenv("FABRIC_API_ENDPOINT")
	fabric := os.Getenv("FABRIC_NAME")
	authEndpoint := os.Getenv("FABRIC_API_AUTH_ENDPOINT")
	configEndpoint := os.Getenv("FABRIC_API_CONFIG_ENDPOINT")
	accessToken := os.Getenv("FABRIC_API_ACCESS_TOKEN")
	refreshToken := os.Getenv("FABRIC_API_REFRESH_TOKEN")
	username := os.Getenv("FABRIC_API_USERNAME")
	password := os.Getenv("FABRIC_API_PASSWORD")
	insecureTLSRaw := os.Getenv("FABRICAPI_INSECURE_TLS")
	insecureTLS := parseBoolEnvDefaultFalse(insecureTLSRaw)

	if !data.Endpoint.IsNull() {
		endpoint = data.Endpoint.ValueString()
	}

	if !data.Fabric.IsNull() {
		fabric = data.Fabric.ValueString()
	}

	if !data.AuthEndpoint.IsNull() {
		authEndpoint = data.AuthEndpoint.ValueString()
	}
	if !data.ConfigEndpoint.IsNull() {
		configEndpoint = data.ConfigEndpoint.ValueString()
	}
	if !data.AccessToken.IsNull() {
		accessToken = data.AccessToken.ValueString()
	}
	if !data.RefreshToken.IsNull() {
		refreshToken = data.RefreshToken.ValueString()
	}
	if !data.Username.IsNull() {
		username = data.Username.ValueString()
	}
	if !data.Password.IsNull() {
		password = data.Password.ValueString()
	}
	if !data.InsecureTLS.IsNull() && !data.InsecureTLS.IsUnknown() {
		insecureTLS = data.InsecureTLS.ValueBool()
	}

	if endpoint == "" {
		resp.Diagnostics.AddError(
			"Missing endpoint",
			"Set provider attribute `endpoint` or environment variable `FABRIC_API_ENDPOINT`.",
		)
		return
	}

	if fabric == "" {
		resp.Diagnostics.AddError(
			"Missing fabric",
			"Set provider attribute `fabric` or environment variable `FABRIC_NAME`.",
		)
		return
	}

	if authEndpoint == "" {
		authEndpoint = endpoint
	}

	client := &APIClient{
		Endpoint:       endpoint,
		Fabric:         fabric,
		AuthEndpoint:   authEndpoint,
		ConfigEndpoint: configEndpoint,
		Token:          strings.TrimSpace(accessToken),
		RefreshToken:   refreshToken,
		Username:       username,
		Password:       password,
		InsecureTLS:    insecureTLS,
	}

	resp.DataSourceData = client
	resp.ResourceData = client
}

func parseBoolEnvDefaultFalse(raw string) bool {
	v := strings.ToLower(strings.TrimSpace(raw))
	switch v {
	case "1", "true", "t", "yes", "y", "on":
		return true
	default:
		// Includes: "", "0", "false", "no", "off", and any unknown value.
		return false
	}
}

func (p *FabricAPIProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewTenantResource,
		NewTenantServersResource,
		NewGpuAllocationsResource,
		NewVfAssignResource,
		NewVpcPeeringResource,
		NewAuthLogoutResource,
		NewFabricResource,
	}
}

func (p *FabricAPIProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewTenantsDataSource,
		NewAvailableServersDataSource,
		NewVfInterfacesDataSource,
	}
}
