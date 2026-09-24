// Package apperr carries errors a client can act on: a stable code the
// frontend localizes, parameters for the localized text (limits, allowed
// values) and an English message for logs and API users. The HTTP layer
// maps codes to statuses through its catalog.
package apperr

import (
	"errors"
	"fmt"
)

type Error struct {
	Code    string
	Message string
	Params  map[string]any
}

func (e *Error) Error() string { return e.Message }

// Is matches another *Error by code, so errors.Is(err, ErrX) holds for any
// error created with ErrX's code.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

// New returns a coded error. kv are alternating parameter names and values.
func New(code, message string, kv ...any) *Error {
	e := &Error{Code: code, Message: message}
	if len(kv) > 0 {
		e.Params = make(map[string]any, len(kv)/2)
		for i := 0; i+1 < len(kv); i += 2 {
			e.Params[fmt.Sprint(kv[i])] = kv[i+1]
		}
	}
	return e
}

// As returns the first coded error in err's chain.
func As(err error) (*Error, bool) {
	var e *Error
	ok := errors.As(err, &e)
	return e, ok
}
