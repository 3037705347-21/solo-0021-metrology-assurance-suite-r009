package domain

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// ComponentSource identifies the origin of an uncertainty contribution.
type ComponentSource string

const (
	SourceEquipment     ComponentSource = "equipment"
	SourceStandard      ComponentSource = "standard"
	SourceEnvironment   ComponentSource = "environment"
	SourceRepeatability ComponentSource = "repeatability"
	SourceResolution    ComponentSource = "resolution"
	SourceOther         ComponentSource = "other"
)

// DistributionType describes how a registered quantity is distributed.
type DistributionType string

const (
	DistributionNormal      DistributionType = "normal"
	DistributionRectangular DistributionType = "rectangular"
	DistributionTriangular  DistributionType = "triangular"
)

// Conformance outcomes after the expanded uncertainty is applied as a guard
// band around the tolerance.
const (
	ConformanceConforms      = "conforms"
	ConformanceIndeterminate = "indeterminate"
	ConformanceNotConforming = "does_not_conform"
	ConformanceNotAssessed   = "not_assessed"
)

// ComponentDraft is the technician-supplied basis for one component. The
// basis text is mandatory: reviewers must be able to see why the value was
// claimed. Reference (calibration certificate id, log entry, standard id) is
// optional but encouraged.
type ComponentDraft struct {
	Source            ComponentSource  `json:"source"`
	Name              string           `json:"name"`
	Method            string           `json:"method"`
	Distribution      DistributionType `json:"distribution"`
	StandardDeviation float64          `json:"standard_deviation,omitempty"`
	Divisor           float64          `json:"divisor,omitempty"`
	HalfWidth         float64          `json:"half_width,omitempty"`
	Sensitivity       float64          `json:"sensitivity,omitempty"`
	DegreesOfFreedom  *float64         `json:"degrees_of_freedom,omitempty"`
	Basis             string           `json:"basis"`
	Reference         string           `json:"reference,omitempty"`
}

// UncertaintyComponent is a registered component with its computed standard
// uncertainty and contribution to the combined standard uncertainty.
type UncertaintyComponent struct {
	ID                  string           `json:"id"`
	Source              ComponentSource  `json:"source"`
	Name                string           `json:"name"`
	Method              string           `json:"method"`
	Distribution        DistributionType `json:"distribution"`
	StandardDeviation   float64          `json:"standard_deviation,omitempty"`
	Divisor             float64          `json:"divisor,omitempty"`
	HalfWidth           float64          `json:"half_width,omitempty"`
	Sensitivity         float64          `json:"sensitivity"`
	StandardUncertainty float64          `json:"standard_uncertainty"`
	Contribution        float64          `json:"contribution"`
	DegreesOfFreedom    float64          `json:"degrees_of_freedom,omitempty"`
	Basis               string           `json:"basis"`
	Reference           string           `json:"reference,omitempty"`
}

// MeasurementReference freezes the measurement the evaluation was built
// against, so later changes to the case can be detected.
type MeasurementReference struct {
	MeasurementID string    `json:"measurement_id"`
	Observed      float64   `json:"observed"`
	Deviation     float64   `json:"deviation"`
	Accepted      bool      `json:"accepted"`
	Technician    string    `json:"technician"`
	RecordedAt    time.Time `json:"recorded_at"`
}

func referenceFromMeasurement(item Measurement) *MeasurementReference {
	return &MeasurementReference{
		MeasurementID: item.ID,
		Observed:      item.Observed,
		Deviation:     item.Deviation,
		Accepted:      item.Accepted,
		Technician:    item.Technician,
		RecordedAt:    item.CreatedAt,
	}
}

