package balance

// DeepInfraFunding carries only the minimum signed checklist facts and a
// service-keyed payer reference, never the original account identifier.
type DeepInfraFunding struct {
	Payer     string            `json:"payer"`
	CheckedAt int64             `json:"checkedAt"`
	Suspended *bool             `json:"suspended"`
	Reason    string            `json:"reason"`
	Stripe    string            `json:"stripe"`
	Recent    string            `json:"recent"`
	Overdue   string            `json:"overdue"`
	Credits   []DeepInfraCredit `json:"credits"`
}

type DeepInfraCredit struct {
	Remaining string `json:"remaining"`
	Expired   *bool  `json:"expired"`
	ExpiresAt *int64 `json:"expiresAt"`
}

// FundingWireReading is the canonical body signed against the selected
// runtime credential. It is transport-private, separate from displayed money.
type FundingWireReading struct {
	QueryID      string            `json:"queryId"`
	ObservedAtMS int64             `json:"observedAtMs"`
	Available    *bool             `json:"available"`
	Balances     []CurrencyBalance `json:"balances"`
	DeepInfra    *DeepInfraFunding `json:"deepInfra,omitempty"`
}

func FundingWire(o Observation) FundingWireReading {
	return FundingWireReading{QueryID: o.QueryID, ObservedAtMS: o.ObservedAtMS, Available: o.Available, Balances: o.Balances, DeepInfra: o.DeepInfraFunding}
}
