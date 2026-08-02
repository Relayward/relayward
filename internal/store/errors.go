package store

import "errors"

var (
	ErrNotFound           = errors.New("record not found")
	ErrConflict           = errors.New("record conflicts with existing data")
	ErrAlreadyInitialized = errors.New("administrator is already initialized")
	ErrStateConflict      = errors.New("record state conflicts with operation")
)
