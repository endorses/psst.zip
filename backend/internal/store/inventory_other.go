//go:build !linux

package store

import "context"

// Durable directory continuation is only implemented for Linux. Other systems
// retain explicit incomplete coverage rather than silently skipping inventory.
func (d *DiskStore) InventoryDirectory(ctx context.Context, directory string, cursor InventoryCursor, maximum int) (InventoryPage, error) {
	if err := ctx.Err(); err != nil {
		return InventoryPage{}, err
	}
	return InventoryPage{}, ErrInventoryUnsupported
}

func (d *DiskStore) RemoveOrphan(ctx context.Context, expected InventoryEntry) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	return false, ErrInventoryUnsupported
}
