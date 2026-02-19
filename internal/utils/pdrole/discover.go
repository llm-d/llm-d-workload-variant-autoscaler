package pdrole

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	epconfigv1alpha1 "sigs.k8s.io/gateway-api-inference-extension/apix/config/v1alpha1"
	"sigs.k8s.io/yaml"

	"github.com/llm-d/llm-d-workload-variant-autoscaler/internal/logging"
	poolutil "github.com/llm-d/llm-d-workload-variant-autoscaler/internal/utils/pool"
)

// byLabelParams mirrors the by-label plugin parameter structure from llm-d-inference-scheduler.
// We define it locally since we don't import the inference-scheduler module.
type byLabelParams struct {
	Label         string   `json:"label"`
	ValidValues   []string `json:"validValues"`
	AllowsNoLabel bool     `json:"allowsNoLabel"`
}

// DiscoverPDRoleLabelConfig discovers the P/D role label configuration by inspecting
// the EPP's EndpointPickerConfig from its mounted ConfigMap.
//
// The pool parameter connects the discovery to a specific EPP instance, enabling
// per-pool P/D label configuration when multiple EPPs exist with different settings.
//
// Returns a PDDiscoveryResult where:
//   - Disaggregated=true means P/D filter plugins were found in the EPP config.
//     The EPP uses INCLUSION-based filters (from llm-d-inference-scheduler):
//     prefill-filter accepts only pods labeled "prefill" (allowsNoLabel=false),
//     decode-filter accepts pods labeled "decode" or "both" plus unlabeled pods
//     (allowsNoLabel=true). Use LabelConfig with GetDeploymentPDRole to determine
//     each deployment's role.
//   - Disaggregated=false means no P/D plugins exist. The EPP routes to all pods
//     equally — all deployments effectively serve both prefill and decode.
//     Callers should treat all deployments as RoleBoth.
//
// Returns an error if the discovery chain encounters a Kubernetes API failure
// (service lookup, deployment listing, ConfigMap fetch). Non-error conditions
// (nil pool, no P/D plugins) return Disaggregated=false with nil error.
//
// Discovery chain:
//  1. Extract EPP service name and namespace from the pool's EndpointPicker
//  2. Find EPP deployment via service selector matching
//  3. Find ConfigMaps mounted as volumes in EPP deployment
//  4. Parse ConfigMap data as EndpointPickerConfig (YAML/JSON)
//  5. Extract P/D label config from filter plugins
func DiscoverPDRoleLabelConfig(
	ctx context.Context,
	k8sClient client.Client,
	pool *poolutil.EndpointPool,
) (PDDiscoveryResult, error) {
	logger := ctrl.LoggerFrom(ctx)

	notDisaggregated := PDDiscoveryResult{
		LabelConfig:   DefaultPDRoleLabelConfig(),
		Disaggregated: false,
	}

	if pool == nil || pool.EndpointPicker == nil {
		logger.V(logging.DEBUG).Info("No pool or EndpointPicker provided, P/D disaggregation not detected")
		return notDisaggregated, nil
	}

	eppServiceName := pool.EndpointPicker.ServiceName
	namespace := pool.EndpointPicker.Namespace

	// Step 1: Find EPP deployment from service
	deploy, err := findEPPDeployment(ctx, k8sClient, namespace, eppServiceName)
	if err != nil {
		return notDisaggregated, fmt.Errorf("finding EPP deployment for pool %s (service %s/%s): %w",
			pool.Name, namespace, eppServiceName, err)
	}

	// Step 2: Find ConfigMap from deployment volumes
	configData, err := findConfigDataFromDeployment(ctx, k8sClient, deploy)
	if err != nil {
		return notDisaggregated, fmt.Errorf("finding EPP config from deployment %s/%s: %w",
			namespace, deploy.Name, err)
	}

	// Step 3: Parse EndpointPickerConfig
	epc, err := parseEndpointPickerConfig(configData)
	if err != nil {
		return notDisaggregated, fmt.Errorf("parsing EndpointPickerConfig from deployment %s/%s: %w",
			namespace, deploy.Name, err)
	}

	// Step 4: Extract P/D label config from plugins
	config, found := extractPDRoleLabelConfig(epc)
	if !found {
		logger.V(logging.DEBUG).Info("No P/D filter plugins found in EndpointPickerConfig, P/D disaggregation not detected",
			"pool", pool.Name,
			"deployment", deploy.Name,
			"namespace", namespace)
		return notDisaggregated, nil
	}

	logger.V(logging.DEBUG).Info("Discovered P/D label config from EndpointPickerConfig",
		"pool", pool.Name,
		"labelKey", config.LabelKey,
		"prefillValues", config.PrefillValues,
		"decodeValues", config.DecodeValues,
		"bothValues", config.BothValues)
	return PDDiscoveryResult{
		LabelConfig:   config,
		Disaggregated: true,
	}, nil
}

