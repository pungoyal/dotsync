package dotsync

import (
	"cmp"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strings"
	"time"
)

// Set at build time (see .goreleaser.yaml).
var (
	Version = "dev"
	Commit  = ""
	Date    = ""
)

func versionString() string {
	s := "dotsync " + Version
	if Commit == "" {
		if bi, ok := debug.ReadBuildInfo(); ok {
			if Version == "dev" && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
				s = "dotsync " + bi.Main.Version // go install ...@version
			}
			for _, kv := range bi.Settings {
				switch kv.Key {
				case "vcs.revision":
					Commit = kv.Value
				case "vcs.time":
					Date = kv.Value
				}
			}
		}
	}
	if Commit != "" {
		s += " (" + short(Commit)
		if Date != "" {
			s += ", " + Date
		}
		s += ")"
	}
	return s + " " + runtime.Version() + " " + runtime.GOOS + "/" + runtime.GOARCH
}

// command is one subcommand. The usage text and dispatch are both built from commandGroups,
// so they can't drift apart.
type command struct {
	name    string
	aliases []string
	args    string
	summary string
	run     func(args []string, stdout, stderr io.Writer) (int, error)
}

var commandGroups = []struct {
	title    string
	commands []command
}{
	{"setup", []command{
		{name: "init", args: "<git-url>", summary: "set up this machine: clone, first sync, install the background agent", run: cmdInit},
		{name: "doctor", summary: "check the setup and explain how to fix problems", run: cmdDoctor},
		{name: "update", summary: "install the latest release (verified)", run: cmdUpdate},
		{name: "agent", args: "install|uninstall|status", summary: "manage the background agent", run: cmdAgent},
	}},
	{"managing files (changes apply to every machine)", []command{
		{name: "add", args: "<path>...", summary: "start managing files or directories", run: cmdAdd},
		{name: "remove", aliases: []string{"rm"}, args: "<path|source>...", summary: "stop managing (local copies are kept on every machine)", run: cmdRemove},
		{name: "set", args: "<path|source> ...", summary: "change an entry: --ignore, --os, --mode, --write, -d, …", run: cmdSet},
		{name: "describe", args: "<path|source> <text>", summary: "change an entry's description", run: cmdDescribe},
	}},
	{"this machine only", []command{
		{name: "exclude", args: "<path|source>", summary: "stop managing an entry here (files are left as they are)", run: cmdExclude},
		{name: "include", args: "<path|source>", summary: "manage an excluded entry here again", run: cmdInclude},
	}},
	{"everyday", []command{
		{name: "status", aliases: []string{"st"}, summary: "managed files and their state on this machine", run: cmdStatus},
		{name: "list", aliases: []string{"ls"}, summary: "the shared manifest", run: cmdList},
		{name: "sync", summary: "synchronize now (the agent does this periodically)", run: cmdSync},
		{name: "diff", args: "[path...]", summary: "differences between this machine and the remote", run: cmdDiff},
		{name: "resolve", args: "<path>... --keep local|remote", summary: "settle a conflict", run: cmdResolve},
		{name: "log", summary: "recent changes to the shared repository", run: cmdLog},
	}},
}

func findCommand(name string) *command {
	for _, g := range commandGroups {
		for i, cmd := range g.commands {
			if cmd.name == name || containsStr(cmd.aliases, name) {
				return &g.commands[i]
			}
		}
	}
	return nil
}

func usage() string {
	const width = 24
	var b strings.Builder
	b.WriteString("dotsync — synchronize dotfiles across macOS and Linux machines via a shared git repository\n\n")
	b.WriteString("usage: dotsync <command> [options]\n")
	for _, g := range commandGroups {
		fmt.Fprintf(&b, "\n%s\n", g.title)
		for _, cmd := range g.commands {
			left := strings.TrimSpace(cmd.name + " " + cmd.args)
			if len(left) >= width {
				fmt.Fprintf(&b, "  %s\n  %-*s%s\n", left, width, "", cmd.summary)
			} else {
				fmt.Fprintf(&b, "  %-*s%s\n", width, left, cmd.summary)
			}
		}
	}
	b.WriteString("\nRun 'dotsync help <command>' for a command's options.\n")
	return b.String()
}

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

// parseArgs lets flags appear before, between or after positional arguments.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos, rest []string
	for i, a := range args {
		if a == "--" {
			args, rest = args[:i], args[i+1:]
			break
		}
	}
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		args = fs.Args()
		if len(args) == 0 {
			return append(pos, rest...), nil
		}
		pos = append(pos, args[0])
		args = args[1:]
	}
}

