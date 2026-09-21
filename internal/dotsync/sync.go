package dotsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const pushAttempts = 5

// executeLocal applies remote changes to this machine and stages this machine's changes in the
// cache clone. Every local write re-checks that the file is still what was planned against.
func executeLocal(c *Ctx, st *State, p *Plan, backups *Backups) []*Item {
	for _, src := range p.Forget {
		delete(st.Entries, src)
	}
	st.Recovered = false // every planned entry now has a base record
	var staged []*Item
	for _, it := range p.Items {
		es := st.entry(it.Entry)
		var err error
		switch it.Action {
		case actOK:
			setBase(es, it.Rel, it.R)
		case actDownload:
			if err = installRemote(it, backups); err == nil {
				setBase(es, it.Rel, it.R)
			}
		case actDeleteLocal:
			if err = deleteLocal(it, backups); err == nil {
				setBase(es, it.Rel, "")
			}
		case actUpload, actDeleteRepo:
			if err = stage(c, it); err == nil {
				staged = append(staged, it)
			}
		}
		if err != nil {
			it.Action, it.Note = actError, err.Error()
		}
	}
	return staged
}

// installRemote replaces the local file with the repository's version, backing it up first.
func installRemote(it *Item, backups *Backups) error {
	cur, err := recheck(it)
	if err != nil {
		return err
	}
	robj, err := readObj(it.Repo)
	if err != nil {
		return err
	}
	if cur != nil {
		b, err := backups.save(it.Local)
		if err != nil {
			return err
		}
		it.Note = "previous version saved to " + tilde(b)
	}
	return writeLocal(it.Local, robj, it.Entry.Mode, it.Entry.Write)
}

// deleteLocal removes a file deleted on another machine, backing it up first.
func deleteLocal(it *Item, backups *Backups) error {
	if _, err := recheck(it); err != nil {
		return err
	}
	b, err := backups.save(it.Local)
	if err != nil {
		return err
	}
	it.Note = "saved to " + tilde(b)
	if err := os.Remove(it.Local); err != nil {
		return err
	}
	if root, err := localRoot(it.Entry); err == nil {
		pruneEmptyDirs(filepath.Dir(it.Local), root)
	}
	return nil
}

// stage writes this machine's change (an edit or a deletion) into the cache clone.
func stage(c *Ctx, it *Item) error {
	c.markRepoDirty()
	if it.Action == actDeleteRepo {
		if err := removePath(it.Repo); err != nil {
			return err
		}
		pruneEmptyDirs(filepath.Dir(it.Repo), filepath.Join(c.Paths.Repo, filesDir))
		return nil
	}
	lobj, err := recheck(it)
	if err != nil {
		return err
	}
	return writeRepo(it.Repo, lobj)
}

// recheck re-reads an item's local file right before acting on it, refusing if it changed since
// the plan was made (the item is then retried on the next sync).
func recheck(it *Item) (*Obj, error) {
	o, err := readObj(it.Local)
	if err == nil && sigOf(o) != it.L {
		err = &UnstableError{it.Local}
	}
	return o, err
}

// recordConflicts remembers conflicts and keeps a copy of each remote side for `dotsync diff`.
// It returns conflicts not seen on the previous run.
func recordConflicts(c *Ctx, st *State, p *Plan) []ConflictInfo {
	old := st.Conflicts
	cur := map[string]ConflictInfo{}
	var fresh []ConflictInfo
	for _, it := range p.Items {
		if it.Action != actConflict {
			continue
		}
		info, seen := old[it.Key()]
		if !seen {
			info = ConflictInfo{Target: it.Display(), Since: nowISO()}
			fresh = append(fresh, info)
		}
		cur[it.Key()] = info
		copyPath := filepath.Join(c.Paths.Conflicts, filepath.FromSlash(it.Key()))
		if robj, err := readObj(it.Repo); err == nil && robj != nil {
			if cur, _ := readObj(copyPath); sigOf(cur) != robj.Sig { // don't rewrite an unchanged copy
				_ = writeRepo(copyPath, robj)
			}
		}
	}
	if stale, err := walkTree(c.Paths.Conflicts, nil, nil); err == nil {
		for _, rel := range stale {
			if _, ok := cur[rel]; !ok {
				full := filepath.Join(c.Paths.Conflicts, filepath.FromSlash(rel))
				_ = os.Remove(full)
				pruneEmptyDirs(filepath.Dir(full), c.Paths.Conflicts)
			}
		}
	}
	st.Conflicts = cur
	return fresh
}

