package domain

func DefaultPolicy() Policy {
	return Policy{TimeoutMS: 30000, MaxRetries: 0, Statuses: []StatusRange{{429, 429}, {500, 599}}, Network: true, Timeouts: true, DelayMode: "fixed", DelayMS: 1000, MaxDelayMS: 10000, DeadlineMS: 120000}
}

func NewDraft() Draft {
	return Draft{SchemaVersion: 1, Templates: []TemplateSource{}, SelectedIDs: []string{}, Settings: map[string]TemplateSettings{}, Method: "POST", Auth: Auth{Mode: "none", Placement: "header"}, Headers: []HeaderRow{}, Policy: DefaultPolicy(), Profile: Profile{Mode: "open", Steps: []Step{{60, 100}}, RetryAccounting: "within"}, SelectionMode: "round_robin", Capacity: 1000, Samples: Samples{5, 20, 65536}}
}
