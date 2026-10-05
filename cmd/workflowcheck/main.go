package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"time"

	"example.com/solo-0021-metrology-assurance-suite/internal/bootstrap"
)

type apiResponse struct {
	Status int
	Body   map[string]any
	Raw    string
}

type harness struct {
	baseURL string
	client  *http.Client
	server  *httptest.Server
}

func main() {
	workflow := flag.String("workflow", "", "workflow id to verify")
	flag.Parse()

	checks := map[string]func() error{
		"register-device":              checkRegisterDevice,
		"open-verification-case":       checkOpenVerificationCase,
		"record-measurement":           checkRecordMeasurement,
		"reopen-verification-case":     checkReopenVerificationCase,
		"seal-verification":            checkSealVerification,
		"uncertainty-evaluation":       checkUncertaintyEvaluation,
		"seal-uncertainty-explanation": checkSealUncertaintyExplanation,
		"uncertainty-guard-band":       checkUncertaintyGuardBand,
	}
	run, ok := checks[*workflow]
	if !ok {
		fmt.Fprintf(flag.CommandLine.Output(), "unknown workflow %q\n", *workflow)
		flag.Usage()
		os.Exit(2)
	}
	if err := run(); err != nil {
		fmt.Printf("workflow %s: failed: %v\n", *workflow, err)
		os.Exit(1)
	}
	fmt.Printf("workflow %s: ok\n", *workflow)
}

