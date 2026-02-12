package interfaces

import inferno "github.com/llm-d/llm-d-workload-variant-autoscaler/pkg/core"

// Captures response from ModelAnalyzer(s) per model
type ModelAnalyzeResponse struct {
	// feasible allocations for all accelerators
	Allocations map[string]*ModelAcceleratorAllocation // accelerator name -> allocation
}

// Allocation details of an accelerator to a variant
type ModelAcceleratorAllocation struct {
	Allocation *inferno.Allocation // allocation result of model analyzer

	RequiredPrefillQPS float64
	RequiredDecodeQPS  float64
	Reason             string
}

type ServiceClassEntry struct {
	Model   string `yaml:"model"`
	SLOTPOT int    `yaml:"slo-tpot"`
	SLOTTFT int    `yaml:"slo-ttft"`
}

type ServiceClass struct {
	Name     string              `yaml:"name"`
	Priority int                 `yaml:"priority"`
	Data     []ServiceClassEntry `yaml:"data"`
}

// PrometheusConfig holds complete Prometheus client configuration including TLS settings
type PrometheusConfig struct {
	// BaseURL is the Prometheus server URL (must use https:// scheme)
	BaseURL string `json:"baseURL"`

	// TLS configuration fields (TLS is always enabled for HTTPS-only support)
	InsecureSkipVerify bool   `json:"insecureSkipVerify,omitempty"` // Skip certificate verification (development/testing only)
	CACertPath         string `json:"caCertPath,omitempty"`         // Path to CA certificate for server validation
	ClientCertPath     string `json:"clientCertPath,omitempty"`     // Path to client certificate for mutual TLS authentication
	ClientKeyPath      string `json:"clientKeyPath,omitempty"`      // Path to client private key for mutual TLS authentication
	ServerName         string `json:"serverName,omitempty"`         // Expected server name for SNI (Server Name Indication)

	// Authentication fields (BearerToken takes precedence over TokenPath)
	BearerToken string `json:"bearerToken,omitempty"` // Direct bearer token string (development/testing)
	TokenPath   string `json:"tokenPath,omitempty"`   // Path to file containing bearer token (production with mounted secrets)
}
