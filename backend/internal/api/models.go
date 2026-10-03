package api

import "time"

// --- Request types ---

// CreateTransferRequest is the body for POST /api/v1/transfers.
type CreateTransferRequest struct {
	ExpiresInSeconds int `json:"expires_in_seconds,omitempty"`
	MaxDownloads     int `json:"max_downloads,omitempty"`
}

// CreateSlotRequest is the body for POST /api/v1/slots.
type CreateSlotRequest struct {
	ExpiresInSeconds int `json:"expires_in_seconds,omitempty"`
}

// --- Response types ---

// TransferResponse is returned when querying a transfer.
type TransferResponse struct {
	ID            string     `json:"id"`
	Status        string     `json:"status"`
	FileCount     int        `json:"file_count"`
	TotalSize     int64      `json:"total_size"`
	HasManifest   bool       `json:"has_manifest"`
	ExpiresAt     time.Time  `json:"expires_at"`
	MaxDownloads  int        `json:"max_downloads"`
	DownloadCount int        `json:"download_count"`
	CreatedAt     time.Time  `json:"created_at"`
	CompletedAt   *time.Time `json:"completed_at,omitempty"`
	DownloadedAt  *time.Time `json:"downloaded_at"`
}

// CreateTransferResponse is returned by POST /api/v1/transfers.
type CreateTransferResponse struct {
	ID          string    `json:"id"`
	ExpiresAt   time.Time `json:"expires_at"`
	DeleteToken string    `json:"delete_token"`
}

// FileInfo describes a file (blob) belonging to a transfer.
type FileInfo struct {
	ID             string `json:"id"`
	Size           int64  `json:"size"`
	UploadOffset   int64  `json:"upload_offset"`
	UploadComplete bool   `json:"upload_complete"`
}

// SlotResponse is returned when querying a slot.
type SlotResponse struct {
	ID        string             `json:"id"`
	Status    string             `json:"status"`
	Transfers []SlotTransferInfo `json:"transfers"`
	ExpiresAt time.Time          `json:"expires_at"`
	CreatedAt time.Time          `json:"created_at"`
}

// SlotTransferInfo is a summary of a transfer linked to a slot.
type SlotTransferInfo struct {
	TransferID string `json:"transfer_id"`
	Status     string `json:"status"`
	FileCount  int    `json:"file_count"`
}

// CreateSlotResponse is returned by POST /api/v1/slots.
type CreateSlotResponse struct {
	ID          string    `json:"id"`
	ExpiresAt   time.Time `json:"expires_at"`
	DeleteToken string    `json:"delete_token"`
}

// ErrorResponse is a generic error body.
type ErrorResponse struct {
	Error string `json:"error"`
}
