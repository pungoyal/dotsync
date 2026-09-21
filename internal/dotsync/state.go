package dotsync

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"time"
)

const defaultInterval = 300

// Config is this machine's private configuration (~/.config/dotsync/config.json).
type Config struct {
	Remote   string   `json:"remote"`
	Branch   string   `json:"branch"`
	Interval int      `json:"interval"`
	Exclude  []string `json:"exclude"` // manifest sources not managed on this machine
	// OnExisting decides what happens when a file becomes managed here while a different local
	// file already exists: "remote" (default) backs it up and installs the shared version,
	// "conflict" leaves it untouched and reports a conflict.
	OnExisting string `json:"on_existing,omitempty"`
	// BackupRetentionDays: backups older than this are deleted, except the most recent copy of
	// each file. nil means the default (90); 0 keeps everything.
	BackupRetentionDays *int `json:"backup_retention_days,omitempty"`
	// Notifications: false turns off the background agent's desktop notifications. nil means on.
	Notifications *bool `json:"notifications,omitempty"`
}

func (c *Config) notificationsOn() bool {
	return c == nil || c.Notifications == nil || *c.Notifications
}

const defaultBackupRetentionDays = 90

func (c *Config) backupRetention() int {
	if c.BackupRetentionDays == nil {
		return defaultBackupRetentionDays
	}
	return *c.BackupRetentionDays
}

type EntryState struct {
	Target string            `json:"target"`
	Kind   string            `json:"kind"`
	Items  map[string]string `json:"items"` // relative path -> base signature
}

type ConflictInfo struct {
	Target string `json:"target"`
	Since  string `json:"since"`
}

type SyncSummary struct {
	Time       string         `json:"time"`
	Outcome    string         `json:"outcome"` // ok, partial, offline
	Online     bool           `json:"online"`
	FetchError string         `json:"fetch_error,omitempty"`
	Problem    string         `json:"problem,omitempty"` // a git failure the user needs to fix
	Fix        string         `json:"fix,omitempty"`
	Commit     string         `json:"commit,omitempty"`
	Counts     map[string]int `json:"counts"`
	Errors     []string       `json:"errors"`
}

// State is this machine's private record of what it last agreed on with the remote.
type State struct {
	Version    int                     `json:"version"`
	Entries    map[string]*EntryState  `json:"entries"`
	PendingOps []*Op                   `json:"pending_ops"`
	Conflicts  map[string]ConflictInfo `json:"conflicts"`
	LastSync   *SyncSummary            `json:"last_sync"`
	// Recovered is set when the state file had to be discarded. Until the next sync has
	// re-established a base for every entry, differing files become conflicts rather than
	// being replaced by the remote version.
	Recovered bool `json:"recovered,omitempty"`
	// StatCache lets unchanged files skip re-reading and hashing (see statcache.go).
	StatCache map[string]statEntry `json:"stat_cache,omitempty"`
	// Housekeeping runs on a fixed schedule, never at random: backups are pruned at most once a
	// day and the cache clone is garbage-collected once a week.
	LastBackupPrune string `json:"last_backup_prune,omitempty"`
	LastGC          string `json:"last_gc,omitempty"`
	LastReset       string `json:"last_reset,omitempty"` // the cache clone is fully reset at least daily
	// RepoConfig is the version of the git settings applied to the cache clone.
	RepoConfig int `json:"repo_config,omitempty"`

	raw      []byte       // file contents as loaded, to skip rewriting an unchanged state
	prevSync *SyncSummary // last_sync as loaded
}

func (s *State) entry(e *Entry) *EntryState {
	es := s.Entries[e.Source]
	if es == nil {
		es = &EntryState{Target: e.TargetSpec, Kind: e.Kind, Items: map[string]string{}}
		s.Entries[e.Source] = es
	}
	return es
}

func setBase(es *EntryState, rel, sig string) {
	if sig == "" {
		delete(es.Items, rel)
	} else {
		es.Items[rel] = sig
	}
}

func loadState(c *Ctx) (*State, error) {
	s := &State{}
	data, err := os.ReadFile(c.Paths.State)
	defer func() {
		if s != nil {
			s.raw = data
			if s.LastSync != nil {
				prev := *s.LastSync
				s.prevSync = &prev
			}
		}
	}()
	if err == nil {
		if jerr := json.Unmarshal(data, s); jerr != nil {
			// Losing the base is safe: with Recovered set, every file that differs from the remote
			// becomes a conflict instead of being replaced.
			bad := c.Paths.State + ".corrupt-" + time.Now().Format("20060102150405")
			if rerr := os.Rename(c.Paths.State, bad); rerr != nil {
				return nil, rerr
			}
			fmt.Fprintf(c.Err, "dotsync: warning: state file was unreadable, moved to %s\n", bad)
			s = &State{Recovered: true}
		}
	} else if !notExist(err) {
		return nil, err
	}
	if s.Version == 0 {
		s.Version = 1
	}
	if s.Entries == nil {
		s.Entries = map[string]*EntryState{}
	}
	for _, es := range s.Entries {
		if es.Items == nil {
			es.Items = map[string]string{}
		}
	}
	if s.Conflicts == nil {
		s.Conflicts = map[string]ConflictInfo{}
	}
	return s, nil
}

