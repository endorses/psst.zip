package protocol

import "time"

const (
	ChunkSize              = 4 * 1024 * 1024
	FrameOverhead          = 60
	MaxFileSize      int64 = 1 << 40
	MaxManifestBytes       = 1024 * 1024
	MaxFiles               = 100
)

type Manifest struct {
	Files []ManifestFile `json:"files"`
}
type ManifestFile struct {
	Name         string `json:"name"`
	Size         int64  `json:"size"`
	MIMEType     string `json:"mime_type"`
	BlobID       string `json:"blob_id"`
	Encoding     string `json:"encoding,omitempty"`
	ChunkSize    int    `json:"chunk_size,omitempty"`
	EncryptionID string `json:"encryption_id,omitempty"`
}
type User struct {
	ID                 string `json:"id"`
	Username           string `json:"username"`
	Role               string `json:"role"`
	Disabled           bool   `json:"disabled"`
	MustChangePassword bool   `json:"must_change_password"`
}
type Session struct {
	Token     string    `json:"token"`
	User      User      `json:"user"`
	SessionID string    `json:"session_id"`
	ExpiresAt time.Time `json:"expires_at"`
}

func (s Session) String() string { return "psst.zip device session [redacted]" }

type Pairing struct {
	Type      string `json:"type"`
	Version   int    `json:"version"`
	ServerURL string `json:"server_url"`
	Code      string `json:"code"`
}

func (p Pairing) String() string { return "psst.zip pairing [redacted]" }

type CreateTransferRequest struct {
	Title            *string `json:"title"`
	ExpiresInSeconds int     `json:"expires_in_seconds,omitempty"`
	MaxDownloads     int     `json:"max_downloads,omitempty"`
}
type CreatedTransfer struct {
	// ServerTime is the HTTP response Date (or local receipt time if absent); it is never part of the wire JSON.
	ServerTime  time.Time `json:"-"`
	Title       *string   `json:"title"`
	ID          string    `json:"id"`
	ExpiresAt   time.Time `json:"expires_at"`
	DeleteToken string    `json:"delete_token"`
}

func (c CreatedTransfer) String() string { return "psst.zip allocation " + c.ID + " [redacted]" }

type FileInfo struct {
	ID                 string `json:"id"`
	Size               int64  `json:"size"`
	UploadOffset       int64  `json:"upload_offset"`
	UploadComplete     bool   `json:"upload_complete"`
	DownloadCount      int    `json:"download_count"`
	RemainingDownloads *int   `json:"remaining_downloads"`
}
type Transfer struct {
	ID             string     `json:"id"`
	Title          *string    `json:"title"`
	Status         string     `json:"status"`
	Files          []FileInfo `json:"files"`
	FileCount      int        `json:"file_count"`
	TotalSize      int64      `json:"total_size"`
	HasManifest    bool       `json:"has_manifest"`
	ExpiresAt      time.Time  `json:"expires_at"`
	CreatedAt      time.Time  `json:"created_at"`
	MaxDownloads   int        `json:"max_downloads"`
	DownloadCount  int        `json:"download_count"`
	DownloadedAt   *time.Time `json:"downloaded_at"`
	InactiveReason *string    `json:"inactive_reason,omitempty"`
}
type CreateReceiveRequest struct {
	Title              *string `json:"title"`
	ReceiveProtocol    int     `json:"receive_protocol"`
	RecipientPublicKey string  `json:"recipient_public_key"`
	MaxFiles           int     `json:"max_files,omitempty"`
	ExpiresInSeconds   int     `json:"expires_in_seconds,omitempty"`
}
type SlotTransfer struct {
	TransferID string `json:"transfer_id"`
	Status     string `json:"status"`
	FileCount  int    `json:"file_count"`
}
type Slot struct {
	ID                 string         `json:"id"`
	Title              *string        `json:"title"`
	ReceiveProtocol    int            `json:"receive_protocol"`
	RecipientPublicKey string         `json:"recipient_public_key"`
	Status             string         `json:"status"`
	Transfers          []SlotTransfer `json:"transfers"`
	ExpiresAt          time.Time      `json:"expires_at"`
	CreatedAt          time.Time      `json:"created_at"`
	MaxFiles           int            `json:"max_files"`
	RemainingFiles     *int64         `json:"remaining_files"`
	TotalSize          *int64         `json:"total_size,omitempty"`
}
type InboxPage struct {
	Slot
	Paginated  bool    `json:"paginated"`
	NextCursor *string `json:"next_cursor"`
}
type HistoryPage struct {
	Paginated  bool       `json:"paginated"`
	Transfers  []Transfer `json:"transfers"`
	Slots      []Slot     `json:"slots"`
	NextCursor *string    `json:"next_cursor"`
	SyncCursor string     `json:"sync_cursor"`
	Generation string     `json:"generation"`
}
type Membership struct {
	SlotID             string `json:"slot_id"`
	TransferID         string `json:"transfer_id"`
	ReceiveProtocol    int    `json:"receive_protocol"`
	RecipientPublicKey string `json:"recipient_public_key"`
}
type Availability struct {
	ServerTime         time.Time           `json:"-"`
	ID                 string              `json:"id"`
	Title              *string             `json:"title"`
	Status             string              `json:"status"`
	Available          bool                `json:"available"`
	ReceiveProtocol    int                 `json:"receive_protocol"`
	RecipientPublicKey string              `json:"recipient_public_key"`
	ExpiresAt          time.Time           `json:"expires_at"`
	RemainingFiles     *int64              `json:"remaining_files"`
	RemainingBytes     int64               `json:"remaining_bytes"`
	RemainingTransfers int                 `json:"remaining_transfers"`
	UploadCapacity     GuestUploadCapacity `json:"upload_capacity"`
}
type GuestUploadCapacity struct {
	State                string    `json:"state"`
	Reason               string    `json:"reason"`
	AvailableWireBytes   *int64    `json:"available_wire_bytes"`
	AvailableFiles       *int64    `json:"available_files"`
	ManifestReserveBytes int64     `json:"manifest_reserve_bytes"`
	CheckedAt            time.Time `json:"checked_at"`
}
type Config struct {
	MaxFileSize           int64 `json:"max_file_size"`
	PublicTransfersPaused bool  `json:"public_transfers_paused"`
	ResourcePolicy        struct {
		MaxRetentionSeconds int64 `json:"max_retention_seconds"`
	} `json:"resource_policy"`
}