// findEPPDeployment finds the EPP deployment by looking up the service and matching
// deployments by the service's selector labels.
func findEPPDeployment(ctx context.Context, k8sClient client.Client, namespace, serviceName string) (*appsv1.Deployment, error) {
	// Get the service
	svc := &corev1.Service{}
	if err := k8sClient.Get(ctx, client.ObjectKey{Namespace: namespace, Name: serviceName}, svc); err != nil {
		return nil, err
	}

	if len(svc.Spec.Selector) == 0 {
		return nil, errNoServiceSelector
	}

	// List deployments matching the service selector
	deployList := &appsv1.DeploymentList{}
	if err := k8sClient.List(ctx, deployList,
		client.InNamespace(namespace),
		client.MatchingLabels(svc.Spec.Selector),
	); err != nil {
		return nil, err
	}

	if len(deployList.Items) == 0 {
		return nil, errNoDeploymentFound
	}

	return &deployList.Items[0], nil
}

// findConfigDataFromDeployment finds and returns the config data from ConfigMap volumes
// mounted in the deployment.
func findConfigDataFromDeployment(ctx context.Context, k8sClient client.Client, deploy *appsv1.Deployment) ([]byte, error) {
	for _, vol := range deploy.Spec.Template.Spec.Volumes {
		if vol.ConfigMap == nil {
			continue
		}

		// Get the ConfigMap
		cm := &corev1.ConfigMap{}
		if err := k8sClient.Get(ctx, client.ObjectKey{
			Namespace: deploy.Namespace,
			Name:      vol.ConfigMap.Name,
		}, cm); err != nil {
			continue // Try next volume
		}

		// Look for config data in the ConfigMap (sorted keys for deterministic traversal)
		keys := make([]string, 0, len(cm.Data))
		for key := range cm.Data {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if isConfigKey(key) {
				return []byte(cm.Data[key]), nil
			}
		}
	}

	return nil, errNoConfigMapFound
}

// isConfigKey checks if a ConfigMap data key likely contains an EndpointPickerConfig.
func isConfigKey(key string) bool {
	lower := strings.ToLower(key)
	return strings.HasSuffix(lower, ".yaml") ||
		strings.HasSuffix(lower, ".yml") ||
		strings.HasSuffix(lower, ".json") ||
		lower == "config" ||
		lower == "epp-config" ||
		lower == "endpointpickerconfig"
}

// parseEndpointPickerConfig parses raw config data as an EndpointPickerConfig.
// Supports both YAML and JSON formats.
func parseEndpointPickerConfig(data []byte) (*epconfigv1alpha1.EndpointPickerConfig, error) {
	var epc epconfigv1alpha1.EndpointPickerConfig

	// sigs.k8s.io/yaml handles both YAML and JSON
	if err := yaml.Unmarshal(data, &epc); err != nil {
		return nil, err
	}

	// Validate that we got meaningful data
	if len(epc.Plugins) == 0 && len(epc.SchedulingProfiles) == 0 {
		return nil, errInvalidConfig
	}

	return &epc, nil
}

