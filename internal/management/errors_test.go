package management

import (
	"errors"
	"testing"
)

func TestFieldErrorPreservesInternalCause(t *testing.T) {
	cause := errors.New("artifact extraction failed")
	err := invalidWithCause("release", "artifacts could not be installed or verified", cause)
	var fieldError *FieldError
	if !errors.As(err, &fieldError) {
		t.Fatal("error is not a FieldError")
	}
	if fieldError.Field != "release" || fieldError.Description != "artifacts could not be installed or verified" {
		t.Fatalf("field error = %+v", fieldError)
	}
	if !errors.Is(err, cause) || fieldError.InternalCause() != cause {
		t.Fatal("field error did not preserve its internal cause")
	}
}