// Run is the program entry point; it returns the process exit code.
func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(stdout, usage())
		return 0
	}
	if args[0] == "--version" || args[0] == "version" {
		fmt.Fprintln(stdout, versionString())
		return 0
	}
	if args[0] == "help" {
		if len(args) == 1 {
			fmt.Fprint(stdout, usage())
			return 0
		}
		args = []string{args[1], "-h"} // `help <command>` is `<command> -h`
	}
	cmd := findCommand(args[0])
	if cmd == nil {
		fmt.Fprintf(stderr, "dotsync: unknown command %q\n\n%s", args[0], usage())
		return 2
	}
	code, err := cmd.run(args[1:], stdout, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintf(stderr, "dotsync: %v\n", err)
		if code == 0 {
			code = 1
		}
	}
	return code
}

func newFlags(name, args string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: dotsync %s %s\n", name, args)
		fs.PrintDefaults()
	}
	return fs
}

// usageError prints a command's usage and reports msg as a usage error.
func usageError(fs *flag.FlagSet, msg string) (int, error) {
	fs.Usage()
	return 2, errors.New(msg)
}

// syncAfter syncs after a change, unless the user asked only to queue it.
func syncAfter(c *Ctx, noSync bool) (int, error) {
	if noSync {
		return 0, nil
	}
	return syncAndReport(c)
}

func withLock(c *Ctx, wait time.Duration, fn func() error) error {
	l, err := acquireLock(c.Paths.Lock, wait)
	if err != nil {
		return err
	}
	defer l.Release()
	return fn()
}

// withState runs fn holding the lock, with this machine's state loaded.
func withState(c *Ctx, fn func(st *State) error) error {
	return withLock(c, time.Minute, func() error {
		st, err := loadState(c)
		if err != nil {
			return err
		}
		return fn(st)
	})
}

func newOpID() string { return time.Now().Format("20060102150405") + "-" + randSuffix() }

// ---------------------------------------------------------------- sync

var actionLabels = map[string]string{
	actDownload: "updated", actDeleteLocal: "removed", actUpload: "sent", actDeleteRepo: "deleted",
	actWaiting: "waiting", actConflict: "CONFLICT", actBlocked: "BLOCKED", actError: "ERROR",
}

func printResult(c *Ctx, r *SyncResult) {
	if c.Quiet {
		return
	}
	for _, it := range r.Items {
		note := it.Note
		switch it.Action {
		case actConflict:
			note = "changed here and on another machine; see `dotsync diff " + it.Path + "`"
		case actDeleteLocal:
			note = "deleted on another machine; " + note
		}
		line := fmt.Sprintf("  %-9s %s", actionLabels[it.Action], it.Path)
		if note != "" {
			line += "  (" + note + ")"
		}
		c.say("%s", line)
	}
	for _, e := range r.Errors {
		shown := false
		for _, it := range r.Items {
			if strings.HasPrefix(e, it.Path+": ") {
				shown = true
			}
		}
		if !shown {
			c.say("  ERROR     %s", e)
		}
	}
	var bits []string
	for _, a := range []string{actDownload, actDeleteLocal, actUpload, actDeleteRepo, actWaiting, actConflict, actBlocked, actError} {
		if n := r.Counts[a]; n > 0 {
			bits = append(bits, fmt.Sprintf("%d %s", n, strings.ToLower(actionLabels[a])))
		}
	}
	summary := strings.Join(bits, ", ")
	if summary == "" {
		summary = fmt.Sprintf("in sync (%d files)", r.Counts[actOK])
	}
	if r.Outcome == "offline" {
		summary += " — remote unreachable (" + r.FetchError + ")"
	}
	if r.Commit != "" {
		summary += " — pushed " + r.Commit
	}
	c.say("dotsync: %s", summary)
	if r.Problem != "" {
		c.say("\n  ✗ %s\n    → %s", r.Problem, r.Fix)
	}
}

func resultCode(r *SyncResult) int {
	if len(r.Errors) > 0 {
		return 1
	}
	return 0
}

func syncAndReport(c *Ctx) (int, error) {
	r, err := runSync(c)
	if err != nil {
		return 1, err
	}
	printResult(c, r)
	return resultCode(r), nil
}

func cmdSync(args []string, stdout, stderr io.Writer) (int, error) {
	fs := newFlags("sync", "[-q]", stderr)
	quiet := fs.Bool("q", false, "no output; exit silently if a sync is already running (used by the agent)")
	fs.BoolVar(quiet, "quiet", false, "same as -q")
	if _, err := parseArgs(fs, args); err != nil {
		return 2, err
	}
	c, err := newCtx(true, *quiet, stdout, stderr)
	if err != nil {
		return 1, err
	}
	wait := time.Minute
	if *quiet {
		wait = 0
		// Background runs are short and I/O-bound: one thread and a small heap are plenty, and
		// keep the agent's footprint minimal and predictable.
		runtime.GOMAXPROCS(1)
		debug.SetMemoryLimit(64 << 20)
	}
	code := 0
	err = withLock(c, wait, func() error {
		code, err = syncAndReport(c)
		return err
	})
	if errors.Is(err, ErrBusy) && *quiet {
		return 0, nil
	}
	if err != nil && *quiet {
		c.log("error        " + err.Error())
	}
	return code, err
}

