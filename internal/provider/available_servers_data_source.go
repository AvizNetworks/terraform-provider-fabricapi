package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &AvailableServersDataSource{}

func NewAvailableServersDataSource() datasource.DataSource {
	return &AvailableServersDataSource{}
}

type AvailableServersDataSource struct {
	client *APIClient
}

type AvailableServersDataSourceModel struct {
	FabricName    types.String `tfsdk:"fabric_name"`
	AvailableGPUs types.List   `tfsdk:"available_gpus"`
	ID            types.String `tfsdk:"id"`
}

func (d *AvailableServersDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_available_servers"
}

func (d *AvailableServersDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up free GPU servers for a fabric via GET " +
			"`/fabrics/{fabricName}/available_servers`. Read-only; refreshed on each plan/apply.",

		Attributes: map[string]schema.Attribute{
			"fabric_name": schema.StringAttribute{
				MarkdownDescription: "Fabric to query. If unset, uses provider-level fabric (FABRIC_NAME).",
				Optional:            true,
			},
			"available_gpus": schema.ListAttribute{
				MarkdownDescription: "Server hostnames currently available for allocation (API field availableGPUs).",
				ElementType:         types.StringType,
				Computed:            true,
			},
			"id": schema.StringAttribute{
				MarkdownDescription: "Data source identifier (fabric name).",
				Computed:            true,
			},
		},
	}
}

func (d *AvailableServersDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*APIClient)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *APIClient, got: %T", req.ProviderData),
		)
		return
	}

	d.client = client
}

func (d *AvailableServersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state AvailableServersDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fabricName := d.client.Fabric
	if !state.FabricName.IsNull() && state.FabricName.ValueString() != "" {
		fabricName = state.FabricName.ValueString()
	}
	if strings.TrimSpace(fabricName) == "" {
		resp.Diagnostics.AddError(
			"Missing fabric",
			"Set fabric_name on the data source or configure provider fabric via FABRIC_NAME / provider \"fabric\".",
		)
		return
	}

	servers, err := d.client.GetAvailableServers(ctx, fabricName)
	if err != nil {
		resp.Diagnostics.AddError("Unable to list available servers", err.Error())
		return
	}

	list, diags := types.ListValueFrom(ctx, types.StringType, servers)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	state.FabricName = types.StringValue(fabricName)
	state.AvailableGPUs = list
	state.ID = types.StringValue(fabricName)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
