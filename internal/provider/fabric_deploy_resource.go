package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"gopkg.in/yaml.v3"
)

var _ resource.Resource = &FabricDeployResource{}

func NewFabricDeployResource() resource.Resource {
	return &FabricDeployResource{}
}

// FabricDeployResource performs the same sequence as the ONES UI's "Deploy Fabric" button,
// against a fabric already created via fabricapi_fabric:
//  1. POST {config_endpoint}/api/config/uploadip           -> fill in real IP/username/password for each device
//  2. POST {config_endpoint}/api/config/validateswitch     -> SSH-validate spine/leaf devices (must all succeed)
//  3. POST {config_endpoint}/api/config/validateserver     -> SSH-validate host/DPU devices (must all succeed)
//  4. POST {config_endpoint}/api/config/updateinventory    -> push device credentials to the FM engine's inventory
//  5. GET  {config_endpoint}/fabrics/{name}                -> fetch the now credential-filled YAML
//     POST {config_endpoint}/api/config                     -> push the parsed YAML to the real switches
//  6. POST {config_endpoint}/api/config/updatefabricstatus -> mark the fabric Deployed
//
// Steps 2 and 3 hard-fail the whole apply if any device fails validation, matching the ONES
// UI's own behavior of keeping the "Deploy Fabric" button disabled until every switch and
// server reports success.
//
// This is intentionally a separate resource from FabricResource: in the ONES UI, designing a
// fabric (addFabricData, Draft/Inactive) and deploying it (this sequence, Deployed) are two
// distinct, separately-triggered actions — unlike e.g. Netris, where a single apply both
// declares and continuously reconciles config onto devices via an always-on controller.
//
// Scope note: this implements only the core path above. Conditional branches the ONES UI
// also supports — UFM host-mapping confirmation, border-leaf port configuration, and NMX
// onboarding/probe — are not implemented; fabrics requiring those still need manual UI steps.
//
// There is no known "undeploy" API, so destroy only removes this resource from Terraform
// state — the fabric remains Deployed in ONES.
//
// Redeploy: changing only redeploy_trigger/redeploy_allow_unreachable (every other attribute
// forces replacement) runs Update instead, matching the ONES UI's Re-Deploy button
// (RedeployFabricButton.jsx) rather than the full sequence above:
//  1. POST {config_endpoint}/api/config/redeployprecheck -> SSH-check every device in the
//     fabric's last saved YAML with its already-stored credentials (no new ones uploaded)
//  2. POST {config_endpoint}/api/config with redeploy=true -> re-push the same config
//
// A device whose stored login now fails always blocks the redeploy (the UI's rule: credential
// problems can't be fixed by redeploying); an unreachable-but-not-auth-failed device only
// blocks when redeploy_allow_unreachable is false.
type FabricDeployResource struct {
	client *APIClient
}

type FabricDeployDeviceModel struct {
	Hostname    types.String `tfsdk:"hostname"`
	IP          types.String `tfsdk:"ip"`
	Username    types.String `tfsdk:"username"`
	Password    types.String `tfsdk:"password"`
	DeviceType  types.String `tfsdk:"device_type"`
	DeviceRole  types.String `tfsdk:"device_role"`
	ApplyConfig types.Bool   `tfsdk:"apply_config"`
}

// fabricDeployDeviceAttrTypes mirrors the "devices" NestedObject schema below — used to
// rebuild data.Devices from a resolved []FabricDeployDeviceModel before writing state.
var fabricDeployDeviceAttrTypes = map[string]attr.Type{
	"hostname":     types.StringType,
	"ip":           types.StringType,
	"username":     types.StringType,
	"password":     types.StringType,
	"device_type":  types.StringType,
	"device_role":  types.StringType,
	"apply_config": types.BoolType,
}

type FabricDeployResourceModel struct {
	FabricName     types.String `tfsdk:"fabric_name"`
	Instance       types.String `tfsdk:"instance"`
	Description    types.String `tfsdk:"description"`
	DeploymentType types.String `tfsdk:"deployment_type"`
	Devices        types.List   `tfsdk:"devices"`
	CustomYaml     types.String `tfsdk:"custom_yaml"`
	// RedeployTrigger is the only attribute without RequiresReplace: changing it (e.g. to a
	// timestamp) is how you ask Terraform to call Update instead of replacing the resource,
	// which runs the lightweight redeploy path (precheck + re-push) instead of the full
	// upload/validate/inventory/push/status sequence. See RedeployAllowUnreachable below.
	RedeployTrigger          types.String `tfsdk:"redeploy_trigger"`
	RedeployAllowUnreachable types.Bool   `tfsdk:"redeploy_allow_unreachable"`

	ID types.String `tfsdk:"id"`
}

