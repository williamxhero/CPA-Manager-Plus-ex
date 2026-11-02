package balance

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1" // Fixed Alibaba RPC v1 protocol, not an alternate auth fallback.
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const AliyunPrepaidCashV20171214 = "aliyun-prepaid-cash-v2017-12-14"
const AliyunBalanceEndpoint = "https://business.aliyuncs.com/"
const aliyunIdentityEndpoint = "https://sts.cn-hangzhou.aliyuncs.com/"

// Site and source association are explicit operator declarations. Matching STS
// AccountId proves the query caller's account, not the inference key or payer.
type AliyunConfig struct {
	Site                    string `json:"site"`
	AccountID               string `json:"accountID"`
	AccountBindingConfirmed bool   `json:"accountBindingConfirmed"`
	ProofReference          string `json:"proofReference"`
}

type AliyunAccountEvidence struct {
	AccountID      string `json:"accountID"`
	IdentityType   string `json:"identityType"`
	ProofReference string `json:"proofReference"`
	Qualification  string `json:"qualification"`
}

type aliyunCredentials struct {
	AccessKeyID     string `json:"accessKeyID"`
	SecretAccessKey string `json:"secretAccessKey"`
}

var aliyunAccountID = regexp.MustCompile(`^[1-9][0-9]{0,31}$`)
var aliyunProofReference = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var aliyunKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_+=/.-]{0,255}$`)

func decodeAliyunContract(raw []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if !newAPIUnambiguousJSON(raw) || decoder.Decode(value) != nil || decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid Alibaba billing contract")
	}
	return nil
}

func (c *AliyunConfig) UnmarshalJSON(raw []byte) error {
	type plain AliyunConfig
	var value plain
	if err := decodeAliyunContract(raw, &value); err != nil {
		return err
	}
	*c = AliyunConfig(value)
	return nil
}

func ValidateAliyunContract(c *AliyunConfig) error {
	if c == nil || c.Site != "china" || !aliyunAccountID.MatchString(c.AccountID) || !c.AccountBindingConfirmed || !aliyunProofReference.MatchString(c.ProofReference) {
		return errors.New("explicit China-site Alibaba source/account association required")
	}
	return nil
}

func parseAliyunSecret(secret string) (aliyunCredentials, error) {
	var c aliyunCredentials
	if len(secret) > 8192 || decodeAliyunContract([]byte(secret), &c) != nil || !aliyunKey.MatchString(c.AccessKeyID) || !aliyunKey.MatchString(c.SecretAccessKey) {
		return aliyunCredentials{}, errors.New("invalid Alibaba billing credential")
	}
	return c, nil
}

func ValidateAliyunSecret(secret string) error {
	_, err := parseAliyunSecret(secret)
	return err
}

func queryAliyun(ctx context.Context, target QueryTarget, resolver CredentialResolver, network NetworkOptions) Observation {
	out := Observation{QueryID: uuid.NewString(), ObservedAtMS: time.Now().UnixMilli(), CapabilityStatus: "needs_configuration", QueryStatus: "needs_configuration", ErrorCode: "ALIYUN_PROFILE_REQUIRED"}
	fail := func(status, code string) Observation {
		out.QueryStatus, out.ErrorCode, out.ObservedAtMS = status, code, time.Now().UnixMilli()
		if status == "needs_configuration" {
			out.CapabilityStatus = status
		}
		return out
	}
	if ValidateAliyunContract(target.Aliyun) != nil {
		return out
	}
	if target.EndpointURL == "" {
		target.EndpointURL = AliyunBalanceEndpoint
	}
	if ValidateAdapterSource(target.Adapter, target.EndpointURL, target.PrivateTargets) != nil {
		return fail("needs_configuration", "INVALID_ENDPOINT")
	}
	if resolver == nil {
		return fail("needs_configuration", "CREDENTIAL_MISSING")
	}
	// Resolve ONCE. STS and BSS use this immutable credential snapshot, under the
	// existing secret generation/config CAS and before/after Core identity fence.
	secret, err := resolver.ResolveCredential(ctx, target.CredentialRef)
	if err != nil {
		return fail("auth_error", "CREDENTIAL_RESOLVE_FAILED")
	}
	if strings.TrimSpace(secret) == "" {
		return fail("needs_configuration", "CREDENTIAL_MISSING")
	}
	credential, err := parseAliyunSecret(secret)
	if err != nil {
		return fail("auth_error", "INVALID_BILLING_CREDENTIAL")
	}
	out.CapabilityStatus = "supported"
	timeout := target.Timeout
	if timeout <= 0 {
		timeout = defaultQueryTimeout
	}
	if timeout > maxQueryTimeout {
		timeout = maxQueryTimeout
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	identityBody, status, code := aliyunRPC(ctx, target, network, credential, aliyunIdentityEndpoint, "GetCallerIdentity", "2015-04-01")
	if status != "" {
		return fail(status, code)
	}
	var identity struct {
		AccountID    string `json:"AccountId"`
		IdentityType string `json:"IdentityType"`
	}
	if !newAPIUnambiguousJSON(identityBody) || json.Unmarshal(identityBody, &identity) != nil || !aliyunAccountID.MatchString(identity.AccountID) || (identity.IdentityType != "Account" && identity.IdentityType != "RAMUser") {
		return fail("parse_error", "INVALID_CALLER_IDENTITY")
	}
	if identity.AccountID != target.Aliyun.AccountID {
		return fail("parse_error", "ALIYUN_ACCOUNT_MISMATCH")
	}
	body, status, code := aliyunRPC(ctx, target, network, credential, AliyunBalanceEndpoint, "QueryAccountBalance", "2017-12-14")
	if status != "" {
		return fail(status, code)
	}
	var data struct {
		Code    string `json:"Code"`
		Success *bool  `json:"Success"`
		Data    struct {
			Cash     *string `json:"AvailableCashAmount"`
			Currency string  `json:"Currency"`
		} `json:"Data"`
	}
	if !newAPIUnambiguousJSON(body) || json.Unmarshal(body, &data) != nil || data.Success == nil || !*data.Success || data.Code != "200" || data.Data.Cash == nil || !money.MatchString(*data.Data.Cash) || (data.Data.Currency != "CNY" && data.Data.Currency != "USD" && data.Data.Currency != "JPY") {
		return fail("parse_error", "INVALID_BALANCE")
	}
	out.Semantic, out.QueryStatus, out.ErrorCode = "account_wallet", "success", ""
	out.AmountDecimal, out.Currency = *data.Data.Cash, data.Data.Currency
	out.AliyunAccount = &AliyunAccountEvidence{AccountID: identity.AccountID, IdentityType: identity.IdentityType, ProofReference: target.Aliyun.ProofReference, Qualification: "caller_account_matched_operator_asserted_source_binding_not_key_or_payer_verified"}
	evidence := sha256.Sum256(append(append(identityBody, '\n'), body...))
	out.EvidenceHash = hex.EncodeToString(evidence[:])
	out.ObservedAtMS = time.Now().UnixMilli()
	return out
}

// Both operations are fixed read-only RPCs. Construct a separate existing secure
// transport for each origin, retaining exact-host DNS pinning, TLS validation,
// no proxies/redirects, and the SAME attempt context/budget for both requests.
func aliyunRPC(ctx context.Context, target QueryTarget, network NetworkOptions, credential aliyunCredentials, endpoint, action, version string) ([]byte, string, string) {
	params := map[string]string{"Action": action, "Version": version, "Format": "JSON", "RegionId": "cn-hangzhou", "AccessKeyId": credential.AccessKeyID, "Timestamp": time.Now().UTC().Format("2006-01-02T15:04:05Z"), "SignatureNonce": uuid.NewString(), "SignatureMethod": "HMAC-SHA1", "SignatureVersion": "1.0", "SignatureType": ""}
	canonical := aliyunCanonicalQuery(params)
	mac := hmac.New(sha1.New, []byte(credential.SecretAccessKey+"&"))
	mac.Write([]byte("GET&%2F&" + aliyunPercentEncode(canonical)))
	params["Signature"] = base64.StdEncoding.EncodeToString(mac.Sum(nil))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+aliyunCanonicalQuery(params), nil)
	if err != nil {
		return nil, "network_error", "INVALID_ENDPOINT"
	}
	req.Header.Set("Accept", "application/json")
	target.EndpointURL = endpoint
	client := secureClient(target, network)
	defer client.CloseIdleConnections()
	res, err := client.Do(req)
	if err != nil {
		return nil, "network_error", "UPSTREAM_UNAVAILABLE"
	}
	defer res.Body.Close()
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return nil, "auth_error", "UPSTREAM_AUTH"
	}
	if res.StatusCode == 429 {
		return nil, "rate_limited", "UPSTREAM_RATE_LIMITED"
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBodySize+1))
	if err != nil {
		return nil, "network_error", "RESPONSE_READ_FAILED"
	}
	if len(body) > maxResponseBodySize {
		return nil, "parse_error", "RESPONSE_TOO_LARGE"
	}
	// RPC and BSS business errors may arrive with HTTP200 or HTTP400. Inspect
	// only known codes; never echo Message, request URLs or credential material.
	var envelope struct {
		Code    string `json:"Code"`
		Success *bool  `json:"Success"`
	}
	if !newAPIUnambiguousJSON(body) || json.Unmarshal(body, &envelope) != nil {
		return nil, "parse_error", "INVALID_RESPONSE"
	}
	if envelope.Code != "" && envelope.Code != "200" {
		switch envelope.Code {
		case "NotAuthorized", "NoPermission", "Forbidden", "InvalidAccessKeyId.NotFound", "SignatureDoesNotMatch":
			return nil, "auth_error", "UPSTREAM_AUTH"
		case "AuthSiteFail":
			return nil, "needs_configuration", "ALIYUN_SITE_MISMATCH"
		case "NotApplicable", "InvalidOwner":
			return nil, "needs_configuration", "ALIYUN_ACCOUNT_UNAVAILABLE"
		case "InternalError", "UndefinedError":
			return nil, "network_error", "UPSTREAM_STATUS"
		default:
			if strings.HasPrefix(envelope.Code, "Throttling") {
				return nil, "rate_limited", "UPSTREAM_RATE_LIMITED"
			}
			return nil, "parse_error", "ALIYUN_UPSTREAM_ERROR"
		}
	}
	if res.StatusCode != 200 {
		return nil, "network_error", "UPSTREAM_STATUS"
	}
	if envelope.Success != nil && !*envelope.Success {
		return nil, "parse_error", "ALIYUN_UPSTREAM_ERROR"
	}
	return body, "", ""
}

// Alibaba RPC v1: first-party SDK v1.63.107 / commit 14528af726d975c27fce77a8caa0dcc27c6bdf22,
// sdk/auth/rpc_signature_composer.go. RFC3986, sorted raw keys, double encoding,
// GET&%2F&, base64 HMAC-SHA1(secret+"&"). No SDK credential chain or auth fallback.
func aliyunCanonicalQuery(params map[string]string) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, aliyunPercentEncode(key)+"="+aliyunPercentEncode(params[key]))
	}
	return strings.Join(parts, "&")
}

func aliyunPercentEncode(value string) string {
	const digits = "0123456789ABCDEF"
	var out strings.Builder
	for i := 0; i < len(value); i++ {
		c := value[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("-_.~", rune(c)) {
			out.WriteByte(c)
		} else {
			out.WriteByte('%')
			out.WriteByte(digits[c>>4])
			out.WriteByte(digits[c&15])
		}
	}
	return out.String()
}
