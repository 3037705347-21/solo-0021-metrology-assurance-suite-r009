# Metrology Assurance Suite

## Project Goal

Metrology Assurance Suite is a local HTTP service that keeps a traceable record
of laboratory measurement assurance work. It registers devices, opens a
verification case for one device, records technician measurements, builds a
measurement uncertainty evaluation from documented components, requests a
repeat measurement when tolerance is exceeded, and seals the final evidence
record after review.

The service is intentionally self-contained. It uses an in-memory repository
with atomic copy-on-write mutations and does not require a database or an
external service.

## Users

- Laboratory steward: registers devices and opens verification cases.
- Measurement technician: records observed values for an open case.
- Quality verifier: reviews accepted measurements and seals the case.

## Core Entities

- Device: a laboratory asset with a unique asset tag, model, room, steward, and
  lifecycle state.
- VerificationCase: one bounded assurance activity for a device, with a nominal
  value, tolerance, current state, version, and assigned steward.
- Measurement: an observed value, computed deviation, acceptance decision,
  technician, and timestamp.
- UncertaintyComponent: one documented contribution (equipment, reference
  standard, environment, method, or other) with a standard uncertainty,
  sensitivity coefficient, evaluation type (A/B), optional degrees of freedom,
  the technician, and the mandatory basis/record reference supporting it.
- UncertaintyEvaluation: an append-only revision of a case's uncertainty
  budget. It combines components by root-sum-of-squares, reports the expanded
  uncertainty with a coverage factor, and is either `draft` or `confirmed`
  against a specific accepted measurement.
- EvidenceSeal: the immutable closing record for a reviewed case, including the
  selected accepted measurement and a frozen copy of the uncertainty basis
  reviewed at seal time.
- AuditEvent: an append-only trace of meaningful case and device changes.

## Workflows

### 1. Register Device

The steward sends `POST /devices` with an asset tag, model, room, and steward.
The service validates the request, normalizes the asset tag, rejects duplicate
tags, and creates an active device.

### 2. Open Verification Case

The steward sends `POST /cases` with a device ID, nominal value, tolerance, and
owner. The service verifies that the device exists, is active, and has no
unfinished case. A valid request creates a case in `awaiting_measurement`.

### 3. Record Measurement

A technician sends `POST /cases/{id}/measurements`. The case must be
`awaiting_measurement`. The service computes the signed deviation from the
nominal value. An in-tolerance measurement moves the case to
`review_ready`; an out-of-tolerance measurement moves it to
`recheck_required`.

### 4. Reopen Verification Case

The steward sends `POST /cases/{id}/reopen` after an out-of-tolerance result.
Only `recheck_required` cases may move back to `awaiting_measurement`. The
service records the actor and reason in the audit trail.

### 5. Evaluate Measurement Uncertainty

A technician registers components with
`POST /cases/{id}/uncertainty/components`. Each component carries a standard
uncertainty, sensitivity coefficient, evaluation type (A/B), optional degrees
of freedom, and the mandatory basis (plus an optional record reference such as
a certificate identifier) that justifies the contribution. The service appends
a new draft evaluation revision every time a component is registered, revised,
or removed; prior revisions are preserved verbatim.

The technician confirms the budget with
`POST /cases/{id}/uncertainty/confirm`, tying the confirmed revision to one
accepted measurement of the case. The service computes the combined standard
uncertainty as the root-sum-of-squares of sensitivity-weighted components, the
Welch-Satterthwaite effective degrees of freedom where known, and the expanded
uncertainty `U = k * u_c`. A component change after confirmation produces a new
draft revision, so stale confirmations can never silently back a seal.
`GET /cases/{id}/uncertainty` returns the current revision, the complete
revision history, and (after sealing) the frozen basis.

### 6. Seal Verification

The verifier sends `POST /cases/{id}/seal` after an accepted measurement. Only
`review_ready` cases may be sealed. The service selects the accepted
measurement with the smallest absolute deviation, reviews the uncertainty
basis, creates exactly one evidence seal, moves the case to `sealed`, and
rejects later mutations.

Sealing is blocked with a concrete list of issues, and an HTTP
`uncertainty_explanation_required` conflict, when any of the following holds
and the verifier has not supplied `uncertainty_explanation`:

- no uncertainty evaluation is registered;
- a component has no recorded basis;
- components changed after confirmation (the latest revision is still draft);
- the evaluation is tied to another or no measurement, or its nominal value or
  tolerance no longer matches the case;
- the guard band fails: `|deviation| + U > tolerance`, so the expanded
  uncertainty does not support unconditional conformance.

A non-empty explicit explanation lets the seal proceed as a documented
exception. The seal freezes an independent copy of the evaluated components,
combined and expanded results, conformance outcome, issue list, and the
reviewer's explanation. Later uncertainty revisions cannot rewrite the sealed
basis; after sealing, uncertainty reads and case events still reconstruct the
basis exactly as reviewed.

## State And Rules

Device state:

- `active -> retired`
- `retired` is terminal.

VerificationCase state:

- `awaiting_measurement -> review_ready`
- `awaiting_measurement -> recheck_required`
- `recheck_required -> awaiting_measurement`
- `review_ready -> sealed`
- `sealed` is terminal.

Required invariants:

- Asset tags are unique after case-insensitive normalization.
- A device has at most one unfinished verification case.
- Measurements are accepted only while a case is awaiting measurement.
- A tolerance must be positive.
- A case can have at most one evidence seal.
- A sealed case cannot accept measurements, reopen, seal again, or accept
  uncertainty component or confirmation changes.
- Every uncertainty component must record a non-empty basis.
- Uncertainty evaluations are append-only revision snapshots; a component
  change after confirmation produces a new draft revision.
- A confirmed evaluation must reference an accepted measurement of the same
  case with the same nominal value and tolerance.
- Sealing with missing basis, unconfirmed or inconsistent evaluations, or a
  failed guard band requires a non-empty explicit reviewer explanation, which
  is stored with the seal.
- The seal owns an independent frozen copy of the uncertainty basis; later
  revisions never mutate it, and sealed reads still return every revision and
  the frozen basis.
- Every mutation that changes a case or its uncertainty evaluation also
  appends an audit event in the same repository commit.

## Modules

- `cmd/metrology-service`: process entrypoint and graceful HTTP shutdown.
- `cmd/workflowcheck`: bounded smoke checks that call the public HTTP API.
- `internal/httpapi`: routing, request decoding, and stable error mapping.
- `internal/assurance`: workflow orchestration and atomic decisions.
- `internal/domain`: entities, validation, state transitions, and errors.
- `internal/repository`: mutex-protected in-memory persistence with copy-on-write
  commits.
- `internal/contracts`: request and response contracts shared by the HTTP layer.

## Public Interfaces

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

## Validation Plan

The baseline build command is `go build ./...`. Each declared workflow has a
bounded production smoke command under `cmd/workflowcheck`. The command starts
an HTTP test server in the same process, calls the public routes, and verifies
both success and failure behavior. The seal check also races two concurrent
seal requests to prove that the repository mutation lock permits exactly one
seal. The uncertainty checks verify the combined/expanded result, revision
history, the draft reset after component changes, the seal gate with and
without an explicit explanation, the guard-band failure, and that the frozen
sealed basis survives later attempts at change.

Tests are intentionally deferred for this initialization baseline. The later
engineering-task stage will add unit and integration tests, including red/green
verification for the workflows and their failure paths.
