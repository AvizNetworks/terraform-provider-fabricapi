package provider

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// This file resolves and validates fabricapi_fabric's inputs the same way the
// ONES UI does before it calls addFabricData (selfcare/src/ui-v2/pages/
// inventory/FabricSuConfig.jsx, FabricNetwork.jsx, components/NewHostPerSu.jsx).
// The addFabricData backend itself accepts out-of-range values and generates a
// fabric anyway, so these checks are the only guard. Error summaries reuse the
// UI's own messages where it has one, so the same mistake reads the same way
// from either side.

const (
	fabricTypeSpX13 = "NVIDIA SpX RA 1.3"
	fabricTypeSpX21 = "NVIDIA SpX RA 2.1"
	fabricTypeAviz  = "Aviz RA 1.0"

	networkTypeEthernet   = "ethernet"
	networkTypeInfiniband = "infiniband"

	tenantControlOnes     = "ones"
	tenantControlExternal = "external"

	// spxGpuSubnet is the GPU subnet the UI fixes (and disables) for SpX RA 2.1.
	spxGpuSubnet = "172"
)

// fabricNodeTypes lists the node_type values each fabric type's UI offers
// (FabricSuConfig.jsx). SpX RA 1.3 isn't listed: its node types aren't
// restricted here.
var fabricNodeTypes = map[string][]string{
	fabricTypeSpX21: {"b300_32", "b300_64", "gb300"},
	fabricTypeAviz:  {"dgx", "b300_32", "b300_64", "gb200", "gb300"},
}

// fabricInput is the fabric configuration with defaults applied. setXxx flags
// record whether the user set an optional value explicitly, so a value the UI
// forces can be filled in when unset but rejected when it contradicts.
type fabricInput struct {
	Type     string
	NodeType string

	NumOfSUs    int
	MaxNumOfSUs int
	FlatHostMap map[string]string            // nil when RackHostMap is used
	RackHostMap map[string]map[string]string // gb200/gb300 only

	TenantControl string

	EnableEW          bool
	NetworkType       string
	NetworkTypeSet    bool
	StartingSubnetGpu string
	EwTenantAware     bool

	EnableNS              bool
	NsOperatingSystem     string
	DedicatedStorage      bool
	StartingSubnetCpu     string
	StartingSubnetStorage string
	StartingSubnetTenants string
	DpuHbn                bool

	IsVxlanFabric         bool
	IsVxlanFabricSet      bool
	StorageTenantAware    bool
	StorageTenantAwareSet bool
}

// fabricIssue is one validation error: Summary is the UI's message (or the
// closest equivalent), Detail names the Terraform attribute to fix.
type fabricIssue struct {
	Summary string
	Detail  string
}

func isRackNodeType(nodeType string) bool { return nodeType == "gb200" || nodeType == "gb300" }

func racksPerSu(nodeType string) int {
	if nodeType == "gb300" {
		return 3
	}
	return 2
}

// maxSusFor mirrors FabricSuConfig.jsx: 16 SUs for 64-node B300 SUs, else 32.
func maxSusFor(nodeType string) int {
	if nodeType == "b300_64" {
		return 16
	}
	return 32
}

// maxHostsPerSuFor mirrors FabricSuConfig.jsx isHostMapComplete.
func maxHostsPerSuFor(nodeType string) int {
	if nodeType == "b300_64" {
		return 64
	}
	return 32
}

const maxNodesPerRack = 18

// suSizeFor is the addFabricData suSize field for B300: the UI derives it from
// the "Size per SU" radio, which node_type already encodes. Without it the
// backend (B300Strategy._resolveSuSize) builds every B300 fabric as 32-node SUs.
func suSizeFor(nodeType string) int {
	switch nodeType {
	case "b300_32":
		return 32
	case "b300_64":
		return 64
	}
	return 0
}

// applyForcedDefaults fills in values the UI fixes for the given choices, when
// the user left them unset. Contradicting explicit values are left alone so
// validate can report them.
func (in *fabricInput) applyForcedDefaults() {
	if !in.NetworkTypeSet {
		// FabricNetwork.jsx: GB200 is forced to InfiniBand (isGB200AvizRa and the
		// E-W checkbox handler); SpX RA 2.1 is forced to Ethernet.
		if in.NodeType == "gb200" && in.Type != fabricTypeSpX21 {
			in.NetworkType = networkTypeInfiniband
		} else {
			in.NetworkType = networkTypeEthernet
		}
	}
	if in.Type == fabricTypeSpX21 && in.EnableEW && strings.TrimSpace(in.StartingSubnetGpu) == "" {
		in.StartingSubnetGpu = spxGpuSubnet
	}
	// handleOSChange: SONiC forces VXLAN + storage tenant aware, except on Aviz RA 1.0.
	if in.EnableNS && in.NsOperatingSystem == "sonic" && in.Type != fabricTypeAviz {
		if !in.IsVxlanFabricSet {
			in.IsVxlanFabric = true
		}
		if !in.StorageTenantAwareSet {
			in.StorageTenantAware = true
		}
	}
	// DPU HBN forces VXLAN (the Tenant Segmentation radio is replaced by "VXLAN").
	if in.DpuHbn && !in.IsVxlanFabricSet {
		in.IsVxlanFabric = true
	}
}

