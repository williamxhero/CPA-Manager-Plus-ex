package association

import (
	"encoding/json"
	"net"
	"net/url"
	"regexp"
	"strings"

	"github.com/google/uuid"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
)

var descriptor = regexp.MustCompile(`^[0-9a-f]{64}$`)

func validUUID(s string) bool          { u, e := uuid.Parse(s); return e == nil && u.String() == s }
func counter(n uint64, zero bool) bool { return (n > 0 || zero) && n <= 9007199254740991 }
func validBinding(b cq.Binding) bool {
	s := b.Scope
	if !validUUID(s.InstanceID) || !validUUID(s.InstanceIncarnation) || !validUUID(s.BootEpoch) || !counter(s.Revision, false) || len(b.Targets) == 0 || len(b.Targets) > 64 {
		return false
	}
	seen := map[string]bool{}
	for _, t := range b.Targets {
		if !validUUID(t.ResourceID) || !validUUID(t.Incarnation) || seen[t.ResourceID] || !counter(t.IdentityVersion, false) || !counter(t.CredentialVersion, false) || !counter(t.ConfigVersion, false) || !counter(t.MembershipVersion, true) {
			return false
		}
		seen[t.ResourceID] = true
	}
	return true
}

// CanonicalOrigin is syntax only. It performs no DNS or credential/deployment
// inference; only the Core actual executor can certify that association.
func CanonicalOrigin(endpoint string) (string, error) {
	u, e := url.Parse(endpoint)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Opaque != "" {
		return "", ErrContract
	}
	host := strings.ToLower(u.Hostname())
	for _, c := range host {
		if c > 127 || c <= 32 || c == '%' {
			return "", ErrContract
		}
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	} else if strings.Contains(host, ":") || strings.HasSuffix(host, ".") {
		return "", ErrContract
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	if !regexp.MustCompile(`^[1-9][0-9]{0,4}$`).MatchString(port) {
		return "", ErrContract
	}
	n := 0
	for _, c := range port {
		n = n*10 + int(c-'0')
	}
	if n > 65535 {
		return "", ErrContract
	}
	return "https://" + net.JoinHostPort(host, port), nil
}
func validate(r Request, mac bool) error {
	c := r.Official
	if r.Version != 1 || !validBinding(r.Selected) || !validBinding(cq.Binding{Scope: c.Scope, Targets: c.Targets}) || r.Selected.Scope != c.Scope || !validUUID(r.QueryID) || !descriptor.MatchString(r.SourceVersion) || r.AuthIndex == "" || len(r.AuthIndex) > 256 || strings.ContainsAny(r.AuthIndex, "\r\n\x00") || c.Version <= 0 || c.Version >= 9007199254740991 || c.TimeoutMS < 0 || c.TimeoutMS > 60000 || len(c.PrivateTargets) > 16 || c.Adapter != balance.NewAPITokenUsageV1 || balance.ValidateNewAPIContract(c.Adapter, c.NewAPI) != nil || balance.ValidateAdapterSource(c.Adapter, c.Endpoint, c.PrivateTargets) != nil {
		return ErrContract
	}
	origin, e := CanonicalOrigin(c.Endpoint)
	if e != nil || origin != r.Origin {
		return ErrContract
	}
	if _, e := raw32(r.Nonce); e != nil {
		return e
	}
	if mac {
		if _, e := raw32(r.CandidateMAC); e != nil {
			return e
		}
	}
	return nil
}
func Prepare(r Request, secret []byte) (Request, Encoded, error) {
	if len(secret) == 0 || validate(r, false) != nil {
		return Request{}, Encoded{}, ErrContract
	}
	encoded, e := Encode(r, secret)
	if e != nil {
		return Request{}, Encoded{}, e
	}
	r.CandidateMAC = encoded.CandidateMAC
	raw, e := json.Marshal(r)
	if e != nil || len(raw) > 32<<10 {
		return Request{}, Encoded{}, ErrContract
	}
	return r, encoded, nil
}
func DecodeRequest(raw []byte) (Request, error) {
	var r Request
	if decodeStrict(raw, &r) != nil || validate(r, true) != nil {
		return Request{}, ErrContract
	}
	return r, nil
}

// ValidateCapture checks the secret-free original envelope, before backend MAC.
// It is not current Core association or readiness.
func ValidateCapture(r Request) error { return validate(r, false) }
