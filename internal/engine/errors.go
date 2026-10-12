package engine

import (
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string       { return e.Message }
func invalid(message string) *Error  { return &Error{"invalid_input", message} }
func conflict(message string) *Error { return &Error{"conflict", message} }

// Client-generated IDs also prevent duplicate creates with a new request key.
func duplicate(message string) *Error { return &Error{"duplicate", message} }
func notFound() *Error                { return &Error{"not_found", "Record not found"} }

// PublicError maps any error to one safe to show a client; internals are never leaked.
func PublicError(err error) *Error {
	var known *Error
	if errors.As(err, &known) {
		return known
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound()
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		switch pg.Code {
		case "23505":
			return duplicate("A record with that ID already exists")
		case "23503":
			return &Error{"conflict", "A referenced record is missing or still in use"}
		case "40001", "40P01":
			// Nothing is wrong with the request: clients retry with the SAME idempotency key (HTTP 503).
			return &Error{"busy", "The service is busy; retry the request"}
		}
	}
	return &Error{"internal", "The operation could not be completed"}
}
