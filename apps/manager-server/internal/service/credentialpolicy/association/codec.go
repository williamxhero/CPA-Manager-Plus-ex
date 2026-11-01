// Package association implements only the frozen Go-backend association wire.
// A MAC is neither a Manager CAS certificate nor quota evidence nor a gate ACK.
package association

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"unicode/utf8"

	cq "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/credentialquota"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
)

var ErrContract = errors.New("credential association contract invalid")

type Request struct {
	Version       uint64     `json:"version"`
	Selected      cq.Binding `json:"selected"`
	AuthIndex     string     `json:"auth_index"`
	SourceVersion string     `json:"sourceVersion"`
	Origin        string     `json:"origin"`
	Official      ob.Config  `json:"official"`
	QueryID       string     `json:"queryId"`
	Nonce         string     `json:"nonce"`
	CandidateMAC  string     `json:"candidateMac,omitempty"`
}
type Encoded struct {
	Envelope      []byte
	Config        []byte
	ChallengeHash string
	ContractHash  string
	CandidateMAC  string
}
type encoder struct {
	bytes.Buffer
	bad bool
}

func (e *encoder) u(n uint64) { _ = binary.Write(&e.Buffer, binary.BigEndian, n) }
func (e *encoder) c(n int) {
	if n < 0 || n > 65536 {
		e.bad = true
		return
	}
	_ = binary.Write(&e.Buffer, binary.BigEndian, uint32(n))
}
func (e *encoder) o(b []byte) { e.c(len(b)); _, _ = e.Write(b) }
func (e *encoder) s(s string) {
	if len(s) > 4096 || !utf8.ValidString(s) {
		e.bad = true
	}
	e.o([]byte(s))
}
func (e *encoder) b(b bool) {
	if b {
		e.WriteByte(1)
	} else {
		e.WriteByte(0)
	}
}
func (e *encoder) binding(b cq.Binding) {
	e.s(b.Scope.InstanceID)
	e.s(b.Scope.InstanceIncarnation)
	e.s(b.Scope.BootEpoch)
	e.u(b.Scope.Revision)
	e.c(len(b.Targets))
	for _, t := range b.Targets {
		e.s(t.ResourceID)
		e.s(t.Incarnation)
		e.u(t.IdentityVersion)
		e.u(t.CredentialVersion)
		e.u(t.ConfigVersion)
		e.u(t.MembershipVersion)
	}
}
func (e *encoder) config(c ob.Config) {
	e.binding(cq.Binding{Scope: c.Scope, Targets: c.Targets})
	e.u(uint64(c.Version))
	e.s(c.Adapter)
	e.s(c.Endpoint)
	e.c(len(c.PrivateTargets))
	for _, p := range c.PrivateTargets {
		e.s(p)
	}
	e.u(uint64(c.TimeoutMS))
	e.b(c.NewAPI != nil)
	if c.NewAPI == nil {
		return
	}
	n := c.NewAPI
	e.b(n.ProtocolConfirmed)
	e.s(n.InstanceVersion)
	e.s(n.Auth)
	e.s(n.UserID)
	e.s(n.AccountScope)
	e.s(n.NativeUnit)
	e.b(n.Conversion != nil)
	if n.Conversion == nil {
		return
	}
	x := n.Conversion
	e.b(x.Confirmed)
	e.s(x.NativeUnit)
	e.s(x.Currency)
	e.s(x.MoneyPerNativeQuota)
	e.s(x.ProofReference)
	e.s(x.ContractVersion)
}
func hash(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }
func raw32(s string) ([]byte, error) {
	b, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil || len(b) != 32 || base64.RawURLEncoding.EncodeToString(b) != s {
		return nil, ErrContract
	}
	return b, nil
}

// Encode is the pure fixed-field codec, including altered semantic vectors.
// Call Prepare for validated outbound requests. Neither function proves authority.
func Encode(r Request, secret []byte) (Encoded, error) {
	nonce, err := raw32(r.Nonce)
	if err != nil {
		return Encoded{}, err
	}
	c := encoder{}
	c.config(r.Official)
	e := encoder{}
	e.s("CPA-SPEC92-CREDENTIAL-ASSOCIATION-V2")
	e.u(r.Version)
	e.binding(r.Selected)
	e.s(r.AuthIndex)
	e.s(r.SourceVersion)
	e.s(r.Origin)
	e.Write(c.Bytes())
	e.s(r.QueryID)
	e.o(nonce)
	if c.bad || e.bad || e.Len() > 32<<10 {
		return Encoded{}, ErrContract
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write(e.Bytes())
	return Encoded{Envelope: e.Bytes(), Config: c.Bytes(), ChallengeHash: hash(e.Bytes()), ContractHash: hash(c.Bytes()), CandidateMAC: base64.RawURLEncoding.EncodeToString(mac.Sum(nil))}, nil
}
