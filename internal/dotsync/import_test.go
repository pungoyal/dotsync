package dotsync

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func isSymlink(t *testing.T, path string) bool {
	t.Helper()
	fi, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("lstat %s: %v", path, err)
	}
	return fi.Mode()&os.ModeSymlink != 0
}

func TestMaterializeFileLink(t *testing.T) {
	home := t.TempDir()
	src := filepath.Join(home, "dotfiles/scripts/bin/a")
	target := filepath.Join(home, "bin/a")
	mkfile(t, src, "#!/bin/sh\necho a\n", 0o755)
	symlink(t, "../dotfiles/scripts/bin/a", target)

	if err := materializeLink(stowLink{Package: "scripts", Target: target, Source: src}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(target)
	if err != nil || !fi.Mode().IsRegular() {
		t.Fatalf("target is not a regular file: %v %v", fi, err)
	}
	if fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %o, want 755", fi.Mode().Perm())
	}
	if b, _ := os.ReadFile(target); string(b) != "#!/bin/sh\necho a\n" {
		t.Fatalf("content = %q", b)
	}
	if b, _ := os.ReadFile(src); string(b) != "#!/bin/sh\necho a\n" {
		t.Fatalf("the stow directory was modified: %q", b)
	}
	// Running it again on a real file does nothing.
	if err := materializeLink(stowLink{Package: "scripts", Target: target, Source: src}); err != nil {
		t.Fatalf("second run: %v", err)
	}
}

func TestMaterializeFoldedDirectory(t *testing.T) {
	home := t.TempDir()
	src := filepath.Join(home, "dotfiles/fish/.config/fish")
	target := filepath.Join(home, ".config/fish")
	mkfile(t, filepath.Join(src, "config.fish"), "set -x A 1\n", 0o644)
	mkfile(t, filepath.Join(src, "functions/ll.fish"), "function ll; ls -l; end\n", 0o644)
	symlink(t, "config.fish", filepath.Join(src, "alias.fish")) // a link inside the package stays a link
	symlink(t, "../dotfiles/fish/.config/fish", target)
	if err := os.Chmod(filepath.Join(src, "functions"), 0o777); err != nil {
		t.Fatal(err)
	}

	if err := materializeLink(stowLink{Package: "fish", Target: target, Source: src, Dir: true}); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Lstat(target)
	if err != nil || !fi.IsDir() || fi.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("target is not a real directory: %v %v", fi, err)
	}
	if b, _ := os.ReadFile(filepath.Join(target, "functions/ll.fish")); string(b) != "function ll; ls -l; end\n" {
		t.Fatalf("nested file = %q", b)
	}
	if !isSymlink(t, filepath.Join(target, "alias.fish")) {
		t.Fatal("inner symlink was not kept as a symlink")
	}
	srcFi, err := os.Stat(filepath.Join(src, "functions"))
	if err != nil {
		t.Fatal(err)
	}
	dstFi, err := os.Stat(filepath.Join(target, "functions"))
	if err != nil {
		t.Fatal(err)
	}
	if dstFi.Mode().Perm() != srcFi.Mode().Perm() {
		t.Fatalf("copied subdirectory mode = %o, want %o (matching the source)", dstFi.Mode().Perm(), srcFi.Mode().Perm())
	}
	if _, err := os.Stat(filepath.Join(src, "config.fish")); err != nil {
		t.Fatalf("the stow directory was modified: %v", err)
	}
	left, _ := filepath.Glob(filepath.Join(home, ".config", ".dotsync-tmp-*"))
	if len(left) != 0 {
		t.Fatalf("temp copies left behind: %v", left)
	}
}

