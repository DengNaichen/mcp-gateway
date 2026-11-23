package apitypes

type Approval struct {
	ID               string `json:"id"`
	Status           string `json:"status"`
	Actor            string `json:"actor"`
	Agent            string `json:"agent"`
	RequesterSession string `json:"requester_session"`
	RepoID           string `json:"repo_id"`
	RepoName         string `json:"repo_name"`
	Protocol         string `json:"protocol"`
	ServiceID        string `json:"service_id"`
	Action           string `json:"action"`
	ReadOnly         bool   `json:"read_only"`
	RuleID           string `json:"rule_id"`
	Reason           string `json:"reason"`
	ArgsRedacted     string `json:"args_redacted"`
	Summary          string `json:"summary"`
	CreatedAt        string `json:"created_at"`
	PendingExpiresAt string `json:"pending_expires_at"`
	ApprovedAt       string `json:"approved_at"`
	ExpiresAt        string `json:"expires_at"`
	DecidedBy        string `json:"decided_by"`
	DecidedAt        string `json:"decided_at"`
	DecisionNote     string `json:"decision_note"`
	LeaseExpiresAt   string `json:"lease_expires_at"`
	ConsumedAt       string `json:"consumed_at"`
}

type ApprovalDecisionRequest struct {
	Note string `json:"note,omitempty"`
}
