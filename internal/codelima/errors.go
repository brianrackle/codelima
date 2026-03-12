package codelima

import "fmt"

const (
	ExitSuccess               = 0
	ExitInvalidArgument       = 2
	ExitDependencyUnavailable = 3
	ExitNotFound              = 4
	ExitPreconditionFailed    = 5
	ExitExternalFailure       = 6
	ExitInternalFailure       = 7
)

type AppError struct {
	Category string         `json:"category"`
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

func newAppError(category, message string, code int, err error, fields map[string]any) error {
	return &AppError{
		Category: category,
		Message:  message,
		Code:     code,
		Err:      err,
		Fields:   fields,
	}
}

func invalidArgument(message string, fields map[string]any) error {
	return newAppError("InvalidArgument", message, ExitInvalidArgument, nil, fields)
}

func notFound(message string, fields map[string]any) error {
	return newAppError("NotFound", message, ExitNotFound, nil, fields)
}

func preconditionFailed(message string, fields map[string]any) error {
	return newAppError("PreconditionFailed", message, ExitPreconditionFailed, nil, fields)
}

func unsupportedFeature(message string, fields map[string]any) error {
	return newAppError("UnsupportedFeature", message, ExitPreconditionFailed, nil, fields)
}

func dependencyUnavailable(message string, err error, fields map[string]any) error {
	return newAppError("DependencyUnavailable", message, ExitDependencyUnavailable, err, fields)
}

func externalCommandFailed(message string, err error, fields map[string]any) error {
	return newAppError("ExternalCommandFailed", message, ExitExternalFailure, err, fields)
}

func patchConflict(message string, fields map[string]any) error {
	return newAppError("PatchConflict", message, ExitPreconditionFailed, nil, fields)
}

func metadataCorruption(message string, err error, fields map[string]any) error {
	return newAppError("MetadataCorruption", message, ExitInternalFailure, err, fields)
}

func exitCodeForError(err error) int {
	if err == nil {
		return ExitSuccess
	}

	var appErr *AppError
	if ok := As(err, &appErr); ok {
		return appErr.Code
	}

	return ExitInternalFailure
}
