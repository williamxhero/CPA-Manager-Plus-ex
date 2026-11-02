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
		{Adapter: VolcengineAccountBalanceV20220101, Supported: true, RequiresConfig: true, ConfigFields: []string{"query_credential", "volcengine.accountID", "volcengine.currency", "volcengine.unit", "volcengine.currencyUnitConfirmed", "volcengine.accountBindingConfirmed", "volcengine.proofReference"}, SupportedScopes: []string{"account_wallet"}, SupportedFormats: []string{"exact_decimal_amount"}},
		{Adapter: "deepseek", Supported: true, RequiresConfig: true, ConfigFields: []string{"query_credential"}, SupportedScopes: []string{"account_wallet"}, SupportedFormats: []string{"exact_currency_balances"}},
		{Adapter: "deepinfra", Supported: true, RequiresConfig: true, ConfigFields: []string{"query_credential"}, SupportedScopes: []string{"signed_prepaid_net"}, SupportedFormats: []string{"exact_decimal_amount"}},
		{Adapter: NewAPIUserSelfV1, Supported: true, RequiresConfig: true, ConfigFields: []string{"query_credential", "protocolConfirmed", "instanceVersion", "auth", "userID", "accountScope", "nativeUnit"}, SupportedScopes: []string{"account_wallet"}, SupportedFormats: []string{"native_quota", "explicit_exact_money"}},
		{Adapter: NewAPITokenUsageV1, Supported: true, RequiresConfig: true, ConfigFields: []string{"query_credential", "protocolConfirmed", "instanceVersion", "auth", "nativeUnit"}, SupportedScopes: []string{"key_remaining"}, SupportedFormats: []string{"native_quota", "explicit_exact_money"}},
	}, nil
}
func (q *defaultQuerier) Query(ctx context.Context, target QueryTarget, resolver CredentialResolver) (Observation, error) {
	if target.Adapter == VolcengineAccountBalanceV20220101 {
		return queryVolcengine(ctx, target, resolver, q.network), nil
	}
	if target.Adapter == NewAPIUserSelfV1 || target.Adapter == NewAPITokenUsageV1 {
		if ValidateNewAPIContract(target.Adapter, target.NewAPI) != nil {
			return Observation{QueryID: uuid.NewString(), CapabilityStatus: "needs_configuration", QueryStatus: "needs_configuration", ErrorCode: "NEWAPI_CONTRACT_REQUIRED", ObservedAtMS: time.Now().UnixMilli()}, nil
		}
		return QueryNewAPI(ctx, target, *target.NewAPI, resolver, q.network), nil
	}
	out := Observation{QueryID: uuid.NewString(), ObservedAtMS: time.Now().UnixMilli(), CapabilityStatus: "unsupported", QueryStatus: "unsupported", ErrorCode: "ADAPTER_NOT_FOUND"}
	adapter := strings.ToLower(strings.TrimSpace(target.Adapter))
	if adapter != "deepseek" && adapter != "deepinfra" {
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
		if adapter == "deepinfra" {
			target.EndpointURL = "https://api.deepinfra.com/payment/checklist"
		}
	}
	if ValidateAdapterSource(adapter, target.EndpointURL, target.PrivateTargets) != nil {
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
	if adapter == "deepinfra" {
		return queryDeepInfra(ctx, target, credential, q.network, out), nil
	}
	return queryDeepSeek(ctx, target, credential, q.network, out), nil
}
