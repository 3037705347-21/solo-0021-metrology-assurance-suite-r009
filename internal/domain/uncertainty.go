package domain

import (
	"fmt"
	"math"
	"strings"
	"time"
)

type ComponentCategory string

const (
	ComponentEquipment   ComponentCategory = "equipment"
	ComponentStandard    ComponentCategory = "standard"
	ComponentEnvironment ComponentCategory = "environment"
	ComponentMethod      ComponentCategory = "method"
	ComponentOther       ComponentCategory = "other"
)

type EvaluationStatus string

const (
	EvaluationDraft     EvaluationStatus = "draft"
	EvaluationConfirmed EvaluationStatus = "confirmed"
)

// UncertaintyComponent is one quantified uncertainty contribution registered
// for a case. The basis (and optional record reference) state the evidence the
// technician relied on, e.g. a calibration certificate or a repeatability study.
type UncertaintyComponent struct {
	ID                     string            `json:"id"`
	CaseID                 string            `json:"case_id"`
	Label                  string            `json:"label"`
	Category               ComponentCategory `json:"category"`
	EvaluationType         string            `json:"evaluation_type"`
	StandardUncertainty    float64           `json:"standard_uncertainty"`
	SensitivityCoefficient float64           `json:"sensitivity_coefficient"`
	DegreesOfFreedom       float64           `json:"degrees_of_freedom,omitempty"`
	Basis                  string            `json:"basis"`
	RecordReference        string            `json:"record_reference,omitempty"`
	Technician             string            `json:"technician"`
	Revision               int               `json:"revision"`
	CreatedAt              time.Time         `json:"created_at"`
	UpdatedAt              time.Time         `json:"updated_at"`
}

func NewUncertaintyComponent(
	id, caseID, label string,
	category ComponentCategory,
	evaluationType string,
	standardUncertainty, sensitivityCoefficient, degreesOfFreedom float64,
	basis, recordReference, technician string,
	revision int,
	createdAt, updatedAt time.Time,
) (UncertaintyComponent, error) {
	item := UncertaintyComponent{
		ID:                     strings.TrimSpace(id),
		CaseID:                 strings.TrimSpace(caseID),
		Label:                  strings.TrimSpace(label),
		Category:               category,
		EvaluationType:         strings.ToUpper(strings.TrimSpace(evaluationType)),
		StandardUncertainty:    standardUncertainty,
		SensitivityCoefficient: sensitivityCoefficient,
		DegreesOfFreedom:       degreesOfFreedom,
		Basis:                  strings.TrimSpace(basis),
		RecordReference:        strings.TrimSpace(recordReference),
		Technician:             strings.TrimSpace(technician),
		Revision:               revision,
		CreatedAt:              createdAt.UTC(),
		UpdatedAt:              updatedAt.UTC(),
	}
	if item.EvaluationType == "" {
		item.EvaluationType = "B"
	}
	if item.SensitivityCoefficient == 0 {
		item.SensitivityCoefficient = 1
	}
	if err := item.Validate(); err != nil {
		return UncertaintyComponent{}, err
	}
	return item, nil
}

func (c UncertaintyComponent) Validate() error {
	if c.ID == "" || c.CaseID == "" || c.Label == "" || c.Technician == "" {
		return fmt.Errorf("%w: component id, case id, label, and technician are required", ErrInvalidInput)
	}
	if c.Basis == "" {
		return fmt.Errorf("%w: component %q must record the basis of the uncertainty contribution", ErrInvalidInput, c.Label)
	}
	switch c.Category {
	case ComponentEquipment, ComponentStandard, ComponentEnvironment, ComponentMethod, ComponentOther:
	default:
		return fmt.Errorf("%w: unsupported component category %q", ErrInvalidInput, c.Category)
	}
	switch c.EvaluationType {
	case "A", "B":
	default:
		return fmt.Errorf("%w: uncertainty evaluation type must be A or B, got %q", ErrInvalidInput, c.EvaluationType)
	}
	if !isFinite(c.StandardUncertainty) || c.StandardUncertainty <= 0 {
		return fmt.Errorf("%w: component %q standard uncertainty must be positive", ErrInvalidInput, c.Label)
	}
	if !isFinite(c.SensitivityCoefficient) {
		return fmt.Errorf("%w: component %q sensitivity coefficient must be finite", ErrInvalidInput, c.Label)
	}
	if !isFinite(c.DegreesOfFreedom) || c.DegreesOfFreedom < 0 {
		return fmt.Errorf("%w: component %q degrees of freedom must be zero (unknown) or positive", ErrInvalidInput, c.Label)
	}
	if c.Revision < 1 || c.CreatedAt.IsZero() || c.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: component %q revision and timestamps are required", ErrInvalidInput, c.Label)
	}
	return nil
}

