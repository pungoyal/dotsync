package dotsync

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
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
	FetchError      string      // last line of git's error, for display
	GitProblem      *gitProblem // what the user needs to do, if anything
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
	rels, rootExists := []string{""}, true
	if e.Kind == kindDir {
		if rels, rootExists, err = dirRels(c, e, root, base); err != nil {
			return nil, err
		}
	}
	var items []*Item
	for _, rel := range rels {
		it, err := planItem(c, e, root, rel, base[rel], rootExists, adoptRemote)
		if err != nil {
			return nil, err
		}
		items = append(items, it)
	}
	return items, nil
}

// dirRels lists every file of a directory entry: here, in the repository, or in the base. It also
// reports whether the local directory has any files.
func dirRels(c *Ctx, e *Entry, root string, base map[string]string) ([]string, bool, error) {
	if fi, err := os.Stat(root); err == nil && !fi.IsDir() {
		return nil, false, fmt.Errorf("%s exists but is not a directory", e.TargetSpec)
	}
	if fi, err := os.Lstat(e.RepoPath); err == nil && !fi.IsDir() {
		return nil, false, fmt.Errorf("files/%s in the repository is not a directory", e.Source)
	}
	local, err := walkTree(root, e.Ignore, c.Paths.OwnDirs())
	if err != nil {
		return nil, false, err
	}
	remote, err := walkTree(e.RepoPath, e.Ignore, nil)
	if err != nil {
		return nil, false, err
	}
	set := map[string]bool{}
	for _, r := range append(local, remote...) {
		set[r] = true
	}
	for r := range base {
		if !ignored(r, e.Ignore) {
			set[r] = true
		}
	}
	rels := make([]string, 0, len(set))
	for r := range set {
		rels = append(rels, r)
	}
	sort.Strings(rels)
	// A missing or empty directory means "not materialized here" (an unmounted volume, a
	// reinstall wiping it) rather than "delete everything everywhere".
	return rels, len(local) > 0, nil
}

// planItem decides what to do with one file. Problems with the file itself are reported on the
// item; an error means the whole entry is inconsistent with the manifest.
func planItem(c *Ctx, e *Entry, root, rel, base string, rootExists, adoptRemote bool) (*Item, error) {
	lp, rp := root, e.RepoPath
	if rel != "" {
		lp = filepath.Join(root, filepath.FromSlash(rel))
		rp = filepath.Join(e.RepoPath, filepath.FromSlash(rel))
	}
	it := &Item{Entry: e, Rel: rel, Local: lp, Repo: rp, B: base, Action: actOK}
	fail := func(err error) (*Item, error) {
		it.Action, it.Note = actError, err.Error()
		return it, nil
	}
	if rel != "" {
		if err := firstErr(checkAncestors(root, rel), checkAncestors(e.RepoPath, rel)); err != nil {
			return fail(err)
		}
	}
	lobj, lerr := c.cache.obj(lp)
	robj, rerr := c.cache.obj(rp)
	if err := firstErr(lerr, rerr); err != nil {
		return fail(err)
	}
	it.L, it.R = sigOf(lobj), sigOf(robj)
	if e.Kind == kindFile {
		if lobj != nil && lobj.Kind != kindFile {
			return nil, fmt.Errorf("%s is a %s, but the manifest says it is a file", e.TargetSpec, lobj.Kind)
		}
		if robj != nil && robj.Kind != kindFile {
			return nil, fmt.Errorf("files/%s in the repository is not a regular file", e.Source)
		}
	}
	if isSpecial(it.L) || isSpecial(it.R) {
		return fail(errors.New("a file on one side is a directory or special file on the other"))
	}
	it.Action = decide(it.L, it.R, it.B, e.Kind == kindDir, rootExists, adoptRemote)
	if it.Action != actUpload || e.AllowSecrets {
		return it, nil
	}
	if lobj != nil && lobj.Kind == kindFile && lobj.Data == nil {
		var err error
		if lobj, err = readObj(lp); err != nil { // cached answer: fetch content for the scan
			return fail(err)
		}
	}
	if reason := secretReason(lp, lobj); reason != "" {
		it.Action, it.Note = actBlocked, reason
	}
	return it, nil
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
				e.Kind = kindFile
				if fi.IsDir() {
					e.Kind = kindDir
				}
			} else if fi, err := os.Stat(t); err == nil && fi.IsDir() {
				e.Kind = kindDir
			} else {
				e.Kind = kindFile
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
		var out string
		p.Online, out = c.fetch()
		if !p.Online {
			p.FetchError, p.GitProblem = lastLine(out), diagnoseGit(c, out)
		}
	}
	c.cache = newStatCache(st.StatCache)
	if err := resetCache(c, st, p); err != nil {
		return nil, err
	}
	if err := replayOps(c, st, p); err != nil {
		return nil, err
	}
	resolveEntries(c, p)
	p.Forget = forgottenEntries(c, st, p)
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

// resetCache resets the cache clone to the remote's commit. That's only needed if a previous run
// modified its working tree (the marker is created before any modification and removed after a
// reset) or HEAD moved; and once a day regardless.
func resetCache(c *Ctx, st *State, p *Plan) error {
	head, base, err := c.headAndOrigin()
	if err != nil {
		return err
	}
	p.BaseCommit = base
	today := time.Now().Format("2006-01-02")
	if head == base && !c.repoDirty() && st.LastReset == today {
		return nil
	}
	if _, err := c.Git.Must("reset", "-q", "--hard", base); err != nil {
		return err
	}
	if _, err := c.Git.Must("clean", "-q", "-ffdx"); err != nil {
		return err
	}
	c.markRepoClean()
	st.LastReset = today
	return nil
}

// replayOps applies this machine's queued manifest changes on top of the remote's manifest.
func replayOps(c *Ctx, st *State, p *Plan) error {
	m, err := loadManifest(c.Paths.Repo)
	if err != nil {
		return err
	}
	original := m.canonical()
	if len(st.PendingOps) > 0 {
		c.markRepoDirty() // replaying a remove deletes files from the working tree
	}
	for _, op := range st.PendingOps {
		if err := applyOp(c, m, op); err != nil {
			var te *transientError
			if errors.As(err, &te) {
				return err
			}
			p.OpErrors = append(p.OpErrors, opError{op, err.Error()})
		} else {
			p.AppliedOps = append(p.AppliedOps, op)
		}
	}
	p.Manifest = m
	p.ManifestChanged = m.canonical() != original
	return nil
}

// forgottenEntries are entries with a base here that are no longer managed here (removed,
// excluded, or moved): their base is dropped and their local files stay as they are.
func forgottenEntries(c *Ctx, st *State, p *Plan) []string {
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
			forgotten = append(forgotten, src)
		}
	}
	sort.Strings(forgotten)
	return forgotten
}
