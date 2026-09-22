package dotsync

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// copyTree copies the directory src to dst: regular files with their permissions, directories,
// and symlinks as symlinks. Anything else (sockets, devices) is not a dotfile and is skipped.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		out := filepath.Join(dst, rel)
		fi, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			if err := os.MkdirAll(out, fi.Mode().Perm()|0o700); err != nil {
				return err
			}
			return os.Chmod(out, fi.Mode().Perm()|0o700) // MkdirAll's mode is subject to umask
		case fi.Mode()&os.ModeSymlink != 0:
			text, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(text, out) //nolint:gosec // G122: copying from trusted stow directory
		case fi.Mode().IsRegular():
			data, err := os.ReadFile(p) //nolint:gosec // G122: copying from trusted stow directory
			if err != nil {
				return err
			}
			if err := os.WriteFile(out, data, fi.Mode().Perm()); err != nil {
				return err
			}
			return os.Chmod(out, fi.Mode().Perm())
		}
		return nil
	})
}

// materializeLink replaces the symlink l.Target with a real copy of what it points at. The stow
// directory is only ever read. A target that is no longer a symlink is left alone.
func materializeLink(l stowLink) error {
	fi, err := os.Lstat(l.Target)
	if err != nil {
		return err
	}
	if fi.Mode()&os.ModeSymlink == 0 {
		return nil
	}
	src, err := filepath.EvalSymlinks(l.Source)
	if err != nil {
		return err
	}
	if !l.Dir {
		sfi, err := os.Stat(src)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(src)
		if err != nil {
			return err
		}
		return atomicWrite(l.Target, data, sfi.Mode().Perm()) // the rename replaces the link itself
	}
	dir := filepath.Dir(l.Target)
	tmp := filepath.Join(dir, fmt.Sprintf(".dotsync-tmp-%d-%s", os.Getpid(), randSuffix()))
	if err := copyTree(src, tmp); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	if err := os.Remove(l.Target); err != nil {
		os.RemoveAll(tmp)
		return err
	}
	if err := os.Rename(tmp, l.Target); err != nil {
		return fmt.Errorf("%w (the copy is at %s; the original is still in %s)", err, tilde(tmp), tilde(src))
	}
	fsyncDir(dir)
	return nil
}

// preflightLink checks that l's source can be read without touching l.Target. Once a symlink is
// replaced by a real file, discoverStow can no longer find it, so we must know every link of an
// entry can be materialized before converting any of them.
func preflightLink(l stowLink) error {
	src, err := filepath.EvalSymlinks(l.Source)
	if err != nil {
		return err
	}
	if l.Dir {
		if _, err := os.ReadDir(src); err != nil {
			return err
		}
		return nil
	}
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	return f.Close()
}

// importRow is what the import will do with one entry.
type importRow struct {
	Entry         importEntry
	Op            *Op    // set when the entry will be added to the manifest
	Skip          string // set when nothing is done, and why
	SecretAllowed bool   // set when this entry looked like a secret and --allow-secrets let it through
	SecretSkipped bool   // set when this entry looked like a secret and was skipped because it was not
}

func (r importRow) action() string {
	switch {
	case r.Skip != "":
		return "SKIP: " + r.Skip
	case r.SecretAllowed:
		return "materialize + manage (secret allowed)"
	case r.Op != nil:
		return "materialize + manage"
	}
	return "materialize"
}

// stowSensitiveDirs hold private keys. Import never manages them as a directory, not even when
// Stow folded one into a single link: files that appear there later would start to sync.
func stowSensitiveDirs() []string {
	return []string{filepath.Join(homeDir(), ".ssh"), filepath.Join(homeDir(), ".gnupg")}
}

// stowNoPromote lists directories that never become one entry: those shared by many programs,
// and the sensitive ones.
func stowNoPromote() []string {
	out := append([]string{filepath.Join(homeDir(), ".local")}, stowSensitiveDirs()...)
	for name := range xdgDefaults {
		out = append(out, mustEnvDir(name))
	}
	return out
}

func inSensitiveDir(path string) bool {
	for _, d := range stowSensitiveDirs() {
		if isWithin(path, d) {
			return true
		}
	}
	return false
}

