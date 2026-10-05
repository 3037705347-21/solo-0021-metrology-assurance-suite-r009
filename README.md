# Metrology Assurance Suite

Metrology Assurance Suite is a Go HTTP service for bounded laboratory
measurement assurance work. It registers devices, opens verification cases,
records measurements, builds documented measurement uncertainty evaluations,
handles failed tolerance decisions, and seals traceable evidence records with
the uncertainty basis frozen at review time.

## Run

```bash
go run ./cmd/metrology-service --addr 127.0.0.1:18121
```

The service listens on `127.0.0.1:18121` by default.

## Build And Verify

```bash
go build ./...
go run ./cmd/workflowcheck --workflow register-device
go run ./cmd/workflowcheck --workflow open-verification-case
go run ./cmd/workflowcheck --workflow record-measurement
go run ./cmd/workflowcheck --workflow reopen-verification-case
go run ./cmd/workflowcheck --workflow uncertainty-evaluation
go run ./cmd/workflowcheck --workflow seal-uncertainty-explanation
go run ./cmd/workflowcheck --workflow uncertainty-guard-band
go run ./cmd/workflowcheck --workflow seal-verification
```

The workflow runner starts a fresh in-process HTTP service and exercises the
public API. It is a production smoke command, not a unit test.

Tests are intentionally deferred for this initialization baseline. The later
engineering-task stage will add the unit and integration test suite and replace
or extend these smoke checks with red/green verification tests.

## API

- `GET /healthz`
- `POST /devices`
- `GET /devices/{id}`
- `POST /cases`
- `GET /cases/{id}`
- `POST /cases/{id}/measurements`
- `POST /cases/{id}/reopen`
- `POST /cases/{id}/uncertainty/components`
- `PUT /cases/{id}/uncertainty/components/{componentId}`
- `DELETE /cases/{id}/uncertainty/components/{componentId}`
- `POST /cases/{id}/uncertainty/confirm`
- `GET /cases/{id}/uncertainty`
- `POST /cases/{id}/seal`
- `GET /cases/{id}/events`

## Measurement uncertainty

Technicians register uncertainty components (equipment, reference standard,
environment, method, other) with a standard uncertainty, sensitivity
coefficient, A/B evaluation type, optional degrees of freedom, and a mandatory
`basis` (with an optional record reference such as a certificate id). The
service keeps an append-only history of evaluation revisions, combines
components by root-sum-of-squares into `combined_standard_uncertainty`, and
reports `expanded_uncertainty = coverage_factor * combined_standard_uncertainty`
(default coverage factor 2). Confirmation ties a revision to one accepted
measurement; any later component change returns the evaluation to draft.

Sealing is blocked with error code `uncertainty_explanation_required` and a
concrete `issues` list when the evaluation is missing, has components without a
basis, was changed after confirmation, does not match the current case facts,
or fails the guard band `|deviation| + U <= tolerance`. The verifier can supply
`uncertainty_explanation` to seal with a documented exception. The seal stores
a frozen copy of the components, combined result, conformance outcome, issues,
and the explanation; `GET /cases/{id}/uncertainty` and the events endpoint
still reconstruct that basis after sealing.

## Layout

- `cmd/metrology-service`: executable HTTP server.
- `cmd/workflowcheck`: bounded workflow verification commands.
- `internal/httpapi`: route and JSON boundary.
- `internal/assurance`: workflow orchestration.
- `internal/domain`: state rules and invariants.
- `internal/repository`: copy-on-write in-memory persistence.
- `internal/contracts`: API request and response models.
- `internal/bootstrap`: concrete dependency wiring.

## Inputs And Outputs

Inputs are JSON HTTP requests. Outputs are JSON response envelopes with either a
resource view or a stable error object:

```json
{
  "error": {
    "code": "state_conflict",
    "message": "case is not awaiting measurement"
  }
}
```

The service has no environment variables and no external dependencies.
