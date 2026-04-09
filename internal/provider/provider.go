package provider

import (
	"context"
	"os"

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
	Endpoint types.String `tfsdk:"endpoint"`
	Fabric   types.String `tfsdk:"fabric"`
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

	if !data.Endpoint.IsNull() {
		endpoint = data.Endpoint.ValueString()
	}

	if !data.Fabric.IsNull() {
		fabric = data.Fabric.ValueString()
	}

	if endpoint == "" {
		endpoint = "http://localhost:8787"
	}

	if fabric == "" {
		fabric = "1SU-Fabric172202"
	}

	client := &APIClient{
		Endpoint: endpoint,
		Fabric:   fabric,
	}

	resp.DataSourceData = client
	resp.ResourceData = client
}

func (p *FabricAPIProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewTenantResource,
		NewTenantServersResource,
		NewVpcPeeringResource,
	}
}

func (p *FabricAPIProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewTenantsDataSource,
	}
}
