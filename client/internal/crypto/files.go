package crypto

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/binary"
	"errors"
	"io"

	"github.com/endorses/psst.zip/client/protocol"
)

// EncryptFile emits chunked-v1 frames using bounded working buffers. It requires
// exactly file.Size plaintext bytes; callers must not publish an incomplete
// upload when this function returns an error. Readers backed by blocking IO must
// themselves support cancellation (for example an HTTP body bound to ctx).
func EncryptFile(ctx context.Context, w io.Writer, r io.Reader, key []byte, file protocol.ManifestFile) error {
	id, err := fileContext(file)
	if err != nil {
		return err
	}
	aead, err := newGCM(key)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	plain := make([]byte, 32+min(int64(ChunkSize), file.Size))
	defer clear(plain)
	frame := make([]byte, nonceSize, nonceSize+len(plain)+aead.Overhead())
	for index := int64(0); index < frameCount(file.Size); index++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		size := min(int64(ChunkSize), file.Size-index*ChunkSize)
		part := plain[:32+int(size)]
		copy(part[:16], id)
		binary.BigEndian.PutUint64(part[16:24], uint64(index))
		binary.BigEndian.PutUint64(part[24:32], uint64(file.Size))
		if err := readExact(ctx, r, part[32:]); err != nil {
			return err
		}
		if _, err := io.ReadFull(rand.Reader, frame[:nonceSize]); err != nil {
			return errors.New("secure nonce generation failed")
		}
		frame = aead.Seal(frame[:nonceSize], frame[:nonceSize], part, nil)
		if err := writeAll(ctx, w, frame); err != nil {
			return err
		}
	}
	return requireEOF(ctx, r)
}

// DecryptFile authenticates and writes individual frames, then verifies EOF.
// The writer MUST be an unpublished temporary destination: a later frame or the
// final boundary can still fail after earlier authenticated plaintext was written.
func DecryptFile(ctx context.Context, w io.Writer, r io.Reader, key []byte, file protocol.ManifestFile) error {
	id, err := fileContext(file)
	if err != nil {
		return err
	}
	aead, err := newGCM(key)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	frame := make([]byte, FrameOverhead+min(int64(ChunkSize), file.Size))
	defer clear(frame)
	for index := int64(0); index < frameCount(file.Size); index++ {
		size := min(int64(ChunkSize), file.Size-index*ChunkSize)
		part := frame[:int(size)+FrameOverhead]
		if err := readExact(ctx, r, part); err != nil {
			return err
		}
		plain, err := aead.Open(part[nonceSize:nonceSize], part[:nonceSize], part[nonceSize:], nil)
		if err != nil {
			return invalid("file authentication failed")
		}
		if len(plain) != 32+int(size) || subtle.ConstantTimeCompare(plain[:16], id) != 1 ||
			binary.BigEndian.Uint64(plain[16:24]) != uint64(index) ||
			binary.BigEndian.Uint64(plain[24:32]) != uint64(file.Size) {
			return invalid("file frame identity mismatch")
		}
		if err := writeAll(ctx, w, plain[32:]); err != nil {
			return err
		}
	}
	return requireEOF(ctx, r)
}

func readExact(ctx context.Context, r io.Reader, dst []byte) error {
	empty := 0
	for len(dst) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := r.Read(dst)
		if n < 0 || n > len(dst) {
			return io.ErrShortBuffer
		}
		dst = dst[n:]
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		if err != nil {
			if len(dst) == 0 && err == io.EOF {
				return nil
			}
			if err == io.EOF {
				return invalid("file stream is incomplete")
			}
			return err
		}
		if n == 0 {
			empty++
			if empty >= 100 {
				return io.ErrNoProgress
			}
		} else {
			empty = 0
		}
	}
	return nil
}

func requireEOF(ctx context.Context, r io.Reader) error {
	var probe [1]byte
	for i := 0; i < 100; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := r.Read(probe[:])
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		if n != 0 {
			return invalid("file stream has trailing data")
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
	return io.ErrNoProgress
}

func writeAll(ctx context.Context, w io.Writer, data []byte) error {
	for len(data) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := w.Write(data)
		if n < 0 || n > len(data) {
			return io.ErrShortWrite
		}
		data = data[n:]
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return ctx.Err()
}
