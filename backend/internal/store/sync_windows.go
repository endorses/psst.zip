package store

import "os"

func syncDiskFile(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	// FlushFileBuffers does not support directory handles on Windows. Payload
	// handles are flushed before offsets/metadata are committed, as on Unix.
	if info.IsDir() {
		return nil
	}
	return file.Sync()
}
