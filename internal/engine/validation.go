package engine

import (
	"strings"
	"time"
	_ "time/tzdata" // IANA zones must resolve in a distroless image

	"github.com/google/uuid"
)

func id(value string) (string, error) {
	parsed, err := uuid.Parse(value)
	if err != nil {
		return "", invalid("id must be a UUID")
	}
	return parsed.String(), nil
}

// createID accepts a client-generated UUID (so a retry under a new key cannot duplicate) or makes one.
func createID(value string) (string, error) {
	if value == "" {
		return uuid.NewString(), nil
	}
	return id(value)
}

func oneOf(value string, allowed ...string) bool {
	for _, a := range allowed {
		if value == a {
			return true
		}
	}
	return false
}

func instant(field, value string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return t, invalid(field + " must be an RFC 3339 timestamp")
	}
	return t.UTC(), nil
}

func zone(name string) (*time.Location, error) {
	if name == "" {
		name = "UTC"
	}
	loc, err := time.LoadLocation(name)
	if err != nil || name == "Local" {
		return nil, invalid("time_zone must be an IANA zone such as Europe/London")
	}
	return loc, nil
}

// paging validates list inputs and returns limit, offset and the soft-delete state.
func paging(in ListInput) (int, int, string, error) {
	limit := in.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 || limit > 200 {
		return 0, 0, "", invalid("limit must be 1-200")
	}
	if in.Offset < 0 || in.Offset > 1_000_000 {
		return 0, 0, "", invalid("offset must be 0-1000000")
	}
	state := in.State
	if state == "" {
		state = "active"
	}
	if !oneOf(state, "active", "deleted", "all") {
		return 0, 0, "", invalid("state must be active, deleted or all")
	}
	return limit, in.Offset, state, nil
}

// like escapes a literal search for ILIKE.
func like(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(s) + "%"
}

func nextOffset(offset, limit, total int) *int {
	if offset+limit >= total {
		return nil
	}
	n := offset + limit
	return &n
}
