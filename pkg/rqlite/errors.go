package rqlite

// errors.go defines error types specific to the rqlite ORM package.

import (
	"errors"
)

var (
	// ErrNotPointer is returned when a non-pointer is passed where a pointer is required.
	ErrNotPointer = errors.New("dest must be a non-nil pointer")

	// ErrNotSlice is returned when dest is not a pointer to a slice.
	ErrNotSlice = errors.New("dest must be pointer to a slice")

	// ErrNotStruct is returned when entity is not a struct.
	ErrNotStruct = errors.New("entity must point to a struct")

	// ErrNoPrimaryKey is returned when no primary key field is found.
	ErrNoPrimaryKey = errors.New("no primary key field found (tag db:\"...,pk\" or field named ID)")

	// ErrNoTableName is returned when unable to resolve table name.
	ErrNoTableName = errors.New("unable to resolve table name; implement TableNamer or set up a repository with explicit table")

	// ErrEntityMustBePointer is returned when entity is not a non-nil pointer to struct.
	ErrEntityMustBePointer = errors.New("entity must be a non-nil pointer to struct")
)
