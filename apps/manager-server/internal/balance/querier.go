package balance

import (
	"context"
	"crypto/x509"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"
)

const defaultQueryTimeout = 15 * time.Second
const maxQueryTimeout = 60 * time.Second
const maxResponseBodySize = 2 << 20

// NetworkOptions varies only system dependencies. The production safety
// transport, address policy, certificate validation and redirects remain intact.
// Never populate this from HTTP input or allow an arbitrary RoundTripper.
type NetworkOptions struct {
	RootCAs     *x509.CertPool
	LookupIP    func(context.Context, string) ([]net.IPAddr, error)
	DialContext func(context.Context, string, string) (net.Conn, error)
}

func New() Querier                            { return NewWithNetwork(NetworkOptions{}) }
func NewWithNetwork(n NetworkOptions) Querier { return &defaultQuerier{network: n} }

type defaultQuerier struct{ network NetworkOptions }

func (q *defaultQuerier) ListCapabilities(context.Context) ([]AdapterCapability, error) {
	return []AdapterCapability{
		{Adapter: "deepseek", Supported: true, RequiresConfig: true, ConfigFields: []string{"query_credential"}, SupportedScopes: []string{"account_wallet"}, SupportedFormats: []string{"exact_currency_balances"}},
		{Adapter: "deepinfra", Supported: false, RequiresConfig: true},
	}, nil
}
func (q *defaultQuerier) Query(ctx context.Context, target QueryTarget, resolver CredentialResolver) (Observation, error) {
	out := Observation{QueryID: uuid.NewString(), ObservedAtMS: time.Now().UnixMilli(), CapabilityStatus: "unsupported", QueryStatus: "unsupported", ErrorCode: "ADAPTER_NOT_FOUND"}
	if strings.ToLower(strings.TrimSpace(target.Adapter)) != "deepseek" {
		return out, nil
	}
	out.CapabilityStatus = "supported"
	credential, err := resolver.ResolveCredential(ctx, target.CredentialRef)
	if err != nil {
		out.QueryStatus = "auth_error"
		out.ErrorCode = "CREDENTIAL_RESOLVE_FAILED"
		return out, nil
	}
	if strings.TrimSpace(credential) == "" {
		out.QueryStatus = "needs_configuration"
		out.ErrorCode = "CREDENTIAL_MISSING"
		return out, nil
	}
	if target.EndpointURL == "" {
		target.EndpointURL = "https://api.deepseek.com/user/balance"
	}
	if ValidateSource(target.EndpointURL, target.PrivateTargets) != nil {
		out.QueryStatus = "network_error"
		out.ErrorCode = "INVALID_ENDPOINT"
		return out, nil
	}
	timeout := target.Timeout
	if timeout <= 0 {
		timeout = defaultQueryTimeout
	}
	if timeout > maxQueryTimeout {
		timeout = maxQueryTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return queryDeepSeek(ctx, target, credential, q.network, out), nil
}
