package assurance

import (
	"context"
	"fmt"
	"strings"
	"time"

	"example.com/solo-0021-metrology-assurance-suite/internal/contracts"
	"example.com/solo-0021-metrology-assurance-suite/internal/domain"
	"example.com/solo-0021-metrology-assurance-suite/internal/repository"
)

const defaultCoverageFactor = 2.0

// RegisterComponent appends a new uncertainty component and a fresh draft
// evaluation revision. The basis is mandatory so reviewers can reconstruct why
// the contribution applies to this case.
func (s *Service) RegisterComponent(ctx context.Context, caseID string, input contracts.RegisterComponentRequest) (contracts.UncertaintyResponse, error) {
	var result contracts.UncertaintyResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		item, err := openUncertaintyCase(m, caseID)
		if err != nil {
			return err
		}
		technician := strings.TrimSpace(input.Technician)
		if technician == "" {
			return fmt.Errorf("%w: technician is required", domain.ErrInvalidInput)
		}
		previous, _ := m.LatestUncertainty(item.ID)
		revision := previous.Revision + 1
		if revision == 0 {
			revision = 1
		}
		now := s.now()
		component, err := domain.NewUncertaintyComponent(
			m.NextID("component"),
			item.ID,
			input.Label,
			domain.ComponentCategory(strings.TrimSpace(input.Category)),
			input.EvaluationType,
			input.StandardUncertainty,
			input.SensitivityCoefficient,
			input.DegreesOfFreedom,
			input.Basis,
			input.RecordReference,
			technician,
			revision,
			now,
			now,
		)
		if err != nil {
			return err
		}
		components := append(previous.Components, component)
		coverageFactor := chooseCoverageFactor(input.CoverageFactor, previous.CoverageFactor)
		evaluation, err := buildDraftEvaluation(m, item, previous, components, technician, coverageFactor, now)
		if err != nil {
			return err
		}
		if err := m.AppendUncertaintyRevision(evaluation); err != nil {
			return err
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"uncertainty",
			evaluation.ID,
			item.ID,
			"uncertainty_component_registered",
			technician,
			fmt.Sprintf("component %q (%s, type %s, u=%.9g, c=%.9g) registered as revision %d; basis: %s", component.Label, component.Category, component.EvaluationType, component.StandardUncertainty, component.SensitivityCoefficient, revision, component.Basis),
			now,
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = uncertaintyResponseMutation(m, item)
		return nil
	})
	return result, err
}

// UpdateComponent revises one component and returns the evaluation to draft,
// forcing re-confirmation even if the budget was previously confirmed.
func (s *Service) UpdateComponent(ctx context.Context, caseID, componentID string, input contracts.UpdateComponentRequest) (contracts.UncertaintyResponse, error) {
	var result contracts.UncertaintyResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		item, err := openUncertaintyCase(m, caseID)
		if err != nil {
			return err
		}
		previous, exists := m.LatestUncertainty(item.ID)
		if !exists {
			return fmt.Errorf("%w: uncertainty evaluation for case %s", domain.ErrEvaluationNotRegistered, item.ID)
		}
		targetID := strings.TrimSpace(componentID)
		var original domain.UncertaintyComponent
		var found bool
		for _, component := range previous.Components {
			if component.ID == targetID {
				original = component
				found = true
				break
			}
		}
		if !found {
			return fmt.Errorf("%w: component %s", domain.ErrNotFound, targetID)
		}
		technician := strings.TrimSpace(input.Technician)
		if technician == "" {
			return fmt.Errorf("%w: technician is required", domain.ErrInvalidInput)
		}
		now := s.now()
		revision := previous.Revision + 1
		revised, err := domain.NewUncertaintyComponent(
			original.ID,
			item.ID,
			input.Label,
			domain.ComponentCategory(strings.TrimSpace(input.Category)),
			input.EvaluationType,
			input.StandardUncertainty,
			input.SensitivityCoefficient,
			input.DegreesOfFreedom,
			input.Basis,
			input.RecordReference,
			technician,
			revision,
			original.CreatedAt,
			now,
		)
		if err != nil {
			return err
		}
		components := make([]domain.UncertaintyComponent, 0, len(previous.Components))
		for _, component := range previous.Components {
			if component.ID == targetID {
				components = append(components, revised)
			} else {
				components = append(components, component)
			}
		}
		coverageFactor := chooseCoverageFactor(input.CoverageFactor, previous.CoverageFactor)
		evaluation, err := buildDraftEvaluation(m, item, previous, components, technician, coverageFactor, now)
		if err != nil {
			return err
		}
		if err := m.AppendUncertaintyRevision(evaluation); err != nil {
			return err
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"uncertainty",
			evaluation.ID,
			item.ID,
			"uncertainty_component_updated",
			technician,
			fmt.Sprintf("component %q revised in revision %d; basis: %s", revised.Label, revision, revised.Basis),
			now,
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = uncertaintyResponseMutation(m, item)
		return nil
	})
	return result, err
}

