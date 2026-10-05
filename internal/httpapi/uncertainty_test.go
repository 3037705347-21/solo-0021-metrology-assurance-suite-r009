package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"example.com/solo-0021-metrology-assurance-suite/internal/assurance"
	"example.com/solo-0021-metrology-assurance-suite/internal/repository"
)

func TestUncertaintyEndToEnd(t *testing.T) {
	handler := NewHandler(assurance.NewService(repository.New()))
	server := httptest.NewServer(handler)
	defer server.Close()

	post := func(t *testing.T, path string, body string) (*http.Response, map[string]any) {
		t.Helper()
		response, err := http.Post(server.URL+path, "application/json", bytes.NewBufferString(body))
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		defer response.Body.Close()
		return response, decode(t, response)
	}
	get := func(t *testing.T, path string) (*http.Response, map[string]any) {
		t.Helper()
		response, err := http.Get(server.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		defer response.Body.Close()
		return response, decode(t, response)
	}

	_, deviceBody := post(t, "/devices", `{"asset_tag":"MET-W-1","model":"Bench Meter","room":"Room A","steward":"Lin"}`)
	deviceID, _ := deviceBody["device"].(map[string]any)["id"].(string)

	_, caseBody := post(t, "/cases", `{"device_id":"`+deviceID+`","nominal":25,"tolerance":0.5,"steward":"Qiao"}`)
	caseID, _ := caseBody["case"].(map[string]any)["id"].(string)

	_, measurementBody := post(t, "/cases/"+caseID+"/measurements", `{"observed":25.1,"technician":"Rui"}`)
	if state, _ := measurementBody["case"].(map[string]any)["state"].(string); state != "review_ready" {
		t.Fatalf("case state = %q", state)
	}

	// Missing basis is a 400 invalid_input.
	missingBasis, _ := post(t, "/cases/"+caseID+"/uncertainty", `{
		"technician":"Rui",
		"components":[{"source":"equipment","name":"x","method":"m","distribution":"rectangular","half_width":0.03,"sensitivity":1}]
	}`)
	if missingBasis.StatusCode != http.StatusBadRequest {
		t.Fatalf("missing basis status = %d", missingBasis.StatusCode)
	}

	registered, registeredBody := post(t, "/cases/"+caseID+"/uncertainty", `{
		"technician":"Rui",
		"components":[
			{"source":"equipment","name":"indicator","method":"type b","distribution":"rectangular","half_width":0.03,"sensitivity":1,"basis":"CAL-1 limit 0.03","reference":"CAL-1"},
			{"source":"environment","name":"temperature","method":"type b","distribution":"triangular","half_width":0.03,"sensitivity":1,"basis":"room log range 0.03"}
		]
	}`)
	if registered.StatusCode != http.StatusCreated {
		t.Fatalf("register status = %d", registered.StatusCode)
	}
	current, _ := registeredBody["current_uncertainty"].(map[string]any)
	if current["conformance"] != "conforms" {
		t.Fatalf("conformance = %v", current["conformance"])
	}

	// Sealing without issues succeeds and freezes the evaluation.
	sealed, sealedBody := post(t, "/cases/"+caseID+"/seal", `{"reviewer":"Nora","summary":"reviewed"}`)
	if sealed.StatusCode != http.StatusCreated {
		t.Fatalf("seal status = %d body=%v", sealed.StatusCode, sealedBody)
	}
	frozen, _ := sealedBody["seal"].(map[string]any)["frozen_uncertainty"].(map[string]any)
	if frozen == nil || frozen["version"].(float64) != 1 {
		t.Fatalf("frozen uncertainty missing: %v", sealedBody["seal"])
	}

	// Revisions after sealing are a 409 state_conflict.
	afterSeal, _ := post(t, "/cases/"+caseID+"/uncertainty", `{
		"technician":"Rui",
		"components":[{"source":"equipment","name":"indicator","method":"type b","distribution":"rectangular","half_width":0.02,"sensitivity":1,"basis":"CAL-2"}]
	}`)
	if afterSeal.StatusCode != http.StatusConflict {
		t.Fatalf("post-seal revision status = %d", afterSeal.StatusCode)
	}

	// The read endpoint still restores the frozen v1 basis.
	_, budgetBody := get(t, "/cases/"+caseID+"/uncertainty")
	if currentVersion, _ := budgetBody["current"].(map[string]any)["version"].(float64); currentVersion != 1 {
		t.Fatalf("current version after seal = %v", currentVersion)
	}
	revisions, _ := budgetBody["revisions"].([]any)
	if len(revisions) != 1 {
		t.Fatalf("post-seal revisions changed: %d", len(revisions))
	}
}

func TestSealGateStableErrorCode(t *testing.T) {
	handler := NewHandler(assurance.NewService(repository.New()))
	server := httptest.NewServer(handler)
	defer server.Close()

	response, err := http.Post(server.URL+"/devices", "application/json", strings.NewReader(
		`{"asset_tag":"MET-W-2","model":"Bench Meter","room":"Room A","steward":"Lin"}`))
	if err != nil {
		t.Fatal(err)
	}
	var deviceEnvelope struct {
		Device struct {
			ID string `json:"id"`
		} `json:"device"`
	}
	decodeInto(t, response, &deviceEnvelope)
	response.Body.Close()

	mustPost := func(body string) *http.Response {
		response, err := http.Post(server.URL+"/cases", "application/json", strings.NewReader(
			`{"device_id":"`+deviceEnvelope.Device.ID+`","nominal":25,"tolerance":0.5,"steward":"Qiao"}`))
		if err != nil {
			t.Fatal(err)
		}
		var caseEnvelope struct {
			Case struct {
				ID string `json:"id"`
			} `json:"case"`
		}
		decodeInto(t, response, &caseEnvelope)
		response.Body.Close()
		caseID := caseEnvelope.Case.ID

		response, err = http.Post(server.URL+"/cases/"+caseID+"/measurements", "application/json",
			strings.NewReader(`{"observed":25.1,"technician":"Rui"}`))
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()

		response, err = http.Post(server.URL+"/cases/"+caseID+"/seal", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return response
	}

	blocked := mustPost(`{"reviewer":"Nora","summary":"reviewed"}`)
	defer blocked.Body.Close()
	if blocked.StatusCode != http.StatusConflict {
		t.Fatalf("gate status = %d", blocked.StatusCode)
	}
	var errorEnvelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	decodeInto(t, blocked, &errorEnvelope)
	if errorEnvelope.Error.Code != "uncertainty_explanation_required" {
		t.Fatalf("error code = %q", errorEnvelope.Error.Code)
	}
}

func decode(t *testing.T, response *http.Response) map[string]any {
	t.Helper()
	body := map[string]any{}
	decodeInto(t, response, &body)
	return body
}

func decodeInto(t *testing.T, response *http.Response, target any) {
	t.Helper()
	if err := json.NewDecoder(response.Body).Decode(target); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