func (r *FabricDeployResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_fabric_deploy"
}

func (r *FabricDeployResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Deploys a fabric created by fabricapi_fabric: uploads real device credentials, validates switches and servers over SSH (hard-fails on any failure, matching the ONES UI's Deploy-button gate), pushes credentials to inventory, pushes the generated config to the real switches, then marks the fabric Deployed. Requires provider `config_endpoint`. Does not implement the UFM/NMX/border-leaf-port conditional flows. There is no known \"undeploy\" API, so destroy only removes this resource from Terraform state — the fabric remains Deployed in ONES. Change `redeploy_trigger` (and/or `redeploy_allow_unreachable`) to re-push the config to recover devices that failed, without a full replace — see those attributes for details; matches the ONES UI's Re-Deploy button.",
		Attributes: map[string]schema.Attribute{
			"fabric_name": schema.StringAttribute{
				MarkdownDescription: "Name of an existing fabric to deploy (e.g. fabricapi_fabric.this.name).",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"instance": schema.StringAttribute{
				MarkdownDescription: "FM instance used for the config push (POST /api/config `instance` field). Should match the `instance` used when the fabric was created. Defaults to \"fm\" if unset.",
				Optional:            true,
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Description recorded on the fabric when marking it Deployed.",
				Optional:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"custom_yaml": schema.StringAttribute{
				MarkdownDescription: "A pre-edited fabric YAML to deploy instead of fetching the server's current generated YAML (GET /fabrics/{name}). Use `data.fabricapi_fabric_yaml.this.yaml` to review the auto-generated YAML first, hand-edit it, then pass the edited content back in here (e.g. `custom_yaml = file(\"fabric.reviewed.yaml\")`) — or supply a YAML from elsewhere entirely to deploy without ever fetching the generated one. Leave unset to use the server's current YAML as-is. Regardless of the source, every device's `ipAddress`/`Credentials` in the YAML are overwritten from `devices` before pushing, so a YAML captured before credentials existed (e.g. an early design-only snapshot) still gets deployed with the real values.",
				Optional:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"deployment_type": schema.StringAttribute{
				MarkdownDescription: "One of DEFAULT, PARTIAL_CONFIG, or BROWNFIELD. Defaults to \"DEFAULT\" if unset.",
				Optional:            true,
				Computed:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"devices": schema.ListNestedAttribute{
				MarkdownDescription: "Every switch, server, and DPU in the fabric that needs real credentials uploaded, validated, and pushed to inventory before deploy — should cover every device from the fabric's generated inventory.",
				Required:            true,
				PlanModifiers: []planmodifier.List{
					listplanmodifier.RequiresReplace(),
				},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"hostname": schema.StringAttribute{
							Required: true,
						},
						"ip": schema.StringAttribute{
							Required: true,
						},
						"username": schema.StringAttribute{
							Required: true,
						},
						"password": schema.StringAttribute{
							Required:  true,
							Sensitive: true,
						},
						"device_type": schema.StringAttribute{
							MarkdownDescription: "e.g. \"switch\", \"server\", \"dpu\" — used with device_role to route this device to switch or server validation.",
							Optional:            true,
						},
						"device_role": schema.StringAttribute{
							MarkdownDescription: "e.g. \"spine\", \"leaf\", \"host\" — used with device_type to route this device to switch or server validation.",
							Optional:            true,
						},
						"apply_config": schema.BoolAttribute{
							MarkdownDescription: "Whether this device is included in switch validation and the config push. Defaults to true.",
							Optional:            true,
							Computed:            true,
						},
					},
				},
			},
			"redeploy_trigger": schema.StringAttribute{
				MarkdownDescription: "Set/change this (e.g. to a timestamp or build id) to re-push this fabric's current config without recreating the resource — matching the ONES UI's Re-Deploy button. Unlike every other attribute here, this does NOT force replacement: changing only this (and/or `redeploy_allow_unreachable`) runs a lightweight redeploy instead of the full uploadip/validateswitch/validateserver/updateinventory/updatefabricstatus sequence. Like `fabricapi_fabric_redeploy`, it re-fetches and re-pushes whatever is currently saved server-side as-is — never the `devices`/`custom_yaml` from Terraform state, which may be stale relative to corrections made directly against the server; to deploy different devices or a different YAML, change those instead, which still forces a full redeploy via replacement. Before pushing, every device in the fabric's last saved YAML is SSH-checked with its already-stored credentials (POST .../redeployprecheck); a device whose login has changed (not merely unreachable) always blocks the redeploy, matching the UI, since this path never uploads new credentials.",
				Optional:            true,
			},
			"redeploy_allow_unreachable": schema.BoolAttribute{
				MarkdownDescription: "When a redeploy's precheck finds devices that are unreachable but not an outright login failure, proceed anyway (those specific devices will fail again; others may still succeed) — matching the UI's \"Re-Deploy Anyway\" confirmation. Never bypasses an actual credential/login failure. Defaults to false (block on any precheck failure).",
				Optional:            true,
			},
			"id": schema.StringAttribute{
				Computed: true,
			},
		},
	}
}

func (r *FabricDeployResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// isSwitchDevice classifies a device as a spine/leaf switch, based on either device_type
// or device_role — matching how validateswitch expects only leaf/spine devices.
func isSwitchDevice(d FabricDeviceInput) bool {
	t := strings.ToLower(strings.TrimSpace(d.DeviceType))
	r := strings.ToLower(strings.TrimSpace(d.DeviceRole))
	return t == "switch" || r == "spine" || r == "leaf"
}

// isServerDevice classifies a device as a host/DPU, based on either device_type or
// device_role — matching how validateserver expects host devices.
func isServerDevice(d FabricDeviceInput) bool {
	t := strings.ToLower(strings.TrimSpace(d.DeviceType))
	r := strings.ToLower(strings.TrimSpace(d.DeviceRole))
	return t == "server" || t == "dpu" || t == "host" || r == "host"
}

// failedDeviceValidations turns a validateswitch/validateserver response into failure
// messages: any Error is a failure, and any result missing the expected success field
// (checked via missingSuccess) with no Error is also treated as a failure.
func failedDeviceValidations(results []ValidateDeviceResult, missingSuccess func(ValidateDeviceResult) bool) []string {
	var failures []string
	for _, res := range results {
		if strings.TrimSpace(res.Error) != "" {
			failures = append(failures, fmt.Sprintf("%s (%s): %s", res.Hostname, res.IP, res.Error))
			continue
		}
		if missingSuccess(res) {
			failures = append(failures, fmt.Sprintf("%s (%s): validation did not report success", res.Hostname, res.IP))
		}
	}
	return failures
}

// findMappingValue returns the value node for key within a YAML mapping node, or nil if
// the mapping is nil, isn't a mapping, or has no such key.
func findMappingValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// setScalarValue overwrites a scalar node's value in place as a plain string.
func setScalarValue(node *yaml.Node, value string) {
	if node == nil {
		return
	}
	node.Kind = yaml.ScalarNode
	node.Tag = "!!str"
	node.Value = value
}

// injectDeviceCredentials walks the fabric YAML's Connectivity section (Spine/Leaf/Tor/
// SSpine/Host, each a sequence of devices keyed by switchName or hostName) and stamps the
// real ipAddress/Credentials for every device matching one of the given devices by
// hostname. Applied unconditionally — for a freshly fetched YAML this is a no-op safety net
// (UploadFabricDeviceIPs already patched the server's own copy), but for a caller-supplied
// custom_yaml it's the actual fix: that file may have been captured before any credentials
// were ever uploaded (e.g. an early Scenario-2 snapshot), and would otherwise be pushed with
// permanently blank ipAddress/Credentials even though real ones were just uploaded above.
func injectDeviceCredentials(doc *yaml.Node, devices []FabricDeviceInput) {
	if doc == nil || len(doc.Content) == 0 {
		return
	}
	connectivity := findMappingValue(doc.Content[0], "Connectivity")
	if connectivity == nil {
		return
	}

	byHostname := make(map[string]FabricDeviceInput, len(devices))
	for _, d := range devices {
		byHostname[strings.ToLower(strings.TrimSpace(d.Hostname))] = d
	}

	for i := 1; i < len(connectivity.Content); i += 2 {
		tier := connectivity.Content[i]
		if tier.Kind != yaml.SequenceNode {
			continue
		}
		for _, device := range tier.Content {
			if device.Kind != yaml.MappingNode {
				continue
			}
			nameNode := findMappingValue(device, "switchName")
			if nameNode == nil {
				nameNode = findMappingValue(device, "hostName")
			}
			if nameNode == nil {
				continue
			}
			match, ok := byHostname[strings.ToLower(strings.TrimSpace(nameNode.Value))]
			if !ok {
				continue
			}
			setScalarValue(findMappingValue(device, "ipAddress"), match.IP)
			if creds := findMappingValue(device, "Credentials"); creds != nil {
				setScalarValue(findMappingValue(creds, "user"), match.Username)
				setScalarValue(findMappingValue(creds, "password"), match.Password)
			}
		}
	}
}

// yamlNodeToOrderedJSON converts a parsed *yaml.Node into JSON, preserving mapping key
// order and sequence order exactly as they appeared in the source document. This matters
// because json.Marshal on a generic map[string]any always sorts keys alphabetically —
// using that here would silently reorder every key in the fabric's YAML on every deploy,
// even though only device credentials (patched by UploadFabricDeviceIPs) actually changed.
func yamlNodeToOrderedJSON(node *yaml.Node) (json.RawMessage, error) {
	switch node.Kind {
	case yaml.DocumentNode:
		if len(node.Content) == 0 {
			return json.RawMessage("null"), nil
		}
		return yamlNodeToOrderedJSON(node.Content[0])
	case yaml.MappingNode:
		var buf bytes.Buffer
		buf.WriteByte('{')
		for i := 0; i+1 < len(node.Content); i += 2 {
			if i > 0 {
				buf.WriteByte(',')
			}
			keyJSON, err := json.Marshal(node.Content[i].Value)
			if err != nil {
				return nil, err
			}
			buf.Write(keyJSON)
			buf.WriteByte(':')
			valJSON, err := yamlNodeToOrderedJSON(node.Content[i+1])
			if err != nil {
				return nil, err
			}
			buf.Write(valJSON)
		}
		buf.WriteByte('}')
		return json.RawMessage(buf.Bytes()), nil
	case yaml.SequenceNode:
		var buf bytes.Buffer
		buf.WriteByte('[')
		for i, item := range node.Content {
			if i > 0 {
				buf.WriteByte(',')
			}
			itemJSON, err := yamlNodeToOrderedJSON(item)
			if err != nil {
				return nil, err
			}
			buf.Write(itemJSON)
		}
		buf.WriteByte(']')
		return json.RawMessage(buf.Bytes()), nil
	case yaml.AliasNode:
		return yamlNodeToOrderedJSON(node.Alias)
	default: // yaml.ScalarNode
		var v any
		if err := node.Decode(&v); err != nil {
			return nil, err
		}
		scalarJSON, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		return json.RawMessage(scalarJSON), nil
	}
}

func (r *FabricDeployResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data FabricDeployResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	instance := "fm"
	if !data.Instance.IsNull() && !data.Instance.IsUnknown() && strings.TrimSpace(data.Instance.ValueString()) != "" {
		instance = data.Instance.ValueString()
	}
	deploymentType := "DEFAULT"
	if !data.DeploymentType.IsNull() && !data.DeploymentType.IsUnknown() && strings.TrimSpace(data.DeploymentType.ValueString()) != "" {
		deploymentType = data.DeploymentType.ValueString()
	}
	fabricName := data.FabricName.ValueString()
	description := data.Description.ValueString()

	var deviceModels []FabricDeployDeviceModel
	if !data.Devices.IsNull() && !data.Devices.IsUnknown() {
		resp.Diagnostics.Append(data.Devices.ElementsAs(ctx, &deviceModels, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	if len(deviceModels) == 0 {
		resp.Diagnostics.AddError(
			"Missing devices",
			"`devices` must list every switch/server/DPU (hostname, ip, username, password) so their credentials can be uploaded and validated before deploy.",
		)
		return
	}

	devices := make([]FabricDeviceInput, 0, len(deviceModels))
	for i, dm := range deviceModels {
		applyConfig := true
		if !dm.ApplyConfig.IsNull() && !dm.ApplyConfig.IsUnknown() {
			applyConfig = dm.ApplyConfig.ValueBool()
		}
		// apply_config is Optional+Computed: resolve it back into the model so the list
		// written to state below has no unknown values (required by the framework).
		deviceModels[i].ApplyConfig = types.BoolValue(applyConfig)
		devices = append(devices, FabricDeviceInput{
			Hostname:    dm.Hostname.ValueString(),
			IP:          dm.IP.ValueString(),
			Username:    dm.Username.ValueString(),
			Password:    dm.Password.ValueString(),
			DeviceType:  dm.DeviceType.ValueString(),
			DeviceRole:  dm.DeviceRole.ValueString(),
			ApplyConfig: applyConfig,
		})
	}

	// Step 1: fill in real IPs/credentials (patches the generated YAML on disk).
	uploadResp, err := r.client.UploadFabricDeviceIPs(ctx, fabricName, devices)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to upload device IPs/credentials for fabric %q: %s", fabricName, err))
		return
	}
	if uploadResp != "" {
		resp.Diagnostics.AddWarning("Device IPs uploaded", uploadResp)
	}

	// Step 2: validate switches. Hard-fails the apply on any device error, matching the
	// ONES UI's Deploy-button gate.
	var switchDevices []FabricDeviceInput
	for _, d := range devices {
		if isSwitchDevice(d) && d.ApplyConfig {
			switchDevices = append(switchDevices, d)
		}
	}
	if len(switchDevices) > 0 {
		results, err := r.client.ValidateSwitches(ctx, fabricName, switchDevices)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to validate switches for fabric %q: %s", fabricName, err))
			return
		}
		if failures := failedDeviceValidations(results, func(res ValidateDeviceResult) bool { return strings.TrimSpace(res.Build) == "" }); len(failures) > 0 {
			resp.Diagnostics.AddError(
				"Switch validation failed",
				fmt.Sprintf("One or more switches failed validation for fabric %q:\n- %s", fabricName, strings.Join(failures, "\n- ")),
			)
			return
		}
	}

	// Step 3: validate servers. Hard-fails the apply on any device error.
	var serverDevices []FabricDeviceInput
	for _, d := range devices {
		if isServerDevice(d) {
			serverDevices = append(serverDevices, d)
		}
	}
	if len(serverDevices) > 0 {
		results, err := r.client.ValidateServers(ctx, fabricName, serverDevices)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to validate servers for fabric %q: %s", fabricName, err))
			return
		}
		if failures := failedDeviceValidations(results, func(res ValidateDeviceResult) bool { return strings.TrimSpace(res.OS) == "" }); len(failures) > 0 {
			resp.Diagnostics.AddError(
				"Server validation failed",
				fmt.Sprintf("One or more servers failed validation for fabric %q:\n- %s", fabricName, strings.Join(failures, "\n- ")),
			)
			return
		}
	}

	// Step 4: push credentials to the downstream FM engine's inventory.
	inventoryResp, err := r.client.UpdateFabricInventory(ctx, fabricName, devices)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update inventory for fabric %q: %s", fabricName, err))
		return
	}
	if inventoryResp != "" {
		resp.Diagnostics.AddWarning("Fabric inventory updated", inventoryResp)
	}

	// Step 5: get the YAML to push to the switches — either the caller's own edited/supplied
	// YAML (custom_yaml, for the "download, hand-edit, redeploy" and "deploy a YAML from
	// elsewhere" flows), or the server's current credential-filled generated YAML otherwise.
	var rawYAML string
	if !data.CustomYaml.IsNull() && !data.CustomYaml.IsUnknown() && strings.TrimSpace(data.CustomYaml.ValueString()) != "" {
		rawYAML = data.CustomYaml.ValueString()
	} else {
		fetched, err := r.client.GetFabricYaml(ctx, fabricName)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to fetch generated fabric YAML for %q: %s", fabricName, err))
			return
		}
		rawYAML = fetched
	}
	// Parsed as a yaml.Node (not a generic map) so key/sequence order from the fetched
	// document is preserved exactly — json.Marshal on a map always sorts keys, which would
	// reformat the whole document even though only device credentials actually changed.
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(rawYAML), &doc); err != nil {
		resp.Diagnostics.AddError("YAML Parse Error", fmt.Sprintf("Unable to parse generated fabric YAML for %q: %s", fabricName, err))
		return
	}
	// Always stamp the real per-device ipAddress/Credentials in, regardless of whether this
	// YAML was just fetched or supplied via custom_yaml — see injectDeviceCredentials.
	injectDeviceCredentials(&doc, devices)
	orderedJSON, err := yamlNodeToOrderedJSON(&doc)
	if err != nil {
		resp.Diagnostics.AddError("YAML Parse Error", fmt.Sprintf("Unable to convert generated fabric YAML for %q to JSON: %s", fabricName, err))
		return
	}
	pushResp, err := r.client.PushFabricConfig(ctx, fabricName, instance, orderedJSON, false)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to push config for fabric %q: %s", fabricName, err))
		return
	}
	if pushResp != "" {
		resp.Diagnostics.AddWarning("Fabric config pushed", pushResp)
	}

	// Step 6: mark the fabric Deployed.
	statusResp, err := r.client.UpdateFabricStatus(ctx, UpdateFabricStatusRequest{
		Name:           fabricName,
		Status:         "Deployed",
		Description:    description,
		DeploymentType: deploymentType,
	})
	if err != nil {
		resp.Diagnostics.AddError(
			"Client Error",
			fmt.Sprintf("Config was pushed for fabric %q, but marking it Deployed failed: %s", fabricName, err),
		)
		return
	}
	if statusResp != "" {
		resp.Diagnostics.AddWarning("Fabric status updated", statusResp)
	}

	devicesList, diags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: fabricDeployDeviceAttrTypes}, deviceModels)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Devices = devicesList

	data.Instance = types.StringValue(instance)
	data.DeploymentType = types.StringValue(deploymentType)
	data.ID = types.StringValue(fabricName)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *FabricDeployResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// No known API distinctly reporting deploy status separate from the fabric itself;
	// keep state as-is (best-effort no-op).
	var data FabricDeployResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// Update only runs when redeploy_trigger and/or redeploy_allow_unreachable are the sole