// RemoveComponent drops a component and returns the evaluation to draft.
func (s *Service) RemoveComponent(ctx context.Context, caseID, componentID string) (contracts.UncertaintyResponse, error) {
	var result contracts.UncertaintyResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		item, err := openUncertaintyCase(m, caseID)
		if err != nil {
			return err
		}
		previous, exists := m.LatestUncertainty(item.ID)
		if !exists {
			return fmt.Errorf("%w: uncertainty evaluation for case %s", domain.ErrEvaluationNotRegistered, item.ID)
		}
		targetID := strings.TrimSpace(componentID)
		components := make([]domain.UncertaintyComponent, 0, len(previous.Components))
		var removed domain.UncertaintyComponent
		found := false
		for _, component := range previous.Components {
			if component.ID == targetID {
				removed = component
				found = true
				continue
			}
			components = append(components, component)
		}
		if !found {
			return fmt.Errorf("%w: component %s", domain.ErrNotFound, targetID)
		}
		actor := previous.PreparedBy
		now := s.now()
		evaluation, err := buildDraftEvaluation(m, item, previous, components, actor, previous.CoverageFactor, now)
		if err != nil {
			return err
		}
		if err := m.AppendUncertaintyRevision(evaluation); err != nil {
			return err
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"uncertainty",
			evaluation.ID,
			item.ID,
			"uncertainty_component_removed",
			actor,
			fmt.Sprintf("component %q (u=%.9g, basis: %s) removed in revision %d", removed.Label, removed.StandardUncertainty, removed.Basis, evaluation.Revision),
			now,
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = uncertaintyResponseMutation(m, item)
		return nil
	})
	return result, err
}

// ConfirmEvaluation locks the current components into a confirmed revision tied
// to a specific accepted measurement of the case.
func (s *Service) ConfirmEvaluation(ctx context.Context, caseID string, input contracts.ConfirmEvaluationRequest) (contracts.UncertaintyResponse, error) {
	var result contracts.UncertaintyResponse
	err := s.repository.Mutate(ctx, func(m *repository.Mutation) error {
		item, err := openUncertaintyCase(m, caseID)
		if err != nil {
			return err
		}
		previous, exists := m.LatestUncertainty(item.ID)
		if !exists {
			return fmt.Errorf("%w: uncertainty evaluation for case %s", domain.ErrEvaluationNotRegistered, item.ID)
		}
		preparedBy := strings.TrimSpace(input.PreparedBy)
		if preparedBy == "" {
			return fmt.Errorf("%w: prepared_by is required", domain.ErrInvalidInput)
		}
		measurementID := strings.TrimSpace(input.MeasurementID)
		if measurementID == "" {
			return fmt.Errorf("%w: measurement_id of an accepted case measurement is required", domain.ErrInvalidInput)
		}
		var linked domain.Measurement
		linkedFound := false
		for _, measurement := range m.Measurements(item.ID) {
			if measurement.ID == measurementID {
				linked = measurement
				linkedFound = true
				break
			}
		}
		if !linkedFound {
			return fmt.Errorf("%w: measurement %s does not belong to case %s", domain.ErrInvalidInput, measurementID, item.ID)
		}
		if !linked.Accepted {
			return fmt.Errorf("%w: measurement %s was not accepted", domain.ErrInvalidInput, measurementID)
		}
		if len(previous.Components) == 0 {
			return fmt.Errorf("%w: at least one uncertainty component with a recorded basis is required before confirmation", domain.ErrInvalidInput)
		}
		coverageFactor := chooseCoverageFactor(input.CoverageFactor, previous.CoverageFactor)
		now := s.now()
		evaluation, err := domain.NewUncertaintyEvaluation(
			m.NextID("uncertainty"),
			item.ID,
			previous.Revision+1,
			domain.EvaluationConfirmed,
			measurementID,
			item.Nominal,
			item.Tolerance,
			coverageFactor,
			preparedBy,
			previous.Components,
			previous.CreatedAt,
			now,
			&now,
		)
		if err != nil {
			return err
		}
		if err := m.AppendUncertaintyRevision(evaluation); err != nil {
			return err
		}
		event, err := domain.NewAuditEvent(
			m.NextID("event"),
			"uncertainty",
			evaluation.ID,
			item.ID,
			"uncertainty_confirmed",
			preparedBy,
			fmt.Sprintf("uncertainty evaluation revision %d confirmed against measurement %s: %d components, uc=%.9g, k=%.9g, U=%.9g", evaluation.Revision, measurementID, len(evaluation.Components), evaluation.CombinedStandardUncertainty, evaluation.CoverageFactor, evaluation.ExpandedUncertainty),
			now,
		)
		if err != nil {
			return err
		}
		m.AppendEvent(event)
		result = uncertaintyResponseMutation(m, item)
		return nil
	})
	return result, err
}

