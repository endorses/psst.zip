package api

import (
	"github.com/google/uuid"

	"github.com/endorses/psst.zip/backend/internal/database"
	"github.com/endorses/psst.zip/backend/internal/tus"
)

// tusStore adapts database.Queries to the tus.Store interface.
type tusStore struct {
	queries     *database.Queries
	maxSlotSize int64
}

var _ tus.Store = (*tusStore)(nil)

func (s *tusStore) GetUpload(id string) (*tus.UploadInfo, error) {
	f, err := s.queries.GetFile(id)
	if err != nil {
		return nil, err
	}
	return &tus.UploadInfo{
		ID:         f.ID,
		Size:       f.Size,
		Offset:     f.UploadOffset,
		IsComplete: f.UploadComplete,
	}, nil
}

func (s *tusStore) CreateUpload(transferID string, size int64) (string, error) {
	id := uuid.New().String()
	if err := s.queries.CreateFileWithQuota(id, transferID, size, s.maxSlotSize); err != nil {
		if err == database.ErrSlotFileQuota {
			return "", tus.ErrUploadFileLimit
		}
		if err == database.ErrSlotQuota {
			return "", tus.ErrUploadLimit
		}
		return "", err
	}
	return id, nil
}

func (s *tusStore) UpdateOffset(id string, offset int64, complete bool) error {
	return s.queries.UpdateFileOffset(id, offset, complete)
}