// Contribution returns the sensitivity-weighted standard uncertainty c_i*u_i.
func (c UncertaintyComponent) Contribution() float64 {
	return c.SensitivityCoefficient * c.StandardUncertainty
}

// UncertaintyEvaluation is an immutable revision of a case's uncertainty
// budget. Every component change appends a new revision; prior revisions are
// never rewritten, so the basis at any point in time stays reconstructable.
type UncertaintyEvaluation struct {
	ID                          string                 `json:"id"`
	CaseID                      string                 `json:"case_id"`
	Revision                    int                    `json:"revision"`
	Status                      EvaluationStatus       `json:"status"`
	MeasurementID               string                 `json:"measurement_id,omitempty"`
	Nominal                     float64                `json:"nominal"`
	Tolerance                   float64                `json:"tolerance"`
	CoverageFactor              float64                `json:"coverage_factor"`
	Components                  []UncertaintyComponent `json:"components"`
	CombinedStandardUncertainty float64                `json:"combined_standard_uncertainty"`
	EffectiveDegreesOfFreedom   float64                `json:"effective_degrees_of_freedom,omitempty"`
	ExpandedUncertainty         float64                `json:"expanded_uncertainty"`
	PreparedBy                  string                 `json:"prepared_by"`
	CreatedAt                   time.Time              `json:"created_at"`
	UpdatedAt                   time.Time              `json:"updated_at"`
	ConfirmedAt                 *time.Time             `json:"confirmed_at,omitempty"`
}

func NewUncertaintyEvaluation(
	id, caseID string,
	revision int,
	status EvaluationStatus,
	measurementID string,
	nominal, tolerance, coverageFactor float64,
	preparedBy string,
	components []UncertaintyComponent,
	createdAt, updatedAt time.Time,
	confirmedAt *time.Time,
) (UncertaintyEvaluation, error) {
	item := UncertaintyEvaluation{
		ID:             strings.TrimSpace(id),
		CaseID:         strings.TrimSpace(caseID),
		Revision:       revision,
		Status:         status,
		MeasurementID:  strings.TrimSpace(measurementID),
		Nominal:        nominal,
		Tolerance:      tolerance,
		CoverageFactor: coverageFactor,
		Components:     append([]UncertaintyComponent(nil), components...),
		PreparedBy:     strings.TrimSpace(preparedBy),
		CreatedAt:      createdAt.UTC(),
		UpdatedAt:      updatedAt.UTC(),
	}
	if confirmedAt != nil {
		stamp := confirmedAt.UTC()
		item.ConfirmedAt = &stamp
	}
	combined, effectiveDOF := combineComponents(item.Components)
	item.CombinedStandardUncertainty = combined
	item.EffectiveDegreesOfFreedom = effectiveDOF
	item.ExpandedUncertainty = item.CoverageFactor * combined
	if err := item.Validate(); err != nil {
		return UncertaintyEvaluation{}, err
	}
	return item, nil
}