// ---------------------------------------------------------------- add / remove / describe

func defaultSource(path string) string {
	rel := strings.TrimPrefix(homeRel(path), xdgDefaults["XDG_CONFIG_HOME"]+"/")
	parts := strings.Split(rel, "/")
	if t := strings.TrimLeft(parts[0], "."); t != "" {
		parts[0] = t
	}
	return strings.Join(parts, "/")
}

func absPath(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		p = homeDir() + p[1:]
	}
	a, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	return a
}

// manifestWithPending is the manifest as this machine will push it: remote plus queued changes.
func manifestWithPending(c *Ctx, st *State) (*Manifest, error) {
	m, err := loadManifest(c.Paths.Repo)
	if err != nil {
		return nil, err
	}
	for _, op := range st.PendingOps {
		_ = applyOpNoFS(c, m, op)
	}
	return m, nil
}

// applyOpNoFS applies an op to an in-memory manifest without touching the cache clone.
func applyOpNoFS(c *Ctx, m *Manifest, op *Op) error {
	if op.Op == "remove" {
		m.remove(op.Source)
		return nil
	}
	return applyOp(c, m, op)
}

// addOptions are the flags of `dotsync add` that shape each new entry.
type addOptions struct {
	source, desc string
	ignore, oses []string
	allowSecrets bool
}

func cmdAdd(args []string, stdout, stderr io.Writer) (int, error) {
	fs := newFlags("add", "[options] <path>...", stderr)
	var o addOptions
	fs.StringVar(&o.source, "source", "", "path inside the repository's files/ (default: derived from the path)")
	fs.StringVar(&o.desc, "d", "", "short human-readable description")
	fs.StringVar(&o.desc, "description", "", "same as -d")
	fs.Var((*stringList)(&o.ignore), "ignore", "for directories: glob of names/paths not to sync (repeatable)")
	fs.Var((*stringList)(&o.oses), "os", "only manage on this OS: darwin or linux (repeatable)")
	fs.BoolVar(&o.allowSecrets, "allow-secrets", false, "allow content that looks like credentials")
	noSync := fs.Bool("no-sync", false, "only queue the change; the next sync pushes it")
	paths, err := parseArgs(fs, args)
	if err != nil {
		return 2, err
	}
	if len(paths) == 0 {
		return usageError(fs, "nothing to add")
	}
	if (o.source != "" || o.desc != "") && len(paths) > 1 {
		return 2, errors.New("-source and -d can only be used when adding a single path")
	}
	if err := validateOS(o.oses); err != nil {
		return 2, err
	}
	c, err := newCtx(true, false, stdout, stderr)
	if err != nil {
		return 1, err
	}
	code := 0
	err = withState(c, func(st *State) error {
		m, err := manifestWithPending(c, st)
		if err != nil {
			return err
		}
		for _, arg := range paths {
			op, err := addOp(c, arg, o)
			if err == nil {
				err = applyOpNoFS(c, m, op)
			}
			if err != nil {
				return fmt.Errorf("%s: %w", arg, err)
			}
			st.PendingOps = append(st.PendingOps, op)
			c.say("managing %s as '%s'", op.Entry["target"], op.Entry["source"])
		}
		if err := saveState(c, st); err != nil {
			return err
		}
		code, err = syncAfter(c, *noSync)
		return err
	})
	return code, err
}

// addOp checks that arg can be managed and returns the queued op that adds it.
func addOp(c *Ctx, arg string, o addOptions) (*Op, error) {
	path := absPath(arg)
	if !isWithin(path, homeDir()) || path == homeDir() {
		return nil, errors.New("only paths inside the home directory can be managed")
	}
	for _, d := range c.Paths.OwnDirs() {
		if isWithin(path, d) {
			return nil, errors.New("that is dotsync's own data")
		}
	}
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	kind := kindFile
	if fi.IsDir() {
		kind = kindDir
	} else if !fi.Mode().IsRegular() {
		return nil, errors.New("only regular files and directories can be managed")
	}
	src := o.source
	if src == "" {
		src = defaultSource(path)
	}
	if src, err = normalizeSource(src); err != nil {
		return nil, err
	}
	description := o.desc
	if description == "" {
		description = filepath.Base(path)
	}
	entry := map[string]any{"source": src, "target": tilde(path), "description": description, "type": kind}
	if len(o.ignore) > 0 {
		entry["ignore"] = o.ignore
	}
	if len(o.oses) > 0 {
		entry["os"] = o.oses
	}
	if o.allowSecrets {
		entry["allow_secrets"] = true
	} else if err := checkNewSecrets(c, path, kind, o.ignore); err != nil {
		return nil, err
	}
	// JSON round trip so the queued entry has the same shape as one read from disk.
	var normalized map[string]any
	b, _ := json.Marshal(entry)
	_ = json.Unmarshal(b, &normalized)
	return &Op{ID: newOpID(), Op: "add", Entry: normalized, Queued: nowISO()}, nil
}