// managedState reports whether target already is, or lies inside, a managed entry (managed), or
// instead contains one (the source of that entry is returned as inside).
func managedState(c *Ctx, m *Manifest, target string) (managed bool, inside string) {
	for _, raw := range m.Entries {
		t, err := expandTarget(str(raw["target"]), c.Paths)
		if err != nil {
			continue
		}
		if isWithin(target, t) {
			return true, ""
		}
		if isWithin(t, target) {
			inside = rawSource(raw)
		}
	}
	return false, inside
}

// planImport decides per entry. Entries new to the manifest go through the same checks as
// 'add' (m is updated in memory only); entries already managed are only materialized.
func planImport(c *Ctx, m *Manifest, entries []importEntry, allowSecrets bool) []importRow {
	rows := make([]importRow, 0, len(entries))
	for _, e := range entries {
		row := importRow{Entry: e}
		managed, inside := managedState(c, m, e.Target)
		switch {
		case managed:
		case e.Kind == "dir" && inSensitiveDir(e.Target):
			row.Skip = "holds private keys; never managed as a directory (add its other files yourself)"
		case inside != "":
			row.Skip = fmt.Sprintf("overlaps managed entry '%s'", inside)
		default:
			desc := fmt.Sprintf("%s (from stow package %s)", filepath.Base(e.Target), e.Package)
			newOp := func(allow bool) (*Op, error) { // the same checks as 'add', applied to m only
				op, err := addOp(c, e.Target, addOptions{desc: desc, allowSecrets: allow})
				if err == nil {
					err = applyOpNoFS(c, m, op)
				}
				return op, err
			}
			op, err := newOp(false)
			var secErr *secretError
			if errors.As(err, &secErr) {
				if allowSecrets {
					op, err = newOp(true)
					if err == nil {
						row.SecretAllowed = true
					}
				} else {
					row.Skip = secErr.reason
					row.SecretSkipped = true
					op, err = nil, nil
				}
			}
			if err != nil {
				row.Skip = err.Error()
			} else if op != nil {
				row.Op = op
			}
		}
		rows = append(rows, row)
	}
	return rows
}

func printImportPlan(c *Ctx, rows []importRow) {
	w1, w2, w3 := len("PACKAGE"), len("TARGET"), len("KIND")
	target := func(r importRow) string {
		if r.Entry.Kind == "dir" {
			return tilde(r.Entry.Target) + "/"
		}
		return tilde(r.Entry.Target)
	}
	for _, r := range rows {
		w1, w2, w3 = max(w1, len(r.Entry.Package)), max(w2, len(target(r))), max(w3, len(r.Entry.kindLabel()))
	}
	c.say("  %-*s  %-*s  %-*s  %s", w1, "PACKAGE", w2, "TARGET", w3, "KIND", "ACTION")
	for _, r := range rows {
		c.say("  %-*s  %-*s  %-*s  %s", w1, r.Entry.Package, w2, target(r), w3, r.Entry.kindLabel(), r.action())
	}
}

func cmdImport(args []string, stdout, stderr io.Writer) (int, error) {
	// not "fs": this file imports io/fs
	fl := newFlags("import", "stow [options] <stow-dir> [package...]", stderr)
	apply := fl.Bool("apply", false, "replace the links with real files and manage them (default: only show the plan)")
	allowSecrets := fl.Bool("allow-secrets", false, "also import files that look like secrets (only those entries are marked allow_secrets)")
	noSync := fl.Bool("no-sync", false, "only queue the change; the next sync pushes it")
	pos, err := parseArgs(fl, args)
	if err != nil {
		return 2, err
	}
	if len(pos) == 0 || pos[0] != "stow" {
		return usageError(fl, "import needs a source; 'stow' (GNU Stow) is the one supported")
	}
	if pos = pos[1:]; len(pos) == 0 {
		return usageError(fl, "which stow directory?")
	}
	c, err := newCtx(true, false, stdout, stderr)
	if err != nil {
		return 1, err
	}
	stowDir := absPath(pos[0])
	fi, err := os.Stat(stowDir)
	if err != nil {
		return 1, err // *fs.PathError already names the path
	}
	if !fi.IsDir() {
		return 1, fmt.Errorf("%s is not a directory", tilde(stowDir))
	}
	code := 0
	err = withState(c, func(st *State) error {
		links, err := discoverStow(stowDir, homeDir(), pos[1:])
		if err != nil {
			return err
		}
		if len(links) == 0 {
			c.say("nothing to import: no links from the home directory into %s", tilde(stowDir))
			return nil
		}
		m, err := manifestWithPending(c, st)
		if err != nil {
			return err
		}
		pkgs := map[string]bool{}
		for _, l := range links {
			pkgs[l.Package] = true
		}
		c.say("found %d packages, %d links\n", len(pkgs), len(links))

		rows := planImport(c, m, groupStowLinks(links, homeDir(), stowNoPromote()), *allowSecrets)
		printImportPlan(c, rows)
		if !*allowSecrets {
			for _, r := range rows {
				if r.SecretSkipped {
					c.say("skipped secrets keep their link; re-run with --allow-secrets to import them too")
					break
				}
			}
		}
		c.say("")

		nLinks, nManage := 0, 0
		for _, r := range rows {
			if r.Skip != "" {
				continue
			}
			nLinks += len(r.Entry.Links)
			if r.Op != nil {
				nManage++
			}
		}
		if !*apply {
			c.say("dry run: nothing changed. Re-run with --apply to replace %d links and manage %d entries.", nLinks, nManage)
			return nil
		}
		code, err = applyImport(c, st, rows, stowDir, *noSync)
		return err
	})
	return code, err
}

