package dotsync

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"
)

// Backup runs are directories named <YYYYMMDD-HHMMSS>-<pid> under Paths.Backups.
const backupRunLayout = "20060102-150405"

var backupSuffix = regexp.MustCompile(`\.~\d+~$`)

type backupRun struct {
	dir  string
	time time.Time
}

func listBackupRuns(root string) ([]backupRun, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		if notExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var runs []backupRun
	for _, e := range entries {
		if !e.IsDir() || len(e.Name()) < len(backupRunLayout) {
			continue
		}
		t, err := time.ParseInLocation(backupRunLayout, e.Name()[:len(backupRunLayout)], time.Local)
		if err != nil {
			continue
		}
		runs = append(runs, backupRun{filepath.Join(root, e.Name()), t})
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].time.After(runs[j].time) }) // newest first
	return runs, nil
}

// pruneBackups deletes backups taken before now-keep, except the most recent backup of each
// file, so a file's last known previous version is never lost. It returns the number of files
// removed.
func pruneBackups(root string, keep time.Duration, now time.Time) (int, error) {
	runs, err := listBackupRuns(root)
	if err != nil {
		return 0, err
	}
	cutoff := now.Add(-keep)
	seen := map[string]bool{}
	removed := 0
	for _, run := range runs {
		old := run.time.Before(cutoff)
		var files []string
		err := filepath.WalkDir(run.dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() {
				files = append(files, p)
			}
			return nil
		})
		if err != nil {
			return removed, err
		}
		sort.Sort(sort.Reverse(sort.StringSlice(files))) // "x.~2~" before "x.~1~" before "x"
		for _, p := range files {
			rel, _ := filepath.Rel(run.dir, p)
			key := backupSuffix.ReplaceAllString(rel, "")
			if seen[key] && old {
				if err := os.Remove(p); err != nil {
					return removed, err
				}
				removed++
			}
			seen[key] = true
		}
		if old {
			removeEmptyDirs(run.dir)
		}
	}
	return removed, nil
}

// removeEmptyDirs removes empty directories below and including dir.
func removeEmptyDirs(dir string) {
	var dirs []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	for i := len(dirs) - 1; i >= 0; i-- {
		_ = os.Remove(dirs[i]) // fails, harmlessly, unless empty
	}
}

// backupUsage reports the number of files and bytes kept in backups.
func backupUsage(root string) (files int, bytes int64) {
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				files++
				bytes += fi.Size()
			}
		}
		return nil
	})
	return files, bytes
}
