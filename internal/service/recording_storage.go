package service

import (
	"context"
	"fmt"

	"github.com/OracleBetX-Projects/ex-chat/pkg/uploadpath"
)

// StageRecording writes an exclusive private object. The caller must finish the
// stage: keep it only after recording metadata is durably saved, or roll it back.
// Existing files are never removed/replaced, including the previous recording.
func (s *LocalStorageService) StageRecording(ctx context.Context, key string, data []byte) (string, func(bool) error, error) {
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if err := s.ScanVirus(ctx, data); err != nil {
		return "", nil, err
	}
	f, finish, err := uploadpath.CreateExclusive(s.baseDir, key)
	if err != nil {
		return "", nil, err
	}
	n, writeErr := f.Write(data)
	if writeErr == nil && n != len(data) {
		writeErr = fmt.Errorf("short recording write")
	}
	if writeErr == nil {
		writeErr = ctx.Err()
	}
	if writeErr == nil {
		writeErr = f.Sync()
	}
	closeErr := f.Close()
	if writeErr == nil {
		writeErr = closeErr
	}
	if writeErr != nil {
		cleanupErr := finish(false)
		if cleanupErr != nil {
			return "", nil, fmt.Errorf("recording write: %w; rollback: %v", writeErr, cleanupErr)
		}
		return "", nil, writeErr
	}
	return uploadpath.URL(key), finish, nil
}
