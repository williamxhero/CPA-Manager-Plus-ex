package providergate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/google/uuid"
	mc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/managerconfig"
	rc "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/resourcecapability"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/store"
)

var (
	ErrInput       = errors.New("invalid provider gate request")
	ErrIdentity    = errors.New("provider gate identity changed")
	ErrUnavailable = errors.New("provider gate proof or outcome unavailable")
)

type Service struct {
	store      *store.Store
	connection *mc.Service
	identity   *rc.Service
	monetary   bool
}

func New(st *store.Store, connection *mc.Service, identity *rc.Service) *Service {
	return &Service{store: st, connection: connection, identity: identity}
}

// Monetary isolates both durable journal and Core owner from the public
// administrative gate. Owner selection is never accepted from an HTTP client.
func (s *Service) Monetary() *Service {
	return &Service{store: s.store, connection: s.connection, identity: s.identity, monetary: true}
}

type document struct {
	Operation   operation `json:"operation"`
	Receipt     result    `json:"receipt"`
	UpdatedAtMS int64     `json:"updatedAtMs"`
}

type proof struct {
	groupBinding
	Version       int    `json:"version"`
	Authority     string `json:"authority"`
	SourceVersion string `json:"sourceVersion"`
	Supported     bool   `json:"supported"`
	Ready         bool   `json:"ready"`
	Members       []struct {
		groupBinding
		AuthIndex     string `json:"auth_index"`
		SourceVersion string `json:"sourceVersion"`
		Routes        []struct {
			Route     string `json:"route"`
			Supported bool   `json:"supported"`
		} `json:"routes"`
	} `json:"members"`
}

func validDigest(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == value
}

// Complete issued membership is checked separately from actual route proof.
// Neither property establishes the provider's payer/funding relationships.
func members(b Binding, inv *rc.Negotiated) (map[string]rc.Target, error) {
	if inv == nil || inv.Identity.State != "available" || inv.Snapshot == nil || !inv.Snapshot.Complete || inv.Snapshot.Scope != b.Scope || len(b.Targets) < 2 || len(b.Targets) > 64 {
		return nil, ErrIdentity
	}
	rows := map[string]rc.Row{}
	for _, row := range inv.Snapshot.Resources {
		rows[row.ResourceID] = row
	}
	parent, found := rows[b.ProviderID]
	if !found || parent.Kind != "provider" || parent.Family != "openai-compatibility" || parent.Identity != "valid" || parent.Readiness != "ready" || len(parent.Members) == 0 || len(parent.Members)+1 != len(b.Targets) {
		return nil, ErrIdentity
	}
	expected := map[string]rc.Target{parent.ResourceID: parent.Target}
	for _, id := range parent.Members {
		row, ok := rows[id]
		if !ok || row.Kind != "credential" || row.ParentID != parent.ResourceID || row.Family != parent.Family || row.Identity != "valid" || row.Readiness != "ready" || len(row.SourceBindings) != 0 {
			return nil, ErrIdentity
		}
		if _, duplicate := expected[id]; duplicate {
			return nil, ErrIdentity
		}
		expected[id] = row.Target
	}
	seen := map[string]bool{}
	for _, target := range b.Targets {
		if issued, ok := expected[target.ResourceID]; !ok || issued != target || seen[target.ResourceID] {
			return nil, ErrIdentity
		}
		seen[target.ResourceID] = true
	}
	delete(expected, parent.ResourceID)
	return expected, nil
}

func (p proof) valid(b Binding, expected map[string]rc.Target, inv *rc.Negotiated) bool {
	if p.Version != 1 || p.Authority != "routing_only" || !p.Ready || !p.Supported || !validDigest(p.SourceVersion) || !reflect.DeepEqual(p.groupBinding, coreGroup(b)) || len(p.Members) != len(expected) || inv.BindingCapability == nil || inv.BindingCapability.State != "available" {
		return false
	}
	seen := map[string]bool{}
	locators := map[string]bool{}
	for _, member := range p.Members {
		if member.Scope != b.Scope || len(member.Targets) != 1 || locators[member.AuthIndex] {
			return false
		}
		target := member.Targets[0]
		if issued, ok := expected[target.ResourceID]; !ok || issued != target || seen[target.ResourceID] || member.AuthIndex == "" || !validDigest(member.SourceVersion) {
			return false
		}
		seen[target.ResourceID] = true
		locators[member.AuthIndex] = true
		bound := false
		for _, binding := range inv.Bindings {
			if binding.AuthIndex == member.AuthIndex && binding.Scope == b.Scope && reflect.DeepEqual(binding.Targets, member.Targets) {
				bound = true
			}
		}
		if !bound || len(member.Routes) != 3 {
			return false
		}
		routes := map[string]bool{"native-http": false, "native-sse": false, "downstream-ws-turn": false}
		for _, route := range member.Routes {
			if done, known := routes[route.Route]; !known || done || !route.Supported {
				return false
			}
			routes[route.Route] = true
		}
	}
	return true
}