func (e UncertaintyEvaluation) Validate() error {
	if e.ID == "" || e.CaseID == "" || e.PreparedBy == "" {
		return fmt.Errorf("%w: evaluation id, case id, and preparer are required", ErrInvalidInput)
	}
	if e.Revision < 1 {
		return fmt.Errorf("%w: evaluation revision must be positive", ErrInvalidInput)
	}
	switch e.Status {
	case EvaluationDraft, EvaluationConfirmed:
	default:
		return fmt.Errorf("%w: unsupported evaluation status %q", ErrInvalidInput, e.Status)
	}
	if !isFinite(e.Nominal) || !isFinite(e.Tolerance) || e.Tolerance <= 0 {
		return fmt.Errorf("%w: evaluation nominal must be finite and tolerance positive", ErrInvalidInput)
	}
	if !isFinite(e.CoverageFactor) || e.CoverageFactor <= 0 {
		return fmt.Errorf("%w: coverage factor must be positive", ErrInvalidInput)
	}
	for _, component := range e.Components {
		if component.CaseID != e.CaseID {
			return fmt.Errorf("%w: component %q belongs to another case", ErrInvalidInput, component.ID)
		}
		if err := component.Validate(); err != nil {
			return err
		}
	}
	if e.CreatedAt.IsZero() || e.UpdatedAt.IsZero() {
		return fmt.Errorf("%w: evaluation timestamps are required", ErrInvalidInput)
	}
	if e.Status == EvaluationConfirmed && e.ConfirmedAt == nil {
		return fmt.Errorf("%w: confirmed evaluation requires a confirmation time", ErrInvalidInput)
	}
	return nil
}

// combineComponents evaluates the root-sum-of-squares combined standard
// uncertainty and, where degrees of freedom are known, the Welch–Satterthwaite
// effective degrees of freedom (0 when unknown).
func combineComponents(components []UncertaintyComponent) (float64, float64) {
	var sumSquares float64
	var dofNumerator float64
	var dofDenominator float64
	for _, component := range components {
		contribution := component.Contribution()
		squared := contribution * contribution
		sumSquares += squared
		if component.DegreesOfFreedom > 0 {
			dofDenominator += (squared * squared) / component.DegreesOfFreedom
		}
	}
	combined := math.Sqrt(sumSquares)
	if dofDenominator > 0 {
		dofNumerator = combined * combined * combined * combined
	}
	if dofDenominator == 0 {
		return combined, 0
	}
	return combined, dofNumerator / dofDenominator
}

// ConformanceAnalysis explains whether the pass decision survives the expanded
// uncertainty using a simple guard band: |deviation| + U <= tolerance.
type ConformanceAnalysis struct {
	Available                   bool    `json:"available"`
	MeasurementID               string  `json:"measurement_id,omitempty"`
	AbsoluteDeviation           float64 `json:"absolute_deviation"`
	Tolerance                   float64 `json:"tolerance"`
	CombinedStandardUncertainty float64 `json:"combined_standard_uncertainty"`
	CoverageFactor              float64 `json:"coverage_factor"`
	ExpandedUncertainty         float64 `json:"expanded_uncertainty"`
	GuardBandMargin             float64 `json:"guard_band_margin"`
	ConformanceSupported        bool    `json:"conformance_supported"`
}

func AnalyzeConformance(evaluation UncertaintyEvaluation, measurement Measurement) ConformanceAnalysis {
	deviation := measurement.AbsoluteDeviation()
	margin := evaluation.Tolerance - deviation - evaluation.ExpandedUncertainty
	return ConformanceAnalysis{
		Available:                   true,
		MeasurementID:               measurement.ID,
		AbsoluteDeviation:           deviation,
		Tolerance:                   evaluation.Tolerance,
		CombinedStandardUncertainty: evaluation.CombinedStandardUncertainty,
		CoverageFactor:              evaluation.CoverageFactor,
		ExpandedUncertainty:         evaluation.ExpandedUncertainty,
		GuardBandMargin:             margin,
		ConformanceSupported:        margin >= 0,
	}
}

