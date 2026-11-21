package officialbalance

import (
	"encoding/json"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/balance"
)

// The database retains only the bounded proof and opaque payer. HTTP status
// and job serialization continue to use the public balance observation.
type observationDocument struct {
	balance.Observation
	PrivateFunding *balance.DeepInfraFunding `json:"privateDeepInfraFunding,omitempty"`
}

func encodeObservation(o balance.Observation) []byte {
	b, _ := json.Marshal(observationDocument{Observation: o, PrivateFunding: o.DeepInfraFunding})
	return b
}

func decodeObservation(raw string) *balance.Observation {
	var doc *observationDocument
	if json.Unmarshal([]byte(raw), &doc) != nil || doc == nil {
		return nil
	}
	doc.Observation.DeepInfraFunding = doc.PrivateFunding
	return &doc.Observation
}

func FundingAdapter(st Status) string {
	switch {
	case st.Adapter == "deepseek" && st.Endpoint == "https://api.deepseek.com/user/balance":
		return st.Adapter
	case st.Adapter == "deepinfra" && st.Endpoint == balance.DeepInfraBillingEndpoint:
		return st.Adapter
	default:
		return ""
	}
}
