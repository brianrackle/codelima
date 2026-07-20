package codelima

import (
	"errors"
	"fmt"

	"github.com/brianrackle/codelima/internal/codelima/daemon"
)

// Process exit codes alias the daemon wire error codes so the CLI and the
// daemon protocol can never disagree on what a code means.
const (
	ExitSuccess               = 0
	ExitInvalidArgument       = daemon.CodeInvalidArgument
	ExitDependencyUnavailable = daemon.CodeDependencyUnavailable
	ExitNotFound              = daemon.CodeNotFound
	ExitPreconditionFailed    = daemon.CodePreconditionFailed
	ExitExternalFailure       = daemon.CodeExternalFailure
	ExitInternalFailure       = daemon.CodeInternalFailure
)

// ErrorCategory is the closed set of AppError classifications. It doubles as
// the RPC error category on the daemon wire, so renaming a value is a protocol
// change.
type ErrorCategory string

const (
	CategoryInvalidArgument       ErrorCategory = "InvalidArgument"
	CategoryNotFound              ErrorCategory = "NotFound"
	CategoryPreconditionFailed    ErrorCategory = "PreconditionFailed"
	CategoryUnsupportedFeature    ErrorCategory = "UnsupportedFeature"
	CategoryDependencyUnavailable ErrorCategory = "DependencyUnavailable"
	CategoryExternalCommandFailed ErrorCategory = "ExternalCommandFailed"
	CategoryMetadataCorruption    ErrorCategory = "MetadataCorruption"
	CategoryInternal              ErrorCategory = "Internal"
)

type AppError struct {
	Category ErrorCategory  `json:"category"`
	Message  string         `json:"message"`
	Code     int            `json:"code"`
	Fields   map[string]any `json:"fields,omitempty"`
	Err      error          `json:"-"`
}

func (e *AppError) Error() string {
	if e == nil {
		return ""
	}

	if e.Err == nil {
		return e.Message
	}

	return fmt.Sprintf("%s: %v", e.Message, e.Err)
}

func (e *AppError) Unwrap() error {
	if e == nil {
		return nil
	}

	return e.Err
}

func newAppError(category ErrorCategory, message string, code int, err error, fields map[string]any) error {
	return &AppError{
		Category: category,
		Message:  message,
		Code:     code,
		Err:      err,
		Fields:   fields,
	}
}

func invalidArgument(message string, fields map[string]any) error {
	return newAppError(CategoryInvalidArgument, message, ExitInvalidArgument, nil, fields)
}

func notFound(message string, fields map[string]any) error {
	return newAppError(CategoryNotFound, message, ExitNotFound, nil, fields)
}

func preconditionFailed(message string, fields map[string]any) error {
	return newAppError(CategoryPreconditionFailed, message, ExitPreconditionFailed, nil, fields)
}

func unsupportedFeature(message string, fields map[string]any) error {
	return newAppError(CategoryUnsupportedFeature, message, ExitPreconditionFailed, nil, fields)
}

func dependencyUnavailable(message string, err error, fields map[string]any) error {
	return newAppError(CategoryDependencyUnavailable, message, ExitDependencyUnavailable, err, fields)
}

func externalCommandFailed(message string, err error, fields map[string]any) error {
	return newAppError(CategoryExternalCommandFailed, message, ExitExternalFailure, err, fields)
}

func metadataCorruption(message string, err error, fields map[string]any) error {
	return newAppError(CategoryMetadataCorruption, message, ExitInternalFailure, err, fields)
}

// IsNotFound reports whether err is an AppError classified NotFound. Callers
// use it instead of comparing Category strings by hand.
func IsNotFound(err error) bool {
	var appErr *AppError
	return errors.As(err, &appErr) && appErr.Category == CategoryNotFound
}

func exitCodeForError(err error) int {
	if err == nil {
		return ExitSuccess
	}

	var appErr *AppError
	if errors.As(err, &appErr) {
		return appErr.Code
	}

	return ExitInternalFailure
}
