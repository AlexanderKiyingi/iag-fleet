package handlers

import (
	"errors"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}

// uniqueConstraint returns the violated unique-constraint name (and true) when
// err is a Postgres 23505 unique_violation, so callers can map a specific
// constraint to a tailored message.
func uniqueConstraint(err error) (string, bool) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return pgErr.ConstraintName, true
	}
	return "", false
}

// snakeToCamel turns a column name into the JSON field these models expose.
//
// Every model in this service tags its fields camelCase against snake_case
// columns (`json:"mountedDate" db:"mounted_date"`), so the mapping is
// mechanical and does not need a per-entity table to stay honest.
func snakeToCamel(s string) string {
	parts := strings.Split(s, "_")
	for i := 1; i < len(parts); i++ {
		if parts[i] == "" {
			continue
		}
		parts[i] = strings.ToUpper(parts[i][:1]) + parts[i][1:]
	}
	return strings.Join(parts, "")
}

// constraintField digs the column out of a constraint name.
//
// Postgres does not report a column for a CHECK violation, only the constraint,
// but the default naming is `<table>_<column>_check` — so
// `inspection_templates_kind_check` yields `kind`. A constraint that does not
// follow the convention yields "", and the caller falls back to a message that
// names no field rather than inventing one.
func constraintField(table, constraint string) string {
	if constraint == "" {
		return ""
	}
	name := strings.TrimSuffix(constraint, "_check")
	if name == constraint {
		return "" // not a check constraint we can read
	}
	if table != "" {
		// `tyres_check` leaves "tyres" once the suffix is gone, and the table
		// prefix does not match because there is nothing after it. Returning it
		// would hand back the table name as if it were a field.
		if name == table {
			return ""
		}
		name = strings.TrimPrefix(name, table+"_")
	}
	if name == "" || name == constraint {
		return ""
	}
	return snakeToCamel(name)
}

// badRequestFromPg turns a write-time constraint violation into a message the
// caller can act on, or ("", false) when the error is not one.
//
// This is the generic half of a problem that was being fixed one entity at a
// time. respondError's fallback returns 500 with err.Error(), which for a
// constraint violation is the raw driver text — and the web app shows the
// service's error verbatim, so an operator filling a form was shown things like
//
//	null value in column "permit_expiry" of relation "drivers" violates
//	not-null constraint (SQLSTATE 23502)
//
// naming a database object rather than the box they left blank. Eighteen tables
// carry a NOT NULL date/uuid column with no default, and the store writes NULL
// for an empty string there (Postgres cannot parse "" as a date or uuid), so
// every one of them could produce that. Answering here covers all of them, and
// anything added later, without a validator per entity.
//
// These three codes are always the request's fault, never the server's, so 400
// is the honest status:
//
//	23502 not_null_violation        — a required field was left empty
//	23514 check_violation           — a field carries a value the column forbids
//	22P02 invalid_text_representation — a field is not the shape its type needs
//
// Entity-specific validators still earn their place: this can say "kind is not
// one of the values this field accepts", while validateInspectionTemplate can
// list them. This is the floor, not a replacement.
func badRequestFromPg(err error) (string, bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return "", false
	}
	switch pgErr.Code {
	case "23502": // not_null_violation — Postgres does report the column here
		if pgErr.ColumnName == "" {
			return "A required field was left empty.", true
		}
		return snakeToCamel(pgErr.ColumnName) + " is required.", true
	case "23514": // check_violation — only the constraint name is reported
		if field := constraintField(pgErr.TableName, pgErr.ConstraintName); field != "" {
			return field + " is not one of the values this field accepts.", true
		}
		return "A field carries a value this record does not accept.", true
	case "22P02": // invalid_text_representation
		// No column is reported for a cast failure, so the message says what
		// kind of problem it is without guessing which field caused it.
		return "A field is not in the format this record expects.", true
	}
	return "", false
}