// validate returns every problem found, so one plan shows them all.
func (in *fabricInput) validate() []fabricIssue {
	var issues []fabricIssue
	add := func(summary, detail string, args ...any) {
		issues = append(issues, fabricIssue{Summary: summary, Detail: fmt.Sprintf(detail, args...)})
	}

	// --- Node type (FabricSuConfig.jsx) ---
	if in.NodeType == "b300" {
		add("Size per SU is required",
			"su_config.node_type \"b300\" needs a size per SU: use \"b300_32\" (32 nodes) or \"b300_64\" (64 nodes).")
	} else if allowed, ok := fabricNodeTypes[in.Type]; ok && !containsString(allowed, in.NodeType) {
		add("Unsupported node type",
			"su_config.node_type %q is not offered for %q. Supported: %s (leave node_type unset for \"dgx\").",
			in.NodeType, in.Type, strings.Join(allowed, ", "))
	}

	// --- Number of SU / max SU ---
	maxSu := maxSusFor(in.NodeType)
	if in.NumOfSUs < 1 || in.NumOfSUs > maxSu {
		add(fmt.Sprintf("Value must be between 1 and %d", maxSu),
			"su_config.number_of_sus is %d; %s allows 1 to %d SUs.", in.NumOfSUs, in.NodeType, maxSu)
	}
	if in.MaxNumOfSUs < 1 || in.MaxNumOfSUs > maxSu {
		add(fmt.Sprintf("Value must be between 1 and %d", maxSu),
			"su_config.max_number_of_sus is %d; %s allows 1 to %d SUs.", in.MaxNumOfSUs, in.NodeType, maxSu)
	}
	if in.NumOfSUs > in.MaxNumOfSUs {
		add("Max SU cannot be less than number of SU",
			"su_config.number_of_sus (%d) is greater than su_config.max_number_of_sus (%d).", in.NumOfSUs, in.MaxNumOfSUs)
	}

	// --- Hosts per SU (NewHostPerSu.jsx) ---
	issues = append(issues, in.validateHosts()...)

	// --- E-W network ---
	if in.NetworkTypeSet && !in.EnableEW {
		add("network_type requires E-W networking",
			"network_config.network_type is set but enable_east_west_networking is false. Remove network_type or enable E-W.")
	}
	if in.EnableEW {
		switch {
		case in.Type == fabricTypeSpX21 && in.NetworkType != networkTypeEthernet:
			add("Unsupported network type",
				"network_config.network_type must be \"ethernet\" for %q: the UI only offers Ethernet E-W.", in.Type)
		case in.NodeType == "gb200" && in.NetworkType != networkTypeInfiniband:
			add("Unsupported network type",
				"network_config.network_type must be \"infiniband\" for node_type \"gb200\": the UI forces InfiniBand with NVLink scale-up.")
		}
		if in.NetworkType == networkTypeInfiniband {
			if strings.TrimSpace(in.StartingSubnetGpu) != "" {
				add("GPU subnet is not used with InfiniBand",
					"network_config.starting_subnet_gpu must be unset when network_type is \"infiniband\" (the UI clears it).")
			}
			if in.EwTenantAware {
				add("E-W tenant aware requires Ethernet",
					"network_config.ew_tenant_aware is only available for Ethernet E-W networking.")
			}
		} else {
			if strings.TrimSpace(in.StartingSubnetGpu) == "" {
				add("Please enter a valid GPU Subnet",
					"network_config.starting_subnet_gpu is required when enable_east_west_networking is true and network_type is \"ethernet\".")
			} else if msg := validateGpuOctet(in.StartingSubnetGpu); msg != "" {
				add(msg, "network_config.starting_subnet_gpu is %q.", in.StartingSubnetGpu)
			} else if in.Type == fabricTypeSpX21 && strings.TrimSpace(in.StartingSubnetGpu) != spxGpuSubnet {
				add("GPU subnet is fixed to 172",
					"network_config.starting_subnet_gpu must be \"172\" (or unset) for %q; the UI fixes it.", in.Type)
			}
		}
	} else if in.EwTenantAware {
		add("E-W tenant aware requires E-W networking",
			"network_config.ew_tenant_aware is set but enable_east_west_networking is false.")
	}
	if in.EwTenantAware && in.TenantControl == tenantControlExternal {
		add("E-W tenant aware is not available with External tenant control",
			"network_config.ew_tenant_aware must be false when su_config.tenant_control is \"external\" (the UI disables it).")
	}

	// --- N-S network ---
	if in.EnableNS {
		if strings.TrimSpace(in.StartingSubnetCpu) == "" {
			add("Please enter a valid CPU Subnet",
				"network_config.starting_subnet_cpu is required when enable_north_south_networking is true (used as the shared user/storage subnet, or the CPU subnet when dedicated_storage is also true).")
		} else if msg := validateTwoOctet(in.StartingSubnetCpu); msg != "" {
			add(msg, "network_config.starting_subnet_cpu is %q.", in.StartingSubnetCpu)
		}
		if in.DedicatedStorage {
			if isRackNodeType(in.NodeType) {
				add("Dedicated Storage Network is not supported for GB200/GB300",
					"network_config.dedicated_storage must be false for node_type %q (the UI disables the switch).", in.NodeType)
			}
			if strings.TrimSpace(in.StartingSubnetStorage) == "" {
				add("Please enter a valid Storage Subnet",
					"network_config.starting_subnet_storage is required when dedicated_storage is true.")
			} else if msg := validateTwoOctet(in.StartingSubnetStorage); msg != "" {
				add(msg, "network_config.starting_subnet_storage is %q.", in.StartingSubnetStorage)
			}
		}
		if in.TenantControl == tenantControlExternal && strings.TrimSpace(in.StartingSubnetTenants) == "" {
			add("Please enter a valid Tenant Subnet",
				"network_config.starting_subnet_tenants is required when su_config.tenant_control is \"external\" and north-south networking is enabled.")
		}
		if in.NsOperatingSystem == "sonic" && in.Type != fabricTypeAviz {
			if in.IsVxlanFabricSet && !in.IsVxlanFabric {
				add("VXLAN is required with SONiC",
					"network_config.is_vxlan_fabric must be true (or unset) when ns_operating_system is \"sonic\" on %q; the UI forces VXLAN.", in.Type)
			}
			if in.StorageTenantAwareSet && !in.StorageTenantAware {
				add("Storage tenant aware is required with SONiC",
					"network_config.storage_tenant_aware must be true (or unset) when ns_operating_system is \"sonic\" on %q; the UI forces it.", in.Type)
			}
		}
	} else if in.DedicatedStorage {
		add("Dedicated Storage Network requires N-S networking",
			"network_config.dedicated_storage is true but enable_north_south_networking is false.")
	}
	if strings.TrimSpace(in.StartingSubnetTenants) != "" {
		if in.TenantControl != tenantControlExternal {
			add("Tenant subnet is only used with External tenant control",
				"network_config.starting_subnet_tenants is set but su_config.tenant_control is %q. Set tenant_control = \"external\" or remove it.", in.TenantControl)
		} else if msg := validatePrivateOctet(in.StartingSubnetTenants); msg != "" {
			add(msg, "network_config.starting_subnet_tenants is %q.", in.StartingSubnetTenants)
		}
	}
	if in.DpuHbn && in.IsVxlanFabricSet && !in.IsVxlanFabric {
		add("VXLAN is required with DPU HBN",
			"network_config.is_vxlan_fabric must be true (or unset) when dpu_hbn is true; the UI forces VXLAN.")
	}

	// --- Overlapping subnets (FabricNetwork.jsx findNetworkConflicts) ---
	issues = append(issues, in.validateOverlaps()...)
	return issues
}