// UncertaintyEvaluation is one revision of the combined uncertainty budget
// for a case. Revisions are append-only: a later registration never rewrites
// an earlier revision, and the revision selected at seal time is copied into
// the evidence seal.
type UncertaintyEvaluation struct {
	ID                          string                 `json:"id"`
	CaseID                      string                 `json:"case_id"`
	Version                     int                    `json:"version"`
	DeviceID                    string                 `json:"device_id"`
	CaseNominal                 float64                `json:"case_nominal"`
	CaseTolerance               float64                `json:"case_tolerance"`
	Components                  []UncertaintyComponent `json:"components"`
	CombinedStandardUncertainty float64                `json:"combined_standard_uncertainty"`
	EffectiveDegreesOfFreedom   float64                `json:"effective_degrees_of_freedom,omitempty"`
	CoverageProbability         float64                `json:"coverage_probability"`
	CoverageFactor              float64                `json:"coverage_factor"`
	ExpandedUncertainty         float64                `json:"expanded_uncertainty"`
	Measurement                 *MeasurementReference  `json:"measurement,omitempty"`
	Conformance                 string                 `json:"conformance"`
	Technician                  string                 `json:"technician"`
	CreatedAt                   time.Time              `json:"created_at"`
}

// ComponentFromDraft converts a technician draft into a validated, computed
// component.
func ComponentFromDraft(id string, draft ComponentDraft) (UncertaintyComponent, error) {
	item := UncertaintyComponent{
		ID:           strings.TrimSpace(id),
		Source:       ComponentSource(strings.TrimSpace(string(draft.Source))),
		Name:         strings.TrimSpace(draft.Name),
		Method:       strings.TrimSpace(draft.Method),
		Distribution: DistributionType(strings.TrimSpace(string(draft.Distribution))),
		Sensitivity:  draft.Sensitivity,
		Basis:        strings.TrimSpace(draft.Basis),
		Reference:    strings.TrimSpace(draft.Reference),
		HalfWidth:    draft.HalfWidth,
	}
	switch item.Source {
	case SourceEquipment, SourceStandard, SourceEnvironment, SourceRepeatability, SourceResolution, SourceOther:
	default:
		return UncertaintyComponent{}, fmt.Errorf("%w: unsupported uncertainty source %q", ErrInvalidInput, draft.Source)
	}
	if item.Name == "" || item.Method == "" || item.Basis == "" {
		return UncertaintyComponent{}, fmt.Errorf("%w: component name, evaluation method, and basis are required", ErrInvalidInput)
	}
	if item.Sensitivity == 0 || !isFinite(item.Sensitivity) {
		return UncertaintyComponent{}, fmt.Errorf("%w: component %q sensitivity must be finite and non-zero", ErrInvalidInput, item.Name)
	}
	if draft.DegreesOfFreedom != nil {
		if !isFinite(*draft.DegreesOfFreedom) || *draft.DegreesOfFreedom <= 0 {
			return UncertaintyComponent{}, fmt.Errorf("%w: component %q degrees of freedom must be positive", ErrInvalidInput, item.Name)
		}
		item.DegreesOfFreedom = *draft.DegreesOfFreedom
	}
	switch item.Distribution {
	case DistributionNormal:
		if !isFinite(draft.StandardDeviation) || draft.StandardDeviation <= 0 {
			return UncertaintyComponent{}, fmt.Errorf("%w: component %q needs a positive standard deviation", ErrInvalidInput, item.Name)
		}
		item.StandardDeviation = draft.StandardDeviation
		divisor := draft.Divisor
		if divisor == 0 {
			divisor = 1
		}
		if !isFinite(divisor) || divisor <= 0 {
			return UncertaintyComponent{}, fmt.Errorf("%w: component %q divisor must be positive", ErrInvalidInput, item.Name)
		}
		if draft.HalfWidth != 0 {
			return UncertaintyComponent{}, fmt.Errorf("%w: component %q half width cannot be combined with a normal distribution", ErrInvalidInput, item.Name)
		}
		item.Divisor = divisor
		item.StandardUncertainty = draft.StandardDeviation / divisor
	case DistributionRectangular, DistributionTriangular:
		if !isFinite(draft.HalfWidth) || draft.HalfWidth <= 0 {
			return UncertaintyComponent{}, fmt.Errorf("%w: component %q needs a positive half width", ErrInvalidInput, item.Name)
		}
		if draft.StandardDeviation != 0 || draft.Divisor != 0 {
			return UncertaintyComponent{}, fmt.Errorf("%w: component %q only accepts a half width for its distribution", ErrInvalidInput, item.Name)
		}
		divisor := math.Sqrt(3)
		if item.Distribution == DistributionTriangular {
			divisor = math.Sqrt(6)
		}
		item.StandardUncertainty = draft.HalfWidth / divisor
	default:
		return UncertaintyComponent{}, fmt.Errorf("%w: unsupported distribution %q for component %q", ErrInvalidInput, draft.Distribution, item.Name)
	}
	item.Contribution = math.Abs(item.Sensitivity) * item.StandardUncertainty
	return item, nil
}