// applyImport materializes every planned entry, queues the add ops of the new ones, and syncs.
// One entry failing does not stop the others. Every entry is pre-flighted before any of its links
// are touched, and the op of a newly managed entry is persisted right after it is queued: a
// materialized file whose op never reaches state.json would be permanently unmanaged, since
// discoverStow can no longer see a target once it stops being a symlink.
func applyImport(c *Ctx, st *State, rows []importRow, stowDir string, noSync bool) (int, error) {
	code, done := 0, 0
	var stillLinked []string // tilde targets of links not fully materialized in this run
	for _, r := range rows {
		if r.Skip != "" {
			for _, l := range r.Entry.Links {
				stillLinked = append(stillLinked, tilde(l.Target))
			}
			continue
		}
		var pfErr error
		for _, l := range r.Entry.Links {
			if pfErr = preflightLink(l); pfErr != nil {
				break
			}
		}
		if pfErr != nil {
			c.say("  FAILED        %s: %v", tilde(r.Entry.Target), pfErr)
			code = 1
			for _, l := range r.Entry.Links {
				stillLinked = append(stillLinked, tilde(l.Target))
			}
			continue
		}
		var failed error
		var converted []string
		for i, l := range r.Entry.Links {
			if failed = materializeLink(l); failed != nil {
				c.say("  FAILED        %s: %v", tilde(l.Target), failed)
				for _, rem := range r.Entry.Links[i:] { // the failing link and the ones never attempted
					stillLinked = append(stillLinked, tilde(rem.Target))
				}
				break
			}
			converted = append(converted, tilde(l.Target))
		}
		if failed != nil {
			code = 1
			if len(converted) > 0 {
				c.say("                already replaced with real files and NOT managed: %s — fix the problem, then run: dotsync add %s",
					strings.Join(converted, ", "), tilde(r.Entry.Target))
			}
			continue
		}
		done++
		if r.Op != nil {
			st.PendingOps = append(st.PendingOps, r.Op)
			if err := saveState(c, st); err != nil {
				c.say("  materialized  %s — but NOT managed (could not save state); once fixed, run: dotsync add %s",
					tilde(r.Entry.Target), tilde(r.Entry.Target))
				return 1, fmt.Errorf("saving state after %s: %w", tilde(r.Entry.Target), err)
			}
			c.say("  materialized  %s", tilde(r.Entry.Target))
			c.say("managing %s as '%s'", str(r.Op.Entry["target"]), str(r.Op.Entry["source"]))
		} else {
			c.say("  materialized  %s", tilde(r.Entry.Target))
		}
	}
	switch {
	case len(stillLinked) == 0 && done > 0:
		c.say("these entries no longer use %s; it can be removed once every machine has run this import", tilde(stowDir))
	case len(stillLinked) > 0:
		shown, suffix := stillLinked, ""
		if len(shown) > 3 {
			shown, suffix = shown[:3], ", …"
		}
		c.say("%d path(s) still link into %s (%s%s); keep it until they are handled",
			len(stillLinked), tilde(stowDir), strings.Join(shown, ", "), suffix)
	}
	if noSync {
		return code, nil
	}
	syncCode, err := syncAndReport(c)
	return max(code, syncCode), err
}
