package domain

import (
	"math"
	"strings"
	"testing"
	"time"
)

func validDrafts() []ComponentDraft {
	return []ComponentDraft{
		{
			Source:       SourceEquipment,
			Name:         "indicator calibration",
			Method:       "type b certificate",
			Distribution: DistributionRectangular,
			HalfWidth:    0.03,
			Sensitivity:  1,
			Basis:        "CAL-1 certificate limit 0.03",
			Reference:    "CAL-1",
		},
		{
			Source:            SourceRepeatability,
			Name:              "repeat measurements",
			Method:            "type a standard deviation",
			Distribution:      DistributionNormal,
			StandardDeviation: 0.01,
			Divisor:           1,
			Sensitivity:       1,
			DegreesOfFreedom:  floatPtr(9),
			Basis:             "ten repeat readings",
		},
		{
			Source:       SourceEnvironment,
			Name:         "temperature drift",
			Method:       "type b range",
			Distribution: DistributionTriangular,
			HalfWidth:    0.03,
			Sensitivity:  1,
			Basis:        "room log range 0.03",
		},
	}
}

func floatPtr(value float64) *float64 { return &value }

func TestNewUncertaintyEvaluationCombinesComponents(t *testing.T) {
	measurement := Measurement{
		ID:        "measurement-1",
		Observed:  25.1,
		Nominal:   25.0,
		Deviation: 0.1,
		Tolerance: 0.5,
		Accepted:  true,
	}
	evaluation, err := NewUncertaintyEvaluation(
		"evaluation-1", "case-1", "device-1", 25.0, 0.5, 1, "Rui", 0.95,
		validDrafts(), &measurement, time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedUC := math.Sqrt(math.Pow(0.03/math.Sqrt(3), 2) +
		math.Pow(0.01, 2) +
		math.Pow(0.03/math.Sqrt(6), 2))
	if math.Abs(evaluation.CombinedStandardUncertainty-expectedUC) > 1e-12 {
		t.Fatalf("combined uc = %v, want %v", evaluation.CombinedStandardUncertainty, expectedUC)
	}
	if evaluation.CoverageFactor <= 1 || evaluation.ExpandedUncertainty <= evaluation.CombinedStandardUncertainty {
		t.Fatalf("coverage factor %v and expanded %v must grow beyond combined",
			evaluation.CoverageFactor, evaluation.ExpandedUncertainty)
	}
	if evaluation.Conformance != ConformanceConforms {
		t.Fatalf("conformance = %q, want conforms", evaluation.Conformance)
	}
	if evaluation.Measurement == nil || evaluation.Measurement.MeasurementID != "measurement-1" {
		t.Fatalf("evaluation was not bound to measurement-1: %+v", evaluation.Measurement)
	}
	if evaluation.EffectiveDegreesOfFreedom <= 0 {
		t.Fatalf("effective degrees of freedom = %v", evaluation.EffectiveDegreesOfFreedom)
	}
}

func TestComponentRequiresBasis(t *testing.T) {
	drafts := validDrafts()
	drafts[1].Basis = "  "
	_, err := NewUncertaintyEvaluation(
		"evaluation-1", "case-1", "device-1", 25.0, 0.5, 1, "Rui", 0,
		drafts, nil, time.Now(),
	)
	if !errorContains(err, "basis") {
		t.Fatalf("expected basis validation error, got %v", err)
	}
}

func TestComponentRejectsMissingOrInvalidDistributionInputs(t *testing.T) {
	cases := []struct {
		name  string
		draft ComponentDraft
	}{
		{"normal without standard deviation", ComponentDraft{
			Source: SourceEquipment, Name: "x", Method: "m", Distribution: DistributionNormal,
			Sensitivity: 1, Basis: "b",
		}},
		{"rectangular without half width", ComponentDraft{
			Source: SourceEquipment, Name: "x", Method: "m", Distribution: DistributionRectangular,
			Sensitivity: 1, Basis: "b",
		}},
		{"unknown distribution", ComponentDraft{
			Source: SourceEquipment, Name: "x", Method: "m", Distribution: "uniform",
			HalfWidth: 0.1, Sensitivity: 1, Basis: "b",
		}},
		{"unknown source", ComponentDraft{
			Source: "operator", Name: "x", Method: "m", Distribution: DistributionRectangular,
			HalfWidth: 0.1, Sensitivity: 1, Basis: "b",
		}},
		{"zero sensitivity", ComponentDraft{
			Source: SourceEquipment, Name: "x", Method: "m", Distribution: DistributionRectangular,
			HalfWidth: 0.1, Sensitivity: 0, Basis: "b",
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ComponentFromDraft("UC-01", tc.draft); err == nil {
				t.Fatalf("expected validation error for %s", tc.name)
			}
		})
	}
}

func TestEvaluationRequiresComponentsAndFiniteCaseFacts(t *testing.T) {
	at := time.Now()
	if _, err := NewUncertaintyEvaluation("e", "c", "d", 25, 0.5, 1, "Rui", 0.95, nil, nil, at); err == nil {
		t.Fatal("expected error for empty components")
	}
	if _, err := NewUncertaintyEvaluation("e", "c", "d", 25, 0, 1, "Rui", 0.95, validDrafts(), nil, at); err == nil {
		t.Fatal("expected error for non-positive tolerance")
	}
	if _, err := NewUncertaintyEvaluation("e", "c", "d", 25, 0.5, 1, "Rui", 0.8, validDrafts(), nil, at); err == nil {
		t.Fatal("expected error for unsupported coverage probability")
	}
}

func TestAssessConformanceGuardBand(t *testing.T) {
	// |deviation| + U <= tolerance -> conforms.
	if got := assessConformance(0.40, 0.5, 0.05); got != ConformanceConforms {
		t.Fatalf("0.40 with U 0.05 = %q", got)
	}
	// |deviation| - U > tolerance -> proven non-conformance.
	if got := assessConformance(0.60, 0.5, 0.05); got != ConformanceNotConforming {
		t.Fatalf("0.60 with U 0.05 = %q", got)
	}
	// Overlapping guard band -> indeterminate.
	if got := assessConformance(0.49, 0.5, 0.05); got != ConformanceIndeterminate {
		t.Fatalf("0.49 with U 0.05 = %q", got)
	}
}

func TestCoverageFactorGrowsWithSmallerDegreesOfFreedom(t *testing.T) {
	small := coverageFactor(0.95, 2)
	large := coverageFactor(0.95, 120)
	normal := coverageFactor(0.95, 0)
	if !(small > large && large > normal) {
		t.Fatalf("coverage factors should decrease toward z as veff grows: %v %v %v", small, large, normal)
	}
}

func TestEvaluationMatchesCaseFactsAndMeasurement(t *testing.T) {
	measurement := Measurement{ID: "measurement-9", Deviation: 0.1}
	evaluation, err := NewUncertaintyEvaluation(
		"e", "case-1", "device-1", 25.0, 0.5, 1, "Rui", 0.95,
		validDrafts(), &Measurement{ID: "measurement-9"}, time.Now(),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !evaluation.MatchesCase("device-1", 25.0, 0.5) {
		t.Fatal("evaluation should match identical case facts")
	}
	if evaluation.MatchesCase("device-2", 25.0, 0.5) {
		t.Fatal("evaluation matched a different device")
	}
	if !evaluation.MatchesMeasurement(measurement) {
		t.Fatal("evaluation should match its bound measurement id")
	}
}

func TestComponentsChangedSince(t *testing.T) {
	original, err := NewUncertaintyEvaluation("e", "c", "d", 25, 0.5, 1, "Rui", 0.95, validDrafts(), nil, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	identical, err := NewUncertaintyEvaluation("e", "c", "d", 25, 0.5, 2, "Rui", 0.95, validDrafts(), nil, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if identical.ComponentsChangedSince(original) {
		t.Fatal("identical component drafts must not count as a component change")
	}
	changedDrafts := validDrafts()
	changedDrafts[0].Basis = "CAL-2 re-issued certificate"
	changed, err := NewUncertaintyEvaluation("e", "c", "d", 25, 0.5, 2, "Rui", 0.95, changedDrafts, nil, time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !changed.ComponentsChangedSince(original) {
		t.Fatal("changed basis must count as a component change")
	}
}

func TestCloneIsIndependent(t *testing.T) {
	original, err := NewUncertaintyEvaluation(
		"e", "c", "d", 25, 0.5, 1, "Rui", 0.95, validDrafts(),
		&Measurement{ID: "measurement-1"}, time.Now(),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	clone := original.Clone()
	clone.Components[0].Basis = "rewritten"
	clone.Measurement.MeasurementID = "measurement-2"
	if original.Components[0].Basis == "rewritten" || original.Measurement.MeasurementID != "measurement-1" {
		t.Fatal("clone shares mutable state with the original evaluation")
	}
}

func errorContains(err error, want string) bool {
	return err != nil && strings.Contains(err.Error(), want)
}
