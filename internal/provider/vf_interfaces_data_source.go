package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

var _ datasource.DataSource = &VfInterfacesDataSource{}

func NewVfInterfacesDataSource() datasource.DataSource {
	return &VfInterfacesDataSource{}
}

type VfInterfacesDataSource struct {
	client *APIClient
}

type VfInterfacesDataSourceModel struct {
	FabricName types.String `tfsdk:"fabric_name"`
	ServerName types.String `tfsdk:"server_name"`
	DpuCount   types.Int64  `tfsdk:"dpu_count"`
	Dpus       types.List   `tfsdk:"dpus"`
	ID         types.String `tfsdk:"id"`
}

func (d *VfInterfacesDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vf_interfaces"
}

func (d *VfInterfacesDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Looks up HBN VF interfaces for a GPU server via GET " +
			"`/fabrics/{fabricName}/servers/{serverName}/vf-interfaces`. " +
			"Read-only; refreshed on each plan/apply. Use before assign to pick a free VF " +
			"(`status=free`). `vf_id` for assign may be `server_if` (e.g. vf4) or DPU `if_name`.",

		Attributes: map[string]schema.Attribute{
			"fabric_name": schema.StringAttribute{
				MarkdownDescription: "Fabric to query. If unset, uses provider-level fabric (FABRIC_NAME).",
				Optional:            true,
			},
			"server_name": schema.StringAttribute{
				MarkdownDescription: "GPU server hostname whose DPU VF interfaces should be listed.",
				Required:            true,
			},
			"dpu_count": schema.Int64Attribute{
				MarkdownDescription: "Number of DPUs returned for the server (API field dpuCount).",
				Computed:            true,
			},
			"dpus": schema.ListNestedAttribute{
				MarkdownDescription: "DPUs and their VF interfaces.",
				Computed:            true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"dpu_name": schema.StringAttribute{
							MarkdownDescription: "DPU device hostname.",
							Computed:            true,
						},
						"vf_interfaces": schema.ListNestedAttribute{
							MarkdownDescription: "VF interfaces on this DPU.",
							Computed:            true,
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"if_name": schema.StringAttribute{
										MarkdownDescription: "DPU-side interface name (e.g. pf1vf0_if).",
										Computed:            true,
									},
									"server_if": schema.StringAttribute{
										MarkdownDescription: "Host-side VF name (e.g. vf4).",
										Computed:            true,
									},
									"status": schema.StringAttribute{
										MarkdownDescription: "VF status: free, provisioned, or error.",
										Computed:            true,
									},
									"tenant_name": schema.StringAttribute{
										MarkdownDescription: "Tenant that currently owns the VF when status is provisioned.",
										Computed:            true,
									},
								},
							},
						},
					},
				},
			},
			"id": schema.StringAttribute{
				MarkdownDescription: "Data source identifier (fabric:server).",
				Computed:            true,
			},
		},
	}
}

func (d *VfInterfacesDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *VfInterfacesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var state VfInterfacesDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	fabricName := resolveFabricName(d.client.Fabric, state.FabricName)
	serverName := strings.TrimSpace(state.ServerName.ValueString())
	if strings.TrimSpace(fabricName) == "" {
		resp.Diagnostics.AddError(
			"Missing fabric",
			"Set fabric_name on the data source or configure provider fabric via FABRIC_NAME / provider \"fabric\".",
		)
		return
	}
	if serverName == "" {
		resp.Diagnostics.AddError("Missing server_name", "server_name is required.")
		return
	}

	raw, err := d.client.GetVfInterfaces(ctx, fabricName, serverName)
	if err != nil {
		resp.Diagnostics.AddError("Unable to list VF interfaces", err.Error())
		return
	}

	dpus, diags := flattenVfDpus(raw)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	state.FabricName = types.StringValue(fabricName)
	state.ServerName = types.StringValue(serverName)
	state.DpuCount = types.Int64Value(int64(raw.DpuCount))
	state.Dpus = dpus
	state.ID = types.StringValue(fmt.Sprintf("%s:%s", fabricName, serverName))

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func flattenVfDpus(raw *VfInterfacesResponse) (types.List, diag.Diagnostics) {
	var diags diag.Diagnostics

	vfAttrTypes := map[string]attr.Type{
		"if_name":     types.StringType,
		"server_if":   types.StringType,
		"status":      types.StringType,
		"tenant_name": types.StringType,
	}
	dpuAttrTypes := map[string]attr.Type{
		"dpu_name":      types.StringType,
		"vf_interfaces": types.ListType{ElemType: types.ObjectType{AttrTypes: vfAttrTypes}},
	}
	dpuObjType := types.ObjectType{AttrTypes: dpuAttrTypes}

	if raw == nil || len(raw.Dpus) == 0 {
		empty, d := types.ListValue(dpuObjType, []attr.Value{})
		diags.Append(d...)
		return empty, diags
	}

	dpuValues := make([]attr.Value, 0, len(raw.Dpus))
	for _, dpu := range raw.Dpus {
		vfValues := make([]attr.Value, 0, len(dpu.VfInterfaces))
		for _, vf := range dpu.VfInterfaces {
			tenant := types.StringNull()
			if strings.TrimSpace(vf.TenantName) != "" {
				tenant = types.StringValue(vf.TenantName)
			}
			obj, d := types.ObjectValue(vfAttrTypes, map[string]attr.Value{
				"if_name":     types.StringValue(vf.IfName),
				"server_if":   types.StringValue(vf.ServerIf),
				"status":      types.StringValue(vf.Status),
				"tenant_name": tenant,
			})
			diags.Append(d...)
			if diags.HasError() {
				empty, _ := types.ListValue(dpuObjType, []attr.Value{})
				return empty, diags
			}
			vfValues = append(vfValues, obj)
		}

		vfList, d := types.ListValue(types.ObjectType{AttrTypes: vfAttrTypes}, vfValues)
		diags.Append(d...)
		if diags.HasError() {
			empty, _ := types.ListValue(dpuObjType, []attr.Value{})
			return empty, diags
		}

		dpuObj, d := types.ObjectValue(dpuAttrTypes, map[string]attr.Value{
			"dpu_name":      types.StringValue(dpu.DpuName),
			"vf_interfaces": vfList,
		})
		diags.Append(d...)
		if diags.HasError() {
			empty, _ := types.ListValue(dpuObjType, []attr.Value{})
			return empty, diags
		}
		dpuValues = append(dpuValues, dpuObj)
	}

	list, d := types.ListValue(dpuObjType, dpuValues)
	diags.Append(d...)
	return list, diags
}