// extractPDRoleLabelConfig extracts P/D role label configuration from EndpointPickerConfig plugins.
//
// Detection logic:
//  1. If prefill-filter or decode-filter plugin types exist → well-known label is used
//  2. If by-label plugins exist and are referenced by "prefill"/"decode" scheduling profiles
//     → extract custom label key and values from plugin parameters
func extractPDRoleLabelConfig(epc *epconfigv1alpha1.EndpointPickerConfig) (PDRoleLabelConfig, bool) {
	// Build plugin index by name for scheduling profile lookups
	pluginsByName := make(map[string]epconfigv1alpha1.PluginSpec, len(epc.Plugins))
	for _, p := range epc.Plugins {
		name := p.Name
		if name == "" {
			name = p.Type
		}
		pluginsByName[name] = p
	}

	// Check for dedicated prefill-filter / decode-filter plugin types
	hasDedicatedFilter := false
	for _, p := range epc.Plugins {
		if p.Type == PluginTypePrefillFilter || p.Type == PluginTypeDecodeFilter {
			hasDedicatedFilter = true
			break
		}
	}

	if hasDedicatedFilter {
		// Dedicated filter types use the well-known label
		return DefaultPDRoleLabelConfig(), true
	}

	// Check for by-label plugins referenced by prefill/decode scheduling profiles
	return extractByLabelConfig(epc, pluginsByName)
}

// extractByLabelConfig attempts to extract P/D role config from by-label plugins
// that are referenced by scheduling profiles named "prefill" or "decode".
//
// Values that appear in both prefill and decode profiles' validValues are classified
// as BothValues (pods with these labels pass both filters). Remaining values are
// exclusive to their respective profile.
func extractByLabelConfig(
	epc *epconfigv1alpha1.EndpointPickerConfig,
	pluginsByName map[string]epconfigv1alpha1.PluginSpec,
) (PDRoleLabelConfig, bool) {
	// Collect raw values from each profile type
	var labelKey string
	var rawPrefill, rawDecode []string
	found := false

	for _, profile := range epc.SchedulingProfiles {
		profileName := strings.ToLower(profile.Name)
		isPrefill := strings.Contains(profileName, "prefill")
		isDecode := strings.Contains(profileName, "decode")

		if !isPrefill && !isDecode {
			continue
		}

		// Look for by-label plugin references in this profile
		for _, sp := range profile.Plugins {
			plugin, exists := pluginsByName[sp.PluginRef]
			if !exists || plugin.Type != PluginTypeByLabel {
				continue
			}

			// Parse the by-label parameters
			var params byLabelParams
			if err := json.Unmarshal(plugin.Parameters, &params); err != nil {
				continue
			}

			if params.Label == "" {
				continue
			}

			// Set the label key (should be same across all P/D plugins)
			labelKey = params.Label
			found = true

			if isPrefill {
				rawPrefill = append(rawPrefill, params.ValidValues...)
			}
			if isDecode {
				rawDecode = append(rawDecode, params.ValidValues...)
			}
		}
	}

	if !found {
		return PDRoleLabelConfig{}, false
	}

	// Compute intersection: values accepted by both profiles are "both" values.
	// A pod with such a label passes both prefill and decode filters,
	// meaning it can serve both roles.
	prefillSet := toStringSet(rawPrefill)
	decodeSet := toStringSet(rawDecode)

	config := PDRoleLabelConfig{LabelKey: labelKey}

	for _, v := range rawPrefill {
		if _, inDecode := decodeSet[v]; inDecode {
			config.BothValues = appendUnique(config.BothValues, v)
		} else {
			config.PrefillValues = append(config.PrefillValues, v)
		}
	}
	for _, v := range rawDecode {
		if _, inPrefill := prefillSet[v]; inPrefill {
			// already added to BothValues above
			continue
		}
		config.DecodeValues = append(config.DecodeValues, v)
	}

	return config, true
}

// toStringSet converts a slice to a set for O(1) lookups.
func toStringSet(values []string) map[string]struct{} {
	set := make(map[string]struct{}, len(values))
	for _, v := range values {
		set[v] = struct{}{}
	}
	return set
}

// appendUnique appends a value to a slice only if it's not already present.
func appendUnique(slice []string, val string) []string {
	for _, v := range slice {
		if v == val {
			return slice
		}
	}
	return append(slice, val)
}
