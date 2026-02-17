// Package pdrole provides utilities for detecting Prefill/Decode (P/D) disaggregation
// roles of deployments. Discovery inspects the EPP's EndpointPickerConfig for P/D
// filter plugins; detection checks pod template labels against the discovered config.
package pdrole

import "errors"

var (
	errNoServiceSelector = errors.New("EPP service has no selector labels")
	errNoDeploymentFound = errors.New("no deployment found matching EPP service selector")
	errNoConfigMapFound  = errors.New("no ConfigMap with valid config found in EPP deployment volumes")
	errInvalidConfig     = errors.New("parsed config has no plugins or scheduling profiles")
)

// PDRole represents the Prefill/Decode role of a deployment in a P/D disaggregation setup.
//
// Role values and label key are aligned with llm-d-inference-scheduler
// (pkg/plugins/filter/pd_role.go). We redefine them locally as typed constants
// to avoid importing that module (heavyweight dependency not otherwise needed)
// and to add RoleUnknown which the scheduler doesn't define.
type PDRole string

const (
	// RolePrefill indicates a prefill-only deployment (KV cache producer).
	// Matches filter.RolePrefill in llm-d-inference-scheduler.
	RolePrefill PDRole = "prefill"
	// RoleDecode indicates a decode-only deployment (KV cache consumer).
	// Matches filter.RoleDecode in llm-d-inference-scheduler.
	RoleDecode PDRole = "decode"
	// RoleBoth indicates a deployment serving both prefill and decode.
	// Matches filter.RoleBoth in llm-d-inference-scheduler.
	RoleBoth PDRole = "both"
	// RoleUnknown indicates the P/D role could not be determined.
	// Not defined in llm-d-inference-scheduler (WVA-specific).
	RoleUnknown PDRole = "unknown"

	// DefaultRoleLabel is the well-known label key used by llm-d-inference-scheduler
	// to identify P/D roles on pods.
	// Matches filter.RoleLabel in llm-d-inference-scheduler.
	DefaultRoleLabel = "llm-d.ai/role"

	// Plugin types from llm-d-inference-scheduler used in EndpointPickerConfig.
	// Match filter.PrefillRoleType and filter.DecodeRoleType respectively.
	PluginTypePrefillFilter = "prefill-filter"
	PluginTypeDecodeFilter  = "decode-filter"
	PluginTypeByLabel       = "by-label"
)

// PDRoleLabelConfig describes how to detect P/D roles from pod template labels.
// The LabelKey specifies which label to inspect, and the value slices define
// which label values correspond to which role.
type PDRoleLabelConfig struct {
	// LabelKey is the pod label key to check for P/D role (e.g., "llm-d.ai/role").
	LabelKey string
	// PrefillValues are label values that indicate a prefill role.
	PrefillValues []string
	// DecodeValues are label values that indicate a decode role.
	DecodeValues []string
	// BothValues are label values that indicate both prefill and decode roles.
	BothValues []string
}

// PDDiscoveryResult holds the result of P/D role label config discovery.
// Callers should check Disaggregated to determine whether P/D disaggregation
// is configured for this pool:
//   - Disaggregated=true: P/D filter plugins found, use LabelConfig with GetDeploymentPDRole
//   - Disaggregated=false: no P/D plugins, all deployments serve both roles (RoleBoth)
type PDDiscoveryResult struct {
	// LabelConfig is the discovered or default P/D label configuration.
	LabelConfig PDRoleLabelConfig
	// Disaggregated indicates whether P/D disaggregation is configured.
	// When false, all deployments should be treated as RoleBoth.
	Disaggregated bool
}

// DefaultPDRoleLabelConfig returns the standard P/D role label configuration
// using the well-known llm-d.ai/role label with standard values.
func DefaultPDRoleLabelConfig() PDRoleLabelConfig {
	return PDRoleLabelConfig{
		LabelKey:      DefaultRoleLabel,
		PrefillValues: []string{"prefill"},
		DecodeValues:  []string{"decode"},
		BothValues:    []string{"both"},
	}
}
