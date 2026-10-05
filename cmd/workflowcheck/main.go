package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
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
		"register-device":          checkRegisterDevice,
		"open-verification-case":   checkOpenVerificationCase,
		"record-measurement":       checkRecordMeasurement,
		"reopen-verification-case": checkReopenVerificationCase,
		"seal-verification":        checkSealVerification,
		"register-uncertainty":     checkRegisterUncertainty,
		"seal-uncertainty-gate":    checkSealUncertaintyGate,
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
	if _, err := h.registerUncertainty(caseID, conformingComponents()); err != nil {
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

func checkRegisterUncertainty() error {
	h := newHarness()
	defer h.close()

	caseID, err := h.readyCase("MET-1501")
	if err != nil {
		return err
	}

	// A component without a basis is rejected before anything is stored.
	missingBasis, err := h.request(http.MethodPost, "/cases/"+caseID+"/uncertainty", map[string]any{
		"technician": "Rui",
		"components": []any{
			map[string]any{
				"source":       "equipment",
				"name":         "indicator calibration",
				"method":       "type b calibration certificate",
				"distribution": "rectangular",
				"half_width":   0.05,
				"sensitivity":  1,
			},
		},
	})
	if err != nil {
		return err
	}
	if err := expectStatus(missingBasis, http.StatusBadRequest); err != nil {
		return err
	}
	if err := expectErrorCode(missingBasis, "invalid_input"); err != nil {
		return err
	}

	registered, err := h.registerUncertainty(caseID, conformingComponents())
	if err != nil {
		return err
	}
	if err := expectStatus(registered, http.StatusCreated); err != nil {
		return err
	}
	conformance, err := nestedString(registered.Body, "current_uncertainty", "conformance")
	if err != nil {
		return err
	}
	if conformance != "conforms" {
		return fmt.Errorf("conforming budget conformance = %q", conformance)
	}
	combined, err := nestedFloat(registered.Body, "current_uncertainty", "combined_standard_uncertainty")
	if err != nil {
		return err
	}
	if combined <= 0 {
		return fmt.Errorf("combined uncertainty = %v", combined)
	}
	expanded, err := nestedFloat(registered.Body, "current_uncertainty", "expanded_uncertainty")
	if err != nil {
		return err
	}
	if expanded <= combined {
		return fmt.Errorf("expanded uncertainty %v should exceed combined %v", expanded, combined)
	}
	boundID, err := nestedString(registered.Body, "current_uncertainty", "measurement", "measurement_id")
	if err != nil {
		return err
	}
	if boundID == "" {
		return errors.New("evaluation was not bound to the accepted measurement")
	}

	// A revision appends v2 and leaves v1 intact; both remain readable.
	revised, err := h.registerUncertainty(caseID, conformingComponents())
	if err != nil {
		return err
	}
	if err := expectStatus(revised, http.StatusCreated); err != nil {
		return err
	}
	revisions, err := nestedSlice(revised.Body, "uncertainty_revisions")
	if err != nil {
		return err
	}
	if len(revisions) != 2 {
		return fmt.Errorf("expected 2 revisions, got %d", len(revisions))
	}
	firstVersion, err := nestedFloat(revised.Body, "uncertainty_revisions", 0, "version")
	if err != nil {
		return err
	}
	secondVersion, err := nestedFloat(revised.Body, "uncertainty_revisions", 1, "version")
	if err != nil {
		return err
	}
	if firstVersion != 1 || secondVersion != 2 {
		return fmt.Errorf("revision versions = %v, %v", firstVersion, secondVersion)
	}

	read, err := h.request(http.MethodGet, "/cases/"+caseID+"/uncertainty", nil)
	if err != nil {
		return err
	}
	if err := expectStatus(read, http.StatusOK); err != nil {
		return err
	}
	readRevisions, err := nestedSlice(read.Body, "revisions")
	if err != nil {
		return err
	}
	if len(readRevisions) != 2 {
		return fmt.Errorf("uncertainty read returned %d revisions", len(readRevisions))
	}

	events, err := h.request(http.MethodGet, "/cases/"+caseID+"/events", nil)
	if err != nil {
		return err
	}
	if err := expectStatus(events, http.StatusOK); err != nil {
		return err
	}
	if !hasEvent(events.Body, "uncertainty_registered") || !hasEvent(events.Body, "uncertainty_revised") {
		return errors.New("uncertainty audit events are missing")
	}
	// The registration event must carry the component basis so the event
	// reader can reconstruct why the value was claimed.
	detail, err := eventDetailForKind(events.Body, "uncertainty_registered")
	if err != nil {
		return err
	}
	if !strings.Contains(detail, "CAL-88") || !strings.Contains(detail, "equipment") {
		return fmt.Errorf("event detail does not preserve component basis: %s", detail)
	}
	return nil
}

func checkSealUncertaintyGate() error {
	h := newHarness()
	defer h.close()

	// Scenario A: sealing without any evaluation is blocked, then succeeds
	// with a conforming evaluation (no explanation needed).
	caseID, err := h.readyCase("MET-1601")
	if err != nil {
		return err
	}
	blocked, err := h.request(http.MethodPost, "/cases/"+caseID+"/seal", map[string]any{
		"reviewer": "Nora",
		"summary":  "attempt without uncertainty basis",
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
	if _, err := h.registerUncertainty(caseID, conformingComponents()); err != nil {
		return err
	}
	sealed, err := h.request(http.MethodPost, "/cases/"+caseID+"/seal", map[string]any{
		"reviewer": "Nora",
		"summary":  "conforms within expanded uncertainty guard band",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(sealed, http.StatusCreated); err != nil {
		return err
	}
	frozenVersion, err := nestedFloat(sealed.Body, "seal", "frozen_uncertainty", "version")
	if err != nil {
		return err
	}
	if frozenVersion != 1 {
		return fmt.Errorf("frozen evaluation version = %v", frozenVersion)
	}
	issues, err := optionalSlice(sealed.Body, "seal", "uncertainty_issues")
	if err != nil {
		return err
	}
	if len(issues) != 0 {
		return fmt.Errorf("clean seal recorded issues: %v", issues)
	}

	// Scenario B: a budget revised after the first assessment forces an
	// explicit explanation; without one the seal is rejected, and the sealed
	// record keeps both the issue list and the reviewer's explanation.
	revisedCase, err := h.readyCase("MET-1602")
	if err != nil {
		return err
	}
	if _, err := h.registerUncertainty(revisedCase, conformingComponents()); err != nil {
		return err
	}
	if _, err := h.registerUncertainty(revisedCase, changedComponents()); err != nil {
		return err
	}
	revisedBlock, err := h.request(http.MethodPost, "/cases/"+revisedCase+"/seal", map[string]any{
		"reviewer": "Nora",
		"summary":  "attempt without explaining revised components",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(revisedBlock, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(revisedBlock, "uncertainty_explanation_required"); err != nil {
		return err
	}
	revisedSeal, err := h.request(http.MethodPost, "/cases/"+revisedCase+"/seal", map[string]any{
		"reviewer":                "Nora",
		"summary":                 "revised budget reviewed and justified",
		"uncertainty_explanation": "component revision only tightened the environmental contribution; certificates re-verified.",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(revisedSeal, http.StatusCreated); err != nil {
		return err
	}
	sealedIssues, err := optionalSlice(revisedSeal.Body, "seal", "uncertainty_issues")
	if err != nil {
		return err
	}
	if len(sealedIssues) == 0 {
		return errors.New("revised-budget seal recorded no issues")
	}
	explanation, err := nestedString(revisedSeal.Body, "seal", "uncertainty_explanation")
	if err != nil {
		return err
	}
	if explanation == "" {
		return errors.New("seal lost the reviewer explanation")
	}

	// Later revisions cannot rewrite the sealed evaluation.
	postSeal, err := h.registerUncertainty(revisedCase, conformingComponents())
	if err != nil {
		return err
	}
	if err := expectStatus(postSeal, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(postSeal, "state_conflict"); err != nil {
		return err
	}
	read, err := h.request(http.MethodGet, "/cases/"+revisedCase+"/uncertainty", nil)
	if err != nil {
		return err
	}
	currentVersion, err := nestedFloat(read.Body, "current", "version")
	if err != nil {
		return err
	}
	if currentVersion != 2 {
		return fmt.Errorf("post-seal read changed the frozen version to %v", currentVersion)
	}

	// Scenario C: an indeterminate guard-band conclusion (measurement near
	// the tolerance edge) forces an explicit explanation as well.
	edgeCase, err := h.edgeCase("MET-1603")
	if err != nil {
		return err
	}
	if _, err := h.registerUncertainty(edgeCase, largeComponents()); err != nil {
		return err
	}
	edgeDetail, err := h.request(http.MethodGet, "/cases/"+edgeCase+"/uncertainty", nil)
	if err != nil {
		return err
	}
	edgeConformance, err := nestedString(edgeDetail.Body, "current", "conformance")
	if err != nil {
		return err
	}
	if edgeConformance != "indeterminate" {
		return fmt.Errorf("edge budget conformance = %q", edgeConformance)
	}
	edgeBlock, err := h.request(http.MethodPost, "/cases/"+edgeCase+"/seal", map[string]any{
		"reviewer": "Nora",
		"summary":  "attempt to seal an indeterminate result",
	})
	if err != nil {
		return err
	}
	if err := expectStatus(edgeBlock, http.StatusConflict); err != nil {
		return err
	}
	if err := expectErrorCode(edgeBlock, "uncertainty_explanation_required"); err != nil {
		return err
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

func (h *harness) edgeCase(tag string) (string, error) {
	deviceID, err := h.createDevice(tag)
	if err != nil {
		return "", err
	}
	opened, err := h.request(http.MethodPost, "/cases", map[string]any{
		"device_id": deviceID,
		"nominal":   25.0,
		"tolerance": 0.5,
		"steward":   "Qiao",
	})
	if err != nil {
		return "", err
	}
	if err := expectStatus(opened, http.StatusCreated); err != nil {
		return "", err
	}
	caseID, err := nestedString(opened.Body, "case", "id")
	if err != nil {
		return "", err
	}
	// Deviation 0.49 is inside the 0.5 tolerance by the raw comparison but
	// sits within the expanded uncertainty guard band, making the pass
	// decision indeterminate.
	response, err := h.recordMeasurement(caseID, 25.49, "Rui")
	if err != nil {
		return "", err
	}
	if err := expectStatus(response, http.StatusCreated); err != nil {
		return "", err
	}
	return caseID, nil
}

func (h *harness) registerUncertainty(caseID string, components []any) (apiResponse, error) {
	return h.request(http.MethodPost, "/cases/"+caseID+"/uncertainty", map[string]any{
		"technician": "Rui",
		"components": components,
	})
}

func conformingComponents() []any {
	return []any{
		map[string]any{
			"source":       "equipment",
			"name":         "indicator calibration",
			"method":       "type b from calibration certificate",
			"distribution": "rectangular",
			"half_width":   0.05,
			"sensitivity":  1,
			"basis":        "calibration certificate CAL-88 states max permissible error 0.05 at 25 units",
			"reference":    "CAL-88",
		},
		map[string]any{
			"source":             "standard",
			"name":               "reference gauge",
			"method":             "type b from reference standard uncertainty",
			"distribution":       "normal",
			"standard_deviation": 0.02,
			"divisor":            1,
			"sensitivity":        1,
			"degrees_of_freedom": 50,
			"basis":              "reference gauge REF-12 calibrated value standard uncertainty 0.02",
			"reference":          "REF-12",
		},
		map[string]any{
			"source":       "environment",
			"name":         "room temperature drift",
			"method":       "type b temperature range",
			"distribution": "triangular",
			"half_width":   0.03,
			"sensitivity":  1,
			"basis":        "room log ENV-7 shows temperature controlled within 0.03 equivalent units",
			"reference":    "ENV-7",
		},
		map[string]any{
			"source":       "resolution",
			"name":         "display resolution",
			"method":       "type b digital resolution",
			"distribution": "rectangular",
			"half_width":   0.01,
			"sensitivity":  1,
			"basis":        "indicator manual lists 0.02 least significant digit",
		},
	}
}

// changedComponents keeps the same four sources as conformingComponents but
// changes the equipment value and its basis, which must force an explanation.
func changedComponents() []any {
	components := conformingComponents()
	components[0].(map[string]any)["half_width"] = 0.04
	components[0].(map[string]any)["basis"] = "re-issued calibration certificate CAL-88R tightens the equipment limit to 0.04"
	components[0].(map[string]any)["reference"] = "CAL-88R"
	return components
}

func largeComponents() []any {
	return []any{
		map[string]any{
			"source":             "equipment",
			"name":               "indicator calibration",
			"method":             "type b from calibration certificate",
			"distribution":       "normal",
			"standard_deviation": 0.03,
			"divisor":            1,
			"sensitivity":        1,
			"basis":              "calibration certificate CAL-90 reports standard uncertainty 0.03",
			"reference":          "CAL-90",
		},
		map[string]any{
			"source":       "environment",
			"name":         "room temperature drift",
			"method":       "type b temperature range",
			"distribution": "rectangular",
			"half_width":   0.08,
			"sensitivity":  1,
			"basis":        "room log ENV-9 shows larger drift during this measurement",
			"reference":    "ENV-9",
		},
	}
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

func optionalSlice(root map[string]any, path ...any) ([]any, error) {
	value, err := nestedAny(root, path...)
	if err != nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("%v is not an array", path)
	}
	return items, nil
}

func eventDetailForKind(root map[string]any, kind string) (string, error) {
	items, err := nestedSlice(root, "events")
	if err != nil {
		return "", err
	}
	for _, item := range items {
		event, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if event["kind"] == kind {
			detail, ok := event["detail"].(string)
			if !ok {
				return "", fmt.Errorf("event %s detail is not a string", kind)
			}
			return detail, nil
		}
	}
	return "", fmt.Errorf("event %s not found", kind)
}

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