func (in *fabricInput) validateHosts() []fabricIssue {
	var issues []fabricIssue
	add := func(summary, detail string, args ...any) {
		issues = append(issues, fabricIssue{Summary: summary, Detail: fmt.Sprintf(detail, args...)})
	}

	if isRackNodeType(in.NodeType) {
		if in.RackHostMap == nil {
			add("Racks/Nodes per SU are required",
				"su_config.rack_host_map is required for node_type %q (%d racks per SU, e.g. {\"0\" = {%s}}). A flat hosts_per_su/host_map count is applied to every rack, giving %d times the hosts.",
				in.NodeType, racksPerSu(in.NodeType), exampleRacks(racksPerSu(in.NodeType)), racksPerSu(in.NodeType))
			return issues
		}
	} else if in.RackHostMap != nil {
		add("rack_host_map is only for GB200/GB300",
			"su_config.rack_host_map is set but node_type is %q. Use hosts_per_su/host_map instead.", in.NodeType)
		return issues
	}

	keys := []string{}
	if in.RackHostMap != nil {
		for k := range in.RackHostMap {
			keys = append(keys, k)
		}
	} else {
		for k := range in.FlatHostMap {
			keys = append(keys, k)
		}
	}
	for _, k := range keys {
		idx, err := strconv.Atoi(k)
		if err != nil || idx < 0 || idx >= in.NumOfSUs {
			add("Hosts specified for an SU that doesn't exist",
				"SU %q in the host map is outside 0..%d (number_of_sus is %d).", k, in.NumOfSUs-1, in.NumOfSUs)
		}
	}

	for i := 0; i < in.NumOfSUs && i < 64; i++ {
		su := strconv.Itoa(i)
		if in.RackHostMap != nil {
			racks, ok := in.RackHostMap[su]
			if !ok {
				add("Racks/Nodes must be specified for every SU", "su_config.rack_host_map has no entry for SU %s.", su)
				continue
			}
			n := racksPerSu(in.NodeType)
			for name := range racks {
				r, err := strconv.Atoi(strings.TrimPrefix(name, "rack"))
				if !strings.HasPrefix(name, "rack") || err != nil || r < 1 || r > n {
					add(fmt.Sprintf("%s has %d racks per SU", strings.ToUpper(in.NodeType), n),
						"su_config.rack_host_map SU %s has rack %q; use rack1..rack%d.", su, name, n)
				}
			}
			for r := 1; r <= n; r++ {
				v, ok := racks[fmt.Sprintf("rack%d", r)]
				if !ok {
					add("Racks/Nodes must be specified for every rack", "su_config.rack_host_map SU %s is missing rack%d.", su, r)
					continue
				}
				if msg := validateCount(v, maxNodesPerRack); msg != "" {
					add(msg, "su_config.rack_host_map SU %s rack%d is %q (1 to %d nodes per rack).", su, r, v, maxNodesPerRack)
				}
			}
		} else {
			v, ok := in.FlatHostMap[su]
			if !ok {
				add("Hosts must be specified for every SU", "The host map (hosts_per_su/host_map) has no entry for SU %s.", su)
				continue
			}
			maxHosts := maxHostsPerSuFor(in.NodeType)
			if msg := validateCount(v, maxHosts); msg != "" {
				add(msg, "Hosts for SU %s is %q (1 to %d hosts per SU for %s).", su, v, maxHosts, in.NodeType)
			}
		}
	}
	return issues
}