// changes — every other attribute has RequiresReplace, so the framework would have planned a
// full replace (Delete+Create) instead if fabric_name/devices/custom_yaml/etc. had changed.
// Like fabricapi_fabric_redeploy, this re-pushes whatever YAML is currently saved server-side
// as-is — it never re-injects devices/custom_yaml from Terraform state, since those may be
// stale relative to corrections made directly against the server.
func (r *FabricDeployResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data FabricDeployResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	instance := "fm"
	if !data.Instance.IsNull() && !data.Instance.IsUnknown() && strings.TrimSpace(data.Instance.ValueString()) != "" {
		instance = data.Instance.ValueString()
	}
	fabricName := data.FabricName.ValueString()
	allowUnreachable := !data.RedeployAllowUnreachable.IsNull() && !data.RedeployAllowUnreachable.IsUnknown() && data.RedeployAllowUnreachable.ValueBool()

	// Pre-check: SSH into every device in the fabric's last saved YAML with its already-
	// stored credentials (not `devices` above — the server reads its own saved copy).
	// Matches the ONES UI's Re-Deploy button exactly, including which failures block outright.
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
			fmt.Sprintf("Login failed or credentials are missing for one or more devices in fabric %q — a redeploy re-pushes the existing config and never uploads new credentials, so this must be fixed by replacing fabricapi_fabric_deploy (new `devices`), not by retrying the redeploy:\n- %s", fabricName, strings.Join(blocking, "\n- ")),
		)
		return
	}
	if len(unreachable) > 0 && !allowUnreachable {
		resp.Diagnostics.AddError(
			"Redeploy precheck found unreachable devices",
			fmt.Sprintf("One or more devices in fabric %q are not reachable (these will fail again if pushed):\n- %s\n\nSet redeploy_allow_unreachable = true to redeploy the rest anyway, matching the ONES UI's \"Re-Deploy Anyway\".", fabricName, strings.Join(unreachable, "\n- ")),
		)
		return
	}
	if len(unreachable) > 0 {
		resp.Diagnostics.AddWarning(
			"Redeploying with unreachable devices",
			fmt.Sprintf("Proceeding past unreachable devices in fabric %q (redeploy_allow_unreachable = true) — these will fail again:\n- %s", fabricName, strings.Join(unreachable, "\n- ")),
		)
	}

	// Re-push the fabric's current saved YAML as-is, matching fabricapi_fabric_redeploy —
	// never re-inject devices/custom_yaml from Terraform state, which may be stale relative
	// to corrections made directly against the server (the precheck above already validated
	// against the server's own stored credentials, not these).
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
	if pushResp != "" {
		resp.Diagnostics.AddWarning("Fabric redeployed", pushResp)
	}

	data.ID = types.StringValue(fabricName)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *FabricDeployResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	// There is no known "undeploy" API. Removing this resource only stops Terraform from
	// managing the deploy action — the fabric remains Deployed in ONES.
	resp.State.RemoveResource(ctx)
}