func commitMessage(p *Plan, staged []*Item) string {
	var parts, added, removed []string
	for _, op := range p.AppliedOps {
		switch op.Op {
		case "add":
			added = append(added, op.subject())
		case "remove":
			removed = append(removed, op.subject())
		}
	}
	if len(added) > 0 {
		parts = append(parts, "manage "+strings.Join(added, ", "))
	}
	if len(removed) > 0 {
		parts = append(parts, "unmanage "+strings.Join(removed, ", "))
	}
	if len(staged) > 0 {
		parts = append(parts, fmt.Sprintf("update %d file(s)", len(staged)))
	}
	if len(parts) == 0 {
		parts = append(parts, "update manifest")
	}
	msg := hostname() + ": " + strings.Join(parts, "; ")
	if len(staged) > 0 {
		msg += "\n"
		for _, it := range staged {
			verb := "update "
			if it.Action == actDeleteRepo {
				verb = "delete "
			}
			msg += "\n" + verb + it.Key()
		}
	}
	return msg
}

type ItemReport struct {
	Path   string `json:"path"`
	Action string `json:"action"`
	Note   string `json:"note,omitempty"`
}

type SyncResult struct {
	SyncSummary
	Items []ItemReport `json:"items"`
}

func clearOps(st *State, applied []*Op) {
	done := map[string]bool{}
	for _, op := range applied {
		done[op.ID] = true
	}
	var kept []*Op
	for _, op := range st.PendingOps {
		if !done[op.ID] {
			kept = append(kept, op)
		}
	}
	st.PendingOps = kept
}

func dropFailedOps(st *State, failed []opError) {
	var ops []*Op
	for _, f := range failed {
		ops = append(ops, f.op)
	}
	clearOps(st, ops)
}

// runSync performs one full reconciliation. The caller holds the lock.
//
// Each attempt starts from the remote's current commit, so there is never a git merge: if
// another machine pushes first, the push is rejected and the whole pass is redone on top of it.
func runSync(c *Ctx) (res *SyncResult, err error) {
	st, err := loadState(c)
	if err != nil {
		return nil, err
	}
	defer func() {
		if c.cache != nil && (c.cache.changed || len(c.cache.used) != len(st.StatCache)) {
			st.StatCache = c.cache.snapshot()
		}
		if serr := saveStateIfChanged(c, st); serr != nil && err == nil {
			err = serr
		}
	}()
	if st.RepoConfig < repoConfigVersion {
		if err := configureRepo(c.Git); err != nil {
			return nil, err
		}
		st.RepoConfig = repoConfigVersion
	}
	backups := &Backups{paths: c.Paths}
	for attempt := 0; attempt < pushAttempts; attempt++ {
		p, err := buildPlan(c, st, true)
		if err != nil {
			return nil, err
		}
		staged := executeLocal(c, st, p, backups)
		dropFailedOps(st, p.OpErrors)
		outcome, commit, err := publish(c, st, p, staged)
		if errors.Is(err, errRemoteMoved) {
			continue // another machine pushed first: redo the pass on top of its commit
		}
		if err != nil {
			return nil, err
		}
		res := finish(c, st, p, outcome, commit)
		housekeeping(c, st, time.Now())
		return res, nil
	}
	return nil, fmt.Errorf("gave up after %d attempts: the remote kept changing", pushAttempts)
}

