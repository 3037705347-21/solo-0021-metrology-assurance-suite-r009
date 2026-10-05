# Metrology Assurance Suite

## Project Goal

Metrology Assurance Suite is a local HTTP service that keeps a traceable record
of laboratory measurement assurance work. It registers devices, opens a
verification case for one device, records technician measurements, requests a
repeat measurement when tolerance is exceeded, and seals the final evidence
record after review.

Technicians also register a measurement uncertainty budget for each case:
the equipment, reference standard, environmental, repeatability, and
resolution components with the basis (certificate, log, or method) behind each
one. The service combines the components into a standard uncertainty, expands
it with a coverage factor, applies it as a guard band to the tolerance
decision, and freezes the exact assessment used at seal time so reviewers can
see what the conclusion rests on.

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
- EvidenceSeal: the immutable closing record for a reviewed case, including the
  selected accepted measurement and a frozen copy of the uncertainty
  evaluation in force at seal time, with any review issues and the reviewer's
  explicit explanation.
- UncertaintyEvaluation: one append-only revision of a case's combined
  uncertainty budget. Each revision captures the case facts (device, nominal,
  tolerance), every component with its basis, the combined standard
  uncertainty, effective degrees of freedom, coverage factor, expanded
  uncertainty, the bound measurement, and the guard-band conformance outcome.
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

### 5. Seal Verification

The verifier sends `POST /cases/{id}/seal` after an accepted measurement. Only
`review_ready` cases may be sealed. The service selects the accepted
measurement with the smallest absolute deviation, checks the uncertainty gate,
creates exactly one evidence seal that freezes the current uncertainty
evaluation, moves the case to `sealed`, and rejects later mutations.

### 6. Register Measurement Uncertainty

A technician sends `POST /cases/{id}/uncertainty` with one or more
components. Every component carries a source (`equipment`, `standard`,
`environment`, `repeatability`, `resolution`, `other`), a name, the evaluation
method, the distribution (`normal`, `rectangular`, `triangular`) and its
inputs, a sensitivity coefficient, and a non-empty **basis** explaining the
value (with an optional certificate or log reference). A component without a
basis is rejected and nothing is stored.

The service converts each component to a standard uncertainty
(`standard_deviation / divisor` for normal inputs, `half_width / sqrt(3)` for
rectangular, `half_width / sqrt(6)` for triangular), multiplies by the
sensitivity coefficient, and quadratically combines them into the combined
standard uncertainty. It derives the effective degrees of freedom
(Welch-Satterthwaite), the coverage factor for the requested coverage
probability (default 95%), and the expanded uncertainty. When an accepted
measurement exists, the evaluation is bound to the measurement that would be
selected at seal time and a guard-band conformance is recorded:

- `conforms` when `|deviation| + U <= tolerance`,
- `indeterminate` when the expanded uncertainty crosses a tolerance limit,
- `does_not_conform` when `|deviation| - U > tolerance`.

Registrations are revisions (v1, v2, ...). A new registration appends a
revision and never rewrites an earlier one. A sealed case rejects new
revisions. `GET /cases/{id}/uncertainty` returns the current evaluation and
every revision; the case view embeds the same data.

### 7. Uncertainty Seal Gate

Before creating a seal the service compares the current evaluation with the
case being sealed. The seal is blocked with
`uncertainty_explanation_required` when any of the following holds:

- no uncertainty evaluation (and therefore no component bases) is registered;
- the evaluation's case facts (device, nominal, tolerance) no longer match
  the case;
- the evaluation was never assessed against the selected measurement or was
  assessed against a different measurement;
- the components or their bases changed after the initial revision;
- the guard-band conclusion is `indeterminate`, `does_not_conform`, or was
  never assessed.

In those cases the verifier must send a non-empty
`uncertainty_explanation`. The seal then records the issue list and the
explanation alongside a frozen copy of the evaluation. When no issue exists
the seal proceeds without an explanation. Later revisions cannot change the
frozen evaluation, and case details plus the event timeline still reproduce
the basis in force when the case was sealed.

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
- A sealed case cannot accept measurements, reopen, seal again, or accept a
  new uncertainty revision.
- Uncertainty components require a non-empty basis, a supported distribution,
  positive inputs, and a finite non-zero sensitivity coefficient.
- Uncertainty evaluations are append-only revisions; later revisions never
  rewrite earlier ones.
- Sealing requires a current uncertainty evaluation; any issue against the
  case facts, bound measurement, changed components, or guard-band conclusion
  must be accompanied by a non-empty reviewer explanation.
- The evidence seal freezes the exact evaluation, issues, and explanation in
  force at seal time; post-seal reads restore that frozen basis.
- Every mutation that changes a case also appends an audit event in the same
  repository commit.

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
- `POST /cases/{id}/seal`
- `POST /cases/{id}/uncertainty`
- `GET /cases/{id}/uncertainty`
- `GET /cases/{id}/events`

## Validation Plan

The build command is `go build ./...`. Each declared workflow has a bounded
production smoke command under `cmd/workflowcheck`. The command starts an
HTTP test server in the same process, calls the public routes, and verifies
both success and failure behavior. The seal check also races two concurrent
seal requests to prove that the repository mutation lock permits exactly one
seal. The uncertainty checks verify component and basis validation, append-only
revisions, the guard-band outcomes, the seal gate and stable error code, and
that post-seal reads restore the frozen evaluation.

Unit and integration tests (`go test ./...`) cover the uncertainty
combination math, distribution and basis validation, coverage-factor
selection, the append-only revision history, the seal gate, the frozen seal
record, the HTTP error contract, and the concurrent seal race.
