package demo

import (
	"testing"
	"time"
)

func TestNextAtPicksTheNextOccurrenceInUTC(t *testing.T) {
	at, _ := time.Parse("15:04", "03:00")
	cases := []struct {
		now, want string
	}{
		{"2026-10-11T01:30:00Z", "2026-10-11T03:00:00Z"}, // later today
		{"2026-10-11T03:00:00Z", "2026-10-12T03:00:00Z"}, // exactly now: tomorrow, never twice
		{"2026-10-11T22:00:00Z", "2026-10-12T03:00:00Z"}, // already passed
		{"2026-12-31T23:59:00Z", "2027-01-01T03:00:00Z"}, // across a year boundary
	}
	for _, tc := range cases {
		now, _ := time.Parse(time.RFC3339, tc.now)
		if got := nextAt(now, at).Format(time.RFC3339); got != tc.want {
			t.Errorf("nextAt(%s) = %s, want %s", tc.now, got, tc.want)
		}
	}
}
