package dotsync

import (
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// statEntry remembers a file's signature together with the metadata it had when hashed.
type statEntry struct {
	Size  int64  `json:"s"`
	Mtime int64  `json:"m"`
	Ctime int64  `json:"c"`
	Ino   uint64 `json:"i"`
	Mode  uint32 `json:"o"`
	Sig   string `json:"h"`
}

// racyWindow: a file modified this recently may change again within the same timestamp tick
// without its metadata changing, so it is always re-read (the same safeguard git uses).
const racyWindow = 2 * time.Second

// statCache avoids re-reading and re-hashing files whose size, mtime, ctime, inode and mode are
// unchanged since the last sync. Any metadata change (including chmod or a rename over the file,
// which changes ctime/inode) forces a re-read.
type statCache struct {
	entries map[string]statEntry
	used    map[string]bool
	changed bool
	now     time.Time
}

func newStatCache(entries map[string]statEntry) *statCache {
	if entries == nil {
		entries = map[string]statEntry{}
	}
	return &statCache{entries: entries, used: map[string]bool{}, now: time.Now()}
}

func entryFor(fi os.FileInfo) (statEntry, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok || !fi.Mode().IsRegular() {
		return statEntry{}, false
	}
	return statEntry{
		Size:  fi.Size(),
		Mtime: fi.ModTime().UnixNano(),
		Ctime: ctimeNanos(st),
		Ino:   uint64(st.Ino), //nolint:unconvert // Ino is uint32 on some platforms
		Mode:  uint32(fi.Mode()),
	}, true
}

// obj is readObj, answered from the cache when the file is known to be unchanged. A cached
// answer has no Data; callers that need the content call readObj.
func (sc *statCache) obj(path string) (*Obj, error) {
	if sc == nil {
		return readObj(path)
	}
	path = filepath.Clean(path)
	fi, err := lstat(path)
	if fi == nil || err != nil {
		return nil, err
	}
	key, cacheable := entryFor(fi)
	if cacheable {
		if e, ok := sc.entries[path]; ok && sc.fresh(key) && e.Size == key.Size && e.Mtime == key.Mtime &&
			e.Ctime == key.Ctime && e.Ino == key.Ino && e.Mode == key.Mode {
			sc.used[path] = true
			return &Obj{Kind: kindFile, Exec: fi.Mode()&0o100 != 0, Sig: e.Sig}, nil
		}
	}
	o, err := readObj(path)
	if err != nil || o == nil || !cacheable || o.Kind != kindFile || !sc.fresh(key) {
		return o, err
	}
	key.Sig = o.Sig
	sc.entries[path] = key
	sc.used[path] = true
	sc.changed = true
	return o, nil
}

// fresh reports whether the file's timestamps are old enough to trust the cache.
func (sc *statCache) fresh(e statEntry) bool {
	limit := sc.now.Add(-racyWindow).UnixNano()
	return e.Mtime < limit && e.Ctime < limit
}

// snapshot returns the entries used in this run, dropping files that are no longer looked at.
func (sc *statCache) snapshot() map[string]statEntry {
	out := make(map[string]statEntry, len(sc.used))
	for p := range sc.used {
		out[p] = sc.entries[p]
	}
	return out
}
