package store

import "errors"

// InventoryIdentity is a lossless, JSON-safe stat snapshot. Directory association
// uses device/inode/type; candidate content identity also uses all other fields.
type InventoryIdentity struct {
	Device    uint64 `json:"device"`
	Inode     uint64 `json:"inode"`
	Mode      uint32 `json:"mode"`
	Size      int64  `json:"size"`
	MTimeSec  int64  `json:"mtime_sec"`
	MTimeNsec int64  `json:"mtime_nsec"`
	CTimeSec  int64  `json:"ctime_sec"`
	CTimeNsec int64  `json:"ctime_nsec"`
}

// Directory is empty for a root child, or the exact transfer component. Names
// that cannot be payload components and unexpected file kinds are unsupported.
type InventoryEntry struct {
	Directory   string            `json:"directory"`
	Name        string            `json:"name"`
	Root        InventoryIdentity `json:"root"`
	Parent      InventoryIdentity `json:"parent"`
	File        InventoryIdentity `json:"file"`
	Unsupported bool              `json:"unsupported"`
}

// Cookie is a filesystem-provided continuation, never an entry count. Unstable
// is sticky for the entire pass: directory stamp drift does not rewind progress.
type InventoryCursor struct {
	Root      InventoryIdentity `json:"root"`
	Directory InventoryIdentity `json:"directory"`
	Cookie    int64             `json:"cookie"`
	Begun     bool              `json:"begun"`
	Unstable  bool              `json:"unstable"`
}

type InventoryPage struct {
	Entries  []InventoryEntry `json:"entries"`
	Next     InventoryCursor  `json:"next"`
	Done     bool             `json:"done"`
	Unstable bool             `json:"unstable"`
}

var (
	ErrInventoryChanged     = errors.New("storage inventory identity changed")
	ErrInventoryUnsupported = errors.New("storage inventory entry or continuation unsupported")
)
