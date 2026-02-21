package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &TenantsDataSource{}

func NewTenantsDataSource() datasource.DataSource {
	return &TenantsDataSource{}
}

type TenantsDataSource struct {
	client *APIClient
}

type TenantsDataSourceModel struct {
	FabricName types.String `tfsdk:"fabric_name"`
	Tenants    types.List   `tfsdk:"tenants"`
}

func (d *TenantsDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_tenants"
}

func (d *TenantsDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"fabric_name": schema.StringAttribute{
				Optional: true,
			},
			"tenants": schema.ListAttribute{
				ElementType: types.StringType,
				Computed:    true,
			},
		},
	}
}

func (d *TenantsDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *TenantsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state TenantsDataSourceModel

	// Read optional fabric_name from config
	resp.Diagnostics.Append(req.Config.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Resolve fabric
	fabricName := d.client.Fabric
	if !state.FabricName.IsNull() && state.FabricName.ValueString() != "" {
		fabricName = state.FabricName.ValueString()
	}

	// Call API
	tenants, err := d.client.ListTenants(ctx, fabricName)
	if err != nil {
		resp.Diagnostics.AddError("Unable to list tenants", err.Error())
		return
	}

	// Extract tenant names
	names := make([]string, 0, len(tenants))
	for _, t := range tenants {
		names = append(names, t.TenantName)
	}

	// Convert to Terraform list
	tenantList, diags := types.ListValueFrom(ctx, types.StringType, names)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Set final state
	state.FabricName = types.StringValue(fabricName)
	state.Tenants = tenantList

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