// NewUncertaintyEvaluation builds and combines one budget revision.
func NewUncertaintyEvaluation(
	id, caseID, deviceID string,
	nominal, tolerance float64,
	version int,
	technician string,
	coverageProbability float64,
	drafts []ComponentDraft,
	bound *Measurement,
	at time.Time,
) (UncertaintyEvaluation, error) {
	evaluation := UncertaintyEvaluation{
		ID:                  strings.TrimSpace(id),
		CaseID:              strings.TrimSpace(caseID),
		DeviceID:            strings.TrimSpace(deviceID),
		CaseNominal:         nominal,
		CaseTolerance:       tolerance,
		Version:             version,
		Technician:          strings.TrimSpace(technician),
		CoverageProbability: coverageProbability,
		CreatedAt:           at.UTC(),
		Conformance:         ConformanceNotAssessed,
	}
	if evaluation.ID == "" || evaluation.CaseID == "" || evaluation.DeviceID == "" || evaluation.Technician == "" {
		return UncertaintyEvaluation{}, fmt.Errorf("%w: evaluation id, case, device, and technician are required", ErrInvalidInput)
	}
	if version < 1 || !isFinite(nominal) || !isFinite(tolerance) || tolerance <= 0 {
		return UncertaintyEvaluation{}, fmt.Errorf("%w: evaluation version and positive case tolerance are required", ErrInvalidInput)
	}
	if len(drafts) == 0 {
		return UncertaintyEvaluation{}, fmt.Errorf("%w: at least one uncertainty component with its basis is required", ErrInvalidInput)
	}
	if coverageProbability == 0 {
		coverageProbability = 0.95
	}
	if !isFinite(coverageProbability) || coverageProbability <= 0 || coverageProbability >= 1 {
		return UncertaintyEvaluation{}, fmt.Errorf("%w: coverage probability must be between 0 and 1", ErrInvalidInput)
	}
	if _, supported := tQuantiles[coverageProbability]; !supported {
		return UncertaintyEvaluation{}, fmt.Errorf("%w: coverage probability %.2f is not supported; use 0.90, 0.95, or 0.99", ErrInvalidInput, coverageProbability)
	}
	evaluation.CoverageProbability = coverageProbability
	if evaluation.CreatedAt.IsZero() {
		return UncertaintyEvaluation{}, fmt.Errorf("%w: evaluation time is required", ErrInvalidInput)
	}

	evaluation.Components = make([]UncertaintyComponent, 0, len(drafts))
	for index, draft := range drafts {
		component, err := ComponentFromDraft(fmt.Sprintf("UC-%02d", index+1), draft)
		if err != nil {
			return UncertaintyEvaluation{}, err
		}
		evaluation.Components = append(evaluation.Components, component)
	}

	var sumSquares, denominator float64
	for _, component := range evaluation.Components {
		sumSquares += component.Contribution * component.Contribution
		if component.DegreesOfFreedom > 0 {
			denominator += math.Pow(component.Contribution, 4) / component.DegreesOfFreedom
		}
	}
	combined := math.Sqrt(sumSquares)
	evaluation.CombinedStandardUncertainty = combined
	if denominator > 0 {
		evaluation.EffectiveDegreesOfFreedom = math.Pow(combined, 4) / denominator
	}
	evaluation.CoverageFactor = coverageFactor(coverageProbability, evaluation.EffectiveDegreesOfFreedom)
	evaluation.ExpandedUncertainty = evaluation.CoverageFactor * combined

	if bound != nil {
		evaluation.Measurement = referenceFromMeasurement(*bound)
		evaluation.Conformance = assessConformance(bound.Deviation, tolerance, evaluation.ExpandedUncertainty)
	}
	if err := evaluation.Validate(); err != nil {
		return UncertaintyEvaluation{}, err
	}
	return evaluation, nil
}