func TestImportStowDryRunChangesNothing(t *testing.T) {
	w := newWorld(t)
	a := w.machine("alpha")
	a.init()
	stow := buildStow(t, a.home)
	mkfile(t, filepath.Join(stow, "keys/.ssh/id_ed25519"), "not really a key\n", 0o600)
	symlink(t, "../dotfiles/keys/.ssh/id_ed25519", a.path(".ssh/id_ed25519"))
	before := w.remoteManifest()
	stateBefore, _ := os.ReadFile(a.path(".local/state/dotsync/state.json"))

	out := a.ok("import", "stow", stow)

	for _, want := range []string{
		"found 8 packages, 9 links",
		"~/.config/nvim", "dir (2 links, unfolded)",
		"~/.config/fish", "dir (folded)",
		"materialize + manage",
		"SKIP:", "id_ed25519",
		"skipped secrets keep their link; re-run with --allow-secrets to import them too",
		"dry run: nothing changed",
		"--apply to replace 8 links and manage 7 entries",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "refusing to manage it") {
		t.Fatalf("the SKIP reason should be short, not the full add-style message:\n%s", out)
	}
	if !isSymlink(t, a.path(".gitconfig")) || !isSymlink(t, a.path(".config/fish")) {
		t.Fatal("a dry run replaced a link")
	}
	if after := w.remoteManifest(); after != before {
		t.Fatalf("a dry run changed the remote manifest:\n%s", after)
	}
	if stateAfter, _ := os.ReadFile(a.path(".local/state/dotsync/state.json")); string(stateAfter) != string(stateBefore) {
		t.Fatal("a dry run changed state.json")
	}
}

