package core

import (
	"errors"
	"fmt"
)

// Error kinds. Adapters map them to protocol specific status codes.
var (
	ErrNotFound     = errors.New("not found")
	ErrForbidden    = errors.New("forbidden")
	ErrUnauthorized = errors.New("unauthorized")
	ErrConflict     = errors.New("already exists")
	ErrInvalid      = errors.New("invalid argument")
	ErrNotEmpty     = errors.New("not empty")
	ErrIsFolder     = errors.New("is a folder")
	ErrNotFolder    = errors.New("not a folder")
	ErrTooLarge     = errors.New("too large")
	ErrTooMany      = errors.New("too many requests")
	ErrPrecondition = errors.New("precondition failed")
	ErrChecksum     = errors.New("checksum mismatch")
)

// Error carries a kind plus a human readable message.
type Error struct {
	Kind error
	Msg  string
}

func (e *Error) Error() string { return e.Msg }
func (e *Error) Unwrap() error { return e.Kind }

func errf(kind error, format string, args ...any) error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, args...)}
}

// KindOf returns the error kind of err, or nil if it has none.
func KindOf(err error) error {
	for _, k := range []error{ErrNotFound, ErrForbidden, ErrUnauthorized, ErrConflict, ErrInvalid,
		ErrNotEmpty, ErrIsFolder, ErrNotFolder, ErrTooLarge, ErrTooMany, ErrPrecondition, ErrChecksum} {
		if errors.Is(err, k) {
			return k
		}
	}
	return nil
}