func checkRegisterDevice() error {
	h := newHarness()
	defer h.close()

	response, err := h.request(http.MethodPost, "/devices", map[string]any{
		"asset_tag": " met-1001 ",
		"model":     "Bench Meter",
		"room":      "Room A",
		"steward":   "Lin",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return err
	}
	id, err := nestedString(response.Body, "device", "id")
	if err != nil {
		return err
	}
	tag, err := nestedString(response.Body, "device", "asset_tag")
	if err != nil {
		return err
	}
	if tag != "MET-1001" {
		return fmt.Errorf("asset tag was not normalized: %q", tag)
	}

	read, err := h.request(http.MethodGet, "/devices/"+id, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}
	readID, err := nestedString(read.Body, "device", "id")
	if err != nil {
		return err
	}
	if readID != id {
		return fmt.Errorf("read returned device %q, expected %q", readID, id)
	}

	duplicate, err := h.request(http.MethodPost, "/devices", map[string]any{
		"asset_tag": "MET-1001",
		"model":     "Other Meter",
		"room":      "Room B",
		"steward":   "Mira",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(duplicate, http.StatusConflict); err != nil {
		return err
	}
	return expectErrorCode(duplicate, "duplicate_asset_tag")
}

func checkOpenVerificationCase() error {
	h := newHarness()
	defer h.close()

	deviceID, err := h.createDevice("MET-1101")
	if err != nil {
		return err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return err
	}
	state, err := nestedString(opened.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "awaiting_measurement" {
		return fmt.Errorf("opened case state = %q", state)
	}

	read, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}

	second, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id": deviceID,
		"nominal":   25.0,
		"tolerance": 0.5,
		"steward":   "Qiao",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(second, http.StatusConflict); err != nil {
		return err
	}
	return expectErrorCode(second, "active_case_exists")
}

func checkRecordMeasurement() error {
	h := newHarness()
	defer h.close()

	deviceID, err := h.createDevice("MET-1201")
	if err != nil {
		return err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return err
	}
	accepted, err := h.recordMeasurement(caseID, 25.2, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(accepted, http.StatusCreated); err != nil {
		return err
	}
	state, err := nestedString(accepted.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "review_ready" {
		return fmt.Errorf("accepted measurement state = %q", state)
	}
	acceptedValue, err := nestedBool(accepted.Body, "measurements", 0, "accepted")
	if err != nil {
		return err
	}
	if !acceptedValue {
		return errors.New("in-tolerance measurement was not accepted")
	}

	second, err := h.recordMeasurement(caseID, 25.1, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(second, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(second, "state_conflict"); err != nil {
		return err
	}

	rejectDeviceID, err := h.createDevice("MET-1202")
	if err != nil {
		return err
	}
	rejectCase, err := h.openCase(rejectDeviceID)
	if err != nil {
		return err
	}
	rejectCaseID, err := nestedString(rejectCase.Body, "case", "id")
	if err != nil {
		return err
	}
	rejected, err := h.recordMeasurement(rejectCaseID, 26.5, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(rejected, http.StatusCreated); err != nil {
		return err
	}
	rejectedState, err := nestedString(rejected.Body, "case", "state")
	if err != nil {
		return err
	}
	if rejectedState != "recheck_required" {
		return fmt.Errorf("out-of-tolerance state = %q", rejectedState)
	}
	rejectedValue, err := nestedBool(rejected.Body, "measurements", 0, "accepted")
	if err != nil {
		return err
	}
	if rejectedValue {
		return errors.New("out-of-tolerance measurement was accepted")
	}
	return nil
}

func checkReopenVerificationCase() error {
	h := newHarness()
	defer h.close()

	caseID, err := h.rejectedCase("MET-1301")
	if err != nil {
		return err
	}
	reopened, err := h.request(http.MethodPost, "/cases/"+caseID+"/reopen", map[string]any{
		"actor":  "Lin",
		"reason": "measurement setup was replaced",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(reopened, http.StatusOK); err != nil {
		return err
	}
	state, err := nestedString(reopened.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "awaiting_measurement" {
		return fmt.Errorf("reopened case state = %q", state)
	}
	measurements, err := nestedSlice(reopened.Body, "measurements")
	if err != nil {
		return err
	}
	if len(measurements) != 1 {
		return fmt.Errorf("reopen changed measurement history: %d entries", len(measurements))
	}

	second, err := h.request(http.MethodPost, "/cases/"+caseID+"/reopen", map[string]any{
		"actor":  "Lin",
		"reason": "duplicate request",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(second, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(second, "state_conflict"); err != nil {
		return err
	}

	events, err := h.request(http.MethodGet, "/cases/"+caseID+"/events", nil)
	if err != nil {
		return err
	}
	if err := expectStatus(events, http.StatusOK); err != nil {
		return err
	}
	if !hasEvent(events.Body, "case_reopened") {
		return errors.New("reopen audit event is missing")
	}
	return nil
}

func checkSealVerification() error {
	h := newHarness()
	defer h.close()

	caseID, err := h.readyCase("MET-1401")
	if err != nil {
		return err
	}
	type outcome struct {
		response apiResponse
		err      error
	}
	results := make(chan outcome, 2)
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			response, err := h.request(http.MethodPost, "/cases/"+caseID+"/seal", map[string]any{
				"reviewer": fmt.Sprintf("Reviewer-%d", index+1),
				"summary":  "accepted measurement sealed for review",
			})
			results <- outcome{response: response, err: err}
		}(index)
	}
	wait.Wait()
	close(results)

	successes := 0
	conflicts := 0
	for item := range results {
		if item.err != nil {
			return item.err
		}
		switch item.response.Status {
		case http.StatusCreated:
			successes++
		case http.StatusConflict:
			conflicts++
		default:
			return fmt.Errorf("unexpected concurrent seal status %d: %s", item.response.Status, item.response.Raw)
		}
	}
	if successes != 1 || conflicts != 1 {
		return fmt.Errorf("concurrent seal outcomes: %d success, %d conflict", successes, conflicts)
	}

	read, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}
	state, err := nestedString(read.Body, "case", "state")
	if err != nil {
		return err
	}
	if state != "sealed" {
		return fmt.Errorf("sealed case state = %q", state)
	}
	sealID, err := nestedString(read.Body, "seal", "id")
	if err != nil {
		return err
	}
	if sealID == "" {
		return errors.New("evidence seal is missing")
	}
	return nil
}

func checkUncertaintyEvaluation() error {
	h := newHarness()
	defer h.close()

	caseID, err := h.readyCaseRaw("MET-1501")
	if err != nil {
		return err
	}

	// A component without a recorded basis is rejected.
	missingBasis, err := h.request(http.MethodPost, "/cases/"+caseID+"/uncertainty/components", map[string]any{
		"label":                "Device repeatability",
		"category":             "equipment",
		"evaluation_type":      "A",
		"standard_uncertainty": 0.02,
		"technician":           "Rui",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(missingBasis, http.StatusBadRequest); err != nil {
		return err
	}

	first, err := h.registerComponent(caseID, map[string]any{
		"label":                "Device repeatability",
		"category":             "equipment",
		"evaluation_type":      "A",
		"standard_uncertainty": 0.02,
		"degrees_of_freedom":   9.0,
		"basis":                "10 repeated readings on 2026-09-30, standard deviation of the mean",
		"record_reference":     "RR-2026-09-30-07",
		"technician":           "Rui",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(first, http.StatusCreated); err != nil {
		return err
	}
	firstID, err := nestedStringPath(first.Body, "current", "components", 0, "id")
	if err != nil {
		return err
	}
	combined, err := nestedFloat(first.Body, "current", "combined_standard_uncertainty")
	if err != nil {
		return err
	}
	if mathAbs(combined-0.02) > 1e-12 {
		return fmt.Errorf("combined standard uncertainty = %.9g, expected 0.02", combined)
	}
	expanded, err := nestedFloat(first.Body, "current", "expanded_uncertainty")
	if err != nil {
		return err
	}
	if mathAbs(expanded-0.04) > 1e-12 {
		return fmt.Errorf("expanded uncertainty = %.9g, expected 0.04 at k=2", expanded)
	}

	second, err := h.registerComponent(caseID, map[string]any{
		"label":                "Reference standard",
		"category":             "standard",
		"evaluation_type":      "B",
		"standard_uncertainty": 0.03,
		"basis":                "calibration certificate CERT-4471 expanded uncertainty 0.06 at k=2",
		"record_reference":     "CERT-4471",
		"technician":           "Rui",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(second, http.StatusCreated); err != nil {
		return err
	}
	combined, err = nestedFloat(second.Body, "current", "combined_standard_uncertainty")
	if err != nil {
		return err
	}
	if mathAbs(combined-mathSqrt(0.02*0.02+0.03*0.03)) > 1e-12 {
		return fmt.Errorf("RSS combined uncertainty = %.9g", combined)
	}
	revisions, err := nestedSlice(second.Body, "revisions")
	if err != nil {
		return err
	}
	if len(revisions) != 2 {
		return fmt.Errorf("expected 2 revision snapshots, got %d", len(revisions))
	}

	environment, err := h.registerComponent(caseID, map[string]any{
		"label":                "Room temperature",
		"category":             "environment",
		"standard_uncertainty": 0.004,
		"basis":                "laboratory temperature log T-2026-09-30, +/-0.4 C converted to output units",
		"technician":           "Rui",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(environment, http.StatusCreated); err != nil {
		return err
	}
	envID, err := nestedStringPath(environment.Body, "current", "components", 2, "id")
	if err != nil {
		return err
	}

	// Revising a component creates a new draft revision and keeps the old one.
	updated, err := h.request(http.MethodPut, "/cases/"+caseID+"/uncertainty/components/"+envID, map[string]any{
		"label":                "Room temperature",
		"category":             "environment",
		"standard_uncertainty": 0.005,
		"basis":                "re-evaluated temperature log T-2026-10-02",
		"record_reference":     "T-2026-10-02",
		"technician":           "Rui",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(updated, http.StatusOK); err != nil {
		return err
	}
	revisions, err = nestedSlice(updated.Body, "revisions")
	if err != nil {
		return err
	}
	if len(revisions) != 4 {
		return fmt.Errorf("expected 4 revision snapshots after update, got %d", len(revisions))
	}

	// Removing the first component keeps prior revisions untouched.
	removed, err := h.request(http.MethodDelete, "/cases/"+caseID+"/uncertainty/components/"+firstID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(removed, http.StatusOK); err != nil {
		return err
	}
	currentComponents, err := nestedSlicePath(removed.Body, "current", "components")
	if err != nil {
		return err
	}
	if len(currentComponents) != 2 {
		return fmt.Errorf("expected 2 current components after removal, got %d", len(currentComponents))
	}

	// Confirmation must target an accepted measurement of the case.
	badMeasurement, err := h.request(http.MethodPost, "/cases/"+caseID+"/uncertainty/confirm", map[string]any{
		"measurement_id": "measurement-9999",
		"prepared_by":    "Rui",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(badMeasurement, http.StatusBadRequest); err != nil {
		return err
	}
	measurements, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return err
	}
	if err := expectStatus(measurements, http.StatusOK); err != nil {
		return err
	}
	measurementID, err := nestedStringPath(measurements.Body, "measurements", 0, "id")
	if err != nil {
		return err
	}
	confirmed, err := h.request(http.MethodPost, "/cases/"+caseID+"/uncertainty/confirm", map[string]any{
		"measurement_id": measurementID,
		"prepared_by":    "Rui",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(confirmed, http.StatusOK); err != nil {
		return err
	}
	status, err := nestedString(confirmed.Body, "current", "status")
	if err != nil {
		return err
	}
	if status != "confirmed" {
		return fmt.Errorf("evaluation status = %q, expected confirmed", status)
	}
	supported, err := nestedBool(confirmed.Body, "current", "conformance", "conformance_supported")
	if err != nil {
		return err
	}
	if !supported {
		return errors.New("guard-band conformance should be supported for a small budget")
	}

	// A component change after confirmation forces the evaluation back to draft.
	changed, err := h.request(http.MethodPut, "/cases/"+caseID+"/uncertainty/components/"+envID, map[string]any{
		"label":                "Room temperature",
		"category":             "environment",
		"standard_uncertainty": 0.006,
		"basis":                "temperature log T-2026-10-04 supersedes the earlier reading",
		"record_reference":     "T-2026-10-04",
		"technician":           "Rui",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(changed, http.StatusOK); err != nil {
		return err
	}
	status, err = nestedString(changed.Body, "current", "status")
	if err != nil {
		return err
	}
	if status != "draft" {
		return fmt.Errorf("evaluation status after component change = %q, expected draft", status)
	}
	reconfirmed, err := h.request(http.MethodPost, "/cases/"+caseID+"/uncertainty/confirm", map[string]any{
		"measurement_id": measurementID,
		"prepared_by":    "Rui",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(reconfirmed, http.StatusOK); err != nil {
		return err
	}

	// Seal without issues and verify the frozen basis is embedded in the seal.
	sealed, err := h.request(http.MethodPost, "/cases/"+caseID+"/seal", map[string]any{
		"reviewer": "Verifier Ma",
		"summary":  "measurement and uncertainty budget reviewed",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(sealed, http.StatusCreated); err != nil {
		return err
	}
	frozenRevision, err := nestedFloat(sealed.Body, "seal", "uncertainty", "revision")
	if err != nil {
		return err
	}
	if int(frozenRevision) != 8 {
		return fmt.Errorf("frozen uncertainty revision = %.0f, expected 8", frozenRevision)
	}
	frozenBasis, err := nestedStringPath(sealed.Body, "seal", "uncertainty", "components", 1, "basis")
	if err != nil {
		return err
	}
	if frozenBasis != "temperature log T-2026-10-04 supersedes the earlier reading" {
		return fmt.Errorf("frozen basis does not reconstruct the reviewed evidence: %q", frozenBasis)
	}
	sealedComponents, err := nestedSlicePath(sealed.Body, "seal", "uncertainty", "components")
	if err != nil {
		return err
	}
	if len(sealedComponents) != 2 {
		return fmt.Errorf("frozen uncertainty has %d components, expected 2", len(sealedComponents))
	}

	// After sealing, uncertainty reads still reconstruct every revision and basis,
	// but every mutation is rejected.
	uncertainty, err := h.request(http.MethodGet, "/cases/"+caseID+"/uncertainty", nil)
	if err != nil {
		return err
	}
	if err := expectStatus(uncertainty, http.StatusOK); err != nil {
		return err
	}
	if _, err := nestedSlicePath(uncertainty.Body, "sealed", "components"); err != nil {
		return fmt.Errorf("sealed basis missing from uncertainty read: %w", err)
	}
	revisions, err = nestedSlice(uncertainty.Body, "revisions")
	if err != nil {
		return err
	}
	if len(revisions) != 8 {
		return fmt.Errorf("expected 8 preserved revisions, got %d", len(revisions))
	}
	forbidden, err := h.registerComponent(caseID, map[string]any{
		"label":                "late contribution",
		"category":             "other",
		"standard_uncertainty": 0.001,
		"basis":                "attempt after sealing",
		"technician":           "Rui",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(forbidden, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(forbidden, "state_conflict"); err != nil {
		return err
	}
	forbiddenConfirm, err := h.request(http.MethodPost, "/cases/"+caseID+"/uncertainty/confirm", map[string]any{
		"measurement_id": measurementID,
		"prepared_by":    "Rui",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(forbiddenConfirm, http.StatusConflict); err != nil {
		return err
	}

	events, err := h.request(http.MethodGet, "/cases/"+caseID+"/events", nil)
	if err != nil {
		return err
	}
	if err := expectStatus(events, http.StatusOK); err != nil {
		return err
	}
	for _, kind := range []string{
		"uncertainty_component_registered",
		"uncertainty_component_updated",
		"uncertainty_component_removed",
		"uncertainty_confirmed",
	} {
		if !hasEvent(events.Body, kind) {
			return fmt.Errorf("audit event %q is missing", kind)
		}
	}
	return nil
}

func checkSealUncertaintyExplanation() error {
	h := newHarness()
	defer h.close()

	// No uncertainty work at all: sealing requires an explicit explanation.
	caseID, err := h.readyCaseRaw("MET-1601")
	if err != nil {
		return err
	}
	blocked, err := h.request(http.MethodPost, "/cases/"+caseID+"/seal", map[string]any{
		"reviewer": "Verifier Ma",
		"summary":  "attempt without uncertainty budget",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(blocked, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(blocked, "uncertainty_explanation_required"); err != nil {
		return err
	}
	issues, err := nestedSlicePath(blocked.Body, "error", "issues")
	if err != nil {
		return err
	}
	if len(issues) == 0 {
		return errors.New("seal rejection did not list concrete uncertainty issues")
	}

	// Sealing with an explicit explanation proceeds and records the explanation.
	explained, err := h.request(http.MethodPost, "/cases/"+caseID+"/seal", map[string]any{
		"reviewer":                "Verifier Ma",
		"summary":                 "accepted with documented justification",
		"uncertainty_explanation": "reference instrument and standard are within controlled limits for this screening check; full budget to follow in corrective record CR-12",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(explained, http.StatusCreated); err != nil {
		return err
	}
	required, err := nestedBool(explained.Body, "seal", "uncertainty", "explanation_required")
	if err != nil {
		return err
	}
	if !required {
		return errors.New("seal did not mark the uncertainty explanation as required")
	}
	explanation, err := nestedString(explained.Body, "seal", "uncertainty", "explanation")
	if err != nil {
		return err
	}
	if explanation == "" {
		return errors.New("seal did not preserve the reviewer explanation")
	}
	events, err := h.request(http.MethodGet, "/cases/"+caseID+"/events", nil)
	if err != nil {
		return err
	}
	if err := expectStatus(events, http.StatusOK); err != nil {
		return err
	}
	if !hasEvent(events.Body, "case_sealed_with_uncertainty_explanation") {
		return errors.New("explained seal audit event is missing")
	}

	// A confirmed budget that is invalidated by a later component change also
	// blocks sealing until the reviewer explains the discrepancy.
	secondCaseID, err := h.readyCaseRaw("MET-1602")
	if err != nil {
		return err
	}
	componentID, measurementID, err := h.smallConfirmedBudget(secondCaseID)
	if err != nil {
		return err
	}
	if _, err := h.request(http.MethodPut, "/cases/"+secondCaseID+"/uncertainty/components/"+componentID, map[string]any{
		"label":                "Device repeatability",
		"category":             "equipment",
		"standard_uncertainty": 0.03,
		"basis":                "repeated readings on 2026-10-05 after probe replacement",
		"record_reference":     "RR-2026-10-05-02",
		"technician":           "Rui",
	}); err != nil {
		return err
	}
	blockedDraft, err := h.request(http.MethodPost, "/cases/"+secondCaseID+"/seal", map[string]any{
		"reviewer": "Verifier Ma",
		"summary":  "attempt after component change",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(blockedDraft, http.StatusConflict); err != nil {
		return err
	}
	issues, err = nestedSlicePath(blockedDraft.Body, "error", "issues")
	if err != nil {
		return err
	}
	if len(issues) == 0 {
		return errors.New("changed-component seal rejection listed no issues")
	}
	explainedDraft, err := h.request(http.MethodPost, "/cases/"+secondCaseID+"/seal", map[string]any{
		"reviewer":                "Verifier Ma",
		"summary":                 "accepted after reviewing the changed contribution",
		"uncertainty_explanation": "the probe replacement was witnessed and the revised contribution still bounds the observed error for this case",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(explainedDraft, http.StatusCreated); err != nil {
		return err
	}
	frozenStatus, err := nestedString(explainedDraft.Body, "seal", "uncertainty", "status")
	if err != nil {
		return err
	}
	if frozenStatus != "draft" {
		return fmt.Errorf("frozen evaluation status = %q, expected draft", frozenStatus)
	}
	frozenMeasurement, err := nestedString(explainedDraft.Body, "seal", "uncertainty", "measurement_id")
	if err != nil {
		return err
	}
	if frozenMeasurement != measurementID {
		return fmt.Errorf("frozen measurement = %q, expected %q", frozenMeasurement, measurementID)
	}
	return nil
}

func checkUncertaintyGuardBand() error {
	h := newHarness()
	defer h.close()

	deviceID, err := h.createDevice("MET-1701")
	if err != nil {
		return err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return err
	}
	// Observed deviation 0.40 against tolerance 0.50.
	recorded, err := h.recordMeasurement(caseID, 25.4, "Rui")
	if err != nil {
		return err
	}
	if err := expectStatus(recorded, http.StatusCreated); err != nil {
		return err
	}
	measurementID, err := nestedStringPath(recorded.Body, "measurements", 0, "id")
	if err != nil {
		return err
	}
	if _, err := h.registerComponent(caseID, map[string]any{
		"label":                "Reference standard",
		"category":             "standard",
		"standard_uncertainty": 0.2,
		"basis":                "calibration certificate CERT-9 limit dominates this near-limit result",
		"technician":           "Rui",
	}); err != nil {
		return err
	}
	confirmed, err := h.request(http.MethodPost, "/cases/"+caseID+"/uncertainty/confirm", map[string]any{
		"measurement_id": measurementID,
		"prepared_by":    "Rui",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(confirmed, http.StatusOK); err != nil {
		return err
	}
	supported, err := nestedBool(confirmed.Body, "current", "conformance", "conformance_supported")
	if err != nil {
		return err
	}
	if supported {
		return errors.New("guard band should reject conformance when |deviation| + U exceeds tolerance")
	}
	margin, err := nestedFloat(confirmed.Body, "current", "conformance", "guard_band_margin")
	if err != nil {
		return err
	}
	if margin >= 0 {
		return fmt.Errorf("guard-band margin = %.9g, expected negative", margin)
	}

	// Even a confirmed evaluation must be explained when the guard band fails.
	blocked, err := h.request(http.MethodPost, "/cases/"+caseID+"/seal", map[string]any{
		"reviewer": "Verifier Ma",
		"summary":  "near-limit result",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(blocked, http.StatusConflict); err != nil {
		return err
	}
	issues, err := nestedSlicePath(blocked.Body, "error", "issues")
	if err != nil {
		return err
	}
	foundGuardIssue := false
	for _, issue := range issues {
		if text, ok := issue.(string); ok && strings.Contains(text, "unconditional conformance") {
			foundGuardIssue = true
		}
	}
	if !foundGuardIssue {
		return errors.New("guard-band issue was not reported among seal issues")
	}
	explained, err := h.request(http.MethodPost, "/cases/"+caseID+"/seal", map[string]any{
		"reviewer":                "Verifier Ma",
		"summary":                 "accepted for rework screening, not metrological release",
		"uncertainty_explanation": "result is only used to trigger device adjustment; the widened acceptance interval is approved by quality procedure QP-31",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(explained, http.StatusCreated); err != nil {
		return err
	}
	frozenSupported, err := nestedBool(explained.Body, "seal", "uncertainty", "conformance_supported")
	if err != nil {
		return err
	}
	if frozenSupported {
		return errors.New("frozen seal should retain the original unsupported conformance decision")
	}
	return nil
}

func newHarness() *harness {
	server := httptest.NewServer(bootstrap.NewHandler())
	return &harness{
		baseURL: server.URL,
		client:  server.Client(),
		server:  server,
	}
}

func (h *harness) close() {
	h.server.Close()
}

func (h *harness) request(method, path string, body any) (apiResponse, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return apiResponse{}, err
		}
		reader = bytes.NewReader(encoded)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, method, h.baseURL+path, reader)
	if err != nil {
		return apiResponse{}, err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := h.client.Do(request)
	if err != nil {
		return apiResponse{}, err
	}
	defer response.Body.Close()
	rawBytes, err := io.ReadAll(response.Body)
	if err != nil {
		return apiResponse{}, err
	}
	result := apiResponse{Status: response.StatusCode, Raw: string(rawBytes)}
	if len(bytes.TrimSpace(rawBytes)) > 0 {
		if err := json.Unmarshal(rawBytes, &result.Body); err != nil {
			return apiResponse{}, fmt.Errorf("decode %s %s response: %w", method, path, err)
		}
	}
	return result, nil
}

func (h *harness) createDevice(tag string) (string, error) {
	response, err := h.request(http.MethodPost, "/devices", map[string]any{
		"asset_tag": tag,
		"model":     "Bench Meter",
		"room":      "Room A",
		"steward":   "Lin",
	})
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return nestedString(response.Body, "device", "id")
}

func (h *harness) openCase(deviceID string) (apiResponse, error) {
	response, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id": deviceID,
		"nominal":   25.0,
		"tolerance": 0.5,
		"steward":   "Qiao",
	})
	if err != nil {
		return apiResponse{}, err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return apiResponse{}, err
	}
	return response, nil
}

func (h *harness) recordMeasurement(caseID string, observed float64, technician string) (apiResponse, error) {
	return h.request(http.MethodPost, "/cases/"+caseID+"/measurements", map[string]any{
		"observed":   observed,
		"technician": technician,
	})
}

func (h *harness) rejectedCase(tag string) (string, error) {
	deviceID, err := h.createDevice(tag)
	if err != nil {
		return "", err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return "", err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return "", err
	}
	response, err := h.recordMeasurement(caseID, 26.5, "Rui")
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return caseID, nil
}

func (h *harness) readyCase(tag string) (string, error) {
	caseID, err := h.readyCaseRaw(tag)
	if err != nil {
		return "", err
	}
	if _, _, err := h.smallConfirmedBudget(caseID); err != nil {
		return "", err
	}
	return caseID, nil
}

func (h *harness) readyCaseRaw(tag string) (string, error) {
	deviceID, err := h.createDevice(tag)
	if err != nil {
		return "", err
	}
	opened, err := h.openCase(deviceID)
	if err != nil {
		return "", err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return "", err
	}
	response, err := h.recordMeasurement(caseID, 25.2, "Rui")
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return caseID, nil
}

func (h *harness) registerComponent(caseID string, body map[string]any) (apiResponse, error) {
	return h.request(http.MethodPost, "/cases/"+caseID+"/uncertainty/components", body)
}

// smallConfirmedBudget registers a small equipment/standard budget and confirms
// it against the first accepted measurement.
func (h *harness) smallConfirmedBudget(caseID string) (componentID, measurementID string, err error) {
	read, err := h.request(http.MethodGet, "/cases/"+caseID, nil)
	if err != nil {
		return "", "", err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return "", "", err
	}
	measurementID, err = nestedStringPath(read.Body, "measurements", 0, "id")
	if err != nil {
		return "", "", err
	}
	registered, err := h.registerComponent(caseID, map[string]any{
		"label":                "Device repeatability",
		"category":             "equipment",
		"evaluation_type":      "A",
		"standard_uncertainty": 0.01,
		"degrees_of_freedom":   9.0,
		"basis":                "10 repeated readings on 2026-09-30, standard deviation of the mean",
		"record_reference":     "RR-2026-09-30-07",
		"technician":           "Rui",
	})
	if err != nil {
		return "", "", err
	}
	if err := expectStatus(registered, http.StatusCreated); err != nil {
		return "", "", err
	}
	componentID, err = nestedStringPath(registered.Body, "current", "components", 0, "id")
	if err != nil {
		return "", "", err
	}
	if _, err := h.registerComponent(caseID, map[string]any{
		"label":                "Reference standard",
		"category":             "standard",
		"evaluation_type":      "B",
		"standard_uncertainty": 0.015,
		"basis":                "calibration certificate CERT-4471 expanded uncertainty 0.03 at k=2",
		"record_reference":     "CERT-4471",
		"technician":           "Rui",
	}); err != nil {
		return "", "", err
	}
	confirmed, err := h.request(http.MethodPost, "/cases/"+caseID+"/uncertainty/confirm", map[string]any{
		"measurement_id": measurementID,
		"prepared_by":    "Rui",
	})
	if err != nil {
		return "", "", err
	}
	if err := expectStatus(confirmed, http.StatusOK); err != nil {
		return "", "", err
	}
	return componentID, measurementID, nil
}

func expectStatus(response apiResponse, expected int) error {
	if response.Status != expected {
		return fmt.Errorf("status = %d, expected %d: %s", response.Status, expected, response.Raw)
	}
	return nil
}

func expectErrorCode(response apiResponse, expected string) error {
	code, err := nestedString(response.Body, "error", "code")
	if err != nil {
		return err
	}
	if code != expected {
		return fmt.Errorf("error code = %q, expected %q", code, expected)
	}
	return nil
}

func nestedString(root map[string]any, path ...string) (string, error) {
	value, err := nestedValue(root, path...)
	if err != nil {
		return "", err
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s is not a string", strings.Join(path, "."))
	}
	return text, nil
}

func nestedBool(root map[string]any, path ...any) (bool, error) {
	value, err := nestedAny(root, path...)
	if err != nil {
		return false, err
	}
	flag, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%v is not a boolean", path)
	}
	return flag, nil
}

func nestedFloat(root map[string]any, path ...any) (float64, error) {
	value, err := nestedAny(root, path...)
	if err != nil {
		return 0, err
	}
	number, ok := value.(float64)
	if !ok {
		return 0, fmt.Errorf("%v is not a number", path)
	}
	return number, nil
}

func mathAbs(value float64) float64 { return math.Abs(value) }

func mathSqrt(value float64) float64 { return math.Sqrt(value) }

func nestedSlice(root map[string]any, key string) ([]any, error) {
	value, ok := root[key]
	if !ok {
		return nil, fmt.Errorf("missing %s", key)
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%s is not an array", key)
	}
	return items, nil
}

func nestedSlicePath(root map[string]any, path ...any) ([]any, error) {
	value, err := nestedAny(root, path...)
	if err != nil {
		return nil, err
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%v is not an array", path)
	}
	return items, nil
}

func nestedStringPath(root map[string]any, path ...any) (string, error) {
	value, err := nestedAny(root, path...)
	if err != nil {
		return "", err
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%v is not a string", path)
	}
	return text, nil
}

func hasEvent(root map[string]any, kind string) bool {
	items, err := nestedSlice(root, "events")
	if err != nil {
		return false
	}
	for _, item := range items {
		event, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if event["kind"] == kind {
			return true
		}
	}
	return false
}

func nestedValue(root map[string]any, path ...string) (any, error) {
	parts := make([]any, 0, len(path))
	for _, part := range path {
		parts = append(parts, part)
	}
	return nestedAny(root, parts...)
}

func nestedAny(root map[string]any, path ...any) (any, error) {
	var current any = root
	for _, part := range path {
		switch typed := part.(type) {
		case string:
			object, ok := current.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s is not an object", typed)
			}
			value, ok := object[typed]
			if !ok {
				return nil, fmt.Errorf("missing %s", typed)
			}
			current = value
		case int:
			items, ok := current.([]any)
			if !ok {
				return nil, fmt.Errorf("index %d is not inside an array", typed)
			}
			if typed < 0 || typed >= len(items) {
				return nil, fmt.Errorf("index %d is out of range", typed)
			}
			current = items[typed]
		default:
			return nil, fmt.Errorf("unsupported path element %T", part)
		}
	}
	return current, nil
}