func TestImportUsageErrors(t *testing.T) {
	w := newWorld(t)
	a := w.machine("alpha")
	if code, out := a.run("import", "stow", a.path("dotfiles")); code == 0 || !strings.Contains(out, "dotsync init") {
		t.Fatalf("import before init: exit %d\n%s", code, out)
	}
	a.init()
	if code, _ := a.run("import"); code != 2 {
		t.Fatalf("import with no source: exit %d, want 2", code)
	}
	if code, _ := a.run("import", "yadm", a.path("x")); code != 2 {
		t.Fatalf("import with an unknown source: exit %d, want 2", code)
	}
	if code, _ := a.run("import", "stow"); code != 2 {
		t.Fatalf("import stow with no directory: exit %d, want 2", code)
	}
	if code, out := a.run("import", "stow", a.path("missing")); code != 1 || !strings.Contains(out, "no such file or directory") {
		t.Fatalf("import stow on a missing dir: exit %d\n%s", code, out)
	}
	if err := os.WriteFile(a.path("afile"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code, out := a.run("import", "stow", a.path("afile")); code != 1 || !strings.Contains(out, "is not a directory") {
		t.Fatalf("import stow on a regular file: exit %d\n%s", code, out)
	}
	if err := os.MkdirAll(a.path("empty/pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	if out := a.ok("import", "stow", a.path("empty")); !strings.Contains(out, "nothing to import") {
		t.Fatalf("no links:\n%s", out)
	}
}

func TestImportStowNotesComeAfterHeader(t *testing.T) {
	w := newWorld(t)
	a := w.machine("alpha")
	a.init()
	stow := a.path("dotfiles")
	mkfile(t, filepath.Join(stow, "app/.config/app/settings.toml"), "x = 1\n", 0o644)
	mkfile(t, filepath.Join(stow, "app/.config/app/credentials.json"), "{}\n", 0o644)
	// A folded directory stays one entry even with a secret-looking file inside (an unfolded
	// one is imported file by file), so planning prints a note about the held-back file.
	symlink(t, "../dotfiles/app/.config/app", a.path(".config/app"))

	out := a.ok("import", "stow", stow)

	if !strings.Contains(out, "found 1 packages, 1 links") {
		t.Fatalf("output lacks the header:\n%s", out)
	}
	if !strings.Contains(out, "note:") {
		t.Fatalf("output lacks a secrets note:\n%s", out)
	}
	iHeader := strings.Index(out, "found 1 packages")
	iNote := strings.Index(out, "note:")
	iTable := strings.Index(out, "PACKAGE")
	if iHeader >= iNote || iNote >= iTable {
		t.Fatalf("wrong order (header=%d note=%d table=%d):\n%s", iHeader, iNote, iTable, out)
	}
	if !strings.Contains(out, "dir (folded)") || !strings.Contains(out, "materialize + manage") {
		t.Fatalf("output lacks expected plan:\n%s", out)
	}
}

func TestImportStowApply(t *testing.T) {
	w := newWorld(t)
	a := w.machine("alpha")
	a.init()
	stow := buildStow(t, a.home)
	mkfile(t, filepath.Join(stow, "keys/.ssh/id_ed25519"), "not really a key\n", 0o600)
	symlink(t, "../dotfiles/keys/.ssh/id_ed25519", a.path(".ssh/id_ed25519"))

	out := a.ok("import", "stow", stow, "--apply")

	for _, rel := range []string{".gitconfig", ".config/fish", ".config/nvim/init.lua", ".config/nvim/lua/opts.lua", ".zshrc", ".ssh/config", "bin/a", "bin/b"} {
		if isSymlink(t, a.path(rel)) {
			t.Fatalf("%s is still a symlink\n%s", rel, out)
		}
	}
	a.expect(".gitconfig", "[user]\n\tname = r\n")
	a.expect(".config/fish/config.fish", "set -x EDITOR nvim\n")
	a.expect(".zshrc", "# zsh\n")
	if fi, _ := os.Stat(a.path("bin/a")); fi.Mode().Perm() != 0o755 {
		t.Fatalf("bin/a mode = %o, want 755", fi.Mode().Perm())
	}
	if !isSymlink(t, a.path(".ssh/id_ed25519")) {
		t.Fatal("a skipped secret must keep its link")
	}
	if strings.Contains(out, "can be removed") {
		t.Fatalf("must not tell the user the stow dir can be removed while a secret still links into it:\n%s", out)
	}
	if !strings.Contains(out, "still link into") || !strings.Contains(out, "id_ed25519") {
		t.Fatalf("output must say which paths still link into the stow dir:\n%s", out)
	}
	a.expect("dotfiles/git/.gitconfig", "[user]\n\tname = r\n") // the stow dir is untouched
	a.expect("dotfiles/zsh/dot-zshrc", "# zsh\n")

	manifest := w.remoteManifest()
	for _, want := range []string{`"~/.gitconfig"`, `"~/.config/fish"`, `"~/.config/nvim"`, `"~/.zshrc"`, `"~/.ssh/config"`, `"~/bin/a"`, `"~/bin/b"`, "from stow package nvim"} {
		if !strings.Contains(manifest, want) {
			t.Fatalf("remote manifest lacks %s:\n%s", want, manifest)
		}
	}
	if strings.Contains(manifest, "id_ed25519") {
		t.Fatalf("the secret reached the manifest:\n%s", manifest)
	}
	if got := w.remoteFile("nvim/lua/opts.lua"); got != "-- opts\n" {
		t.Fatalf("remote nvim/lua/opts.lua = %q (a symlink was uploaded?)", got)
	}
	for _, e := range a.statusJSON()["entries"].([]any) {
		if row := e.(map[string]any); row["status"] != "ok" {
			t.Fatalf("entry %v has status %v", row["source"], row["status"])
		}
	}

	// A second run has only the skipped secret left; nothing new is managed.
	before := w.remoteManifest()
	out = a.ok("import", "stow", stow, "--apply")
	if strings.Contains(out, "materialize + manage") || w.remoteManifest() != before {
		t.Fatalf("second run was not a no-op:\n%s", out)
	}

	// And with the secret's package left out, there is nothing at all.
	out = a.ok("import", "stow", stow, "git", "nvim", "fish")
	if !strings.Contains(out, "nothing to import") {
		t.Fatalf("expected nothing to import:\n%s", out)
	}
}

// TestImportStowApplyAllowSecrets covers ruling 3: --allow-secrets only marks allow_secrets on
// the entries that needed it, not on every entry import manages.
func TestImportStowApplyAllowSecrets(t *testing.T) {
	w := newWorld(t)
	a := w.machine("alpha")
	a.init()
	stow := buildStow(t, a.home)
	mkfile(t, filepath.Join(stow, "keys/.ssh/id_ed25519"), "not really a key\n", 0o600)
	symlink(t, "../dotfiles/keys/.ssh/id_ed25519", a.path(".ssh/id_ed25519"))

	out := a.ok("import", "stow", stow, "--allow-secrets")
	if !strings.Contains(out, "secret allowed") {
		t.Fatalf("output should mark the key row as secret allowed:\n%s", out)
	}

	out = a.ok("import", "stow", stow, "--allow-secrets", "--apply")
	if isSymlink(t, a.path(".ssh/id_ed25519")) {
		t.Fatalf("the key should have been materialized with --allow-secrets:\n%s", out)
	}

	manifest := w.remoteManifest()
	var doc struct {
		Entries []map[string]any `json:"entries"`
	}
	if err := json.Unmarshal([]byte(manifest), &doc); err != nil {
		t.Fatalf("manifest is not valid JSON: %v\n%s", err, manifest)
	}
	entryFor := func(target string) map[string]any {
		for _, e := range doc.Entries {
			if e["target"] == target {
				return e
			}
		}
		t.Fatalf("manifest lacks an entry for %s:\n%s", target, manifest)
		return nil
	}
	if v := entryFor("~/.ssh/id_ed25519")["allow_secrets"]; v != true {
		t.Fatalf("~/.ssh/id_ed25519 entry should have allow_secrets=true, got %v", v)
	}
	if v := entryFor("~/.gitconfig")["allow_secrets"]; v != nil {
		t.Fatalf("~/.gitconfig entry should not carry allow_secrets, got %v", v)
	}
	if v := entryFor("~/.config/nvim")["allow_secrets"]; v != nil {
		t.Fatalf("~/.config/nvim entry should not carry allow_secrets, got %v", v)
	}
}

func TestImportStowApplyUnreadableSource(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read anything")
	}
	w := newWorld(t)
	a := w.machine("alpha")
	a.init()
	stow := buildStow(t, a.home)
	optsLua := filepath.Join(stow, "nvim/.config/nvim/lua/opts.lua")
	if err := os.Chmod(optsLua, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(optsLua, 0o644) })

	code, out := a.run("import", "stow", stow, "--apply")

	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "FAILED") || !strings.Contains(out, "~/.config/nvim") {
		t.Fatalf("output lacks a FAILED line for ~/.config/nvim:\n%s", out)
	}
	if !isSymlink(t, a.path(".config/nvim/init.lua")) || !isSymlink(t, a.path(".config/nvim/lua/opts.lua")) {
		t.Fatal("pre-flight must fail before touching any link of the entry")
	}
	if isSymlink(t, a.path(".gitconfig")) {
		t.Fatal("another entry should still have been imported")
	}
	manifest := w.remoteManifest()
	if !strings.Contains(manifest, `"~/.gitconfig"`) {
		t.Fatalf("remote manifest lacks ~/.gitconfig:\n%s", manifest)
	}
	if strings.Contains(manifest, `"~/.config/nvim"`) {
		t.Fatalf("the failed entry must not reach the manifest:\n%s", manifest)
	}
}