var errRemoteMoved = errors.New("the remote changed during the sync")

// publish commits and pushes this pass's changes to the cache clone, if any. Changes wait (the
// outcome is "offline") when the remote is unreachable, and errRemoteMoved means another machine
// pushed first.
func publish(c *Ctx, st *State, p *Plan, staged []*Item) (outcome, commit string, err error) {
	if p.ManifestChanged {
		c.markRepoDirty()
		if err := saveManifest(c.Paths.Repo, p.Manifest); err != nil {
			return "", "", err
		}
	}
	// Only ask git about the working tree if this pass changed it.
	dirty := false
	if len(staged) > 0 || p.ManifestChanged || len(p.AppliedOps) > 0 {
		if _, err := c.Git.Must("add", "-A", "--", "."); err != nil {
			return "", "", err
		}
		dirty = c.Git.Try("diff", "--cached", "--quiet").Code != 0
	}
	if !dirty {
		clearOps(st, p.AppliedOps)
		return "ok", "", nil
	}
	waiting := func() (string, string, error) {
		for _, it := range staged {
			it.Action, it.Note = actWaiting, "will be sent when the remote is reachable"
		}
		return "offline", "", nil
	}
	if !p.Online {
		return waiting()
	}
	if _, err := c.Git.Must("commit", "-q", "--no-verify", "-m", commitMessage(p, staged)); err != nil {
		return "", "", err
	}
	if ok, perr := c.push(); !ok {
		online, ferr := c.fetch()
		if !online {
			p.FetchError, p.GitProblem = lastLine(ferr), diagnoseGit(c, ferr)
			return waiting()
		}
		if head, _ := c.originCommit(); head != p.BaseCommit {
			return "", "", errRemoteMoved
		}
		return "", "", fmt.Errorf("git push failed: %s", perr)
	}
	commit, _ = c.Git.Must("rev-parse", "--short", "HEAD")
	c.markRepoClean() // everything in the working tree is now committed and pushed
	for _, it := range staged {
		sig := it.L
		if it.Action == actDeleteRepo {
			sig = ""
		}
		setBase(st.entry(it.Entry), it.Rel, sig)
	}
	clearOps(st, p.AppliedOps)
	return "ok", commit, nil
}

func finish(c *Ctx, st *State, p *Plan, outcome, commit string) *SyncResult {
	prev := st.LastSync
	fresh := recordConflicts(c, st, p)
	counts := map[string]int{}
	var items []ItemReport
	var errs []string
	var srcs []string
	for s := range p.EntryErrors {
		srcs = append(srcs, s)
	}
	sort.Strings(srcs)
	for _, s := range srcs {
		errs = append(errs, s+": "+p.EntryErrors[s])
	}
	for _, it := range p.Items {
		counts[it.Action]++
		if it.Action != actOK {
			items = append(items, ItemReport{it.Display(), it.Action, it.Note})
		}
		if it.Action == actError {
			errs = append(errs, it.Display()+": "+it.Note)
		}
	}
	for _, oe := range p.OpErrors {
		errs = append(errs, fmt.Sprintf("queued %s of '%s' dropped: %s", oe.op.Op, oe.op.subject(), oe.msg))
	}
	if len(errs) > 0 && outcome == "ok" {
		outcome = "partial"
	}
	res := &SyncResult{
		SyncSummary: SyncSummary{Time: nowISO(), Outcome: outcome, Online: p.Online, FetchError: p.FetchError,
			Problem: p.GitProblem.problem(), Fix: p.GitProblem.fix(),
			Commit: commit, Counts: counts, Errors: errs},
		Items: items,
	}
	st.LastSync = &res.SyncSummary
	logResult(c, res, prev)
	if c.Quiet {
		notifyResult(p, prev, fresh)
	}
	return res
}

