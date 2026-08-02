package eventprocessor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/klauspost/compress/zstd"

	"github.com/Relayward/relayward/internal/eventstore"
)

const defaultArchiveInterval = time.Minute

type ArchiveOptions struct {
	Directory        string
	HotRetention     time.Duration
	ArchiveRetention time.Duration
}

type Archiver struct {
	events           *eventstore.Store
	logger           *slog.Logger
	directory        string
	hotRetention     time.Duration
	archiveRetention time.Duration
	interval         time.Duration
	now              func() time.Time
}

func NewArchiver(events *eventstore.Store, logger *slog.Logger, options ArchiveOptions) (*Archiver, error) {
	if events == nil {
		return nil, errors.New("event store is required")
	}
	if !filepath.IsAbs(options.Directory) || filepath.Clean(options.Directory) != options.Directory {
		return nil, errors.New("event archive directory must be absolute and clean")
	}
	if options.HotRetention < time.Hour {
		return nil, errors.New("event hot retention must be at least one hour")
	}
	if options.ArchiveRetention < 24*time.Hour || options.ArchiveRetention < options.HotRetention {
		return nil, errors.New("event archive retention must be at least one day and not shorter than hot retention")
	}
	if err := os.MkdirAll(options.Directory, 0o700); err != nil {
		return nil, fmt.Errorf("create event archive directory: %w", err)
	}
	if err := os.Chmod(options.Directory, 0o700); err != nil {
		return nil, fmt.Errorf("protect event archive directory: %w", err)
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Archiver{
		events: events, logger: logger, directory: options.Directory,
		hotRetention: options.HotRetention, archiveRetention: options.ArchiveRetention,
		interval: defaultArchiveInterval,
		now:      func() time.Time { return time.Now().UTC().Truncate(time.Second) },
	}, nil
}

func (archiver *Archiver) Run(ctx context.Context) error {
	archiver.runCycle(ctx)
	ticker := time.NewTicker(archiver.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			archiver.runCycle(ctx)
		}
	}
}

func (archiver *Archiver) RunOnce(ctx context.Context) error {
	now := archiver.now()
	if err := archiver.writeClosedDays(ctx, now); err != nil {
		return err
	}
	if _, _, err := archiver.events.PruneHotData(ctx, now.Add(-archiver.hotRetention), ConsumerIDs); err != nil {
		return err
	}
	return archiver.expireArchives(ctx, now)
}

func (archiver *Archiver) runCycle(ctx context.Context) {
	if err := archiver.RunOnce(ctx); err != nil && ctx.Err() == nil {
		archiver.logger.Warn("event archive cycle failed", "error", err)
	}
}

func (archiver *Archiver) writeClosedDays(ctx context.Context, now time.Time) error {
	today := now.UTC().Format(time.DateOnly)
	days, err := archiver.events.PendingAccessArchiveDays(ctx, today)
	if err != nil {
		return err
	}
	for _, day := range days {
		if err := archiver.writeDay(ctx, day, now); err != nil {
			return fmt.Errorf("archive access day %s: %w", day.Day, err)
		}
	}
	return nil
}