func TestImportStowApplyNoSync(t *testing.T) {
	w := newWorld(t)
	a := w.machine("alpha")
	a.init()
	stow := buildStow(t, a.home)
	before := w.remoteManifest()

	out := a.ok("import", "stow", stow, "git", "--apply", "--no-sync")

	if isSymlink(t, a.path(".gitconfig")) {
		t.Fatal("--no-sync must still materialize the link")
	}
	if w.remoteManifest() != before {
		t.Fatal("--no-sync must not push")
	}
	if !strings.Contains(out, "can be removed") {
		t.Fatalf("with nothing skipped, output should say the stow dir can be removed:\n%s", out)
	}

	a.ok("sync")

	if after := w.remoteManifest(); !strings.Contains(after, `"~/.gitconfig"`) {
		t.Fatalf("the queued op was not persisted for the later sync:\n%s", after)
	}
}

// TestImportStowApplyMidEntryFailure covers ruling C: pre-flight passes for both of nvim's links
// (their sources are readable), but materializing the second one fails because its target
// directory cannot be written to. The first link is left converted and unmanaged; the output must
// say so and name the remedy.
func TestImportStowApplyMidEntryFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write through a read-only directory")
	}
	w := newWorld(t)
	a := w.machine("alpha")
	a.init()
	stow := buildStow(t, a.home)
	luaTargetDir := a.path(".config/nvim/lua") // holds only opts.lua's link; init.lua's parent is a sibling dir
	if err := os.Chmod(luaTargetDir, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(luaTargetDir, 0o755) })

	code, out := a.run("import", "stow", stow, "--apply")

	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "FAILED") || !strings.Contains(out, "opts.lua") {
		t.Fatalf("output lacks a FAILED line for opts.lua:\n%s", out)
	}
	if !strings.Contains(out, "already replaced with real files and NOT managed") ||
		!strings.Contains(out, "~/.config/nvim/init.lua") || !strings.Contains(out, "dotsync add ~/.config/nvim") {
		t.Fatalf("output lacks the partial-conversion remedy:\n%s", out)
	}
	if isSymlink(t, a.path(".config/nvim/init.lua")) {
		t.Fatal("init.lua should already have been converted before opts.lua failed")
	}
	if !isSymlink(t, a.path(".config/nvim/lua/opts.lua")) {
		t.Fatal("opts.lua's materialize should have failed, leaving its symlink in place")
	}
	if strings.Contains(w.remoteManifest(), `"~/.config/nvim"`) {
		t.Fatalf("the failed entry must not reach the manifest:\n%s", w.remoteManifest())
	}
}

