package domain

import (
	"fmt"
	"strings"
	"time"
)

type EvidenceSeal struct {
	ID                     string                 `json:"id"`
	CaseID                 string                 `json:"case_id"`
	DeviceID               string                 `json:"device_id"`
	MeasurementID          string                 `json:"measurement_id"`
	Reviewer               string                 `json:"reviewer"`
	Summary                string                 `json:"summary"`
	SealedAt               time.Time              `json:"sealed_at"`
	FrozenUncertainty      *UncertaintyEvaluation `json:"frozen_uncertainty,omitempty"`
	UncertaintyIssues      []string               `json:"uncertainty_issues,omitempty"`
	UncertaintyExplanation string                 `json:"uncertainty_explanation,omitempty"`
}

func NewEvidenceSeal(
	id, caseID, deviceID, measurementID, reviewer, summary string,
	frozen *UncertaintyEvaluation,
	issues []string,
	explanation string,
	at time.Time,
) (EvidenceSeal, error) {
	item := EvidenceSeal{
		ID:                     strings.TrimSpace(id),
		CaseID:                 strings.TrimSpace(caseID),
		DeviceID:               strings.TrimSpace(deviceID),
		MeasurementID:          strings.TrimSpace(measurementID),
		Reviewer:               strings.TrimSpace(reviewer),
		Summary:                strings.TrimSpace(summary),
		SealedAt:               at.UTC(),
		UncertaintyExplanation: strings.TrimSpace(explanation),
	}
	if frozen != nil {
		clone := frozen.Clone()
		item.FrozenUncertainty = &clone
	}
	if len(issues) > 0 {
		item.UncertaintyIssues = append([]string(nil), issues...)
	}
	if err := item.Validate(); err != nil {
		return EvidenceSeal{}, err
	}
	return item, nil
}

func (s EvidenceSeal) Validate() error {
	if s.ID == "" || s.CaseID == "" || s.DeviceID == "" || s.MeasurementID == "" || s.Reviewer == "" || s.Summary == "" {
		return fmt.Errorf("%w: seal id, case, device, measurement, reviewer, and summary are required", ErrInvalidInput)
	}
	if s.SealedAt.IsZero() {
		return fmt.Errorf("%w: seal time is required", ErrInvalidInput)
	}
	if s.FrozenUncertainty != nil {
		if err := s.FrozenUncertainty.Validate(); err != nil {
			return err
		}
		if s.FrozenUncertainty.CaseID != s.CaseID {
			return fmt.Errorf("%w: frozen uncertainty belongs to another case", ErrInvalidInput)
		}
	}
	return nil
}
