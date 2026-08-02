package store

import "errors"

var (
	ErrNotFound           = errors.New("record not found")
	ErrAlreadyInitialized = errors.New("administrator is already initialized")
	ErrStateConflict      = errors.New("record state conflicts with operation")
)