// TestImportStowDirOutsideHome covers ruling 11: a stow directory that lives outside $HOME
// (for example on another volume) must still be importable.
func TestImportStowDirOutsideHome(t *testing.T) {
	w := newWorld(t)
	a := w.machine("alpha")
	a.init()
	stow := filepath.Join(w.root, "stow-elsewhere")
	mkfile(t, filepath.Join(stow, "git/.gitconfig"), "[user]\n\tname = r\n", 0o644)
	symlink(t, filepath.Join(stow, "git/.gitconfig"), a.path(".gitconfig")) // absolute, outside home

	a.ok("import", "stow", stow, "--apply")

	if isSymlink(t, a.path(".gitconfig")) {
		t.Fatal(".gitconfig is still a symlink")
	}
	a.expect(".gitconfig", "[user]\n\tname = r\n")
	manifest := w.remoteManifest()
	if !strings.Contains(manifest, `"~/.gitconfig"`) {
		t.Fatalf("remote manifest lacks ~/.gitconfig:\n%s", manifest)
	}
	if b, err := os.ReadFile(filepath.Join(stow, "git/.gitconfig")); err != nil || string(b) != "[user]\n\tname = r\n" {
		t.Fatalf("the outside stow file was touched: %q, %v", b, err)
	}
}

func remoteHead(w *world) string {
	return strings.TrimSpace(gitCmd(w.t, w.root, "--git-dir", w.remote, "rev-parse", "main"))
}

func TestImportStowOnSecondMachine(t *testing.T) {
	w := newWorld(t)
	a, b := w.machine("alpha"), w.machine("beta")
	a.init()
	a.ok("import", "stow", buildStow(t, a.home), "--apply")

	// beta has the same stow setup. init alone makes it sync…
	stowB := buildStow(t, b.home)
	b.init()
	if isSymlink(t, b.path(".config/nvim/init.lua")) {
		t.Fatal("init should have replaced the per-file link inside the managed directory")
	}
	if !isSymlink(t, b.path(".gitconfig")) || !isSymlink(t, b.path(".config/fish")) {
		t.Fatal("init is expected to follow top-level links, not replace them")
	}

	// …and import cuts the remaining ties to ~/dotfiles without touching the shared manifest.
	head := remoteHead(w)
	out := b.ok("import", "stow", stowB, "--apply")
	if strings.Contains(out, "materialize + manage") {
		t.Fatalf("beta tried to manage entries that are already shared:\n%s", out)
	}
	for _, rel := range []string{".gitconfig", ".config/fish", ".zshrc", ".ssh/config", "bin/a", "bin/b"} {
		if isSymlink(t, b.path(rel)) {
			t.Fatalf("%s is still a symlink on beta\n%s", rel, out)
		}
	}
	if got := remoteHead(w); got != head {
		t.Fatalf("beta's import pushed a commit: %s -> %s", head, got)
	}
	for _, e := range b.statusJSON()["entries"].([]any) {
		if row := e.(map[string]any); row["status"] != "ok" {
			t.Fatalf("beta: entry %v has status %v", row["source"], row["status"])
		}
	}

	// Edits now flow between real files.
	b.write(".gitconfig", "[user]\n\tname = beta\n")
	b.ok("sync")
	a.ok("sync")
	a.expect(".gitconfig", "[user]\n\tname = beta\n")
}

