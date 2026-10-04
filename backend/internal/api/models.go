package api

import (
	"time"

	"github.com/endorses/psst.zip/backend/internal/database"
)

// --- Request types ---

// CreateTransferRequest is the body for POST /api/v1/transfers.
type CreateTransferRequest struct {
	ExpiresInSeconds int `json:"expires_in_seconds,omitempty"`
	MaxDownloads     int `json:"max_downloads,omitempty"`
}

// CreateSlotRequest is the body for POST /api/v1/slots.
type CreateSlotRequest struct {
	ReceiveProtocol    int    `json:"receive_protocol,omitempty"`
	RecipientPublicKey string `json:"recipient_public_key,omitempty"`
	MaxFiles           int    `json:"max_files,omitempty"`
	ExpiresInSeconds   int    `json:"expires_in_seconds,omitempty"`
}

// --- Response types ---

// TransferResponse is returned when querying a transfer.
type TransferResponse struct {
	Files         []FileInfo `json:"files"`
	OwnerID       string     `json:"owner_id,omitempty"`
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
	DownloadCount      int    `json:"download_count"`
	RemainingDownloads *int   `json:"remaining_downloads"`
	ID                 string `json:"id"`
	Size               int64  `json:"size"`
	UploadOffset       int64  `json:"upload_offset"`
	UploadComplete     bool   `json:"upload_complete"`
}

// SlotResponse is returned when querying a slot.
type SlotResponse struct {
	FileCount          *int64             `json:"file_count,omitempty"`
	ReceiveProtocol    int                `json:"receive_protocol"`
	RecipientPublicKey string             `json:"recipient_public_key"`
	MaxFiles           int                `json:"max_files"`
	ReservedFiles      int64              `json:"reserved_files"`
	CompletedFiles     int64              `json:"completed_files"`
	RemainingFiles     *int64             `json:"remaining_files"`
	TotalSize          *int64             `json:"total_size,omitempty"`
	OwnerID            string             `json:"owner_id,omitempty"`
	ID                 string             `json:"id"`
	Status             string             `json:"status"`
	Transfers          []SlotTransferInfo `json:"transfers"`
	ExpiresAt          time.Time          `json:"expires_at"`
	CreatedAt          time.Time          `json:"created_at"`
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

// SlotAvailability exposes submission policy, never private inbox history.
type SlotAvailability struct {
	UploadCapacity     database.GuestUploadCapacity `json:"upload_capacity"`
	ID                 string                       `json:"id"`
	Status             string                       `json:"status"`
	ExpiresAt          time.Time                    `json:"expires_at"`
	ReceiveProtocol    int                          `json:"receive_protocol"`
	RecipientPublicKey string                       `json:"recipient_public_key"`
	MaxFiles           int                          `json:"max_files"`
	RemainingFiles     *int64                       `json:"remaining_files"`
	RemainingBytes     int64                        `json:"remaining_bytes"`
	RemainingTransfers int                          `json:"remaining_transfers"`
	Available          bool                         `json:"available"`
}