type subnetRef struct {
	field string
	oct1  int
	oct2  int // -1 when the subnet is a single first octet
}

func (in *fabricInput) validateOverlaps() []fabricIssue {
	var nets []subnetRef
	if in.EnableEW && in.NetworkType == networkTypeEthernet {
		if o, ok := parsePrivateOctet(in.StartingSubnetGpu); ok {
			nets = append(nets, subnetRef{"starting_subnet_gpu", o, -1})
		}
	}
	if in.EnableNS {
		if a, b, ok := parseTwoOctet(in.StartingSubnetCpu); ok {
			nets = append(nets, subnetRef{"starting_subnet_cpu", a, b})
		}
		if in.DedicatedStorage {
			if a, b, ok := parseTwoOctet(in.StartingSubnetStorage); ok {
				nets = append(nets, subnetRef{"starting_subnet_storage", a, b})
			}
		}
		if in.TenantControl == tenantControlExternal {
			if o, ok := parsePrivateOctet(in.StartingSubnetTenants); ok {
				nets = append(nets, subnetRef{"starting_subnet_tenants", o, -1})
			}
		}
	}

	var issues []fabricIssue
	for i := 0; i < len(nets); i++ {
		for j := i + 1; j < len(nets); j++ {
			a, b := nets[i], nets[j]
			// The UI only checks the tenant pool against the GPU subnet.
			involvesTenant := a.field == "starting_subnet_tenants" || b.field == "starting_subnet_tenants"
			involvesGpu := a.field == "starting_subnet_gpu" || b.field == "starting_subnet_gpu"
			if involvesTenant && !involvesGpu {
				continue
			}
			if a.oct1 == b.oct1 && (a.oct2 == -1 || b.oct2 == -1 || a.oct2 == b.oct2) {
				issues = append(issues, fabricIssue{
					Summary: fmt.Sprintf("Network clash: %s conflicts with %s. Please pick non-overlapping private ranges.", a.field, b.field),
					Detail:  fmt.Sprintf("network_config.%s and network_config.%s overlap (same first octet %d).", a.field, b.field, a.oct1),
				})
			}
		}
	}
	return issues
}

