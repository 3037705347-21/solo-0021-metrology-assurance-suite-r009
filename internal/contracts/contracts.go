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
	Reviewer               string `json:"reviewer"`
	Summary                string `json:"summary"`
	UncertaintyExplanation string `json:"uncertainty_explanation,omitempty"`
}

type RegisterComponentRequest struct {
	Label                  string  `json:"label"`
	Category               string  `json:"category"`
	EvaluationType         string  `json:"evaluation_type,omitempty"`
	StandardUncertainty    float64 `json:"standard_uncertainty"`
	SensitivityCoefficient float64 `json:"sensitivity_coefficient,omitempty"`
	DegreesOfFreedom       float64 `json:"degrees_of_freedom,omitempty"`
	Basis                  string  `json:"basis"`
	RecordReference        string  `json:"record_reference,omitempty"`
	Technician             string  `json:"technician"`
	CoverageFactor         float64 `json:"coverage_factor,omitempty"`
}

type UpdateComponentRequest struct {
	Label                  string  `json:"label"`
	Category               string  `json:"category"`
	EvaluationType         string  `json:"evaluation_type,omitempty"`
	StandardUncertainty    float64 `json:"standard_uncertainty"`
	SensitivityCoefficient float64 `json:"sensitivity_coefficient,omitempty"`
	DegreesOfFreedom       float64 `json:"degrees_of_freedom,omitempty"`
	Basis                  string  `json:"basis"`
	RecordReference        string  `json:"record_reference,omitempty"`
	Technician             string  `json:"technician"`
	CoverageFactor         float64 `json:"coverage_factor,omitempty"`
}

type ConfirmEvaluationRequest struct {
	MeasurementID  string  `json:"measurement_id"`
	PreparedBy     string  `json:"prepared_by"`
	CoverageFactor float64 `json:"coverage_factor,omitempty"`
}

type DeviceResponse struct {
	Device domain.Device `json:"device"`
}

type CaseResponse struct {
	Case         domain.VerificationCase    `json:"case"`
	Measurements []domain.Measurement       `json:"measurements"`
	Seal         *domain.EvidenceSeal       `json:"seal,omitempty"`
	Uncertainty  *UncertaintyEvaluationView `json:"uncertainty,omitempty"`
	Events       []domain.AuditEvent        `json:"events"`
}

type UncertaintyEvaluationView struct {
	domain.UncertaintyEvaluation
	Conformance *domain.ConformanceAnalysis `json:"conformance,omitempty"`
}

type UncertaintyResponse struct {
	CaseID    string                         `json:"case_id"`
	Current   *UncertaintyEvaluationView     `json:"current,omitempty"`
	Revisions []domain.UncertaintyEvaluation `json:"revisions"`
	Sealed    *domain.SealedUncertainty      `json:"sealed,omitempty"`
}

type EventsResponse struct {
	Events []domain.AuditEvent `json:"events"`
}

type ErrorResponse struct {
	Error ErrorBody `json:"error"`
}

type ErrorBody struct {
	Code    string   `json:"code"`
	Message string   `json:"message"`
	Issues  []string `json:"issues,omitempty"`
}