func TestImportStowSecondMachineWithDifferentContent(t *testing.T) {
	w := newWorld(t)
	a, b := w.machine("alpha"), w.machine("beta")
	a.init()
	a.ok("import", "stow", buildStow(t, a.home), "--apply")

	stowB := buildStow(t, b.home)
	mkfile(t, filepath.Join(stowB, "git/.gitconfig"), "[user]\n\tname = beta's own\n", 0o644)
	b.init() // adopts the shared version through the link, backing up beta's
	b.expect(".gitconfig", "[user]\n\tname = r\n")
	// The two candidate relative paths cover the common case, but under some test setups (notably
	// macOS, where t.TempDir() lives under /var, itself a symlink to /private/var) $HOME resolves
	// to a different absolute path than filepath.EvalSymlinks would follow the link's target
	// through, and homeRel() (paths.go, pre-existing, unrelated to this feature) files the backup
	// under the fully-resolved absolute path instead. Search the whole backups tree so the test
	// still verifies beta's version was not lost, wherever it landed.
	kept := append(b.backups(".gitconfig"), b.backups("dotfiles/git/.gitconfig")...)
	if len(kept) != 1 {
		kept = nil
		dirs, _ := filepath.Glob(filepath.Join(b.home, ".local/state/dotsync/backups/*"))
		for _, d := range dirs {
			_ = filepath.Walk(d, func(p string, info os.FileInfo, err error) error {
				if err != nil || info.IsDir() {
					return nil
				}
				if strings.HasSuffix(filepath.ToSlash(p), "git/.gitconfig") {
					if b, err := os.ReadFile(p); err == nil {
						kept = append(kept, string(b))
					}
				}
				return nil
			})
		}
	}
	if len(kept) != 1 || kept[0] != "[user]\n\tname = beta's own\n" {
		t.Fatalf("beta's version was not backed up: %q", kept)
	}

	b.ok("import", "stow", stowB, "--apply")
	if isSymlink(t, b.path(".gitconfig")) {
		t.Fatal(".gitconfig is still a symlink on beta")
	}
	b.expect(".gitconfig", "[user]\n\tname = r\n")
	if s := entryStatusOf(b.statusJSON(), "gitconfig"); s != "ok" {
		t.Fatalf("status = %q, want ok", s)
	}
}

func TestImportStowPlanForManagedAndOverlappingEntries(t *testing.T) {
	w := newWorld(t)
	a := w.machine("alpha")
	a.init()
	stow := buildStow(t, a.home)
	a.ok("add", a.path(".gitconfig"))
	a.ok("add", a.path(".config/nvim/init.lua"))

	out := a.ok("import", "stow", stow)

	lineWith := func(needle string) string {
		for _, line := range strings.Split(out, "\n") {
			if strings.Contains(line, needle) {
				return line
			}
		}
		t.Fatalf("no line contains %q:\n%s", needle, out)
		return ""
	}
	gitLine := lineWith("~/.gitconfig")
	if !strings.HasSuffix(strings.TrimRight(gitLine, " "), "materialize") || strings.Contains(gitLine, "materialize + manage") {
		t.Fatalf("~/.gitconfig line = %q, want it to end with plain 'materialize'", gitLine)
	}
	nvimLine := lineWith("~/.config/nvim/")
	if !strings.Contains(nvimLine, "SKIP: overlaps managed entry 'nvim/init.lua'") {
		t.Fatalf("~/.config/nvim/ line = %q, want an overlap skip", nvimLine)
	}
	zshLine := lineWith("~/.zshrc")
	if !strings.Contains(zshLine, "materialize + manage") {
		t.Fatalf("~/.zshrc line = %q, want it planned normally", zshLine)
	}
	if !isSymlink(t, a.path(".gitconfig")) || !isSymlink(t, a.path(".config/nvim/lua/opts.lua")) {
		t.Fatal("a dry run replaced a link")
	}
}

