package balance

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	defaultQueryTimeout    = 30 * time.Second
	maxResponseBodySize    = 1 << 20 // 1MB
	defaultHTTPDialTimeout = 10 * time.Second
)

// New creates a new balance querier with all registered adapters
func New() Querier {
	return &defaultQuerier{
		client:   newSecureHTTPClient(),
		adapters: registerAdapters(),
	}
}

type defaultQuerier struct {
	client   *http.Client
	adapters map[string]protocolAdapter
}

func (q *defaultQuerier) ListCapabilities(ctx context.Context) ([]AdapterCapability, error) {
	caps := make([]AdapterCapability, 0, len(q.adapters))
	for _, adapter := range q.adapters {
		caps = append(caps, adapter.Capability())
	}
	return caps, nil
}

func (q *defaultQuerier) Query(ctx context.Context, target QueryTarget, resolver CredentialResolver) (Observation, error) {
	// Resolve adapter
	adapterName := normalizeAdapterName(target.Adapter)
	adapter, ok := q.adapters[adapterName]
	if !ok {
		return Observation{
			QueryID:          generateQueryID(),
			CapabilityStatus: "unsupported",
			QueryStatus:      "unsupported",
			ErrorCode:        "ADAPTER_NOT_FOUND",
			ErrorMessage:     fmt.Sprintf("adapter %q not supported", target.Adapter),
			ObservedAtMS:     time.Now().UnixMilli(),
		}, nil
	}

	// Resolve credential
	credential, err := resolver.ResolveCredential(ctx, target.CredentialRef)
	if err != nil {
		return Observation{
			QueryID:          generateQueryID(),
			CapabilityStatus: adapter.Capability().Supported ? "supported" : "unsupported",
			QueryStatus:      "auth_error",
			ErrorCode:        "CREDENTIAL_RESOLVE_FAILED",
			ErrorMessage:     "failed to resolve credential reference",
			ObservedAtMS:     time.Now().UnixMilli(),
		}, nil
	}
	if strings.TrimSpace(credential) == "" {
		return Observation{
			QueryID:          generateQueryID(),
			CapabilityStatus: adapter.Capability().Supported ? "supported" : "unsupported",
			QueryStatus:      "needs_configuration",
			ErrorCode:        "CREDENTIAL_MISSING",
			ErrorMessage:     "query credential not configured",
			ObservedAtMS:     time.Now().UnixMilli(),
		}, nil
	}

	// Validate endpoint URL
	if target.EndpointURL != "" {
		if err := validateEndpointURL(target.EndpointURL); err != nil {
			return Observation{
				QueryID:          generateQueryID(),
				CapabilityStatus: adapter.Capability().Supported ? "supported" : "unsupported",
				QueryStatus:      "network_error",
				ErrorCode:        "INVALID_ENDPOINT",
				ErrorMessage:     safeError(err),
				ObservedAtMS:     time.Now().UnixMilli(),
			}, nil
		}
	}

	// Execute query with timeout
	timeout := target.Timeout
	if timeout <= 0 {
		timeout = defaultQueryTimeout
	}
	queryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	return adapter.Query(queryCtx, target, credential)
}

func registerAdapters() map[string]protocolAdapter {
	adapters := []protocolAdapter{
		newDeepInfraAdapter(),
		// TODO: Add OpenRouter, NewAPI, generic billing JSON adapters
	}
	m := make(map[string]protocolAdapter, len(adapters))
	for _, a := range adapters {
		m[a.Name()] = a
	}
	return m
}

func normalizeAdapterName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

func validateEndpointURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("HTTPS required, got %s", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("empty hostname")
	}
	// Deny metadata addresses
	if isMetadataAddress(host) {
		return fmt.Errorf("metadata addresses not allowed")
	}
	// Deny private IPs unless explicitly authorized (TODO: add authorization check)
	ip := net.ParseIP(host)
	if ip != nil && isPrivateIP(ip) {
		return fmt.Errorf("private IP addresses require explicit authorization")
	}
	return nil
}

func isMetadataAddress(host string) bool {
	host = strings.ToLower(host)
	// AWS, GCP, Azure metadata
	if host == "169.254.169.254" || host == "metadata.google.internal" || host == "metadata" {
		return true
	}
	return false
}

func isPrivateIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsPrivate() {
		return true
	}
	return false
}

func newSecureHTTPClient() *http.Client {
	return &http.Client{
		Timeout: defaultQueryTimeout,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: defaultHTTPDialTimeout,
			}).DialContext,
			TLSClientConfig: &tls.Config{
				MinVersion: tls.VersionTLS12,
			},
			MaxIdleConnsPerHost:   2,
			IdleConnTimeout:       30 * time.Second,
			DisableKeepAlives:     false,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

func generateQueryID() string {
	// TODO: Implement proper UUID generation
	return fmt.Sprintf("q-%d", time.Now().UnixNano())
}

func safeError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	// Redact any potential credential leaks
	msg = strings.ReplaceAll(msg, "Bearer ", "Bearer ***")
	msg = strings.ReplaceAll(msg, "apikey=", "apikey=***")
	return msg
}