// Validate checks the stored evaluation, including values that were computed.
func (e UncertaintyEvaluation) Validate() error {
	if e.ID == "" || e.CaseID == "" || e.DeviceID == "" || e.Technician == "" {
		return fmt.Errorf("%w: evaluation id, case, device, and technician are required", ErrInvalidInput)
	}
	if e.Version < 1 || len(e.Components) == 0 {
		return fmt.Errorf("%w: evaluation version and components are required", ErrInvalidInput)
	}
	if !isFinite(e.CaseNominal) || !isFinite(e.CaseTolerance) || e.CaseTolerance <= 0 {
		return fmt.Errorf("%w: evaluation case facts must be finite with positive tolerance", ErrInvalidInput)
	}
	if !isFinite(e.CombinedStandardUncertainty) || e.CombinedStandardUncertainty <= 0 ||
		!isFinite(e.CoverageFactor) || e.CoverageFactor <= 0 ||
		!isFinite(e.ExpandedUncertainty) || e.ExpandedUncertainty <= 0 {
		return fmt.Errorf("%w: combined and expanded uncertainty must be positive", ErrInvalidInput)
	}
	if !isFinite(e.CoverageProbability) || e.CoverageProbability <= 0 || e.CoverageProbability >= 1 {
		return fmt.Errorf("%w: coverage probability must be between 0 and 1", ErrInvalidInput)
	}
	if e.EffectiveDegreesOfFreedom < 0 {
		return fmt.Errorf("%w: effective degrees of freedom cannot be negative", ErrInvalidInput)
	}
	switch e.Conformance {
	case ConformanceConforms, ConformanceIndeterminate, ConformanceNotConforming, ConformanceNotAssessed:
	default:
		return fmt.Errorf("%w: unsupported conformance outcome %q", ErrInvalidInput, e.Conformance)
	}
	if e.CreatedAt.IsZero() {
		return fmt.Errorf("%w: evaluation time is required", ErrInvalidInput)
	}
	return nil
}

// MatchesCase reports whether the case facts captured by the evaluation still
// hold for the case being sealed.
func (e UncertaintyEvaluation) MatchesCase(deviceID string, nominal, tolerance float64) bool {
	return e.DeviceID == strings.TrimSpace(deviceID) &&
		floatEqual(e.CaseNominal, nominal) &&
		floatEqual(e.CaseTolerance, tolerance)
}

// MatchesMeasurement reports whether the measurement selected for the seal is
// the measurement the budget was bound to.
func (e UncertaintyEvaluation) MatchesMeasurement(item Measurement) bool {
	return e.Measurement != nil && e.Measurement.MeasurementID == item.ID
}

// Clone returns a deep copy so a frozen evaluation inside a seal can never
// share mutable slices with later revisions.
func (e UncertaintyEvaluation) Clone() UncertaintyEvaluation {
	clone := e
	clone.Components = append([]UncertaintyComponent(nil), e.Components...)
	if e.Measurement != nil {
		reference := *e.Measurement
		clone.Measurement = &reference
	}
	return clone
}