// SealIssues lists the reasons why sealing this evaluation together with the
// selected measurement needs an explicit reviewer explanation. An empty slice
// means the evaluation is complete, current, and supports conformance.
func (e UncertaintyEvaluation) SealIssues(item VerificationCase, selected Measurement) []string {
	var issues []string
	if len(e.Components) == 0 {
		issues = append(issues, "measurement uncertainty evaluation has no registered components")
	}
	for _, component := range e.Components {
		if component.Basis == "" {
			issues = append(issues, fmt.Sprintf("component %q has no recorded basis", component.Label))
		}
	}
	if e.Status != EvaluationConfirmed {
		issues = append(issues, fmt.Sprintf("uncertainty evaluation revision %d has unconfirmed component changes and must be confirmed before sealing", e.Revision))
	}
	switch {
	case e.MeasurementID == "":
		issues = append(issues, "uncertainty evaluation is not tied to an accepted measurement of the case")
	case e.MeasurementID != selected.ID:
		issues = append(issues, fmt.Sprintf("uncertainty evaluation was prepared for measurement %s but the case result is now %s", e.MeasurementID, selected.ID))
	}
	if e.Nominal != item.Nominal || e.Tolerance != item.Tolerance {
		issues = append(issues, "uncertainty evaluation facts (nominal value or tolerance) no longer match the current case")
	}
	analysis := AnalyzeConformance(e, selected)
	if !analysis.ConformanceSupported {
		issues = append(issues, fmt.Sprintf("expanded uncertainty %.6g does not support unconditional conformance: |deviation| %.6g plus U exceeds tolerance %.6g", e.ExpandedUncertainty, analysis.AbsoluteDeviation, e.Tolerance))
	}
	return issues
}

// SealedUncertainty is the immutable evaluation basis frozen into an evidence
// seal. It owns independent copies so later working revisions can never rewrite
// what a reviewer sealed.
type SealedUncertainty struct {
	EvaluationID                string                 `json:"evaluation_id,omitempty"`
	Revision                    int                    `json:"revision"`
	Status                      EvaluationStatus       `json:"status"`
	MeasurementID               string                 `json:"measurement_id,omitempty"`
	Components                  []UncertaintyComponent `json:"components"`
	CombinedStandardUncertainty float64                `json:"combined_standard_uncertainty"`
	CoverageFactor              float64                `json:"coverage_factor"`
	ExpandedUncertainty         float64                `json:"expanded_uncertainty"`
	EffectiveDegreesOfFreedom   float64                `json:"effective_degrees_of_freedom,omitempty"`
	AbsoluteDeviation           float64                `json:"absolute_deviation"`
	ConformanceSupported        bool                   `json:"conformance_supported"`
	Issues                      []string               `json:"issues,omitempty"`
	Explanation                 string                 `json:"explanation,omitempty"`
	ExplanationRequired         bool                   `json:"explanation_required"`
	PreparedBy                  string                 `json:"prepared_by,omitempty"`
	SealedAt                    time.Time              `json:"sealed_at"`
}

// FreezeUncertainty snapshots an evaluation (or its absence) at sealing time.
func FreezeUncertainty(evaluation *UncertaintyEvaluation, selected Measurement, issues []string, explanation string, at time.Time) SealedUncertainty {
	frozen := SealedUncertainty{
		Status:              EvaluationDraft,
		AbsoluteDeviation:   selected.AbsoluteDeviation(),
		Issues:              append([]string(nil), issues...),
		Explanation:         strings.TrimSpace(explanation),
		ExplanationRequired: len(issues) > 0,
		SealedAt:            at.UTC(),
	}
	if evaluation != nil {
		frozen.EvaluationID = evaluation.ID
		frozen.Revision = evaluation.Revision
		frozen.Status = evaluation.Status
		frozen.MeasurementID = evaluation.MeasurementID
		frozen.CombinedStandardUncertainty = evaluation.CombinedStandardUncertainty
		frozen.CoverageFactor = evaluation.CoverageFactor
		frozen.ExpandedUncertainty = evaluation.ExpandedUncertainty
		frozen.EffectiveDegreesOfFreedom = evaluation.EffectiveDegreesOfFreedom
		frozen.PreparedBy = evaluation.PreparedBy
		frozen.Components = append([]UncertaintyComponent(nil), evaluation.Components...)
		if measurementID := evaluation.MeasurementID; measurementID == selected.ID && measurementID != "" {
			frozen.ConformanceSupported = AnalyzeConformance(*evaluation, selected).ConformanceSupported
		}
	}
	return frozen
}

func (s SealedUncertainty) Validate() error {
	if s.SealedAt.IsZero() {
		return fmt.Errorf("%w: sealed uncertainty requires a seal time", ErrInvalidInput)
	}
	for _, component := range s.Components {
		if err := component.Validate(); err != nil {
			return err
		}
	}
	return nil
}