var (
	digitsOnly = regexp.MustCompile(`^\d{1,3}$`)
	allDigits  = regexp.MustCompile(`^\d+$`)
)

// validateGpuOctet mirrors FabricNetwork.jsx handleChange for startingSubnetGpu.
func validateGpuOctet(v string) string {
	v = strings.TrimSpace(v)
	n, err := strconv.Atoi(v)
	if !digitsOnly.MatchString(v) || err != nil || n < 0 || n > 255 {
		return "Please enter value between 0-255"
	}
	if n != 10 && n != 172 && n != 192 {
		return "GPU first octet must be one of: 10, 172, 192"
	}
	return ""
}

// validateTwoOctet mirrors FabricNetwork.jsx parseTwoOctet plus the reserved
// E-W range check (10.253 / 10.254) for CPU and storage subnets.
func validateTwoOctet(v string) string {
	parts := strings.Split(strings.TrimSpace(v), ".")
	if len(parts) != 2 {
		return "Enter as A.B (e.g. 10.2 or 172.16 or 192.168)"
	}
	for _, p := range parts {
		if !digitsOnly.MatchString(p) || (len(p) > 1 && p[0] == '0') {
			return "Octets must be in range of 0–255"
		}
	}
	a, _ := strconv.Atoi(parts[0])
	b, _ := strconv.Atoi(parts[1])
	if a > 255 || b > 255 {
		return "Octets must be numbers between 0 and 255"
	}
	if a != 10 && a != 172 && a != 192 {
		return "First octet must be 10, 172 or 192"
	}
	if a == 10 && (b == 253 || b == 254) {
		return "The IP range is reserved for E-W"
	}
	return ""
}

// validatePrivateOctet mirrors FabricNetwork.jsx parsePrivateOctet (tenant pool).
func validatePrivateOctet(v string) string {
	if _, ok := parsePrivateOctet(v); !ok {
		return "First octet must be 10, 172 or 192"
	}
	return ""
}

func parsePrivateOctet(v string) (int, bool) {
	v = strings.TrimSpace(v)
	if !allDigits.MatchString(v) {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil || (n != 10 && n != 172 && n != 192) {
		return 0, false
	}
	return n, true
}

func parseTwoOctet(v string) (int, int, bool) {
	if strings.TrimSpace(v) == "" || validateTwoOctet(v) != "" {
		return 0, 0, false
	}
	parts := strings.Split(strings.TrimSpace(v), ".")
	a, _ := strconv.Atoi(parts[0])
	b, _ := strconv.Atoi(parts[1])
	return a, b, true
}

// validateCount mirrors NewHostPerSu.jsx: a whole number from 1 to max.
func validateCount(v string, max int) string {
	v = strings.TrimSpace(v)
	if strings.Contains(v, ".") {
		return "Decimals are not allowed"
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > max {
		return fmt.Sprintf("Value must be between 1 and %d", max)
	}
	return ""
}

func exampleRacks(n int) string {
	parts := make([]string, 0, n)
	for r := 1; r <= n; r++ {
		parts = append(parts, fmt.Sprintf("rack%d = \"1\"", r))
	}
	return strings.Join(parts, ", ")
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// gpuScaleModeFor mirrors FabricNetwork.jsx handleSubmit: the E-W mode stored
// on the fabric, and whether NVLink scale-up applies. ONES reads gpuScaleMode
// back when the fabric is reopened, and only loads the NMXC (NVLink) GPU
// inventory when it contains SCALE_UP.
func gpuScaleModeFor(fabricType, nodeType, networkType string, enableEW bool) (string, bool) {
	scaleUp := (isRackNodeType(nodeType) && networkType == networkTypeInfiniband) ||
		(fabricType == fabricTypeSpX21 && nodeType == "gb300" && networkType == networkTypeEthernet)
	switch {
	case !enableEW:
		return "SCALE_NONE", scaleUp
	case scaleUp && networkType == networkTypeInfiniband:
		return "SCALE_OUT_SCALE_UP_IB", scaleUp
	case scaleUp:
		return "SCALE_OUT_SCALE_UP_ETH", scaleUp
	case networkType == networkTypeInfiniband:
		return "SCALE_OUT_IB", scaleUp
	default:
		return "SCALE_OUT_ETH", scaleUp
	}
}

// wireNetworkType is the addFabricData networkType value; the UI spells
// InfiniBand "infinyband".
func wireNetworkType(networkType string) string {
	if networkType == networkTypeInfiniband {
		return "infinyband"
	}
	return networkTypeEthernet
}