// checkNewSecrets refuses a file that looks like it holds a secret. For a directory it only
// warns: those files are held back on every sync, while the rest of the directory syncs.
func checkNewSecrets(c *Ctx, path, kind string, ignore []string) error {
	real, _ := filepath.EvalSymlinks(path)
	if kind == kindFile {
		o, err := readObj(real)
		if err != nil {
			return err
		}
		if reason := secretReason(path, o); reason != "" {
			return fmt.Errorf("refusing to manage it: %s (use -allow-secrets if the remote is meant to hold it)", reason)
		}
		return nil
	}
	files, _ := walkTree(real, append(append([]string(nil), defaultIgnore...), ignore...), c.Paths.OwnDirs())
	var held []string
	for _, f := range files {
		if secretPathReason(filepath.Join(path, f)) == "" {
			continue
		}
		if o, err := readObj(filepath.Join(real, f)); err != nil || secretReason(filepath.Join(path, f), o) != "" {
			held = append(held, f)
		}
	}
	if len(held) > 0 {
		c.say("note: %d file(s) under %s look like secrets and will not be sent (e.g. %s)", len(held), tilde(path), held[0])
	}
	return nil
}

func findEntry(c *Ctx, m *Manifest, needle string) map[string]any {
	path := absPath(needle)
	for _, raw := range m.Entries {
		src := rawSource(raw)
		if src == "" {
			continue
		}
		if src == strings.Trim(needle, "/") {
			return raw
		}
		if t, err := expandTarget(str(raw["target"]), c.Paths); err == nil && t == path {
			return raw
		}
	}
	return nil
}

func queueOps(c *Ctx, needles []string, noSync bool, mk func(raw map[string]any) (*Op, string)) (int, error) {
	code := 0
	err := withState(c, func(st *State) error {
		m, err := manifestWithPending(c, st)
		if err != nil {
			return err
		}
		for _, n := range needles {
			raw := findEntry(c, m, n)
			if raw == nil {
				return fmt.Errorf("%s: not a managed entry (see `dotsync list`)", n)
			}
			op, msg := mk(raw)
			if err := applyOpNoFS(c, m, op); err != nil {
				return err
			}
			st.PendingOps = append(st.PendingOps, op)
			c.say("%s", msg)
		}
		if err := saveState(c, st); err != nil {
			return err
		}
		code, err = syncAfter(c, noSync)
		return err
	})
	return code, err
}

func cmdRemove(args []string, stdout, stderr io.Writer) (int, error) {
	fs := newFlags("remove", "[-no-sync] <path|source>...", stderr)
	noSync := fs.Bool("no-sync", false, "only queue the change")
	needles, err := parseArgs(fs, args)
	if err != nil {
		return 2, err
	}
	if len(needles) == 0 {
		return usageError(fs, "nothing to remove")
	}
	c, err := newCtx(true, false, stdout, stderr)
	if err != nil {
		return 1, err
	}
	return queueOps(c, needles, *noSync, func(raw map[string]any) (*Op, string) {
		src := rawSource(raw)
		return &Op{ID: newOpID(), Op: "remove", Source: src, Queued: nowISO()},
			fmt.Sprintf("no longer managing %s ('%s'); local copies are left in place on every machine", str(raw["target"]), src)
	})
}

// ---------------------------------------------------------------- inspect

func itemsMatching(p *Plan, needles []string) []*Item {
	if len(needles) == 0 {
		return p.Items
	}
	var out []*Item
	for _, n := range needles {
		path := absPath(n)
		key := strings.Trim(n, "/")
		for _, it := range p.Items {
			if isWithin(it.Path(), path) || it.Key() == key || strings.HasPrefix(it.Key(), key+"/") {
				out = append(out, it)
			}
		}
	}
	return out
}

type entryRow struct {
	Source      string       `json:"source"`
	Target      string       `json:"target"`
	Description string       `json:"description"`
	Type        string       `json:"type"`
	Status      string       `json:"status"`
	Note        string       `json:"note,omitempty"`
	Files       int          `json:"files"`
	Items       []ItemReport `json:"items,omitempty"`
}

func entryStatus(items []*Item, entryErr string) (string, string) {
	if entryErr != "" {
		return "ERROR", entryErr
	}
	counts := map[string]int{}
	for _, it := range items {
		counts[it.Action]++
	}
	for _, a := range []string{actConflict, actError, actBlocked} {
		if n := counts[a]; n > 0 {
			label := strings.ToUpper(a)
			if len(items) > 1 {
				label += fmt.Sprintf(" (%d)", n)
			}
			note := ""
			if a != actConflict {
				for _, it := range items {
					if it.Action == a {
						note = it.Note
						break
					}
				}
			}
			return label, note
		}
	}
	in := counts[actDownload] + counts[actDeleteLocal]
	out := counts[actUpload] + counts[actDeleteRepo]
	var bits []string
	if in > 0 {
		bits = append(bits, fmt.Sprintf("%d incoming", in))
	}
	if out > 0 {
		bits = append(bits, fmt.Sprintf("%d outgoing", out))
	}
	if len(bits) > 0 {
		return strings.Join(bits, ", "), ""
	}
	return "ok", ""
}

