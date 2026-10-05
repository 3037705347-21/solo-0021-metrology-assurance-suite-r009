package assurance

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"example.com/solo-0021-metrology-assurance-suite/internal/contracts"
	"example.com/solo-0021-metrology-assurance-suite/internal/domain"
	"example.com/solo-0021-metrology-assurance-suite/internal/repository"
)

// RegisterUncertainty appends one uncertainty budget revision for a case.
// Earlier revisions are never rewritten, and a sealed case rejects revisions
// at the repository boundary.
func (s *Service) RegisterUncertainty(ctx context.Context, caseID string, input contracts.RegisterUncertaintyRequest) (contracts.CaseResponse, error) {
	var result contracts.CaseResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		id := strings.TrimSpace(caseID)
		item, exists := m.Case(id)
		if !exists {
			return fmt.Errorf("%w: case %s", domain.ErrNotFound, id)
		}
		technician := strings.TrimSpace(input.Technician)
		if technician == "" {
			return fmt.Errorf("%w: technician is required", domain.ErrInvalidInput)
		}
		if len(input.Components) == 0 {
			return fmt.Errorf("%w: at least one uncertainty component with its basis is required", domain.ErrInvalidInput)
		}

		revisions := m.Evaluations(item.ID)
		version := len(revisions) + 1

		// The budget is assessed against the accepted measurement that would
		// be selected at seal time; if none exists yet the evaluation records
		// the combined result without a conformance outcome.
		bound, _ := bestAcceptedMeasurement(m.Measurements(item.ID))
		var boundPtr *domain.Measurement
		if bound.ID != "" {
			selected := bound
			boundPtr = &selected
		}

		evaluation, err := domain.NewUncertaintyEvaluation(
			m.NextID("evaluation"),
			item.ID,
			item.DeviceID,
			item.Nominal,
			item.Tolerance,
			version,
			technician,
			input.CoverageProbability,
			input.Components,
			boundPtr,
			s.now(),
		)
		if err != nil {
			return err
		}
		if err := m.AppendEvaluation(evaluation); err != nil {
			return err
		}

		kind := "uncertainty_registered"
		action := "measurement uncertainty budget registered"
		if version > 1 {
			kind = "uncertainty_revised"
			action = fmt.Sprintf("measurement uncertainty budget revised to v%d; previous revisions remain unchanged", version)
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"case",
			item.ID,
			item.ID,
			kind,
			technician,
			uncertaintyEventDetail(action, evaluation),
			s.now(),
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = caseResponse(m, item)
		return nil
	})
	return result, err
}

// Uncertainty returns the current budget and every prior revision for review.
func (s *Service) Uncertainty(ctx context.Context, caseID string) (contracts.UncertaintyResponse, error) {
	var result contracts.UncertaintyResponse
	err := s.repository.Read(ctx, func(snapshot repository.Snapshot) error {
		id := strings.TrimSpace(caseID)
		item, exists := snapshot.Case(id)
		if !exists {
			return fmt.Errorf("%w: case %s", domain.ErrNotFound, id)
		}
		result.CaseID = item.ID
		result.Revisions = snapshot.Evaluations(item.ID)
		if seal, sealed := snapshot.Seal(item.ID); sealed && seal.FrozenUncertainty != nil {
			frozen := seal.FrozenUncertainty.Clone()
			result.Current = &frozen
		} else if current, ok := snapshot.CurrentEvaluation(item.ID); ok {
			result.Current = &current
		}
		return nil
	})
	return result, err
}