func (archiver *Archiver) writeDay(ctx context.Context, candidate eventstore.ArchiveCandidate, now time.Time) error {
	parsedDay, err := time.Parse(time.DateOnly, candidate.Day)
	if err != nil {
		return errors.New("invalid access archive day")
	}
	relative := filepath.Join("access", parsedDay.Format("2006"), parsedDay.Format("01"), candidate.Day+".jsonl.zst")
	destination, err := archiver.archivePath(relative)
	if err != nil {
		return err
	}
	backup := destination + ".previous"
	directory := filepath.Dir(destination)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create access archive day directory: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return fmt.Errorf("protect access archive day directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".access-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary access archive: %w", err)
	}
	temporaryPath := temporary.Name()
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return fmt.Errorf("protect temporary access archive: %w", err)
	}
	digest := sha256.New()
	compressed, err := zstd.NewWriter(io.MultiWriter(temporary, digest),
		zstd.WithEncoderConcurrency(1), zstd.WithEncoderLevel(zstd.SpeedBetterCompression))
	if err != nil {
		temporary.Close()
		return fmt.Errorf("create Zstandard access archive: %w", err)
	}
	encoder := json.NewEncoder(compressed)
	count := int64(0)
	maximumID := int64(0)
	if candidate.ArchivedEventCount > 0 {
		if candidate.ArchivedPath != filepath.ToSlash(relative) || candidate.ArchivedMaxID < 1 || len(candidate.ArchivedSHA256) != 64 {
			compressed.Close()
			temporary.Close()
			return errors.New("invalid existing access archive metadata")
		}
		if err := restoreMatchingArchive(destination, backup, candidate.ArchivedSHA256); err != nil {
			compressed.Close()
			temporary.Close()
			return err
		}
		count, maximumID, err = copyAccessArchive(destination, encoder)
		if err != nil {
			compressed.Close()
			temporary.Close()
			return err
		}
		if count != candidate.ArchivedEventCount || maximumID != candidate.ArchivedMaxID {
			compressed.Close()
			temporary.Close()
			return errors.New("existing access archive does not match its metadata")
		}
	}
	addedCount, addedMaximumID, visitErr := archiver.events.VisitAccessEventsForDayAfterID(ctx, candidate.Day, candidate.ArchivedMaxID, func(value eventstore.AccessRecord) error {
		if err := encoder.Encode(value); err != nil {
			return fmt.Errorf("encode access archive record: %w", err)
		}
		return nil
	})
	closeCompressedErr := compressed.Close()
	if visitErr != nil {
		temporary.Close()
		return visitErr
	}
	if closeCompressedErr != nil {
		temporary.Close()
		return fmt.Errorf("finish Zstandard access archive: %w", closeCompressedErr)
	}
	count += addedCount
	if addedMaximumID > maximumID {
		maximumID = addedMaximumID
	}
	if count != candidate.EventCount || maximumID != candidate.MaxAccessID {
		temporary.Close()
		return errors.New("access archive source changed while being written")
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return fmt.Errorf("sync temporary access archive: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary access archive: %w", err)
	}
	hadExisting := candidate.ArchivedEventCount > 0
	if hadExisting {
		if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove stale access archive backup: %w", err)
		}
		if err := os.Rename(destination, backup); err != nil {
			return fmt.Errorf("preserve previous access archive: %w", err)
		}
	}
	if err := os.Rename(temporaryPath, destination); err != nil {
		if hadExisting {
			_ = os.Rename(backup, destination)
		}
		return fmt.Errorf("publish access archive: %w", err)
	}
	removeTemporary = false
	if err := syncDirectory(directory); err != nil {
		return err
	}
	if err := archiver.events.RecordAccessArchive(ctx, eventstore.ArchiveDay{
		Day: candidate.Day, RelativePath: filepath.ToSlash(relative), EventCount: count,
		MaxAccessID: maximumID, SHA256: hex.EncodeToString(digest.Sum(nil)), CompletedAt: now,
	}); err != nil {
		_ = os.Remove(destination)
		if hadExisting {
			_ = os.Rename(backup, destination)
		}
		_ = syncDirectory(directory)
		return err
	}
	if hadExisting {
		if err := os.Remove(backup); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove previous access archive: %w", err)
		}
		if err := syncDirectory(directory); err != nil {
			return err
		}
	}
	return nil
}

func restoreMatchingArchive(destination, backup, expectedSHA256 string) error {
	for _, path := range []string{destination, backup} {
		digest, err := fileSHA256(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if digest != expectedSHA256 {
			continue
		}
		if path == backup {
			if err := os.Remove(destination); err != nil && !errors.Is(err, os.ErrNotExist) {
				return fmt.Errorf("remove incomplete access archive: %w", err)
			}
			if err := os.Rename(backup, destination); err != nil {
				return fmt.Errorf("restore previous access archive: %w", err)
			}
			if err := syncDirectory(filepath.Dir(destination)); err != nil {
				return err
			}
		}
		return nil
	}
	return errors.New("existing access archive checksum does not match metadata")
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", fmt.Errorf("hash access archive: %w", err)
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func copyAccessArchive(path string, encoder *json.Encoder) (int64, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, fmt.Errorf("open existing access archive: %w", err)
	}
	defer file.Close()
	compressed, err := zstd.NewReader(file)
	if err != nil {
		return 0, 0, fmt.Errorf("open existing Zstandard access archive: %w", err)
	}
	defer compressed.Close()
	decoder := json.NewDecoder(compressed)
	var count, maximumID int64
	for {
		var value eventstore.AccessRecord
		if err := decoder.Decode(&value); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return 0, 0, fmt.Errorf("decode existing access archive: %w", err)
		}
		if value.ID <= maximumID {
			return 0, 0, errors.New("existing access archive records are not strictly ordered")
		}
		if err := encoder.Encode(value); err != nil {
			return 0, 0, fmt.Errorf("copy existing access archive record: %w", err)
		}
		count++
		maximumID = value.ID
	}
	return count, maximumID, nil
}

func (archiver *Archiver) expireArchives(ctx context.Context, now time.Time) error {
	beforeDay := now.UTC().Add(-archiver.archiveRetention).Format(time.DateOnly)
	archives, err := archiver.events.AccessArchivesBefore(ctx, beforeDay)
	if err != nil {
		return err
	}
	for _, archive := range archives {
		path, err := archiver.archivePath(filepath.FromSlash(archive.RelativePath))
		if err != nil {
			return err
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove expired access archive: %w", err)
		}
		if err := archiver.events.DeleteAccessArchive(ctx, archive.Day); err != nil {
			return fmt.Errorf("delete expired access archive metadata: %w", err)
		}
	}
	return nil
}

func (archiver *Archiver) archivePath(relative string) (string, error) {
	path := filepath.Join(archiver.directory, relative)
	clean := filepath.Clean(path)
	prefix := archiver.directory + string(filepath.Separator)
	if clean == archiver.directory || !strings.HasPrefix(clean, prefix) {
		return "", errors.New("access archive path escapes archive directory")
	}
	return clean, nil
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open access archive directory for sync: %w", err)
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("sync access archive directory: %w", err)
	}
	return nil
}