func cmdStatus(args []string, stdout, stderr io.Writer) (int, error) {
	fs := newFlags("status", "[-fetch] [-v] [-json]", stderr)
	fetch := fs.Bool("fetch", false, "fetch from the remote first")
	verbose := fs.Bool("v", false, "list every file")
	asJSON := fs.Bool("json", false, "machine-readable output")
	if _, err := parseArgs(fs, args); err != nil {
		return 2, err
	}
	c, err := newCtx(true, false, stdout, stderr)
	if err != nil {
		return 1, err
	}
	var p *Plan
	var st *State
	err = withState(c, func(s *State) error {
		st = s
		p, err = buildPlan(c, st, *fetch)
		return err
	})
	if err != nil {
		return 1, err
	}
	rows := statusRows(p, *verbose)
	if *asJSON {
		b, _ := json.MarshalIndent(map[string]any{
			"host": hostname(), "remote": c.Config.Remote, "branch": c.Config.Branch,
			"remote_commit": short(p.BaseCommit), "last_sync": st.LastSync, "pending_ops": st.PendingOps,
			"conflicts": st.Conflicts, "agent": agentStatus(), "entries": rows,
		}, "", "  ")
		fmt.Fprintln(stdout, string(b))
		return 0, nil
	}
	printStatusHeader(stdout, c, st, p)
	if len(rows) == 0 {
		fmt.Fprintln(stdout, "nothing is managed yet — add something with `dotsync add <path>`")
		return 0, nil
	}
	printStatusTable(stdout, rows, *verbose)
	printStatusAttention(stdout, st, rows)
	return 0, nil
}

// statusRows is one row per manifest entry: managed, skipped here, or broken.
func statusRows(p *Plan, verbose bool) []entryRow {
	byEntry := map[string][]*Item{}
	for _, it := range p.Items {
		byEntry[it.Entry.Source] = append(byEntry[it.Entry.Source], it)
	}
	var rows []entryRow
	listed := map[string]bool{}
	for _, e := range p.Entries {
		items := byEntry[e.Source]
		label, note := entryStatus(items, p.EntryErrors[e.Source])
		row := entryRow{Source: e.Source, Target: e.TargetSpec, Description: e.Description, Type: e.Kind, Status: label, Note: note, Files: len(items)}
		for _, it := range items {
			if it.Action != actOK || verbose {
				row.Items = append(row.Items, ItemReport{it.Display(), it.Action, it.Note})
			}
		}
		rows = append(rows, row)
		listed[e.Source] = true
	}
	for _, s := range p.Skipped {
		rows = append(rows, entryRow{Source: s.entry.Source, Target: s.entry.TargetSpec, Description: s.entry.Description, Type: s.entry.Kind, Status: "skipped", Note: s.reason})
		listed[s.entry.Source] = true
	}
	for src, msg := range p.EntryErrors {
		if !listed[src] {
			rows = append(rows, entryRow{Source: src, Target: "?", Status: "ERROR", Note: msg})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Source < rows[j].Source })
	return rows
}

func printStatusHeader(w io.Writer, c *Ctx, st *State, p *Plan) {
	fmt.Fprintf(w, "dotsync on %s: %s (%s @ %s)\n", hostname(), c.Config.Remote, c.Config.Branch, short(p.BaseCommit))
	if ls := st.LastSync; ls != nil {
		extra := ""
		if ls.Outcome == "offline" {
			extra = ": " + ls.FetchError
		}
		fmt.Fprintf(w, "last sync: %s (%s%s)\n", lastSyncTime(c, st).Format(time.RFC3339), ls.Outcome, extra)
		if ls.Problem != "" {
			fmt.Fprintf(w, "  ✗ %s\n    → %s\n", ls.Problem, ls.Fix)
		}
	} else {
		fmt.Fprintln(w, "last sync: never")
	}
	fmt.Fprintf(w, "automatic sync: %s\n", agentStatus())
	if len(st.PendingOps) > 0 {
		var ops []string
		for _, op := range st.PendingOps {
			ops = append(ops, op.Op+" "+op.subject())
		}
		fmt.Fprintf(w, "queued, not yet pushed: %s\n", strings.Join(ops, ", "))
	}
	fmt.Fprintln(w)
}

