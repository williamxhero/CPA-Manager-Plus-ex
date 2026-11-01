package fullrefresh

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
	ob "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/officialbalance"
	"net/url"
	"reflect"
	"strings"
	"time"
)

func verifiedWallet(status ob.Status) *WalletMembership {
	// Only this actual adapter independently checks the response ID == configured
	// numeric UserID. DeepSeek/DeepInfra return no such identity proof. Token quota
	// is key_remaining, never an account wallet. Neither origin nor AccountScope
	// nor a conversion operator assertion is an account ownership proof.
	if !status.Configured || status.Adapter != balance.NewAPIUserSelfV1 || status.NewAPI == nil || status.LastSuccess == nil || status.Attempt == nil {
		return nil
	}
	c := status.NewAPI
	obs := status.LastSuccess
	if balance.ValidateNewAPIContract(status.Adapter, c) != nil || obs.QueryStatus != "success" || status.Attempt.QueryID != obs.QueryID || status.Attempt.QueryStatus != "success" || obs.Semantic != "account_wallet" || obs.NativeQuota == nil || obs.NativeQuota.Unit != c.NativeUnit || time.Since(time.UnixMilli(obs.ObservedAtMS)) > 24*time.Hour || time.Until(time.UnixMilli(obs.ObservedAtMS)) > time.Minute {
		return nil
	}
	var bound ob.Binding
	if json.Unmarshal(obs.Binding, &bound) != nil || !reflect.DeepEqual(bound, status.Binding) {
		return nil
	}
	u, err := url.Parse(status.Endpoint)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil
	}
	origin := u.Scheme + "://" + strings.ToLower(u.Host)
	// Include protocol deployment version, native units AND full conversion proof.
	// Never combine currencies, a key limit, or a different conversion contract.
	contract := struct {
		Instance, Incarnation, Origin, Path, Account, Protocol, Version, Unit string
		Conversion                                                            *balance.NewAPIConversionProfile
	}{status.Scope.InstanceID, status.Scope.InstanceIncarnation, origin, u.EscapedPath(), c.UserID, status.Adapter, c.InstanceVersion, c.NativeUnit, c.Conversion}
	raw, _ := json.Marshal(contract)
	sum := sha256.Sum256(raw)
	q := ob.Query{Binding: status.Binding, Version: status.Version}
	return &WalletMembership{GroupID: hex.EncodeToString(sum[:]), Source: SourceRequest{Kind: Official, Official: &q}, AccountID: c.UserID, Origin: origin, Protocol: status.Adapter, NativeUnit: c.NativeUnit, ConfigVersion: status.Version, AuthenticatedAttemptID: obs.QueryID, AuthenticatedAtMS: obs.ObservedAtMS}
}
func sameMembership(a, b WalletMembership) bool { return reflect.DeepEqual(a, b) }
func planWallets(p *Progress) {
	leaders := map[string]string{}
	// A promoted catchup item joins the same already-admitted verified wallet read.
	for _, item := range p.Items {
		if item.WalletProof != nil && item.SharedFromID == "" && item.JobID != "" && (item.Outcome == "running" || item.Outcome == "success") {
			leaders[item.WalletProof.GroupID] = item.ID
		}
	}
	for i := range p.Items {
		item := &p.Items[i]
		if item.Outcome != "queued" || item.WalletProof == nil {
			continue
		}
		key := item.WalletProof.GroupID
		if leader := leaders[key]; leader != "" {
			item.SharedFromID = leader
			item.Outcome = "waiting_shared"
		} else {
			leaders[key] = item.ID
		}
	}
}
func (s *Service) settleShared(ctx context.Context, p *Progress, item *Item) {
	var leader *Item
	for i := range p.Items {
		if p.Items[i].ID == item.SharedFromID {
			leader = &p.Items[i]
			break
		}
	}
	if leader == nil {
		item.Outcome = "identity_changed"
		return
	}
	if leader.Outcome == "queued" || leader.Outcome == "running" {
		return
	}
	if leader.Outcome != "success" {
		item.Outcome = "queued"
		item.SharedFromID = ""
		return
	}
	current, err := s.Official.Status(ctx, item.Request.Official.Binding)
	proof := verifiedWallet(current)
	if err != nil || proof == nil || item.WalletProof == nil || !sameMembership(*proof, *item.WalletProof) || proof.GroupID != leader.WalletProof.GroupID {
		item.Outcome = "identity_changed"
		item.Stale = true
		return
	}
	if leader.LastSuccess == nil || leader.LastSuccess.QueryID != leader.JobID {
		item.Outcome = "identity_changed"
		return
	}
	item.SharedWallet = &SharedWalletObservation{Source: leader.Request, AttemptID: leader.JobID, Observation: *leader.LastSuccess, Membership: *proof}
	item.Outcome = "shared_wallet"
	// Queried remains FALSE: authentication success belongs only to its actual key.
}
func (s *Service) walletNotificationMembers(ctx context.Context, event CompletedObservation) []WalletMembership {
	if event.Official == nil || event.Official.Attempt == nil || event.Official.Attempt.QueryStatus != "success" {
		return nil
	}
	return s.walletMembersForAttempt(ctx, event.AttemptID)
}
func (s *Service) walletMembersForAttempt(ctx context.Context, attemptID string) []WalletMembership {
	s.mu.Lock()
	members := []WalletMembership{}
	for _, p := range s.rounds {
		if p.State != "running" && p.State != "complete" {
			continue
		}
		leaderID := ""
		for _, item := range p.Items {
			if item.JobID == attemptID {
				leaderID = item.ID
				break
			}
		}
		if leaderID == "" {
			continue
		}
		for _, item := range p.Items {
			if item.SharedFromID == leaderID && item.WalletProof != nil {
				members = append(members, *item.WalletProof)
			}
		}
	}
	s.mu.Unlock()
	out := []WalletMembership{}
	for _, member := range members {
		status, err := s.Official.Status(ctx, member.Source.Official.Binding)
		if err != nil {
			continue
		}
		proof := verifiedWallet(status)
		if proof != nil && sameMembership(*proof, member) {
			out = append(out, member)
		}
	}
	return out
}
