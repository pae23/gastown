package health

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/steveyegge/gastown/internal/doltserver"
)

const (
	// BackupStaleAfter is how long a backup can go without any sync activity
	// before it is considered stale.
	BackupStaleAfter = 2 * time.Hour

	// minBackupSizeRatio is the fraction of the live database's COLLECTED size a
	// backup must reach to be plausible. A dolt file remote stores the same
	// collected chunks as the live store, so a healthy backup lands in the same
	// order of magnitude. Anything below this floor is a truncated or
	// half-written backup. The live chunk journal is excluded from the
	// comparison — see liveStoreSize.
	minBackupSizeRatio = 0.10

	// backupHashMarker is written by the backup patrol with the HEAD hash it
	// last synced.
	backupHashMarker = ".last-backup-hash"

	// doltJournalFile is the name dolt gives the chunk journal inside
	// .dolt/noms: a 32-character chunk-source name of all 'v's. journalIndexFile
	// is its companion index.
	doltJournalFile  = "vvvvvvvvvvvvvvvvvvvvvvvvvvvvvvvv"
	journalIndexFile = "journal.idx"
)

// ansiEscape matches SGR color sequences. `dolt log --oneline` colorizes the
// hash even when piped, so the marker file the backup patrol writes can contain
// escape codes (hq-hg40j7). Strip them before the value is compared or shown.
var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]`)

// Backup store layouts. The backup patrol points each database's `file://`
// remote at <db>/<db>-backup; towns that predate that layout synced straight
// into <db>.
const (
	LayoutCurrent = "current"
	LayoutLegacy  = "legacy"

	// LayoutMissing marks a live database with no backup directory at all.
	LayoutMissing = "missing"
)

// BackupStatus is the result of inspecting one database's filesystem backup.
type BackupStatus struct {
	Name          string `json:"name"`
	StorePath     string `json:"store_path,omitempty"`
	Layout        string `json:"layout,omitempty"`
	SizeBytes     int64  `json:"size_bytes"`
	LiveSizeBytes int64  `json:"live_size_bytes,omitempty"`
	// LiveCollectedBytes is the live store minus its uncompressed chunk
	// journal — the only part of the live size a backup's compressed store can
	// be compared against.
	LiveCollectedBytes int64         `json:"live_collected_bytes,omitempty"`
	ChunkFiles         int           `json:"chunk_files"`
	HasManifest        bool          `json:"has_manifest"`
	HeadHash           string        `json:"head_hash,omitempty"`
	AgeSeconds         int           `json:"age_seconds"`
	Age                time.Duration `json:"-"`
	Problems           []string      `json:"problems,omitempty"`
	Diagnostics        []string      `json:"diagnostics,omitempty"`
}

// Healthy reports whether the backup passed every check.
func (b BackupStatus) Healthy() bool { return len(b.Problems) == 0 }

// InspectBackups verifies that every live production database has a backup, and
// that the backup holds real content — not just a recently-touched directory.
//
// The set inspected is the LIVE databases in .dolt-data (the same set the backup
// patrol enumerates, test databases excluded), unioned with whatever backup
// directories exist. Enumerating only .dolt-backup made the most dangerous state
// invisible: a database created since the last backup cycle has no directory
// there, so it dropped out of the denominator entirely instead of going red
// (gt-drmn — health reported 39 databases verified while 42 were live). This is
// the only check that can see the create-to-first-backup window.
//
// Each backup is checked for a real dolt store — a manifest plus chunk files, of
// a size within range of the live database — and for sync activity within
// staleAfter. The backup patrol touches each directory every cycle to signal
// liveness, so an mtime-only check reports an empty backup as fresh forever
// (hq-o40bdm: three months of GREEN over empty backups).
//
// Returns one BackupStatus per database. A missing or unreadable .dolt-backup
// directory returns nil: backups are simply not configured, and every live
// database going red would be noise, not news. Once the root exists, backups ARE
// configured, and a live database with nothing under it is a real gap.
func InspectBackups(townRoot string, staleAfter time.Duration) []BackupStatus {
	backupDir := filepath.Join(townRoot, ".dolt-backup")
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return nil
	}

	var names []string
	seen := make(map[string]bool)
	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}

	// The live databases are the denominator: these are what a backup exists to
	// protect, whether or not one has been taken yet.
	live, err := doltserver.ProductionDatabases(townRoot)
	if err == nil {
		for _, name := range live {
			add(name)
		}
	}
	// Plus any backup with no live database behind it — an archived or dropped
	// database still gets its store verified.
	for _, entry := range entries {
		if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		add(entry.Name())
	}
	sort.Strings(names)

	now := time.Now()
	statuses := make([]BackupStatus, 0, len(names))
	for _, name := range names {
		statuses = append(statuses, inspectBackup(townRoot, filepath.Join(backupDir, name), name, now, staleAfter))
	}
	if len(statuses) == 0 {
		return nil
	}
	return statuses
}