func printStatusTable(w io.Writer, rows []entryRow, verbose bool) {
	w1, w2, w3 := len("SOURCE"), len("TARGET"), len("STATUS")
	for i := range rows {
		if rows[i].Type == kindDir {
			rows[i].Target += "/"
		}
		w1 = max(w1, len(rows[i].Source))
		w2 = max(w2, len(rows[i].Target))
		w3 = max(w3, len(rows[i].Status))
	}
	fmt.Fprintf(w, "%-*s  %-*s  %-*s  %s\n", w1, "SOURCE", w2, "TARGET", w3, "STATUS", "DESCRIPTION")
	pad := strings.Repeat(" ", w1+w2+4)
	for _, r := range rows {
		fmt.Fprintf(w, "%-*s  %-*s  %-*s  %s\n", w1, r.Source, w2, r.Target, w3, r.Status, r.Description)
		if r.Note != "" {
			fmt.Fprintf(w, "%s↳ %s\n", pad, r.Note)
		}
		if r.Type == kindDir || verbose {
			for _, it := range r.Items {
				line := fmt.Sprintf("%s- %s: %s", pad, it.Path, it.Action)
				if it.Note != "" {
					line += " (" + it.Note + ")"
				}
				fmt.Fprintln(w, line)
			}
		}
	}
}

// printStatusAttention explains what needs the user: held-back secrets and conflicts.
func printStatusAttention(w io.Writer, st *State, rows []entryRow) {
	for _, r := range rows {
		if strings.HasPrefix(r.Status, "BLOCKED") {
			fmt.Fprintln(w, "\nBLOCKED files look like they contain secrets, so their changes stay on this machine.")
			fmt.Fprintln(w, "  remove the secret, or mark a false positive with a `dotsync:allow-secret` comment on that line:")
			fmt.Fprintln(w, "  https://pungoyal.github.io/dotsync/guides/secrets/")
			break
		}
	}
	if len(st.Conflicts) > 0 {
		fmt.Fprintln(w, "\nconflicts (neither version has been changed):")
		for _, cf := range st.Conflicts {
			fmt.Fprintf(w, "  %s  since %s\n", cf.Target, cf.Since)
		}
		fmt.Fprintln(w, "  inspect: dotsync diff <path>    settle: dotsync resolve <path> --keep local|remote")
		fmt.Fprintln(w, "  (to merge by hand, edit the local file, then --keep local)")
	}
}

func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

func cmdList(args []string, stdout, stderr io.Writer) (int, error) {
	fs := newFlags("list", "[-json]", stderr)
	asJSON := fs.Bool("json", false, "machine-readable output")
	if _, err := parseArgs(fs, args); err != nil {
		return 2, err
	}
	c, err := newCtx(true, false, stdout, stderr)
	if err != nil {
		return 1, err
	}
	var m *Manifest
	err = withLock(c, time.Minute, func() error {
		m, err = loadManifest(c.Paths.Repo)
		return err
	})
	if err != nil {
		return 1, err
	}
	entries := append([]map[string]any(nil), m.Entries...)
	sort.Slice(entries, func(i, j int) bool { return str(entries[i]["source"]) < str(entries[j]["source"]) })
	if *asJSON {
		b, _ := json.MarshalIndent(entries, "", "  ")
		fmt.Fprintln(stdout, string(b))
		return 0, nil
	}
	if len(entries) == 0 {
		fmt.Fprintln(stdout, "nothing is managed yet")
	}
	for _, e := range entries {
		extra := ""
		if o, ok := e["os"].([]any); ok {
			extra = fmt.Sprintf("  [%v]", o)
		}
		fmt.Fprintf(stdout, "%s  ->  %s%s\n    %s\n", str(e["source"]), str(e["target"]), extra, str(e["description"]))
	}
	return 0, nil
}

