package assurance

import (
	"context"
	"strings"
	"testing"

	"example.com/solo-0021-metrology-assurance-suite/internal/contracts"
	"example.com/solo-0021-metrology-assurance-suite/internal/domain"
	"example.com/solo-0021-metrology-assurance-suite/internal/repository"
)

func newTestService() (*Service, context.Context) {
	return NewService(repository.New()), context.Background()
}

func openReadyCase(t *testing.T, service *Service, ctx context.Context) string {
	t.Helper()
	device, err := service.RegisterDevice(ctx, contracts.RegisterDeviceRequest{
		AssetTag: "MET-T-1", Model: "Bench Meter", Room: "Room A", Steward: "Lin",
	})
	if err != nil {
		t.Fatalf("register device: %v", err)
	}
	opened, err := service.OpenCase(ctx, contracts.OpenCaseRequest{
		DeviceID: device.ID, Nominal: 25.0, Tolerance: 0.5, Steward: "Qiao",
	})
	if err != nil {
		t.Fatalf("open case: %v", err)
	}
	if _, err := service.RecordMeasurement(ctx, opened.Case.ID, contracts.RecordMeasurementRequest{
		Observed: 25.1, Technician: "Rui",
	}); err != nil {
		t.Fatalf("record measurement: %v", err)
	}
	return opened.Case.ID
}

func conformingDrafts() []domain.ComponentDraft {
	return []domain.ComponentDraft{
		{
			Source: domain.SourceEquipment, Name: "indicator", Method: "type b",
			Distribution: domain.DistributionRectangular, HalfWidth: 0.03, Sensitivity: 1,
			Basis: "CAL-1 limit 0.03", Reference: "CAL-1",
		},
		{
			Source: domain.SourceEnvironment, Name: "temperature", Method: "type b",
			Distribution: domain.DistributionTriangular, HalfWidth: 0.03, Sensitivity: 1,
			Basis: "room log range 0.03",
		},
	}
}

func registerConforming(t *testing.T, service *Service, ctx context.Context, caseID string) {
	t.Helper()
	if _, err := service.RegisterUncertainty(ctx, caseID, contracts.RegisterUncertaintyRequest{
		Technician: "Rui",
		Components: conformingDrafts(),
	}); err != nil {
		t.Fatalf("register uncertainty: %v", err)
	}
}

func TestSealBlockedWithoutUncertaintyEvaluation(t *testing.T) {
	service, ctx := newTestService()
	caseID := openReadyCase(t, service, ctx)

	_, err := service.SealCase(ctx, caseID, contracts.SealCaseRequest{Reviewer: "Nora", Summary: "sealed"})
	if !errorIs(err, domain.ErrUncertaintyExplanationRequired) {
		t.Fatalf("expected uncertainty explanation error, got %v", err)
	}
}

func TestSealSucceedsWithConformingEvaluation(t *testing.T) {
	service, ctx := newTestService()
	caseID := openReadyCase(t, service, ctx)
	registerConforming(t, service, ctx, caseID)

	view, err := service.SealCase(ctx, caseID, contracts.SealCaseRequest{Reviewer: "Nora", Summary: "sealed"})
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if view.Seal == nil || view.Seal.FrozenUncertainty == nil {
		t.Fatal("seal did not freeze the uncertainty evaluation")
	}
	if len(view.Seal.UncertaintyIssues) != 0 || view.Seal.UncertaintyExplanation != "" {
		t.Fatalf("clean seal recorded issues: %v explanation %q", view.Seal.UncertaintyIssues, view.Seal.UncertaintyExplanation)
	}
}

func TestSealIndeterminateRequiresExplanationAndFreezesIt(t *testing.T) {
	service, ctx := newTestService()
	// Deviation 0.49 and a wide budget make the guard band indeterminate.
	caseID := openReadyCaseAt(t, service, ctx, 25.49)
	drafts := []domain.ComponentDraft{{
		Source: domain.SourceEquipment, Name: "indicator", Method: "type b",
		Distribution: domain.DistributionNormal, StandardDeviation: 0.03, Divisor: 1, Sensitivity: 1,
		Basis: "CAL-2 uncertainty 0.03",
	}, {
		Source: domain.SourceEnvironment, Name: "temperature", Method: "type b",
		Distribution: domain.DistributionRectangular, HalfWidth: 0.08, Sensitivity: 1,
		Basis: "ENV-9 wide drift 0.08",
	}}
	if _, err := service.RegisterUncertainty(ctx, caseID, contracts.RegisterUncertaintyRequest{
		Technician: "Rui", Components: drafts,
	}); err != nil {
		t.Fatalf("register uncertainty: %v", err)
	}

	_, err := service.SealCase(ctx, caseID, contracts.SealCaseRequest{Reviewer: "Nora", Summary: "sealed"})
	if !errorIs(err, domain.ErrUncertaintyExplanationRequired) {
		t.Fatalf("expected explanation gate, got %v", err)
	}

	view, err := service.SealCase(ctx, caseID, contracts.SealCaseRequest{
		Reviewer:               "Nora",
		Summary:                "sealed with risk accepted",
		UncertaintyExplanation: "customer accepts the guard-band overlap per concession note CN-3",
	})
	if err != nil {
		t.Fatalf("seal with explanation: %v", err)
	}
	if !containsIssue(view.Seal.UncertaintyIssues, "uncertainty_conformance_indeterminate") {
		t.Fatalf("seal issues missing indeterminate reason: %v", view.Seal.UncertaintyIssues)
	}
	if view.Seal.UncertaintyExplanation == "" {
		t.Fatal("seal dropped the reviewer explanation")
	}
}

