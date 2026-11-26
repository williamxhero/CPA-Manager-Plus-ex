package balance

import (
	"context"
	"io"
	"mime"
	"net/http"
	"strings"
)

// ProbeResult contains only controlled evidence, never a header, body, value
// or transport error that could echo the bearer credential.
type ProbeResult struct {
	Status int
	Fields []string
	JSON   bool
	Reason string
}

// ProbeMonetarySource makes one GET on the existing pinned balance transport.
// Only adapters whose monetary contract can be checked with a bearer key alone
// are eligible. Automatic discovery never grants private-target exceptions.
func ProbeMonetarySource(ctx context.Context, adapter, endpoint, credential string, network NetworkOptions) ProbeResult {
	out := ProbeResult{Fields: []string{}}
	switch adapter {
	case "deepseek", "deepinfra", OpenAICompatUsageV1:
	default:
		out.Reason = "unsupported_adapter"
		return out
	}
	if ValidateAdapterSource(adapter, endpoint, nil) != nil {
		out.Reason = "invalid_endpoint"
		return out
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		out.Reason = "invalid_endpoint"
		return out
	}
	req.Header.Set("Authorization", "Bearer "+credential)
	req.Header.Set("Accept", "application/json")
	client := secureClient(QueryTarget{EndpointURL: endpoint}, network)
	// Record redirects as status evidence, without following them.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	defer client.CloseIdleConnections()
	res, err := client.Do(req)
	if err != nil {
		out.Reason = "upstream_unavailable"
		return out
	}
	defer res.Body.Close()
	out.Status = res.StatusCode
	if res.StatusCode != http.StatusOK {
		out.Reason = "upstream_status"
		return out
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBodySize+1))
	if err != nil || len(body) > maxResponseBodySize {
		out.Reason = "unreadable_response"
		return out
	}
	switch adapter {
	case "deepseek":
		if available, _ := parseDeepSeekBalance(body); available != nil {
			out.Fields = []string{"is_available", "balance_infos[].currency", "balance_infos[].total_balance", "balance_infos[].granted_balance", "balance_infos[].topped_up_balance"}
		}
	case "deepinfra":
		if _, ok := parseDeepInfraBalance(body); ok {
			out.Fields = []string{"stripe_balance", "recent"}
		}
	case OpenAICompatUsageV1:
		if _, field, ok := parseCompatUsageBalance(body); ok {
			out.Fields = []string{field}
		}
	}
	// DeepInfra and compat usage specify USD in their adapter contract rather
	// than on the wire. Evidence lists only fields that actually matched.
	contentType, _, err := mime.ParseMediaType(res.Header.Get("Content-Type"))
	out.JSON = err == nil && (contentType == "application/json" || strings.HasPrefix(contentType, "application/") && strings.HasSuffix(contentType, "+json"))
	if len(out.Fields) == 0 {
		out.Reason = "contract_mismatch"
	} else if !out.JSON {
		out.Reason = "non_json_content_type"
	}
	return out
}
