package balance

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

const VolcengineAccountBalanceV20220101 = "volcengine-account-balance-v2022-01-01"
const VolcengineBalanceEndpoint = "https://billing.volcengineapi.com/"
const volcengineBalanceQuery = "Action=QueryBalanceAcct&Version=2022-01-01"

// This is an explicit operator assertion for an AI-provider source, not proof
// derived from the inference hostname/key, nor a Coding Plan quota contract.
type VolcengineConfig struct {
	AccountID               string `json:"accountID"`
	Currency                string `json:"currency"`
	Unit                    string `json:"unit"`
	CurrencyUnitConfirmed   bool   `json:"currencyUnitConfirmed"`
	AccountBindingConfirmed bool   `json:"accountBindingConfirmed"`
	ProofReference          string `json:"proofReference"`
}

type VolcengineAccountEvidence struct {
	AccountID      string `json:"accountID"`
	Unit           string `json:"unit"`
	ProofReference string `json:"proofReference"`
	Qualification  string `json:"qualification"`
}

type volcengineCredentials struct {
	AccessKeyID     string `json:"accessKeyID"`
	SecretAccessKey string `json:"secretAccessKey"`
}

var volcengineAccountID = regexp.MustCompile(`^[1-9][0-9]{0,31}$`)
var volcengineProofReference = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
var volcengineKey = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_+=/.-]{0,255}$`)

func decodeVolcengineJSON(raw []byte, target any) error {
	bad := errors.New("invalid Volcengine contract")
	if !newAPIUnambiguousJSON(raw) {
		return bad
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(target) != nil || d.Decode(new(any)) != io.EOF {
		return bad
	}
	return nil
}

func (c *VolcengineConfig) UnmarshalJSON(raw []byte) error {
	type plain VolcengineConfig
	var value plain
	if err := decodeVolcengineJSON(raw, &value); err != nil {
		return err
	}
	*c = VolcengineConfig(value)
	return nil
}

func ValidateVolcengineContract(c *VolcengineConfig) error {
	if c == nil || !volcengineAccountID.MatchString(c.AccountID) || c.Currency != "CNY" || c.Unit != "major" || !c.CurrencyUnitConfirmed || !c.AccountBindingConfirmed || !volcengineProofReference.MatchString(c.ProofReference) {
		return errors.New("explicit Volcengine account binding and CNY major-unit profile required")
	}
	return nil
}

func parseVolcengineSecret(secret string) (volcengineCredentials, error) {
	var c volcengineCredentials
	if len(secret) > 8192 || decodeVolcengineJSON([]byte(secret), &c) != nil || !volcengineKey.MatchString(c.AccessKeyID) || !volcengineKey.MatchString(c.SecretAccessKey) {
		return volcengineCredentials{}, errors.New("invalid Volcengine billing credential")
	}
	return c, nil
}

// Validate the write-only encrypted secret without ever returning its contents.
func ValidateVolcengineSecret(secret string) error {
	_, err := parseVolcengineSecret(secret)
	return err
}

func queryVolcengine(ctx context.Context, target QueryTarget, resolver CredentialResolver, network NetworkOptions) Observation {
	out := Observation{QueryID: uuid.NewString(), ObservedAtMS: time.Now().UnixMilli(), CapabilityStatus: "needs_configuration", QueryStatus: "needs_configuration", ErrorCode: "VOLCENGINE_PROFILE_REQUIRED"}
	fail := func(status, code string) Observation {
		out.QueryStatus, out.ErrorCode, out.ObservedAtMS = status, code, time.Now().UnixMilli()
		if status == "needs_configuration" {
			out.CapabilityStatus = status
		}
		return out
	}
	if ValidateVolcengineContract(target.Volcengine) != nil {
		return out
	}
	if target.EndpointURL == "" {
		target.EndpointURL = VolcengineBalanceEndpoint
	}
	if ValidateAdapterSource(target.Adapter, target.EndpointURL, target.PrivateTargets) != nil {
		return fail("needs_configuration", "INVALID_ENDPOINT")
	}
	if resolver == nil {
		return fail("needs_configuration", "CREDENTIAL_MISSING")
	}
	secret, err := resolver.ResolveCredential(ctx, target.CredentialRef)
	if err != nil {
		return fail("auth_error", "CREDENTIAL_RESOLVE_FAILED")
	}
	if strings.TrimSpace(secret) == "" {
		return fail("needs_configuration", "CREDENTIAL_MISSING")
	}
	credential, err := parseVolcengineSecret(secret)
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, VolcengineBalanceEndpoint+"?"+volcengineBalanceQuery, nil)
	if err != nil {
		return fail("network_error", "INVALID_ENDPOINT")
	}
	signVolcengineBalance(req, credential, time.Now().UTC())
	req.Header.Set("Accept", "application/json")
	client := secureClient(target, network)
	defer client.CloseIdleConnections()
	res, err := client.Do(req)
	if err != nil {
		return fail("network_error", "UPSTREAM_UNAVAILABLE")
	}
	defer res.Body.Close()
	if res.StatusCode == 401 || res.StatusCode == 403 {
		return fail("auth_error", "UPSTREAM_AUTH")
	}
	if res.StatusCode == 429 {
		return fail("rate_limited", "UPSTREAM_RATE_LIMITED")
	}
	if res.StatusCode != 200 {
		return fail("network_error", "UPSTREAM_STATUS")
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, maxResponseBodySize+1))
	if err != nil {
		return fail("network_error", "RESPONSE_READ_FAILED")
	}
	if len(body) > maxResponseBodySize {
		return fail("parse_error", "RESPONSE_TOO_LARGE")
	}
	var data struct {
		ResponseMetadata struct {
			Error json.RawMessage `json:"Error"`
		} `json:"ResponseMetadata"`
		Result struct {
			AccountID        json.RawMessage `json:"AccountID"`
			AvailableBalance *string         `json:"AvailableBalance"`
		} `json:"Result"`
	}
	if !newAPIUnambiguousJSON(body) || json.Unmarshal(body, &data) != nil {
		return fail("parse_error", "INVALID_BALANCE")
	}
	if len(data.ResponseMetadata.Error) != 0 && string(data.ResponseMetadata.Error) != "null" {
		var failure struct{ Code string }
		if json.Unmarshal(data.ResponseMetadata.Error, &failure) != nil {
			return fail("parse_error", "INVALID_BALANCE")
		}
		switch failure.Code {
		case "RecordNoFound":
			return fail("needs_configuration", "VOLCENGINE_ACCOUNT_UNAVAILABLE")
		case "AccessDenied":
			return fail("auth_error", "UPSTREAM_AUTH")
		case "InternalError":
			return fail("network_error", "UPSTREAM_STATUS")
		default:
			return fail("parse_error", "VOLCENGINE_UPSTREAM_ERROR")
		}
	}
	if !volcengineAccountID.Match(data.Result.AccountID) || data.Result.AvailableBalance == nil || !money.MatchString(*data.Result.AvailableBalance) {
		return fail("parse_error", "INVALID_BALANCE")
	}
	if string(data.Result.AccountID) != target.Volcengine.AccountID {
		return fail("parse_error", "VOLCENGINE_ACCOUNT_MISMATCH")
	}
	out.Semantic, out.QueryStatus, out.ErrorCode = "account_wallet", "success", ""
	out.AmountDecimal, out.Currency = *data.Result.AvailableBalance, target.Volcengine.Currency
	out.VolcengineAccount = &VolcengineAccountEvidence{AccountID: string(data.Result.AccountID), Unit: target.Volcengine.Unit, ProofReference: target.Volcengine.ProofReference, Qualification: "operator_asserted_binding_and_units_not_live_verified"}
	out.ObservedAtMS = time.Now().UnixMilli()
	return out
}

func signVolcengineBalance(req *http.Request, credential volcengineCredentials, now time.Time) {
	date := now.Format("20060102T150405Z")
	req.Header.Set("X-Date", date)
	const signedHeaders = "host;x-date"
	emptyBody := sha256.Sum256(nil)
	canonical := "GET\n/\n" + volcengineBalanceQuery + "\nhost:billing.volcengineapi.com\nx-date:" + date + "\n\n" + signedHeaders + "\n" + hex.EncodeToString(emptyBody[:])
	canonicalHash := sha256.Sum256([]byte(canonical))
	scope := date[:8] + "/cn-beijing/billing/request"
	toSign := "HMAC-SHA256\n" + date + "\n" + scope + "\n" + hex.EncodeToString(canonicalHash[:])
	mac := func(key []byte, value string) []byte {
		h := hmac.New(sha256.New, key)
		h.Write([]byte(value))
		return h.Sum(nil)
	}
	key := mac([]byte(credential.SecretAccessKey), date[:8])
	key = mac(key, "cn-beijing")
	key = mac(key, "billing")
	key = mac(key, "request")
	signature := hex.EncodeToString(mac(key, toSign))
	req.Header.Set("Authorization", "HMAC-SHA256 Credential="+credential.AccessKeyID+"/"+scope+", SignedHeaders="+signedHeaders+", Signature="+signature)
}
