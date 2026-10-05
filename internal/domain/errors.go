package domain

import "errors"

var (
	ErrInvalidInput            = errors.New("invalid input")
	ErrNotFound                = errors.New("resource not found")
	ErrDuplicateAssetTag       = errors.New("duplicate asset tag")
	ErrActiveCaseExists        = errors.New("device already has an unfinished case")
	ErrStateConflict           = errors.New("resource state conflict")
	ErrSealAlreadyCreated      = errors.New("case already has an evidence seal")
	ErrEvaluationNotRegistered = errors.New("measurement uncertainty evaluation is not registered for this case")
	ErrExplanationRequired     = errors.New("sealing requires an explicit uncertainty explanation")
)

// UncertaintyIssueError carries the concrete reasons a seal cannot proceed
// without an explicit reviewer explanation.
type UncertaintyIssueError struct {
	Issues []string
}

func (e *UncertaintyIssueError) Error() string {
	if len(e.Issues) == 1 {
		return ErrExplanationRequired.Error() + ": " + e.Issues[0]
	}
	return ErrExplanationRequired.Error() + ": uncertainty evaluation has unresolved issues"
}

func (e *UncertaintyIssueError) Unwrap() error {
	return ErrExplanationRequired
}

func IsKnownError(err error) bool {
	return errors.Is(err, ErrInvalidInput) ||
		errors.Is(err, ErrNotFound) ||
		errors.Is(err, ErrDuplicateAssetTag) ||
		errors.Is(err, ErrActiveCaseExists) ||
		errors.Is(err, ErrStateConflict) ||
		errors.Is(err, ErrSealAlreadyCreated) ||
		errors.Is(err, ErrEvaluationNotRegistered) ||
		errors.Is(err, ErrExplanationRequired)
}