func writeJSON(path string, v any, perm os.FileMode) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(path, append(data, '\n'), perm)
}

func saveState(c *Ctx, s *State) error { return writeJSON(c.Paths.State, s, 0o600) }

// saveStateIfChanged writes the state only when something other than the sync time changed.
// Otherwise it just touches the file's modification time, which records when the last sync ran
// (see lastSyncTime) without writing any data.
func saveStateIfChanged(c *Ctx, s *State) error {
	if prev, cur := s.prevSync, s.LastSync; prev != nil && cur != nil && sameSummary(*prev, *cur) {
		cur.Time = prev.Time
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if bytes.Equal(data, s.raw) {
		now := time.Now()
		return os.Chtimes(c.Paths.State, now, now)
	}
	if err := atomicWrite(c.Paths.State, data, 0o600); err != nil {
		return err
	}
	s.raw = data
	return nil
}

func sameSummary(a, b SyncSummary) bool {
	a.Time, b.Time = "", ""
	return reflect.DeepEqual(a, b)
}

// lastSyncTime is when the last sync ran: the recorded time, or the state file's modification
// time if a later sync changed nothing (and so only touched the file).
func lastSyncTime(c *Ctx, s *State) time.Time {
	var t time.Time
	if s.LastSync != nil {
		t, _ = time.Parse(time.RFC3339, s.LastSync.Time)
	}
	if fi, err := os.Stat(c.Paths.State); err == nil && fi.ModTime().After(t) {
		t = fi.ModTime()
	}
	return t
}

// ErrBusy means another dotsync process holds the lock.
var ErrBusy = errors.New("another dotsync process is running")

type Lock struct{ f *os.File }

func acquireLock(path string, wait time.Duration) (*Lock, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	for {
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			return &Lock{f}, nil
		}
		if time.Now().After(deadline) {
			f.Close()
			return nil, ErrBusy
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (l *Lock) Release() {
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	l.f.Close()
}

// Backups holds copies of local files taken before dotsync replaces or deletes them, one
// directory per sync run, mirroring paths relative to $HOME.
type Backups struct {
	paths *Paths
	root  string
}

func (b *Backups) save(path string) (string, error) {
	if fi, err := lstat(path); fi == nil || err != nil {
		return "", err // nothing to back up, or a real error: never replace what we couldn't copy
	}
	if b.root == "" {
		b.root = filepath.Join(b.paths.Backups, fmt.Sprintf("%s-%d", time.Now().Format("20060102-150405"), os.Getpid()))
	}
	dest := filepath.Join(b.root, filepath.FromSlash(homeRel(path)))
	// A retried pass may back up the same path twice; never overwrite an earlier copy.
	for n := 1; ; n++ {
		if _, err := os.Lstat(dest); notExist(err) {
			break
		}
		dest = filepath.Join(b.root, filepath.FromSlash(homeRel(path))) + fmt.Sprintf(".~%d~", n)
	}
	if err := copyToBackup(path, dest); err != nil {
		return "", fmt.Errorf("could not back up %s, leaving it alone: %w", tilde(path), err)
	}
	return dest, nil
}

// Ctx carries everything a command needs.
type Ctx struct {
	cache  *statCache // set by buildPlan
	Paths  *Paths
	Config *Config
	Git    *Git
	Quiet  bool
	Out    io.Writer
	Err    io.Writer
}

var errNotInitialized = errors.New("not set up on this machine; run: dotsync init <git-remote-url>")

func newCtx(needInit, quiet bool, out, errw io.Writer) (*Ctx, error) {
	p := NewPaths()
	c := &Ctx{Paths: p, Git: &Git{Repo: p.Repo}, Quiet: quiet, Out: out, Err: errw}
	data, err := os.ReadFile(p.Config)
	if err == nil {
		c.Config = &Config{}
		if err := json.Unmarshal(data, c.Config); err != nil {
			return nil, fmt.Errorf("%s is not valid JSON: %w", tilde(p.Config), err)
		}
		if c.Config.Branch == "" {
			c.Config.Branch = "main"
		}
	} else if !notExist(err) {
		return nil, err
	}
	if needInit {
		if _, err := os.Stat(filepath.Join(p.Repo, ".git")); c.Config == nil || err != nil {
			return nil, errNotInitialized
		}
	}
	return c, nil
}

func (c *Ctx) say(format string, args ...any) {
	if !c.Quiet {
		fmt.Fprintf(c.Out, format+"\n", args...)
	}
}

func (c *Ctx) log(msg string) {
	if err := os.MkdirAll(c.Paths.StateDir, 0o700); err != nil {
		return
	}
	if fi, err := os.Stat(c.Paths.Log); err == nil && fi.Size() > 1<<20 {
		_ = os.Rename(c.Paths.Log, c.Paths.Log+".1")
	}
	f, err := os.OpenFile(c.Paths.Log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", nowISO(), msg)
}
