package pdrole

import (
	appsv1 "k8s.io/api/apps/v1"
)

// GetDeploymentPDRole determines the P/D role of a deployment by checking
// pod template labels against the provided labelConfig.
//
// This is label-only detection, matching how the EPP's filter plugins work:
// the EPP's prefill-filter and decode-filter (and by-label) plugins route
// traffic based solely on pod labels, never deployment names.
//
// Returns RoleUnknown if the deployment has no matching label. Callers should
// use the PDDiscoveryResult.Disaggregated flag to interpret RoleUnknown:
//   - If Disaggregated=false, skip detection entirely and treat all deployments as RoleBoth
//   - If Disaggregated=true and RoleUnknown, the deployment has no P/D label set
func GetDeploymentPDRole(deploy *appsv1.Deployment, labelConfig PDRoleLabelConfig) PDRole {
	if deploy == nil {
		return RoleUnknown
	}

	return detectFromLabels(deploy, labelConfig)
}

// detectFromLabels checks pod template labels against the label config.
func detectFromLabels(deploy *appsv1.Deployment, config PDRoleLabelConfig) PDRole {
	if config.LabelKey == "" {
		return RoleUnknown
	}

	labels := deploy.Spec.Template.Labels
	if labels == nil {
		return RoleUnknown
	}

	value, exists := labels[config.LabelKey]
	if !exists {
		return RoleUnknown
	}

	return matchLabelValue(value, config)
}

// matchLabelValue matches a label value against the config's role value lists.
func matchLabelValue(value string, config PDRoleLabelConfig) PDRole {
	for _, v := range config.PrefillValues {
		if value == v {
			return RolePrefill
		}
	}
	for _, v := range config.DecodeValues {
		if value == v {
			return RoleDecode
		}
	}
	for _, v := range config.BothValues {
		if value == v {
			return RoleBoth
		}
	}
	return RoleUnknown
}