// TestImportStowApplySaveStateFails covers ruling A: materializing succeeds but saving the
// queued op to state.json fails. The file is already a real file on disk and must not be
// silently unmanaged: the output must name it and the remedy, and the stow-dir summary line
// must not be printed on this path.
func TestImportStowApplySaveStateFails(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores permission bits")
	}
	w := newWorld(t)
	a := w.machine("alpha")
	a.init()
	stow := buildStow(t, a.home)
	stateDir := a.path(".local/state/dotsync")
	if err := os.Chmod(stateDir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(stateDir, 0o700) })

	code, out := a.run("import", "stow", stow, "git", "--apply", "--no-sync")

	if code != 1 {
		t.Fatalf("exit code = %d, want 1\n%s", code, out)
	}
	if !strings.Contains(out, "materialized") || !strings.Contains(out, "NOT managed") || !strings.Contains(out, "~/.gitconfig") {
		t.Fatalf("output lacks the materialized-but-not-managed line:\n%s", out)
	}
	if strings.Contains(out, "can be removed") || strings.Contains(out, "still link into") {
		t.Fatalf("the stow-dir summary must not be printed on this path:\n%s", out)
	}
	if isSymlink(t, a.path(".gitconfig")) {
		t.Fatal(".gitconfig should already have been materialized before saveState failed")
	}
}

// A directory that holds only a secret must not become a managed directory: the key is skipped
// as a file, keeps its link, and ~/.ssh never enters the manifest.
func TestImportStowSecretOnlyDirectory(t *testing.T) {
	w := newWorld(t)
	a := w.machine("alpha")
	a.init()
	stow := a.path("dotfiles")
	mkfile(t, filepath.Join(stow, "git/.gitconfig"), "[user]\n\tname = r\n", 0o644)
	mkfile(t, filepath.Join(stow, "keys/.ssh/id_ed25519"), "not really a key\n", 0o600)
	symlink(t, "dotfiles/git/.gitconfig", a.path(".gitconfig"))
	symlink(t, "../dotfiles/keys/.ssh/id_ed25519", a.path(".ssh/id_ed25519"))

	out := a.ok("import", "stow", stow, "--apply")

	var keyLine string
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "~/.ssh") && strings.Contains(line, "keys") {
			keyLine = line
			break
		}
	}
	if !strings.Contains(keyLine, "~/.ssh/id_ed25519") || !strings.Contains(keyLine, "file") || !strings.Contains(keyLine, "SKIP:") {
		t.Fatalf("the key must be planned as a skipped file, got %q\n%s", keyLine, out)
	}
	if !isSymlink(t, a.path(".ssh/id_ed25519")) {
		t.Fatal("a skipped key must keep its link")
	}
	if strings.Contains(out, "can be removed") || !strings.Contains(out, "still link into") {
		t.Fatalf("the key still links into the stow dir:\n%s", out)
	}
	if manifest := w.remoteManifest(); strings.Contains(manifest, ".ssh") || !strings.Contains(manifest, `"~/.gitconfig"`) {
		t.Fatalf("remote manifest:\n%s", manifest)
	}
	if status := a.ok("status"); strings.Contains(status, "BLOCKED") {
		t.Fatalf("nothing should be blocked:\n%s", status)
	}
}

// Stow folds ~/.ssh into one link when the directory did not exist. It is never managed whole.
func TestImportStowFoldedSensitiveDirectory(t *testing.T) {
	w := newWorld(t)
	a := w.machine("alpha")
	a.init()
	stow := a.path("dotfiles")
	mkfile(t, filepath.Join(stow, "keys/.ssh/config"), "Host *\n", 0o644)
	mkfile(t, filepath.Join(stow, "keys/.ssh/id_ed25519"), "not really a key\n", 0o600)
	symlink(t, "dotfiles/keys/.ssh", a.path(".ssh"))

	out := a.ok("import", "stow", stow, "--apply")

	if !strings.Contains(out, "~/.ssh/") || !strings.Contains(out, "SKIP: holds private keys") {
		t.Fatalf("a folded ~/.ssh must be skipped:\n%s", out)
	}
	if !isSymlink(t, a.path(".ssh")) {
		t.Fatal("a skipped directory must keep its link")
	}
	if strings.Contains(out, "can be removed") || !strings.Contains(out, "still link into") {
		t.Fatalf("~/.ssh still links into the stow dir:\n%s", out)
	}
	if manifest := w.remoteManifest(); strings.Contains(manifest, ".ssh") {
		t.Fatalf("~/.ssh reached the manifest:\n%s", manifest)
	}
}
