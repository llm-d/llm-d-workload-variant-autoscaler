package utils

import (
	"testing"

	interfaces "github.com/llm-d/llm-d-workload-variant-autoscaler/internal/interfaces"
	"github.com/llm-d/llm-d-workload-variant-autoscaler/internal/logging"
	"github.com/stretchr/testify/assert"
)

func init() {
	// Initialize logger for tests
	logging.NewTestLogger()
}

func TestCreateTLSConfig(t *testing.T) {
	tests := []struct {
		name        string
		promConfig  *interfaces.PrometheusConfig
		expectError bool
	}{
		{
			name:        "nil config",
			promConfig:  nil,
			expectError: false,
		},
		{
			name: "TLS with insecure skip verify",
			promConfig: &interfaces.PrometheusConfig{
				InsecureSkipVerify: true,
			},
			expectError: false,
		},
		{
			name: "TLS with server name",
			promConfig: &interfaces.PrometheusConfig{
				ServerName: "prometheus.example.com",
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config, err := CreateTLSConfig(tt.promConfig)
			if tt.expectError {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			if tt.promConfig != nil {
				assert.NotNil(t, config)
			} else {
				assert.Nil(t, config)
			}
		})
	}
}

func TestValidateTLSConfig(t *testing.T) {
	tests := []struct {
		name        string
		promConfig  *interfaces.PrometheusConfig
		expectError bool
		expectPanic bool
	}{
		{
			name:        "nil config - should panic",
			promConfig:  nil,
			expectError: false,
			expectPanic: true,
		},
		{
			name: "HTTP URL - should fail",
			promConfig: &interfaces.PrometheusConfig{
				BaseURL: "http://prometheus:9090",
			},
			expectError: true,
			expectPanic: false,
		},
		{
			name: "TLS with insecure skip verify",
			promConfig: &interfaces.PrometheusConfig{
				InsecureSkipVerify: true,
				BaseURL:            "https://prometheus:9090",
			},
			expectError: false,
			expectPanic: false,
		},
		{
			name: "TLS with server name",
			promConfig: &interfaces.PrometheusConfig{
				ServerName: "prometheus.example.com",
				BaseURL:    "https://prometheus:9090",
			},
			expectError: false,
			expectPanic: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.expectPanic {
				assert.Panics(t, func() {
					_ = ValidateTLSConfig(tt.promConfig)
				})
			} else {
				err := ValidateTLSConfig(tt.promConfig)
				if tt.expectError {
					assert.Error(t, err)
				} else {
					assert.NoError(t, err)
				}
			}
		})
	}
}