func textLines(o *Obj) ([]string, bool) {
	if o == nil {
		return nil, true
	}
	if o.Kind == kindLink {
		return []string{"symlink -> " + string(o.Data)}, true
	}
	for _, b := range o.Data {
		if b == 0 {
			return nil, false
		}
	}
	s := string(o.Data)
	if s == "" {
		return nil, true
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n"), true
}

func cmdDiff(args []string, stdout, stderr io.Writer) (int, error) {
	fs := newFlags("diff", "[-fetch] [path...]", stderr)
	fetch := fs.Bool("fetch", false, "fetch from the remote first")
	needles, err := parseArgs(fs, args)
	if err != nil {
		return 2, err
	}
	c, err := newCtx(true, false, stdout, stderr)
	if err != nil {
		return 1, err
	}
	return 0, withState(c, func(st *State) error {
		p, err := buildPlan(c, st, *fetch)
		if err != nil {
			return err
		}
		shown := 0
		for _, it := range itemsMatching(p, needles) {
			if it.L == it.R {
				continue
			}
			lobj, lerr := readObj(it.Local)
			robj, rerr := readObj(it.Repo)
			if e := firstErr(lerr, rerr); e != nil {
				fmt.Fprintf(stdout, "%s: %v\n", it.Display(), e)
				continue
			}
			fmt.Fprintf(stdout, "=== %s  [%s]\n", it.Display(), it.Action)
			a, okA := textLines(robj)
			b, okB := textLines(lobj)
			if !okA || !okB {
				fmt.Fprintln(stdout, "binary files differ")
			} else {
				fmt.Fprint(stdout, unifiedDiff(a, b, "remote/"+it.Key(), "local/"+it.Key()))
			}
			shown++
		}
		if shown == 0 {
			fmt.Fprintln(stdout, "no differences")
		}
		return nil
	})
}

func cmdResolve(args []string, stdout, stderr io.Writer) (int, error) {
	fs := newFlags("resolve", "<path>... --keep local|remote", stderr)
	keep := fs.String("keep", "", "which version wins: local (this machine) or remote")
	needles, err := parseArgs(fs, args)
	if err != nil {
		return 2, err
	}
	if len(needles) == 0 || (*keep != "local" && *keep != "remote") {
		return usageError(fs, "need at least one path and --keep local or --keep remote")
	}
	c, err := newCtx(true, false, stdout, stderr)
	if err != nil {
		return 1, err
	}
	code := 0
	err = withState(c, func(st *State) error {
		p, err := buildPlan(c, st, true)
		if err != nil {
			return err
		}
		var found []*Item
		for _, it := range itemsMatching(p, needles) {
			if it.Action == actConflict {
				found = append(found, it)
			}
		}
		if len(found) == 0 {
			return fmt.Errorf("no conflicts at %s", strings.Join(needles, ", "))
		}
		for _, src := range p.Forget {
			delete(st.Entries, src)
		}
		for _, it := range found {
			// Resolving only moves the base, turning the conflict into a one-sided change that the
			// following sync carries out (with a backup if the local file is replaced).
			if *keep == "local" {
				setBase(st.entry(it.Entry), it.Rel, it.R)
				c.say("keeping this machine's version of %s", it.Display())
			} else {
				setBase(st.entry(it.Entry), it.Rel, it.L)
				c.say("taking the remote version of %s", it.Display())
			}
		}
		if err := saveState(c, st); err != nil {
			return err
		}
		code, err = syncAndReport(c)
		return err
	})
	return code, err
}

func cmdLog(args []string, stdout, stderr io.Writer) (int, error) {
	fs := newFlags("log", "[-n N]", stderr)
	n := fs.Int("n", 20, "number of commits")
	if _, err := parseArgs(fs, args); err != nil {
		return 2, err
	}
	c, err := newCtx(true, false, stdout, stderr)
	if err != nil {
		return 1, err
	}
	out, err := c.Git.Must("log", fmt.Sprintf("-%d", *n), "--format=%h %ad %s", "--date=format:%Y-%m-%d %H:%M", "refs/remotes/origin/"+c.Config.Branch)
	if err != nil {
		return 1, err
	}
	fmt.Fprintln(stdout, out)
	return 0, nil
}

// ---------------------------------------------------------------- init

const skeletonReadme = `# dotfiles (managed by dotsync)

- ` + "`manifest.json`" + ` lists every managed file or directory: ` + "`source`" + ` (under ` + "`files/`" + `),
  ` + "`target`" + ` (portable path on each machine) and a ` + "`description`" + `.
- ` + "`files/`" + ` holds the content.

Prefer the ` + "`dotsync`" + ` command to editing this repository by hand. If you edit
` + "`manifest.json`" + ` directly, keep it valid JSON: machines refuse to act on a broken manifest.
`

func cmdInit(args []string, stdout, stderr io.Writer) (int, error) {
	fs := newFlags("init", "[options] <git-url>", stderr)
	branch := fs.String("branch", "", "branch to use (default main)")
	interval := fs.Int("interval", 0, fmt.Sprintf("seconds between automatic syncs (default %d)", defaultInterval))
	keepLocal := fs.Bool("keep-existing", false, "report existing differing local files as conflicts instead of replacing them (after a backup)")
	noAgent := fs.Bool("no-agent", false, "do not install the background agent")
	noInstall := fs.Bool("no-install", false, "do not copy dotsync to ~/.local/bin")
	pos, err := parseArgs(fs, args)
	if err != nil {
		return 2, err
	}
	c, err := newCtx(false, false, stdout, stderr)
	if err != nil {
		return 1, err
	}
	cfg := c.Config
	if cfg == nil {
		cfg = &Config{}
	}
	if len(pos) > 0 {
		cfg.Remote = pos[0]
	}
	if cfg.Remote == "" {
		return usageError(fs, "give the URL of a (private) git repository to sync through")
	}
	cfg.Branch = cmp.Or(*branch, cfg.Branch, "main")
	cfg.Interval = cmp.Or(*interval, cfg.Interval, defaultInterval)
	if *keepLocal {
		cfg.OnExisting = "conflict"
	}
	if cfg.Exclude == nil {
		cfg.Exclude = []string{}
	}
	c.Config = cfg
	if err := ensureClone(c); err != nil {
		return 1, err
	}
	var res *SyncResult
	err = withLock(c, time.Minute, func() error {
		if err := prepareRemote(c); err != nil {
			return err
		}
		if err := writeJSON(c.Paths.Config, cfg, 0o600); err != nil {
			return err
		}
		if !*noInstall {
			if err := installSelf(c); err != nil {
				c.say("note: could not install to %s: %v", tilde(c.Paths.Bin), err)
			}
		}
		res, err = runSync(c)
		return err
	})
	if err != nil {
		return 1, err
	}
	printResult(c, res)
	code := resultCode(res)
	if res.Counts[actDownload] > 0 {
		c.say("existing local files that were replaced are saved under %s", tilde(c.Paths.Backups))
	}
	if !*noAgent {
		msg, err := agentInstall(c)
		if err != nil {
			c.say("automatic sync: NOT installed: %v", err)
			code = 1
		} else {
			c.say("automatic sync: %s", msg)
		}
	}
	return code, nil
}

// ensureClone clones the remote into the cache, unless a clone of it is already there.
func ensureClone(c *Ctx) error {
	if _, err := os.Stat(filepath.Join(c.Paths.Repo, ".git")); err == nil {
		cur, _ := c.Git.Must("remote", "get-url", "origin")
		if cur != "" && cur != c.Config.Remote {
			return fmt.Errorf("this machine already syncs with %s; remove %s to switch", cur, tilde(c.Paths.Repo))
		}
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(c.Paths.Repo), 0o700); err != nil {
		return err
	}
	c.say("cloning %s ...", c.Config.Remote)
	r, err := c.Git.run(false, 10*time.Minute, append(append([]string{"clone", "-q", "--no-checkout"}, cloneConfigArgs()...), c.Config.Remote, c.Paths.Repo)...)
	if err != nil {
		return err
	}
	if r.Code != 0 {
		return gitError("git clone failed", c, r.Stderr)
	}
	return nil
}

// prepareRemote configures the cache clone, initializes an empty remote and fetches it.
func prepareRemote(c *Ctx) error {
	if err := configureRepo(c.Git); err != nil {
		return err
	}
	heads, err := c.Git.Must("ls-remote", "--heads", "origin")
	if err != nil {
		return err
	}
	if !hasHead(heads, c.Config.Branch) {
		if strings.TrimSpace(heads) != "" {
			return fmt.Errorf("the remote has branches but no '%s'; pass -branch", c.Config.Branch)
		}
		c.say("remote is empty; creating the manifest")
		if err := initRepo(c); err != nil {
			return err
		}
	}
	if ok, ferr := c.fetch(); !ok {
		return gitError("cannot fetch from "+c.Config.Remote, c, ferr)
	}
	return nil
}

// gitError reports a failed git command, with the problem and fix when the cause is known.
func gitError(what string, c *Ctx, stderr string) error {
	if gp := diagnoseGit(c, stderr); gp != nil {
		return fmt.Errorf("%s: %s\n  %s\n  → %s", what, lastLine(stderr), gp.Problem, gp.Fix)
	}
	return fmt.Errorf("%s: %s", what, lastLine(stderr))
}

func hasHead(lsRemote, branch string) bool {
	for _, line := range strings.Split(lsRemote, "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == "refs/heads/"+branch {
			return true
		}
	}
	return false
}