// logResult appends the result to the agent log. A sync that changed nothing isn't logged, so an
// idle machine never writes to the log.
func logResult(c *Ctx, r *SyncResult, prev *SyncSummary) {
	for _, it := range r.Items {
		c.log(strings.TrimSpace(fmt.Sprintf("%-12s %s %s", it.Action, it.Path, it.Note)))
	}
	for _, e := range r.Errors {
		c.log("error        " + e)
	}
	if len(r.Items) == 0 && len(r.Errors) == 0 && r.Commit == "" && prev != nil && prev.Outcome == r.Outcome && prev.FetchError == r.FetchError {
		return
	}
	line := "sync " + r.Outcome
	if r.Commit != "" {
		line += " pushed " + r.Commit
	}
	if r.Outcome == "offline" {
		line += " (" + r.FetchError + ")"
	}
	c.log(line)
}

// notifyResult tells the user, once, about what needs them: a git problem, or new conflicts.
func notifyResult(p *Plan, prev *SyncSummary, fresh []ConflictInfo) {
	if p.GitProblem != nil && (prev == nil || prev.Problem != p.GitProblem.Problem) {
		notify("dotsync can't reach your dotfiles repository", p.GitProblem.Problem+". Run `dotsync doctor` for the fix.")
	}
	if len(fresh) > 0 {
		var names []string
		for i, f := range fresh {
			if i == 3 {
				names = append(names, "…")
				break
			}
			names = append(names, f.Target)
		}
		notify("dotsync: conflict", "Changed here and on another machine: "+strings.Join(names, ", ")+". Run `dotsync status`.")
	}
}

// repoConfigVersion is bumped whenever configureRepo changes, so existing installs pick up new
// git settings on their next sync.
const repoConfigVersion = 2

// housekeeping runs on a fixed calendar, never at random: backups are pruned at most once a day
// and the cache clone is garbage-collected at most once a week.
func housekeeping(c *Ctx, st *State, now time.Time) {
	today := now.Format("2006-01-02")
	if days := c.Config.backupRetention(); days > 0 && st.LastBackupPrune != today {
		if n, err := pruneBackups(c.Paths.Backups, time.Duration(days)*24*time.Hour, now); err != nil {
			c.log("error        pruning backups: " + err.Error())
		} else {
			st.LastBackupPrune = today
			if n > 0 {
				c.log(fmt.Sprintf("pruned %d backup file(s) older than %d days", n, days))
			}
		}
	}
	last, err := time.Parse("2006-01-02", st.LastGC)
	if err != nil || now.Sub(last) >= 7*24*time.Hour {
		if r := c.Git.Try("gc", "--quiet"); r.Code == 0 {
			st.LastGC = today
		} else {
			c.log("error        git gc: " + lastLine(r.Stderr))
		}
	}
}

// notifiers are tried in order; the first available on this OS shows the notification.
var notifiers = []struct {
	os, program string
	args        func(title, message, icon string) []string
}{
	// The only way to show dotsync's icon on a macOS notification from a command-line tool.
	{"darwin", "terminal-notifier", func(t, m, icon string) []string {
		return []string{"-title", t, "-message", m, "-group", "dotsync", "-appIcon", icon}
	}},
	{"darwin", "osascript", func(t, m, _ string) []string {
		qt, _ := json.Marshal(t)
		qm, _ := json.Marshal(m)
		return []string{"-e", fmt.Sprintf("display notification %s with title %s", qm, qt)}
	}},
	{"linux", "notify-send", func(t, m, icon string) []string {
		return []string{"--app-name=dotsync", "--icon=" + icon, t, m}
	}},
}

func notify(title, message string) {
	if os.Getenv("DOTSYNC_NO_NOTIFY") != "" {
		return
	}
	for _, n := range notifiers {
		if n.os == osName() && hasCommand(n.program) {
			runQuiet(n.program, n.args(title, message, installIcon())...)
			return
		}
	}
}

func hasCommand(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
