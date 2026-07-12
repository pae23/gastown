package health

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	// BackupStaleAfter is how long a backup can go without any sync activity
	// before it is considered stale.
	BackupStaleAfter = 2 * time.Hour

	// minBackupSizeRatio is the fraction of the live database size a backup must
	// reach to be plausible. A dolt file remote stores the same chunks as the
	// live store, so a healthy backup lands in the same order of magnitude.
	// Anything below this floor is a truncated or half-written backup.
	minBackupSizeRatio = 0.10

	// backupHashMarker is written by the backup patrol with the HEAD hash it
	// last synced.
	backupHashMarker = ".last-backup-hash"
)

// ansiEscape matches SGR color sequences. `dolt log --oneline` colorizes the
// hash even when piped, so the marker file the backup patrol writes can contain
// escape codes (hq-hg40j7). Strip them before the value is compared or shown.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// BackupStatus is the result of inspecting one database's filesystem backup.
type BackupStatus struct {
	Name          string        `json:"name"`
	StorePath     string        `json:"store_path,omitempty"`
	SizeBytes     int64         `json:"size_bytes"`
	LiveSizeBytes int64         `json:"live_size_bytes,omitempty"`
	ChunkFiles    int           `json:"chunk_files"`
	HasManifest   bool          `json:"has_manifest"`
	HeadHash      string        `json:"head_hash,omitempty"`
	AgeSeconds    int           `json:"age_seconds"`
	Age           time.Duration `json:"-"`
	Problems      []string      `json:"problems,omitempty"`
}

// Healthy reports whether the backup passed every check.
func (b BackupStatus) Healthy() bool { return len(b.Problems) == 0 }

// InspectBackups verifies the CONTENT of every database backup under
// <townRoot>/.dolt-backup, not just directory mtimes.
//
// The backup patrol touches each database's backup directory on every cycle to
// signal liveness, so an mtime-only check reports an empty backup as fresh
// forever (hq-o40bdm: three months of GREEN over empty backups). Each backup is
// checked for a real dolt store — a manifest plus chunk files, of a size within
// range of the live database — and for sync activity within staleAfter.
//
// Returns one BackupStatus per database directory found. A missing or unreadable
// .dolt-backup directory returns nil: backups may simply not be configured.
func InspectBackups(townRoot string, staleAfter time.Duration) []BackupStatus {
	backupDir := filepath.Join(townRoot, ".dolt-backup")
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return nil
	}

	now := time.Now()
	var statuses []BackupStatus
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		statuses = append(statuses, inspectBackup(townRoot, filepath.Join(backupDir, entry.Name()), entry.Name(), now, staleAfter))
	}
	return statuses
}

func inspectBackup(townRoot, dbBackupDir, name string, now time.Time, staleAfter time.Duration) BackupStatus {
	st := BackupStatus{Name: name}

	// The backup patrol points the `file://` remote at <db>/<db>-backup, but
	// older towns synced straight into <db>. Accept either.
	store := filepath.Join(dbBackupDir, name+"-backup")
	if !hasManifest(store) {
		store = dbBackupDir
	}

	newest := modTime(dbBackupDir) // the patrol's liveness touch
	if hash, mtime, ok := readHashMarker(filepath.Join(dbBackupDir, backupHashMarker)); ok {
		st.HeadHash = hash
		newest = later(newest, mtime)
	}

	st.HasManifest = hasManifest(store)
	if st.HasManifest {
		st.StorePath = store
		st.SizeBytes, st.ChunkFiles, _ = walkStore(store)
		newest = later(newest, modTime(filepath.Join(store, "manifest")))
	}

	st.Age = now.Sub(newest)
	st.AgeSeconds = int(st.Age.Seconds())

	// Content checks. A backup with no dolt store is worthless no matter how
	// recently the patrol touched it.
	switch {
	case !st.HasManifest:
		st.Problems = append(st.Problems, "contains no dolt store (no manifest) — backup is empty")
	case st.ChunkFiles == 0:
		st.Problems = append(st.Problems, "has a manifest but no chunk files — backup holds no data")
	}

	// Size floor: a backup far smaller than the live database is truncated.
	st.LiveSizeBytes = liveStoreSize(townRoot, name)
	if st.HasManifest && st.ChunkFiles > 0 && st.LiveSizeBytes > 0 {
		if floor := int64(float64(st.LiveSizeBytes) * minBackupSizeRatio); st.SizeBytes < floor {
			st.Problems = append(st.Problems, fmt.Sprintf("is %s, only %.0f%% of the %s live database — backup looks truncated",
				humanBytes(st.SizeBytes), 100*float64(st.SizeBytes)/float64(st.LiveSizeBytes), humanBytes(st.LiveSizeBytes)))
		}
	}

	if newest.IsZero() {
		st.Problems = append(st.Problems, "has never been synced")
	} else if st.Age > staleAfter {
		st.Problems = append(st.Problems, fmt.Sprintf("has not synced in %s (threshold %s) — backup patrol may be stalled",
			st.Age.Round(time.Minute), staleAfter))
	}

	return st
}

// hasManifest reports whether dir looks like a dolt chunk store root.
func hasManifest(dir string) bool {
	info, err := os.Stat(filepath.Join(dir, "manifest"))
	return err == nil && !info.IsDir() && info.Size() > 0
}

// walkStore sums the size of a dolt chunk store and counts its chunk files
// (archive `.darc` files, or bare table files named by their content hash).
func walkStore(store string) (size int64, chunks int, newest time.Time) {
	_ = filepath.Walk(store, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil //nolint:nilerr // unreadable entries are skipped, not fatal
		}
		size += info.Size()
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		if isChunkFile(info.Name()) {
			chunks++
		}
		return nil
	})
	return size, chunks, newest
}

// isChunkFile reports whether a file in a dolt store holds table data. Dolt
// writes either archive files (`<hash>.darc`) or bare table files named by their
// 32-character base32 content hash. Bookkeeping files (manifest, LOCK) are not
// chunk data.
func isChunkFile(name string) bool {
	if strings.HasSuffix(name, ".darc") {
		return true
	}
	if len(name) != 32 || strings.ContainsAny(name, ".") {
		return false
	}
	for _, r := range name {
		if !strings.ContainsRune("0123456789abcdefghijklmnopqrstuv", r) {
			return false
		}
	}
	return true
}

// liveStoreSize returns the on-disk size of the live database's chunk store, or
// 0 if the database is not present locally.
func liveStoreSize(townRoot, db string) int64 {
	noms := filepath.Join(townRoot, ".dolt-data", db, ".dolt", "noms")
	if info, err := os.Stat(noms); err != nil || !info.IsDir() {
		return 0
	}
	size, _, _ := walkStore(noms)
	return size
}

// readHashMarker reads the HEAD hash the backup patrol last synced. The marker
// is written from `dolt log --oneline`, which emits ANSI color even when piped,
// so the raw file can read `\x1b[33m<hash>` — strip the escapes.
func readHashMarker(path string) (hash string, mtime time.Time, ok bool) {
	info, err := os.Stat(path)
	if err != nil {
		return "", time.Time{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", time.Time{}, false
	}
	hash = strings.TrimSpace(ansiEscape.ReplaceAllString(string(data), ""))
	if hash == "" {
		return "", info.ModTime(), false
	}
	return hash, info.ModTime(), true
}

func modTime(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func later(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(n)/float64(div), "KMGTPE"[exp])
}
