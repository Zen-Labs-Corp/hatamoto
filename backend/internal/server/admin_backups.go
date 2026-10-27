package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"

	"hatamoto.dev/core/internal/admin"
	"hatamoto.dev/core/internal/backup"
)

// adminBackups implements admin.BackupsService over the backup engine. The
// engine is built once by the caller (the same one the updater uses) and
// handed in through SetBackups, so there is one construction.
type adminBackups struct {
	eng    *backup.Engine
	base   func() context.Context // the server lifecycle context; nil reads as Background
	logger *slog.Logger
	wg     sync.WaitGroup // background manual backups
}

func newAdminBackups(eng *backup.Engine, logger *slog.Logger, base func() context.Context) *adminBackups {
	if logger == nil {
		logger = slog.Default()
	}
	return &adminBackups{eng: eng, base: base, logger: logger}
}

func (b *adminBackups) List(context.Context) (admin.BackupsState, error) {
	infos, err := b.eng.List()
	if err != nil {
		return admin.BackupsState{}, err
	}
	manual, preUpdate := backup.KeepFromEnv()
	st := admin.BackupsState{FreeBytes: b.eng.FreeDisk(), KeepManual: manual, KeepPreUpdate: preUpdate}
	for _, i := range infos { // the store lists newest first
		if i.State == backup.StateRunning {
			st.Running = true
		}
		st.Rows = append(st.Rows, backupInfo(i))
	}
	return st, nil
}

// Start takes the lock and writes the running record before it returns (so a
// second Start gets ErrBackupRunning at once), then does the work under the
// lifecycle context: never the request's, which ends when the page is served.
func (b *adminBackups) Start(_ context.Context, actor string) (string, error) {
	parent := context.Background()
	if b.base != nil {
		if c := b.base(); c != nil {
			parent = c
		}
	}
	info, work, err := b.eng.Begin(parent, backup.Request{Kind: backup.KindManual})
	if err != nil {
		if errors.Is(err, backup.ErrBusy) {
			return "", admin.ErrBackupRunning
		}
		return "", err
	}
	b.wg.Add(1)
	go func() {
		defer b.wg.Done()
		if _, err := work(parent); err != nil {
			b.logger.Error("manual backup failed", "id", info.ID, "actor", actor, "err", err)
		}
	}()
	return info.ID, nil
}

func (b *adminBackups) Open(_ context.Context, id string) (io.ReadSeekCloser, admin.BackupInfo, error) {
	f, info, err := b.eng.Open(id)
	if err != nil {
		return nil, admin.BackupInfo{}, mapBackupErr(err)
	}
	return f, backupInfo(info), nil
}

func (b *adminBackups) Delete(_ context.Context, id string) error {
	return mapBackupErr(b.eng.Delete(id))
}

// Wait blocks until background backups have ended or ctx is done. The server
// calls it after cancelling the lifecycle context.
func (b *adminBackups) Wait(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		b.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

func mapBackupErr(err error) error {
	switch {
	case errors.Is(err, backup.ErrNotFound):
		return admin.ErrBackupNotFound
	case errors.Is(err, backup.ErrRunning), errors.Is(err, backup.ErrBusy):
		return admin.ErrBackupRunning
	}
	return err
}

func backupInfo(i backup.Info) admin.BackupInfo {
	return admin.BackupInfo{
		ID: i.ID, Kind: string(i.Kind), Target: i.Target, State: string(i.State), Percent: i.Percent,
		Error: i.Error, Created: i.Created, Size: i.Size, Parts: i.Parts, Plugins: i.Plugins, MediaMissing: i.MediaMissing,
	}
}

// SetBackups hands the server the backup engine and the admin its service.
// The routes are mounted in New, before the engine exists; call it before the
// server starts serving.
func (s *Server) SetBackups(eng *backup.Engine) {
	s.backups = newAdminBackups(eng, s.logger, func() context.Context { return s.lifecycleCtx })
	if s.admin != nil {
		s.admin.SetBackups(s.backups)
	}
}