// ComponentsChangedSince reports whether the registered components differ from
// the original revision: a different count or any changed component basis or
// numerical value. Computed combined results are not compared.
func (e UncertaintyEvaluation) ComponentsChangedSince(original UncertaintyEvaluation) bool {
	if len(e.Components) != len(original.Components) {
		return true
	}
	for index := range e.Components {
		current := e.Components[index]
		previous := original.Components[index]
		if current.Source != previous.Source ||
			current.Name != previous.Name ||
			current.Method != previous.Method ||
			current.Distribution != previous.Distribution ||
			!floatEqual(current.StandardDeviation, previous.StandardDeviation) ||
			!floatEqual(current.Divisor, previous.Divisor) ||
			!floatEqual(current.HalfWidth, previous.HalfWidth) ||
			!floatEqual(current.Sensitivity, previous.Sensitivity) ||
			!floatEqual(current.DegreesOfFreedom, previous.DegreesOfFreedom) ||
			current.Basis != previous.Basis ||
			current.Reference != previous.Reference {
			return true
		}
	}
	return false
}

// assessConformance applies the expanded uncertainty as a guard band around
// the tolerance interval.
func assessConformance(deviation, tolerance, expanded float64) string {
	distance := abs(deviation)
	switch {
	case distance+expanded <= tolerance:
		return ConformanceConforms
	case distance-expanded > tolerance:
		return ConformanceNotConforming
	default:
		return ConformanceIndeterminate
	}
}

func floatEqual(a, b float64) bool {
	return math.Abs(a-b) <= 1e-12
}

// tQuantiles holds two-sided Student t values for common confidence levels.
// Zero effective degrees of freedom falls back to the normal quantile.
var tQuantiles = map[float64]map[int]float64{
	0.90: {
		1: 6.314, 2: 2.920, 3: 2.353, 4: 2.132, 5: 2.015,
		6: 1.943, 7: 1.895, 8: 1.860, 9: 1.833, 10: 1.812,
		12: 1.782, 15: 1.753, 20: 1.725, 25: 1.708, 30: 1.697,
		40: 1.684, 60: 1.671, 120: 1.658,
	},
	0.95: {
		1: 12.706, 2: 4.303, 3: 3.182, 4: 2.776, 5: 2.571,
		6: 2.447, 7: 2.365, 8: 2.306, 9: 2.262, 10: 2.228,
		12: 2.179, 15: 2.131, 20: 2.086, 25: 2.060, 30: 2.042,
		40: 2.021, 60: 2.000, 120: 1.980,
	},
	0.99: {
		1: 63.657, 2: 9.925, 3: 5.841, 4: 4.604, 5: 4.032,
		6: 3.707, 7: 3.499, 8: 3.355, 9: 3.250, 10: 3.169,
		12: 3.055, 15: 2.947, 20: 2.845, 25: 2.787, 30: 2.750,
		40: 2.704, 60: 2.660, 120: 2.617,
	},
}

var normalQuantiles = map[float64]float64{
	0.90: 1.644853626951,
	0.95: 1.959963984540,
	0.99: 2.575829303549,
}

// coverageFactor selects the coverage factor from the effective degrees of
// freedom. The largest tabulated degree of freedom not exceeding the
// effective value is used, which is the conservative choice.
func coverageFactor(probability, effectiveDegreesOfFreedom float64) float64 {
	table := tQuantiles[probability]
	if effectiveDegreesOfFreedom <= 0 {
		return normalQuantiles[probability]
	}
	limit := int(math.Floor(effectiveDegreesOfFreedom))
	degrees := make([]int, 0, len(table))
	for value := range table {
		degrees = append(degrees, value)
	}
	sort.Ints(degrees)
	chosen := degrees[0]
	for _, value := range degrees {
		if value <= limit && value > chosen {
			chosen = value
		}
	}
	if limit > degrees[len(degrees)-1] {
		return normalQuantiles[probability]
	}
	return table[chosen]
}
