package contracts

import "example.com/solo-0021-metrology-assurance-suite/internal/domain"

type RegisterDeviceRequest struct {
	AssetTag string `json:"asset_tag"`
	Model    string `json:"model"`
	Room     string `json:"room"`
	Steward  string `json:"steward"`
}

type OpenCaseRequest struct {
	DeviceID  string  `json:"device_id"`
	Nominal   float64 `json:"nominal"`
	Tolerance float64 `json:"tolerance"`
	Steward   string  `json:"steward"`
}

type RecordMeasurementRequest struct {
	Observed   float64 `json:"observed"`
	Technician string  `json:"technician"`
}

type ReopenCaseRequest struct {
	Actor  string `json:"actor"`
	Reason string `json:"reason"`
}

type SealCaseRequest struct {
	Reviewer string `json:"reviewer"`
	Summary  string `json:"summary"`
	// UncertaintyExplanation is required when the current uncertainty
	// assessment cannot support sealing without explanation (missing basis,
	// revised components, stale facts, or an indeterminate guard-band
	// conclusion).
	UncertaintyExplanation string `json:"uncertainty_explanation,omitempty"`
}

type RegisterUncertaintyRequest struct {
	Technician          string                  `json:"technician"`
	CoverageProbability float64                 `json:"coverage_probability,omitempty"`
	Components          []domain.ComponentDraft `json:"components"`
}

type DeviceResponse struct {
	Device domain.Device `json:"device"`
}

type CaseResponse struct {
	Case                 domain.VerificationCase        `json:"case"`
	Measurements         []domain.Measurement           `json:"measurements"`
	Seal                 *domain.EvidenceSeal           `json:"seal,omitempty"`
	CurrentUncertainty   *domain.UncertaintyEvaluation  `json:"current_uncertainty,omitempty"`
	UncertaintyRevisions []domain.UncertaintyEvaluation `json:"uncertainty_revisions"`
	Events               []domain.AuditEvent            `json:"events"`
}

type UncertaintyResponse struct {
	CaseID    string                         `json:"case_id"`
	Current   *domain.UncertaintyEvaluation  `json:"current,omitempty"`
	Revisions []domain.UncertaintyEvaluation `json:"revisions"`
}

type EventsResponse struct {
	Events []domain.AuditEvent `json:"events"`
}

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}
