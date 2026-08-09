package management

import (
	"errors"
	"fmt"
)

var ErrUpstreamUnavailable = errors.New("upstream service is unavailable")

type FieldError struct {
	Field       string
	Description string
	cause       error
}

func (err *FieldError) Error() string {
	return fmt.Sprintf("%s: %s", err.Field, err.Description)
}

func (err *FieldError) Unwrap() error {
	return err.cause
}

func (err *FieldError) InternalCause() error {
	return err.cause
}

func invalid(field, description string) error {
	return &FieldError{Field: field, Description: description}
}

func invalidWithCause(field, description string, cause error) error {
	return &FieldError{Field: field, Description: description, cause: cause}
}
