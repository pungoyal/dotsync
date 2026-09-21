package dotsync

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Item is one file or symlink of a managed entry with its three signatures:
// L (this machine), R (remote), B (base: what this machine last agreed on with the remote).
type Item struct {
	Entry        *Entry
	Rel          string // "" for a file entry
	Local, Repo  string
	L, R, B      string
	Action, Note string
}

func (it *Item) Key() string {
	if it.Rel == "" {
		return it.Entry.Source
	}
	return it.Entry.Source + "/" + it.Rel
}

func (it *Item) Display() string {
	if it.Rel == "" {
		return it.Entry.TargetSpec
	}
	return it.Entry.TargetSpec + "/" + it.Rel
}

// Path is the local path as the user would type it (symlinks not resolved).
func (it *Item) Path() string {
	if it.Rel == "" {
		return it.Entry.Target
	}
	return filepath.Join(it.Entry.Target, filepath.FromSlash(it.Rel))
}

type opError struct {
	op  *Op
	msg string
}

type skippedEntry struct {
	entry  *Entry
	reason string
}

type Plan struct {
	Online          bool
	FetchError      string
	BaseCommit      string
	Manifest        *Manifest
	ManifestChanged bool
	AppliedOps      []*Op
	OpErrors        []opError
	Entries         []*Entry
	Skipped         []skippedEntry
	EntryErrors     map[string]string
	Items           []*Item
	Forget          []string
}

// Actions an item can take.
const (
	actOK          = "ok"
	actDownload    = "download"     // apply remote version here (after a backup)
	actDeleteLocal = "delete-local" // deleted remotely; remove here (after a backup)
	actUpload      = "upload"       // send this machine's version
	actDeleteRepo  = "delete-repo"  // deleted here; delete remotely
	actConflict    = "conflict"     // changed on both sides: touch nothing
	actBlocked     = "blocked"      // would upload something that looks like a secret
	actWaiting     = "waiting"      // upload committed locally but remote unreachable
	actError       = "error"
)

// decide is the whole synchronization policy. Signatures are "" when the object is absent.
func decide(L, R, B string, isDir, rootExists, adoptRemote bool) string {
	if L == R {
		return actOK
	}
	if B != "" && L == B { // only the remote changed
		if R != "" {
			return actDownload
		}
		if isDir {
			return actDeleteLocal
		}
		return actUpload // a file entry's content vanished from the repository: re-seed it
	}
	if B != "" && R == B { // only this machine changed
		if L != "" {
			return actUpload
		}
		// Deletions propagate only from inside a directory that still exists. A missing file
		// entry, or a missing directory root, means "not materialized here", not "delete everywhere".
		if isDir && rootExists {
			return actDeleteRepo
		}
		return actDownload
	}
	// Both sides changed since the base, or there is no base yet (newly managed on this machine).
	if L == "" {
		return actDownload // nothing local to lose: a modification beats a deletion
	}
	if R == "" {
		return actUpload
	}
	if B == "" && adoptRemote {
		return actDownload // pre-existing local file: backed up, then replaced by the shared version
	}
	return actConflict
}

// localRoot is where an entry lives here. A symlinked target (e.g. a previous stow-style setup)
// is followed so its real file is updated rather than the link replaced.
func localRoot(e *Entry) (string, error) {
	fi, err := os.Lstat(e.Target)
	if err != nil || fi.Mode()&os.ModeSymlink == 0 {
		return e.Target, nil
	}
	real, err := filepath.EvalSymlinks(e.Target)
	if err != nil {
		return "", fmt.Errorf("%s is a broken symlink", e.TargetSpec)
	}
	if !isWithin(real, homeDir()) && !isWithin(real, realHome()) {
		return "", fmt.Errorf("%s is a symlink to %s, outside the home directory", e.TargetSpec, real)
	}
	return real, nil
}

