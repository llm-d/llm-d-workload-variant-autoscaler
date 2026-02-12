package utils

import (
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/prometheus/client_golang/api"
	ctrl "sigs.k8s.io/controller-runtime"

	interfaces "github.com/llm-d/llm-d-workload-variant-autoscaler/internal/interfaces"
	"github.com/llm-d/llm-d-workload-variant-autoscaler/internal/logging"
)

// CreatePrometheusTransport creates a custom HTTPS transport for Prometheus client with TLS support.
// TLS is always enabled for HTTPS-only support with configurable certificate validation.
func CreatePrometheusTransport(config *interfaces.PrometheusConfig) (http.RoundTripper, error) {
	// Clone the default transport to get all the good defaults
	transport := http.DefaultTransport.(*http.Transport).Clone()

	// Configure TLS (always required for HTTPS-only support)
	tlsConfig, err := CreateTLSConfig(config)
	if err != nil {
		return nil, err
	}
	transport.TLSClientConfig = tlsConfig
	ctrl.Log.V(logging.VERBOSE).Info("TLS configuration applied to Prometheus HTTPS transport")

	return transport, nil
}

// CreatePrometheusClientConfig creates a complete Prometheus client configuration with HTTPS support.
// Supports both direct bearer tokens and token files for flexible authentication.
func CreatePrometheusClientConfig(config *interfaces.PrometheusConfig) (*api.Config, error) {
	clientConfig := &api.Config{
		Address: config.BaseURL,
	}

	// Create custom HTTPS transport with TLS support
	transport, err := CreatePrometheusTransport(config)
	if err != nil {
		return nil, err
	}

	// Add bearer token authentication if provided
	bearerToken := config.BearerToken

	// If no direct bearer token but token path is provided, read from file
	if bearerToken == "" && config.TokenPath != "" {
		tokenBytes, err := os.ReadFile(config.TokenPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read bearer token from %s: %w", config.TokenPath, err)
		}
		bearerToken = strings.TrimSpace(string(tokenBytes))
		ctrl.Log.V(logging.VERBOSE).Info("Bearer token loaded from file", "path", config.TokenPath)
	}

	if bearerToken != "" {
		// Create a custom round tripper that adds the bearer token
		transport = &bearerTokenRoundTripper{
			base:  transport,
			token: bearerToken,
		}
	}

	clientConfig.RoundTripper = transport

	return clientConfig, nil
}

// bearerTokenRoundTripper adds bearer token authentication to HTTPS requests
type bearerTokenRoundTripper struct {
	base  http.RoundTripper
	token string
}

// RoundTrip adds the Authorization header with bearer token
func (b *bearerTokenRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(req)
}