// Uncertainty returns the current revision, every past revision, and the basis
// frozen at seal time. Reads work at every case state, including sealed.
func (s *Service) Uncertainty(ctx context.Context, caseID string) (contracts.UncertaintyResponse, error) {
	var result contracts.UncertaintyResponse
	err := s.repository.Read(ctx, func(snapshot repository.Snapshot) error {
		id := strings.TrimSpace(caseID)
		item, exists := snapshot.Case(id)
		if !exists {
			return fmt.Errorf("%w: case %s", domain.ErrNotFound, id)
		}
		result = uncertaintyResponseSnapshot(snapshot, item)
		return nil
	})
	return result, err
}

func openUncertaintyCase(m *repository.Mutation, caseID string) (domain.VerificationCase, error) {
	id := strings.TrimSpace(caseID)
	item, exists := m.Case(id)
	if !exists {
		return domain.VerificationCase{}, fmt.Errorf("%w: case %s", domain.ErrNotFound, id)
	}
	if item.State == domain.CaseSealed {
		return domain.VerificationCase{}, fmt.Errorf("%w: case %s is sealed; the sealed uncertainty evaluation cannot be revised", domain.ErrStateConflict, item.ID)
	}
	return item, nil
}

func chooseCoverageFactor(requested, previous float64) float64 {
	if requested > 0 {
		return requested
	}
	if previous > 0 {
		return previous
	}
	return defaultCoverageFactor
}

func buildDraftEvaluation(
	m *repository.Mutation,
	item domain.VerificationCase,
	previous domain.UncertaintyEvaluation,
	components []domain.UncertaintyComponent,
	preparedBy string,
	coverageFactor float64,
	at time.Time,
) (domain.UncertaintyEvaluation, error) {
	revision := 1
	createdAt := at
	measurementID := ""
	if previous.ID != "" {
		revision = previous.Revision + 1
		createdAt = previous.CreatedAt
		measurementID = previous.MeasurementID
	}
	return domain.NewUncertaintyEvaluation(
		m.NextID("uncertainty"),
		item.ID,
		revision,
		domain.EvaluationDraft,
		measurementID,
		item.Nominal,
		item.Tolerance,
		coverageFactor,
		preparedBy,
		components,
		createdAt,
		at,
		nil,
	)
}

func uncertaintyView(reader repository.Reader, item domain.VerificationCase, evaluation domain.UncertaintyEvaluation) *contracts.UncertaintyEvaluationView {
	view := &contracts.UncertaintyEvaluationView{UncertaintyEvaluation: evaluation}
	for _, measurement := range reader.Measurements(item.ID) {
		if measurement.ID == evaluation.MeasurementID {
			analysis := domain.AnalyzeConformance(evaluation, measurement)
			view.Conformance = &analysis
			break
		}
	}
	return view
}

func uncertaintyResponseMutation(m *repository.Mutation, item domain.VerificationCase) contracts.UncertaintyResponse {
	return buildUncertaintyResponse(m, item)
}

func uncertaintyResponseSnapshot(snapshot repository.Snapshot, item domain.VerificationCase) contracts.UncertaintyResponse {
	return buildUncertaintyResponse(snapshot, item)
}

func buildUncertaintyResponse(reader repository.Reader, item domain.VerificationCase) contracts.UncertaintyResponse {
	result := contracts.UncertaintyResponse{
		CaseID:    item.ID,
		Revisions: reader.UncertaintyRevisions(item.ID),
	}
	if evaluation, exists := reader.LatestUncertainty(item.ID); exists {
		result.Current = uncertaintyView(reader, item, evaluation)
	}
	if seal, exists := reader.Seal(item.ID); exists {
		frozen := seal.Uncertainty
		result.Sealed = &frozen
	}
	return result
}
