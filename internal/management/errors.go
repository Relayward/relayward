package management

import "fmt"

type FieldError struct {
	Field       string
	Description string
}

func (err *FieldError) Error() string {
	return fmt.Sprintf("%s: %s", err.Field, err.Description)
}

func invalid(field, description string) error {
	return &FieldError{Field: field, Description: description}
}
