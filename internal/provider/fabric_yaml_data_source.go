package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &FabricYamlDataSource{}

func NewFabricYamlDataSource() datasource.DataSource {
	return &FabricYamlDataSource{}
}

// FabricYamlDataSource fetches a fabric's generated YAML for review — the "design a fabric
// and view the YAML" step, without running any part of fabricapi_fabric_deploy. Backed by
// the same GET {config_endpoint}/fabrics/{name} call fabricapi_fabric_deploy uses internally
// (client.GetFabricYaml). The fabric must already exist (fabricapi_fabric.this must have
// succeeded) and have a generated YAML.
type FabricYamlDataSource struct {
	client *APIClient
}

type FabricYamlDataSourceModel struct {
	FabricName types.String `tfsdk:"fabric_name"`
	Yaml       types.String `tfsdk:"yaml"`
	ID         types.String `tfsdk:"id"`
}

func (d *FabricYamlDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_fabric_yaml"
}

func (d *FabricYamlDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches a fabric's generated YAML (GET /fabrics/{name}) for review — lets you design a fabric and inspect its generated topology without deploying it. Re-fetched on every plan/apply, so it always reflects the current on-disk YAML (including any edits already pushed via a prior fabricapi_fabric_deploy). Requires provider `config_endpoint`.",
		Attributes: map[string]schema.Attribute{
			"fabric_name": schema.StringAttribute{
				MarkdownDescription: "Name of an existing fabric (e.g. fabricapi_fabric.this.name).",
				Required:            true,
			},
			"yaml": schema.StringAttribute{
				MarkdownDescription: "The fabric's current generated YAML, as raw text. Save with a `local_file` resource (from the `hashicorp/local` provider) to review or hand-edit it, e.g. `local_file.review.content = data.fabricapi_fabric_yaml.this.yaml`.",
				Computed:            true,
			},
			"id": schema.StringAttribute{
				Computed: true,
			},
		},
	}
}

func (d *FabricYamlDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *FabricYamlDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state FabricYamlDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fabricName := strings.TrimSpace(state.FabricName.ValueString())
	if fabricName == "" {
		resp.Diagnostics.AddError("Missing fabric_name", "fabric_name is required.")
		return
	}

	rawYAML, err := d.client.GetFabricYaml(ctx, fabricName)
	if err != nil {
		resp.Diagnostics.AddError("Unable to fetch fabric YAML", err.Error())
		return
	}

	state.FabricName = types.StringValue(fabricName)
	state.Yaml = types.StringValue(rawYAML)
	state.ID = types.StringValue(fabricName)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
