# Metrology Assurance Suite

Metrology Assurance Suite is a Go HTTP service for bounded laboratory
measurement assurance work. It registers devices, opens verification cases,
records measurements, handles failed tolerance decisions, evaluates
measurement uncertainty from documented components, and seals traceable
evidence records that freeze the uncertainty basis reviewed at seal time.

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
go run ./cmd/workflowcheck --workflow seal-verification
go run ./cmd/workflowcheck --workflow register-uncertainty
go run ./cmd/workflowcheck --workflow seal-uncertainty-gate
```

The workflow runner starts a fresh in-process HTTP service and exercises the
public API. It is a production smoke command, not a unit test. The unit and
integration test suite runs with `go test ./...` (add `-race` for the
concurrent seal path).

## API

- `GET /healthz`
- `POST /devices`
- `GET /devices/{id}`
- `POST /cases`
- `GET /cases/{id}`
- `POST /cases/{id}/measurements`
- `POST /cases/{id}/reopen`
- `POST /cases/{id}/seal`
- `POST /cases/{id}/uncertainty`
- `GET /cases/{id}/uncertainty`
- `GET /cases/{id}/events`

## Measurement Uncertainty

Technicians register the uncertainty components for a case at
`POST /cases/{id}/uncertainty`. Each component needs a source (`equipment`,
`standard`, `environment`, `repeatability`, `resolution`, `other`), a name,
method, distribution (`normal`, `rectangular`, `triangular`), inputs,
sensitivity coefficient, and a mandatory `basis` (plus an optional certificate
or log reference). The service combines the standard uncertainties, expands
them with a coverage factor (default 95%), and records a guard-band
conformance of `conforms`, `indeterminate`, or `does_not_conform` against the
accepted measurement.

Registrations are append-only revisions. Sealing requires a current
evaluation; a missing basis, changed components, stale case facts, a
mismatched measurement, or a non-conforming/indeterminate guard band blocks the
seal with `uncertainty_explanation_required` until the reviewer supplies
`uncertainty_explanation`. The seal freezes the evaluation, issue list, and
explanation, and later revisions cannot alter that frozen record.

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