type prepared struct {
	status  Status
	record  store.ProviderGateRecord
	doc     document
	core    session
	ledger  ledger
	settled bool
	proof   proof
}

func (s *Service) prepare(ctx context.Context, b Binding) (prepared, error) {
	var out prepared
	b = canonical(b)
	if uuid.Validate(b.ProviderID) != nil || len(b.Targets) > 64 || s.identity == nil || s.store == nil {
		return out, ErrInput
	}
	link, err := openSession(ctx, s.connection)
	if err != nil {
		return out, err
	}
	inv := s.identity.Inventory(ctx, true)
	expected, err := members(b, inv)
	if err != nil {
		return out, err
	}
	var caps struct {
		Version  int      `json:"version"`
		Durable  bool     `json:"durable"`
		Mode     string   `json:"mode"`
		Subjects []string `json:"subjects"`
	}
	if err := link.request(ctx, "GET", "capabilities", nil, &caps); err != nil {
		return out, err
	}
	providerSubject := false
	for _, subject := range caps.Subjects {
		providerSubject = providerSubject || subject == "provider"
	}
	if caps.Version != 1 || !caps.Durable || caps.Mode != "standalone" || !providerSubject {
		return out, ErrUnavailable
	}
	var p proof
	query := struct {
		groupBinding
		Version int    `json:"version"`
		Route   string `json:"route"`
	}{coreGroup(b), 1, "all-current-provider"}
	if err := link.request(ctx, "POST", "providers/eligibility", query, &p); err != nil {
		return out, err
	}
	if !p.valid(b, expected, inv) {
		return out, ErrUnavailable
	}
	if err := link.request(ctx, "GET", "", nil, &out.ledger); err != nil {
		return out, err
	}
	if out.ledger.Version != 1 || !out.ledger.Ready || out.ledger.Scope != b.Scope || out.ledger.Gates == nil || out.ledger.GateRevision > 9007199254740991 {
		return out, ErrUnavailable
	}
	installation, err := s.store.ProviderGateInstallation(ctx)
	namespace := "cpa-manager.provider-gate.v1/"
	if s.monetary {
		installation, err = s.store.CredentialPolicyInstallation(ctx)
		namespace = "cpa-manager.provider-balance.v1/"
	}
	if err != nil {
		return out, err
	}
	raw, _ := json.Marshal([]string{b.Scope.InstanceID, b.Scope.InstanceIncarnation, b.ProviderID})
	if s.monetary {
		raw = append([]byte(namespace), raw...)
	}
	digest := sha256.Sum256(raw)
	id := hex.EncodeToString(digest[:])
	out.status = Status{Binding: b, SubjectID: id, Owner: namespace + installation, SourceVersion: p.SourceVersion, Ready: true, State: "unpaused"}
	// The latest command's outcome and the current restriction are independent.
	// A rejected/pending clear must not hide an existing verified own pause.
	for _, gate := range out.ledger.Gates {
		if gate.Owner == out.status.Owner && gate.Subject == "provider" && gate.Model == "" && gate.Applicability == "current" && reflect.DeepEqual(gate.Targets, b.Targets) {
			out.status.OwnPaused = true
		}
	}
	out.record, err = s.store.ProviderGate(ctx, id)
	if err != nil {
		return out, err
	}
	out.status.Generation = out.record.Generation
	if out.record.Generation > 0 {
		if json.Unmarshal([]byte(out.record.Document), &out.doc) != nil || out.doc.Operation.Owner != out.status.Owner || uuid.Validate(out.doc.Operation.OpID) != nil || out.doc.Operation.Subject != "provider" {
			return out, ErrUnavailable
		}
		op := out.doc.Operation
		out.status.Action, out.status.State, out.status.Reason = op.Action, "pending", "operation_outcome_unverified"
		receipt := out.doc.Receipt
		if sameInstance(op.Scope, b.Scope) && receipt.OpID == "" {
			// An immutable operation missing after its CAS revision advanced
			// cannot commit later. A bare 404 at the same revision is unknown.
			err := link.request(ctx, "GET", "operations/"+op.OpID, nil, &receipt)
			if errors.Is(err, errOperationMissing) && out.ledger.GateRevision > op.ExpectedGateRevision {
				out.settled = true
				out.status.State, out.status.Reason = "rejected", "operation_cas_expired"
			}
		}
		out.settled = out.settled || sameInstance(op.Scope, b.Scope) && validReceipt(op, receipt)
		if sameInstance(op.Scope, b.Scope) && reflect.DeepEqual(op.Targets, b.Targets) {
			if confirms(op, receipt, out.ledger) {
				out.status.Applied, out.status.Reason = true, "current_provider_gate_confirmed"
				out.status.OwnPaused = op.Action == "pause"
				out.status.State = "unpaused"
				if out.status.OwnPaused {
					out.status.State = "paused"
				}
			}
		}
	}
	closing := s.identity.Inventory(ctx, true)
	if _, err := members(b, closing); err != nil || !s.identity.SameInventoryConnection(inv, closing) || !link.current(ctx) {
		return prepared{}, ErrIdentity
	}
	out.core = link
	out.proof = p
	return out, nil
}