func planEntry(c *Ctx, e *Entry, base map[string]string, adoptRemote bool) ([]*Item, error) {
	root, err := localRoot(e)
	if err != nil {
		return nil, err
	}
	var rels []string
	rootExists := true
	if e.Kind == "file" {
		rels = []string{""}
	} else {
		if fi, err := os.Stat(root); err == nil && !fi.IsDir() {
			return nil, fmt.Errorf("%s exists but is not a directory", e.TargetSpec)
		}
		if fi, err := os.Lstat(e.RepoPath); err == nil && !fi.IsDir() {
			return nil, fmt.Errorf("files/%s in the repository is not a directory", e.Source)
		}
		set := map[string]bool{}
		local, err := walkTree(root, e.Ignore, c.Paths.OwnDirs())
		if err != nil {
			return nil, err
		}
		// A missing or empty directory means "not materialized here" (an unmounted volume, a
		// reinstall wiping it) rather than "delete everything everywhere".
		rootExists = len(local) > 0
		remote, err := walkTree(e.RepoPath, e.Ignore, nil)
		if err != nil {
			return nil, err
		}
		for _, r := range append(local, remote...) {
			set[r] = true
		}
		for r := range base {
			if !ignored(r, e.Ignore) {
				set[r] = true
			}
		}
		for r := range set {
			rels = append(rels, r)
		}
		sort.Strings(rels)
	}

	var items []*Item
	for _, rel := range rels {
		lp, rp := root, e.RepoPath
		if rel != "" {
			lp = filepath.Join(root, filepath.FromSlash(rel))
			rp = filepath.Join(e.RepoPath, filepath.FromSlash(rel))
		}
		it := &Item{Entry: e, Rel: rel, Local: lp, Repo: rp, B: base[rel], Action: actOK}
		items = append(items, it)
		if rel != "" {
			if err := firstErr(checkAncestors(root, rel), checkAncestors(e.RepoPath, rel)); err != nil {
				it.Action, it.Note = actError, err.Error()
				continue
			}
		}
		lobj, lerr := readObj(lp)
		robj, rerr := readObj(rp)
		if lerr != nil || rerr != nil {
			it.Action, it.Note = actError, firstErr(lerr, rerr).Error()
			continue
		}
		it.L, it.R = sigOf(lobj), sigOf(robj)
		if e.Kind == "file" {
			if lobj != nil && lobj.Kind != "file" {
				return nil, fmt.Errorf("%s is a %s, but the manifest says it is a file", e.TargetSpec, lobj.Kind)
			}
			if robj != nil && robj.Kind != "file" {
				return nil, fmt.Errorf("files/%s in the repository is not a regular file", e.Source)
			}
		}
		if it.L == "d" || it.L == "?" || it.R == "d" || it.R == "?" {
			it.Action, it.Note = actError, "a file on one side is a directory or special file on the other"
			continue
		}
		it.Action = decide(it.L, it.R, it.B, e.Kind == "dir", rootExists, adoptRemote)
		if it.Action == actUpload && !e.AllowSecrets {
			if reason := secretReason(lp, lobj); reason != "" {
				it.Action, it.Note = actBlocked, reason
			}
		}
	}
	return items, nil
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func resolveEntries(c *Ctx, p *Plan) {
	var seen []string
	type active struct{ target, source string }
	var targets []active
	for _, raw := range p.Manifest.Entries {
		e, err := parseEntry(raw)
		if err != nil {
			key := rawSource(raw)
			if key == "" {
				key = fmt.Sprintf("%v", raw["source"])
			}
			p.EntryErrors[key] = err.Error()
			continue
		}
		clash := ""
		for _, s := range seen {
			if overlaps(strings.ToLower(s), strings.ToLower(e.Source)) {
				clash = s
			}
		}
		if clash != "" {
			p.EntryErrors[e.Source] = fmt.Sprintf("source overlaps '%s'", clash)
			continue
		}
		seen = append(seen, e.Source)
		if len(e.OS) > 0 && !containsStr(e.OS, osName()) {
			p.Skipped = append(p.Skipped, skippedEntry{e, "only for " + strings.Join(e.OS, ", ")})
			continue
		}
		if containsStr(c.Config.Exclude, e.Source) {
			p.Skipped = append(p.Skipped, skippedEntry{e, "excluded on this machine"})
			continue
		}
		t, err := expandTarget(e.TargetSpec, c.Paths)
		if err != nil {
			p.EntryErrors[e.Source] = err.Error()
			continue
		}
		e.Target = t
		clashT := -1
		for i, a := range targets {
			if overlaps(a.target, t) {
				clashT = i
			}
		}
		if clashT >= 0 {
			p.EntryErrors[e.Source] = fmt.Sprintf("target overlaps %s (managed as '%s')", tilde(targets[clashT].target), targets[clashT].source)
			continue
		}
		targets = append(targets, active{t, e.Source})
		e.RepoPath = filepath.Join(c.Paths.Repo, filesDir, filepath.FromSlash(e.Source))
		if e.Kind == "" {
			if fi, err := os.Lstat(e.RepoPath); err == nil {
				e.Kind = map[bool]string{true: "dir", false: "file"}[fi.IsDir()]
			} else if fi, err := os.Stat(t); err == nil && fi.IsDir() {
				e.Kind = "dir"
			} else {
				e.Kind = "file"
			}
		}
		p.Entries = append(p.Entries, e)
	}
}

// buildPlan resets the cache clone to the remote, replays queued manifest changes and computes
// every item's action. It changes nothing but the (disposable) cache clone.
func buildPlan(c *Ctx, st *State, fetch bool) (*Plan, error) {
	p := &Plan{EntryErrors: map[string]string{}}
	if fetch {
		p.Online, p.FetchError = c.fetch()
	}
	var err error
	if p.BaseCommit, err = c.originCommit(); err != nil {
		return nil, err
	}
	if _, err := c.Git.Must("reset", "-q", "--hard", p.BaseCommit); err != nil {
		return nil, err
	}
	if _, err := c.Git.Must("clean", "-q", "-ffdx"); err != nil {
		return nil, err
	}
	m, err := loadManifest(c.Paths.Repo)
	if err != nil {
		return nil, err
	}
	original := m.canonical()
	for _, op := range st.PendingOps {
		if err := applyOp(c, m, op); err != nil {
			var te *transientError
			if errors.As(err, &te) {
				return nil, err
			}
			p.OpErrors = append(p.OpErrors, opError{op, err.Error()})
		} else {
			p.AppliedOps = append(p.AppliedOps, op)
		}
	}
	p.Manifest = m
	p.ManifestChanged = m.canonical() != original
	resolveEntries(c, p)

	bySource := map[string]*Entry{}
	for _, e := range p.Entries {
		bySource[e.Source] = e
	}
	var forgotten []string
	for src, es := range st.Entries {
		if _, bad := p.EntryErrors[src]; bad {
			continue // keep the base through transient errors
		}
		e := bySource[src]
		if e != nil && es.Kind == e.Kind && es.Target != e.TargetSpec {
			// Same place written differently (e.g. ~/.config/x vs $XDG_CONFIG_HOME/x): keep the base.
			if old, err := expandTarget(es.Target, c.Paths); err == nil && old == e.Target {
				es.Target = e.TargetSpec
			}
		}
		if e == nil || es.Target != e.TargetSpec || es.Kind != e.Kind {
			forgotten = append(forgotten, src) // no longer managed here: local files stay as they are
		}
	}
	sort.Strings(forgotten)
	p.Forget = forgotten

	for _, e := range p.Entries {
		base := map[string]string{}
		es := st.Entries[e.Source]
		known := es != nil && !containsStr(p.Forget, e.Source)
		if known {
			base = es.Items
		}
		// Only replace pre-existing local files when this machine has never synced the entry.
		// Once an entry has a history here, a file without a base that differs (created on two
		// machines independently, or a lost base) is a conflict.
		adoptRemote := c.Config.OnExisting != "conflict" && !known && !st.Recovered
		items, err := planEntry(c, e, base, adoptRemote)
		if err != nil {
			p.EntryErrors[e.Source] = err.Error()
			continue
		}
		p.Items = append(p.Items, items...)
	}
	return p, nil
}
