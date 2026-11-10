package domain

import "time"

type SyncStatus string

const (
	SyncStatusSuccess SyncStatus = "success"
	SyncStatusFailed  SyncStatus = "failed"
)

// SyncRun records the outcome of fetching a single provider.
type SyncRun struct {
	Provider   string     `json:"provider"`
	StartedAt  time.Time  `json:"started_at"`
	FinishedAt time.Time  `json:"finished_at"`
	Status     SyncStatus `json:"status"`
	Fetched    int        `json:"fetched"`
	Upserted   int        `json:"upserted"`
	// Removed counts stored items the provider no longer returns (only after a complete feed).
	Removed int    `json:"removed"`
	Error   string `json:"error,omitempty"`
}
