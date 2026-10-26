// Package sync defines cluster synchronization data without depending on the
// cluster transport or AdGuard Home modules.
package sync

import "time"

// Operation is one target-specific write retry.  Body is deliberately excluded
// from JSON so that queue snapshots cannot accidentally expose payloads.
type Operation struct {
	ID          string    `json:"id"`
	Target      string    `json:"target"`
	Method      string    `json:"method"`
	Path        string    `json:"path"`
	Query       string    `json:"query,omitempty"`
	Body        []byte    `json:"-"`
	ContentType string    `json:"content_type,omitempty"`
	Attempts    int       `json:"attempts"`
	NextAttempt time.Time `json:"next_attempt"`
	LastError   string    `json:"last_error,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	InFlight    bool      `json:"-"`
}

// AuditEntry contains metadata only; Message is always a stable sanitized code.
type AuditEntry struct {
	Time      time.Time `json:"time"`
	Operation string    `json:"operation"`
	Source    string    `json:"source"`
	Target    string    `json:"target"`
	Result    string    `json:"result"`
	Message   string    `json:"message,omitempty"`
}

// AlignmentResult summarizes one manual or automatic full alignment.
type AlignmentResult struct {
	Target      string    `json:"target"`
	Succeeded   int       `json:"succeeded"`
	Overwritten int       `json:"overwritten"`
	Failed      int       `json:"failed"`
	Skipped     int       `json:"skipped"`
	StartedAt   time.Time `json:"started_at"`
	FinishedAt  time.Time `json:"finished_at"`
}