func TestRevisionsAreAppendOnlyAndSealFreezesVersion(t *testing.T) {
	service, ctx := newTestService()
	caseID := openReadyCase(t, service, ctx)
	registerConforming(t, service, ctx, caseID)

	changed := conformingDrafts()
	changed[0].Basis = "CAL-1R re-issued certificate"
	changed[0].HalfWidth = 0.02
	if _, err := service.RegisterUncertainty(ctx, caseID, contracts.RegisterUncertaintyRequest{
		Technician: "Rui", Components: changed,
	}); err != nil {
		t.Fatalf("revise uncertainty: %v", err)
	}

	budget, err := service.Uncertainty(ctx, caseID)
	if err != nil {
		t.Fatalf("read uncertainty: %v", err)
	}
	if len(budget.Revisions) != 2 || budget.Revisions[0].Version != 1 || budget.Revisions[1].Version != 2 {
		t.Fatalf("revisions = %+v", budget.Revisions)
	}
	if budget.Revisions[0].Components[0].Basis != "CAL-1 limit 0.03" {
		t.Fatal("later revision rewrote the v1 basis")
	}

	// Changed components require an explanation to seal.
	_, err = service.SealCase(ctx, caseID, contracts.SealCaseRequest{Reviewer: "Nora", Summary: "sealed"})
	if !errorIs(err, domain.ErrUncertaintyExplanationRequired) {
		t.Fatalf("expected component-change gate, got %v", err)
	}
	view, err := service.SealCase(ctx, caseID, contracts.SealCaseRequest{
		Reviewer:               "Nora",
		Summary:                "sealed after revision review",
		UncertaintyExplanation: "v2 tightens equipment contribution; certificate CAL-1R verified",
	})
	if err != nil {
		t.Fatalf("seal with explanation: %v", err)
	}
	if view.Seal.FrozenUncertainty.Version != 2 {
		t.Fatalf("frozen version = %d", view.Seal.FrozenUncertainty.Version)
	}

	// A later registration cannot alter the sealed evaluation.
	if _, err := service.RegisterUncertainty(ctx, caseID, contracts.RegisterUncertaintyRequest{
		Technician: "Rui", Components: changed,
	}); !errorIs(err, domain.ErrStateConflict) {
		t.Fatalf("expected sealed case to reject revisions, got %v", err)
	}
	budget, err = service.Uncertainty(ctx, caseID)
	if err != nil {
		t.Fatalf("read uncertainty after seal: %v", err)
	}
	if budget.Current == nil || budget.Current.Version != 2 {
		t.Fatalf("post-seal read did not restore frozen v2: %+v", budget.Current)
	}
}

func TestCaseResponseExposesCurrentEvaluationAndEvents(t *testing.T) {
	service, ctx := newTestService()
	caseID := openReadyCase(t, service, ctx)
	registerConforming(t, service, ctx, caseID)

	view, err := service.Case(ctx, caseID)
	if err != nil {
		t.Fatalf("read case: %v", err)
	}
	if view.CurrentUncertainty == nil || len(view.UncertaintyRevisions) != 1 {
		t.Fatal("case view did not expose the uncertainty budget")
	}
	found := false
	for _, event := range view.Events {
		if event.Kind == "uncertainty_registered" && strings.Contains(event.Detail, "CAL-1") {
			found = true
		}
	}
	if !found {
		t.Fatalf("registration event lost the component basis: %+v", view.Events)
	}
}

func openReadyCaseAt(t *testing.T, service *Service, ctx context.Context, observed float64) string {
	t.Helper()
	device, err := service.RegisterDevice(ctx, contracts.RegisterDeviceRequest{
		AssetTag: "MET-T-EDGE", Model: "Bench Meter", Room: "Room A", Steward: "Lin",
	})
	if err != nil {
		t.Fatalf("register device: %v", err)
	}
	opened, err := service.OpenCase(ctx, contracts.OpenCaseRequest{
		DeviceID: device.ID, Nominal: 25.0, Tolerance: 0.5, Steward: "Qiao",
	})
	if err != nil {
		t.Fatalf("open case: %v", err)
	}
	if _, err := service.RecordMeasurement(ctx, opened.Case.ID, contracts.RecordMeasurementRequest{
		Observed: observed, Technician: "Rui",
	}); err != nil {
		t.Fatalf("record measurement: %v", err)
	}
	return opened.Case.ID
}

func containsIssue(issues []string, prefix string) bool {
	for _, issue := range issues {
		if strings.HasPrefix(issue, prefix) {
			return true
		}
	}
	return false
}

func errorIs(err, target error) bool {
	return err != nil && strings.Contains(err.Error(), target.Error())
}