// uncertaintySealIssues checks the current budget against the case facts and
// the measurement selected for sealing. Every returned issue forces an
// explicit reviewer explanation before the seal is created.
func uncertaintySealIssues(item domain.VerificationCase, selected domain.Measurement, revisions []domain.UncertaintyEvaluation, current *domain.UncertaintyEvaluation) []string {
	if current == nil {
		return []string{"uncertainty_evaluation_missing: no uncertainty evaluation with component bases is registered for the case"}
	}
	var issues []string
	if !current.MatchesCase(item.DeviceID, item.Nominal, item.Tolerance) {
		issues = append(issues, fmt.Sprintf(
			"uncertainty_case_facts_changed: evaluation v%d facts (device %s, nominal %s, tolerance %s) no longer match the sealed case (device %s, nominal %s, tolerance %s)",
			current.Version,
			current.DeviceID, formatNumber(current.CaseNominal), formatNumber(current.CaseTolerance),
			item.DeviceID, formatNumber(item.Nominal), formatNumber(item.Tolerance),
		))
	}
	switch {
	case current.Measurement == nil:
		issues = append(issues, "uncertainty_measurement_unbound: the evaluation was never assessed against the measurement selected for sealing")
	case !current.MatchesMeasurement(selected):
		issues = append(issues, fmt.Sprintf(
			"uncertainty_measurement_mismatch: evaluation v%d was assessed against measurement %s but the seal selects %s",
			current.Version, current.Measurement.MeasurementID, selected.ID,
		))
	}
	if current.Version > 1 {
		if original := originalRevision(revisions); original != nil && current.ComponentsChangedSince(*original) {
			issues = append(issues, fmt.Sprintf(
				"uncertainty_components_changed: the components or their bases changed between v1 and v%d (%d components at seal)",
				current.Version, len(current.Components),
			))
		}
	}
	switch current.Conformance {
	case domain.ConformanceIndeterminate:
		issues = append(issues, fmt.Sprintf(
			"uncertainty_conformance_indeterminate: |deviation| %s plus expanded uncertainty %s reaches tolerance %s, so the pass/fail decision is not proven",
			formatNumber(selected.AbsoluteDeviation()), formatNumber(current.ExpandedUncertainty), formatNumber(item.Tolerance),
		))
	case domain.ConformanceNotConforming:
		issues = append(issues, fmt.Sprintf(
			"uncertainty_conformance_failing: |deviation| %s minus expanded uncertainty %s still exceeds tolerance %s",
			formatNumber(selected.AbsoluteDeviation()), formatNumber(current.ExpandedUncertainty), formatNumber(item.Tolerance),
		))
	case domain.ConformanceNotAssessed:
		issues = append(issues, "uncertainty_conformance_not_assessed: the evaluation produced no conformance outcome against the selected measurement")
	}
	return issues
}

// originalRevision returns the first (v1) stored revision, which is the basis
// later component changes are measured against.
func originalRevision(revisions []domain.UncertaintyEvaluation) *domain.UncertaintyEvaluation {
	for index := range revisions {
		if revisions[index].Version == 1 {
			return &revisions[index]
		}
	}
	if len(revisions) > 0 {
		return &revisions[0]
	}
	return nil
}

func uncertaintyEventDetail(action string, evaluation domain.UncertaintyEvaluation) string {
	var builder strings.Builder
	builder.WriteString(action)
	fmt.Fprintf(&builder,
		" [evaluation %s v%d, uc=%s, veff=%s, p=%.2f, k=%s, U=%s, conformance=%s] components:",
		evaluation.ID, evaluation.Version,
		formatNumber(evaluation.CombinedStandardUncertainty),
		formatNumber(evaluation.EffectiveDegreesOfFreedom),
		evaluation.CoverageProbability,
		formatNumber(evaluation.CoverageFactor),
		formatNumber(evaluation.ExpandedUncertainty),
		evaluation.Conformance,
	)
	for _, component := range evaluation.Components {
		fmt.Fprintf(&builder,
			" %s(%s/%s, u=%s, c*u=%s, basis=%q, reference=%q);",
			component.ID, component.Source, component.Name,
			formatNumber(component.StandardUncertainty),
			formatNumber(component.Contribution),
			component.Basis, component.Reference,
		)
	}
	if evaluation.Measurement != nil {
		fmt.Fprintf(&builder, " bound measurement %s observed=%s deviation=%s",
			evaluation.Measurement.MeasurementID,
			formatNumber(evaluation.Measurement.Observed),
			formatNumber(evaluation.Measurement.Deviation),
		)
	}
	return builder.String()
}

func formatNumber(value float64) string {
	return strconv.FormatFloat(value, 'g', -1, 64)
}
