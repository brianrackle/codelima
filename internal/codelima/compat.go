package codelima

import stderrors "errors"

func As(err error, target any) bool {
	return stderrors.As(err, target)
}

func Is(err, target error) bool {
	return stderrors.Is(err, target)
}