func (s *Service) Status(ctx context.Context, b Binding) (Status, error) {
	out, err := s.prepare(ctx, b)
	return out.status, err
}

func (s *Service) Update(ctx context.Context, q Update) (Status, error) {
	if s.monetary || q.Version != 1 || q.ExpectedGeneration == nil || *q.ExpectedGeneration < 0 || *q.ExpectedGeneration >= 9007199254740991 || !validDigest(q.SourceVersion) || q.Action != "pause" && q.Action != "clear" {
		return Status{}, ErrInput
	}
	out, err := s.prepare(ctx, q.Binding)
	if err != nil {
		return Status{}, err
	}
	if out.status.SourceVersion != q.SourceVersion {
		return Status{}, ErrIdentity
	}
	if out.record.Generation != *q.ExpectedGeneration {
		return Status{}, store.ErrProviderGateConflict
	}
	if out.record.Generation > 0 && !out.settled {
		return Status{}, ErrUnavailable
	}
	return s.send(ctx, out, q.Action, "", nil)
}

// ApplyMonetary accepts only host-derived evidence and a final authorization
// check under the policy's fenced DB lease. It cannot select another owner.
func (s *Service) ApplyMonetary(ctx context.Context, b Binding, sourceVersion, action, evidence string, authorize func(context.Context) error) (Status, error) {
	if !s.monetary || authorize == nil || !validDigest(evidence) || !validDigest(sourceVersion) || action != "pause" && action != "clear" {
		return Status{}, ErrInput
	}
	out, err := s.prepare(ctx, b)
	if err != nil {
		return Status{}, err
	}
	if out.status.SourceVersion != sourceVersion {
		return Status{}, ErrIdentity
	}
	if out.record.Generation > 0 && !out.settled {
		return Status{}, ErrUnavailable
	}
	return s.send(ctx, out, action, evidence, authorize)
}

func (s *Service) send(ctx context.Context, out prepared, action, evidence string, authorize func(context.Context) error) (Status, error) {
	opID := uuid.NewString()
	reason := "operator_provider_pause"
	if action == "clear" {
		reason = "operator_provider_resume"
	}
	if s.monetary {
		reason = "provider_balance_low"
		if action == "clear" {
			reason = "provider_balance_recovered"
		}
	} else {
		evidence = "manual/" + opID
	}
	if authorize != nil {
		if err := authorize(ctx); err != nil {
			return Status{}, err
		}
	}
	op := operation{groupBinding: coreGroup(out.status.Binding), Version: 1, OpID: opID, ExpectedGateRevision: out.ledger.GateRevision, SourceVersion: out.status.SourceVersion, Subject: "provider", Owner: out.status.Owner, Action: action, Reason: reason, EvidenceID: evidence}
	doc := document{Operation: op, UpdatedAtMS: time.Now().UnixMilli()}
	raw, _ := json.Marshal(doc)
	record := store.ProviderGateRecord{ID: out.record.ID, Generation: out.record.Generation + 1, Document: string(raw)}
	if err := s.store.SaveProviderGate(ctx, record, out.record.Generation); err != nil {
		return Status{}, err
	}
	if authorize != nil {
		if err := authorize(ctx); err != nil {
			return Status{}, err
		}
	}
	// Even a transport error leaves the attempted immutable command durable.
	// A later status read can confirm its outcome from the current Core ledger.
	if err := out.core.request(ctx, "POST", "operations", op, &doc.Receipt); err != nil {
		return Status{}, err
	}
	raw, _ = json.Marshal(doc)
	if err := s.store.CompleteProviderGate(ctx, record, string(raw)); err != nil {
		return Status{}, err
	}
	status, err := s.Status(ctx, out.status.Binding)
	if err == nil && !status.Applied {
		err = ErrUnavailable
	}
	return status, err
}