func inspectBackup(townRoot, dbBackupDir, name string, now time.Time, staleAfter time.Duration) BackupStatus {
	st := BackupStatus{Name: name}

	// A live database with no directory at all under .dolt-backup has never been
	// backed up — the state between a database's creation and the patrol's next
	// cycle. Report it by name; do not age it (there is no sync to be stale
	// against, and a zero mtime would claim a fifty-year-old backup).
	if !isDir(dbBackupDir) {
		st.Layout = LayoutMissing
		st.StorePath = dbBackupDir
		st.LiveSizeBytes, st.LiveCollectedBytes = liveStoreSize(townRoot, name)
		st.Problems = append(st.Problems, "has no directory under .dolt-backup — the database has never been backed up")
		return st
	}

	// The backup patrol points the `file://` remote at <db>/<db>-backup, but
	// older towns synced straight into <db>. Pick the store by which directory
	// EXISTS, not by which one happens to hold a manifest: the presence of
	// <db>-backup means that is where the patrol writes, so an empty one is a
	// dead backup — and must not be papered over by a leftover legacy store
	// sitting next to it. Twelve town databases carry both (gt-1j3e), and
	// preferring "whichever has a manifest" reported a wiped backup as verified.
	store := filepath.Join(dbBackupDir, name+"-backup")
	st.Layout = LayoutCurrent
	if !isDir(store) {
		store = dbBackupDir
		st.Layout = LayoutLegacy
		st.Diagnostics = append(st.Diagnostics,
			fmt.Sprintf("no %s-backup directory — verifying the legacy store in the backup root (%s)", name, dbBackupDir))
	}
	st.StorePath = store

	newest := modTime(dbBackupDir) // the patrol's liveness touch
	if hash, mtime, ok := readHashMarker(filepath.Join(dbBackupDir, backupHashMarker)); ok {
		st.HeadHash = hash
		newest = later(newest, mtime)
	}

	st.HasManifest = hasManifest(store)
	if st.HasManifest {
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

	// Size floor: a backup far smaller than the live database is truncated. It
	// has to compare like with like. The live store keeps recent writes in an
	// UNCOMPRESSED chunk journal, while a backup holds only collected, compressed
	// chunks, so a journal-dominated database — freshly created, or write-heavy
	// and not yet collected into oldgen — makes a complete, current backup look
	// like a 6% stub (gt-l8tz). Only the collected bytes are comparable; when the
	// live store has none, the floor has nothing to say and stays quiet. An empty
	// or wiped backup is still caught by the content checks above, which do not
	// depend on size.
	st.LiveSizeBytes, st.LiveCollectedBytes = liveStoreSize(townRoot, name)
	switch {
	case !st.HasManifest || st.ChunkFiles == 0 || st.LiveSizeBytes == 0:
		// Nothing to compare: already reported, or the live db is not local.
	case st.LiveCollectedBytes == 0:
		st.Diagnostics = append(st.Diagnostics, fmt.Sprintf(
			"live store is %s of uncompressed chunk journal with nothing collected into oldgen — size floor not applicable",
			humanBytes(st.LiveSizeBytes)))
	case st.SizeBytes < int64(float64(st.LiveCollectedBytes)*minBackupSizeRatio):
		st.Problems = append(st.Problems, fmt.Sprintf("is %s, only %.0f%% of the %s collected in the live database — backup looks truncated",
			humanBytes(st.SizeBytes), 100*float64(st.SizeBytes)/float64(st.LiveCollectedBytes), humanBytes(st.LiveCollectedBytes)))
	}

	if newest.IsZero() {
		st.Problems = append(st.Problems, "has never been synced")
	} else if st.Age > staleAfter {
		st.Problems = append(st.Problems, fmt.Sprintf("has not synced in %s (threshold %s) — backup patrol may be stalled",
			st.Age.Round(time.Minute), staleAfter))
	}

	return st
}

// isDir reports whether path exists and is a directory.
func isDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
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

// liveStoreSize returns the on-disk size of the live database's chunk store and
// the collected part of it — the store minus its uncompressed chunk journal.
// Both are 0 if the database is not present locally.
//
// Only the collected bytes can be weighed against a backup: dolt appends new
// writes to the journal verbatim and only compresses them into chunk files when
// it collects them into oldgen, whereas a backup store holds compressed chunks
// exclusively. A database whose data still sits in the journal therefore dwarfs
// its own complete backup.
func liveStoreSize(townRoot, db string) (total, collected int64) {
	noms := filepath.Join(townRoot, ".dolt-data", db, ".dolt", "noms")
	if info, err := os.Stat(noms); err != nil || !info.IsDir() {
		return 0, 0
	}
	_ = filepath.Walk(noms, func(_ string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil //nolint:nilerr // unreadable entries are skipped, not fatal
		}
		total += info.Size()
		if isChunkFile(info.Name()) && !isJournalFile(info.Name()) {
			collected += info.Size()
		}
		return nil
	})
	return total, collected
}

// isJournalFile reports whether a file in a live dolt store is the chunk
// journal — the uncompressed write-ahead log — or its index. Note the journal's
// name is itself valid base32, so isChunkFile matches it too.
func isJournalFile(name string) bool {
	return name == doltJournalFile || name == journalIndexFile
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