func initRepo(c *Ctx) error {
	g := c.Git
	if _, err := g.Must("checkout", "-q", "--orphan", c.Config.Branch); err != nil {
		return err
	}
	if err := saveManifest(c.Paths.Repo, &Manifest{Top: map[string]any{"version": 1}}); err != nil {
		return err
	}
	files := map[string]string{
		filepath.Join(filesDir, ".keep"): "",
		"README.md":                      skeletonReadme,
		".gitattributes":                 "* -text\n",
	}
	for name, content := range files {
		p := filepath.Join(c.Paths.Repo, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			return err
		}
	}
	if _, err := g.Must("add", "-A"); err != nil {
		return err
	}
	if _, err := g.Must("commit", "-q", "--no-verify", "-m", hostname()+": initialize dotsync repository"); err != nil {
		return err
	}
	_, err := g.Must("push", "-q", "origin", "HEAD:refs/heads/"+c.Config.Branch)
	return err
}

func installSelf(c *Ctx) error {
	me, err := os.Executable()
	if err != nil {
		return err
	}
	me, _ = filepath.EvalSymlinks(me)
	if dest, _ := filepath.EvalSymlinks(c.Paths.Bin); dest == me {
		return nil
	}
	data, err := os.ReadFile(me)
	if err != nil {
		return err
	}
	if err := atomicWrite(c.Paths.Bin, data, 0o755); err != nil {
		return err
	}
	c.say("installed %s", tilde(c.Paths.Bin))
	if !containsStr(filepath.SplitList(os.Getenv("PATH")), filepath.Dir(c.Paths.Bin)) {
		c.say("note: add %s to your PATH", tilde(filepath.Dir(c.Paths.Bin)))
	}
	return nil
}
