package dotsync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// world is a shared bare remote plus any number of simulated machines, each with its own $HOME.
type world struct {
	t      *testing.T
	root   string
	remote string
}

type machine struct {
	w    *world
	name string
	home string
}

func newWorld(t *testing.T) *world {
	t.Helper()
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("DOTSYNC_NO_NOTIFY", "1")
	for _, v := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME"} {
		t.Setenv(v, "")
	}
	gitCmd(t, root, "init", "-q", "--bare", "--initial-branch=main", remote)
	return &world{t: t, root: root, remote: remote}
}

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=test", "-c", "user.email=t@example.com"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (w *world) machine(name string) *machine {
	home := filepath.Join(w.root, name)
	if err := os.MkdirAll(home, 0o755); err != nil {
		w.t.Fatal(err)
	}
	return &machine{w: w, name: name, home: home}
}

func (m *machine) activate() {
	m.w.t.Setenv("HOME", m.home)
	m.w.t.Setenv("DOTSYNC_HOSTNAME", m.name)
}

func (m *machine) run(args ...string) (int, string) {
	m.w.t.Helper()
	m.activate()
	var out bytes.Buffer
	code := Run(args, &out, &out)
	return code, out.String()
}

// ok runs a command that must succeed.
func (m *machine) ok(args ...string) string {
	m.w.t.Helper()
	code, out := m.run(args...)
	if code != 0 {
		m.w.t.Fatalf("[%s] dotsync %s: exit %d\n%s", m.name, strings.Join(args, " "), code, out)
	}
	return out
}

func (m *machine) init(extra ...string) string {
	return m.ok(append([]string{"init", "-no-agent", "-no-install"}, append(extra, m.w.remote)...)...)
}

func (m *machine) path(rel string) string { return filepath.Join(m.home, filepath.FromSlash(rel)) }

func (m *machine) write(rel, content string) {
	m.w.t.Helper()
	p := m.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		m.w.t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		m.w.t.Fatal(err)
	}
}

func (m *machine) read(rel string) string {
	m.w.t.Helper()
	b, err := os.ReadFile(m.path(rel))
	if err != nil {
		m.w.t.Fatalf("[%s] read %s: %v", m.name, rel, err)
	}
	return string(b)
}

func (m *machine) exists(rel string) bool {
	_, err := os.Lstat(m.path(rel))
	return err == nil
}

func (m *machine) expect(rel, want string) {
	m.w.t.Helper()
	if got := m.read(rel); got != want {
		m.w.t.Fatalf("[%s] %s = %q, want %q", m.name, rel, got, want)
	}
}

// backups returns the content of every backup of rel on this machine.
func (m *machine) backups(rel string) []string {
	var out []string
	dirs, _ := filepath.Glob(filepath.Join(m.home, ".local/state/dotsync/backups/*"))
	for _, d := range dirs {
		if b, err := os.ReadFile(filepath.Join(d, filepath.FromSlash(rel))); err == nil {
			out = append(out, string(b))
		}
	}
	return out
}

func (m *machine) statusJSON() map[string]any {
	m.w.t.Helper()
	out := m.ok("status", "-json")
	var v map[string]any
	if err := json.Unmarshal([]byte(out), &v); err != nil {
		m.w.t.Fatalf("status -json: %v\n%s", err, out)
	}
	return v
}

func entryStatusOf(v map[string]any, source string) string {
	for _, e := range v["entries"].([]any) {
		row := e.(map[string]any)
		if row["source"] == source {
			return row["status"].(string)
		}
	}
	return ""
}

func (w *world) remoteManifest() string {
	return gitCmd(w.t, w.root, "--git-dir", w.remote, "show", "main:manifest.json")
}

func (w *world) remoteFile(path string) string {
	return gitCmd(w.t, w.root, "--git-dir", w.remote, "show", "main:files/"+path)
}

// ---------------------------------------------------------------------------

func TestDecide(t *testing.T) {
	cases := []struct {
		L, R, B          string
		dir, root, adopt bool
		want             string
	}{
		{"x", "x", "", false, true, true, actOK},
		{"x", "y", "x", false, true, true, actDownload},
		{"x", "", "x", true, true, true, actDeleteLocal},
		{"x", "", "x", false, true, true, actUpload},
		{"y", "x", "x", false, true, true, actUpload},
		{"", "x", "x", true, true, true, actDeleteRepo},
		{"", "x", "x", true, false, true, actDownload}, // whole directory missing: restore, don't delete everywhere
		{"", "x", "x", false, true, true, actDownload}, // missing file entry: restore
		{"a", "b", "c", false, true, true, actConflict},
		{"a", "b", "", false, true, true, actDownload}, // newly managed here, adopt shared version
		{"a", "b", "", false, true, false, actConflict},
		{"", "b", "c", true, true, true, actDownload}, // modification beats deletion
		{"a", "", "c", true, true, true, actUpload},
		{"a", "", "", true, true, true, actUpload},
	}
	for _, c := range cases {
		if got := decide(c.L, c.R, c.B, c.dir, c.root, c.adopt); got != c.want {
			t.Errorf("decide(L=%q R=%q B=%q dir=%v root=%v adopt=%v) = %s, want %s", c.L, c.R, c.B, c.dir, c.root, c.adopt, got, c.want)
		}
	}
}

func TestPropagationBothWays(t *testing.T) {
	w := newWorld(t)
	a, b := w.machine("alpha"), w.machine("beta")
	a.init()
	a.write(".config/fish/config.fish", "set -x EDITOR vim\n")
	a.ok("add", "-d", "Fish shell configuration", a.path(".config/fish/config.fish"))

	if !strings.Contains(w.remoteManifest(), `"source": "fish/config.fish"`) ||
		!strings.Contains(w.remoteManifest(), `"target": "~/.config/fish/config.fish"`) {
		t.Fatalf("manifest not pushed:\n%s", w.remoteManifest())
	}

	b.init()
	b.expect(".config/fish/config.fish", "set -x EDITOR vim\n")

	b.write(".config/fish/config.fish", "set -x EDITOR nvim\n")
	b.ok("sync")
	a.ok("sync")
	a.expect(".config/fish/config.fish", "set -x EDITOR nvim\n")
	if got := a.backups(".config/fish/config.fish"); len(got) != 1 || got[0] != "set -x EDITOR vim\n" {
		t.Fatalf("expected a backup of the replaced version, got %q", got)
	}
	if s := entryStatusOf(a.statusJSON(), "fish/config.fish"); s != "ok" {
		t.Fatalf("status = %q", s)
	}
}

func TestConcurrentEditsBecomeConflict(t *testing.T) {
	w := newWorld(t)
	a, b := w.machine("alpha"), w.machine("beta")
	a.init()
	a.write(".gitconfig", "[user]\n\tname = me\n")
	a.ok("add", a.path(".gitconfig"))
	b.init()

	a.write(".gitconfig", "[user]\n\tname = alpha edit\n")
	b.write(".gitconfig", "[user]\n\tname = beta edit\n")
	a.ok("sync")
	_, out := b.run("sync")
	if !strings.Contains(out, "CONFLICT") {
		t.Fatalf("expected conflict, got:\n%s", out)
	}
	b.expect(".gitconfig", "[user]\n\tname = beta edit\n") // untouched
	b.ok("sync")                                           // stays a conflict, still untouched
	b.expect(".gitconfig", "[user]\n\tname = beta edit\n")
	if s := entryStatusOf(b.statusJSON(), "gitconfig"); s != "CONFLICT" {
		t.Fatalf("status = %q", s)
	}
	if out := b.ok("diff", b.path(".gitconfig")); !strings.Contains(out, "-\tname = alpha edit") || !strings.Contains(out, "+\tname = beta edit") {
		t.Fatalf("diff:\n%s", out)
	}
	if w.remoteFile("gitconfig") != "[user]\n\tname = alpha edit\n" {
		t.Fatal("conflict leaked into the remote")
	}

	b.ok("resolve", b.path(".gitconfig"), "--keep", "local")
	a.ok("sync")
	a.expect(".gitconfig", "[user]\n\tname = beta edit\n")
	if s := entryStatusOf(b.statusJSON(), "gitconfig"); s != "ok" {
		t.Fatalf("status after resolve = %q", s)
	}
}

func TestResolveKeepRemote(t *testing.T) {
	w := newWorld(t)
	a, b := w.machine("alpha"), w.machine("beta")
	a.init()
	a.write(".vimrc", "one\n")
	a.ok("add", a.path(".vimrc"))
	b.init()
	a.write(".vimrc", "alpha\n")
	b.write(".vimrc", "beta\n")
	a.ok("sync")
	b.ok("sync")
	b.ok("resolve", "vimrc", "--keep", "remote")
	b.expect(".vimrc", "alpha\n")
	if got := b.backups(".vimrc"); len(got) != 1 || got[0] != "beta\n" {
		t.Fatalf("backups = %q", got)
	}
}

func TestExistingFilesOnNewMachine(t *testing.T) {
	w := newWorld(t)
	a, b, c := w.machine("alpha"), w.machine("beta"), w.machine("gamma")
	a.init()
	a.write(".bashrc", "shared\n")
	a.ok("add", a.path(".bashrc"))

	b.write(".bashrc", "distro default\n")
	b.init()
	b.expect(".bashrc", "shared\n")
	if got := b.backups(".bashrc"); len(got) != 1 || got[0] != "distro default\n" {
		t.Fatalf("pre-existing file not backed up: %q", got)
	}

	c.write(".bashrc", "gamma's own\n")
	c.init("-keep-existing")
	c.expect(".bashrc", "gamma's own\n")
	if s := entryStatusOf(c.statusJSON(), "bashrc"); s != "CONFLICT" {
		t.Fatalf("status = %q", s)
	}
}

func TestDirectoryEntry(t *testing.T) {
	w := newWorld(t)
	a, b := w.machine("alpha"), w.machine("beta")
	a.init()
	a.write(".config/nvim/init.lua", "require('x')\n")
	a.write(".config/nvim/lua/x.lua", "return {}\n")
	a.write(".config/nvim/old.lua", "old\n")
	a.write(".config/nvim/.DS_Store", "junk")
	a.write(".config/nvim/cache/big", "cache")
	if err := os.WriteFile(a.path(".config/nvim/run.sh"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("init.lua", a.path(".config/nvim/link.lua")); err != nil {
		t.Fatal(err)
	}
	a.ok("add", "-ignore", "cache", a.path(".config/nvim"))

	b.init()
	b.expect(".config/nvim/lua/x.lua", "return {}\n")
	if b.exists(".config/nvim/.DS_Store") || b.exists(".config/nvim/cache/big") {
		t.Fatal("ignored files were synced")
	}
	if fi, err := os.Stat(b.path(".config/nvim/run.sh")); err != nil || fi.Mode().Perm()&0o100 == 0 {
		t.Fatalf("exec bit not synced: %v %v", fi, err)
	}
	if l, err := os.Readlink(b.path(".config/nvim/link.lua")); err != nil || l != "init.lua" {
		t.Fatalf("symlink not synced: %q %v", l, err)
	}

	// Additions and deletions inside the directory propagate; deletions are backed up.
	os.Remove(a.path(".config/nvim/old.lua"))
	a.write(".config/nvim/lua/y.lua", "new\n")
	a.ok("sync")
	b.ok("sync")
	if b.exists(".config/nvim/old.lua") {
		t.Fatal("deletion did not propagate")
	}
	if got := b.backups(".config/nvim/old.lua"); len(got) != 1 {
		t.Fatalf("deleted file not backed up: %q", got)
	}
	b.expect(".config/nvim/lua/y.lua", "new\n")

	// Losing the whole directory restores it rather than deleting it everywhere.
	os.RemoveAll(b.path(".config/nvim"))
	b.ok("sync")
	b.expect(".config/nvim/init.lua", "require('x')\n")
	a.ok("sync")
	a.expect(".config/nvim/init.lua", "require('x')\n")
}

func TestRemoveStopsManagingButKeepsFiles(t *testing.T) {
	w := newWorld(t)
	a, b := w.machine("alpha"), w.machine("beta")
	a.init()
	a.write(".tmux.conf", "set -g mouse on\n")
	a.ok("add", a.path(".tmux.conf"))
	b.init()
	a.ok("remove", "~/.tmux.conf")
	b.ok("sync")
	b.expect(".tmux.conf", "set -g mouse on\n")
	a.expect(".tmux.conf", "set -g mouse on\n")
	if strings.Contains(w.remoteManifest(), "tmux") {
		t.Fatal("entry still in manifest")
	}
	// Local edits are now unmanaged and stay local.
	b.write(".tmux.conf", "local only\n")
	b.ok("sync")
	a.ok("sync")
	a.expect(".tmux.conf", "set -g mouse on\n")
}

func TestSecretsAreNotUploaded(t *testing.T) {
	w := newWorld(t)
	a := w.machine("alpha")
	a.init()

	a.write(".aws/config", "[default]\naws_access_key_id = "+fakeAWSKey+"\n")
	if code, out := a.run("add", a.path(".aws/config")); code == 0 || !strings.Contains(out, "AWS access key") {
		t.Fatalf("secret was accepted (exit %d):\n%s", code, out)
	}
	a.write(".ssh/id_ed25519", "-----BEGIN OPENSSH PRIVATE KEY-----\n")
	if code, _ := a.run("add", a.path(".ssh/id_ed25519")); code == 0 {
		t.Fatal("private key accepted")
	}

	// A secret added later to a managed file is held back and reported.
	a.write(".zshrc", "alias ll='ls -l'\n")
	a.ok("add", a.path(".zshrc"))
	a.write(".zshrc", "export GITHUB_TOKEN=ghp_"+strings.Repeat("x", 36)+"\n")
	_, out := a.run("sync")
	if !strings.Contains(out, "BLOCKED") {
		t.Fatalf("expected BLOCKED:\n%s", out)
	}
	if w.remoteFile("zshrc") != "alias ll='ls -l'\n" {
		t.Fatal("secret reached the remote")
	}

	// Inside a managed directory, credential-looking files stay local.
	a.write(".config/gh/config.yml", "editor: vim\n")
	a.write(".config/gh/hosts.yml", "github.com:\n  oauth_token: x\n")
	a.ok("add", a.path(".config/gh"))
	if strings.Contains(gitCmd(t, w.root, "--git-dir", w.remote, "ls-tree", "-r", "--name-only", "main"), "hosts.yml") {
		t.Fatal("hosts.yml uploaded")
	}
}

func TestOfflineThenReconnect(t *testing.T) {
	w := newWorld(t)
	a, b := w.machine("alpha"), w.machine("beta")
	a.init()
	a.write(".inputrc", "set editing-mode vi\n")
	a.ok("add", a.path(".inputrc"))
	b.init()

	away := w.remote + ".away"
	if err := os.Rename(w.remote, away); err != nil {
		t.Fatal(err)
	}
	a.write(".inputrc", "set editing-mode emacs\n")
	a.write(".curlrc", "silent\n")
	code, out := a.run("add", a.path(".curlrc"))
	if code != 0 || !strings.Contains(out, "unreachable") {
		t.Fatalf("offline add: exit %d\n%s", code, out)
	}
	if st := a.statusJSON(); len(st["pending_ops"].([]any)) != 1 {
		t.Fatalf("pending op not recorded: %v", st["pending_ops"])
	}
	if err := os.Rename(away, w.remote); err != nil {
		t.Fatal(err)
	}
	a.ok("sync")
	b.ok("sync")
	b.expect(".inputrc", "set editing-mode emacs\n")
	b.expect(".curlrc", "silent\n")
	if st := a.statusJSON(); st["pending_ops"] != nil {
		t.Fatalf("pending ops not cleared: %v", st["pending_ops"])
	}
}

func TestPushRaceIsRetried(t *testing.T) {
	w := newWorld(t)
	a, b := w.machine("alpha"), w.machine("beta")
	a.init()
	a.write(".one", "1\n")
	a.write(".two", "2\n")
	a.ok("add", a.path(".one"), a.path(".two"))
	b.init()

	// While alpha is about to push, beta pushes its own change.
	fired := false
	testHookBeforePush = func() {
		if fired {
			return
		}
		fired = true
		b.write(".two", "2 from beta\n")
		b.ok("sync")
		a.activate()
	}
	defer func() { testHookBeforePush = nil }()
	a.write(".one", "1 from alpha\n")
	a.ok("sync")
	testHookBeforePush = nil
	if !fired {
		t.Fatal("hook did not run")
	}
	if w.remoteFile("one") != "1 from alpha\n" || w.remoteFile("two") != "2 from beta\n" {
		t.Fatalf("lost an update: one=%q two=%q", w.remoteFile("one"), w.remoteFile("two"))
	}
	a.expect(".two", "2 from beta\n")
}

func TestConcurrentAddsOfDifferentFiles(t *testing.T) {
	w := newWorld(t)
	a, b := w.machine("alpha"), w.machine("beta")
	a.init()
	b.init()
	a.write(".a", "a\n")
	b.write(".b", "b\n")
	a.ok("add", "-no-sync", a.path(".a"))
	b.ok("add", "-no-sync", b.path(".b"))
	a.ok("sync")
	b.ok("sync")
	a.ok("sync")
	a.expect(".b", "b\n")
	b.expect(".a", "a\n")
}

func TestPermissionsPreservedAndPortableTargets(t *testing.T) {
	w := newWorld(t)
	a, b := w.machine("alpha"), w.machine("beta")
	a.init()
	a.write(".ssh/config", "Host *\n  ServerAliveInterval 60\n")
	a.ok("add", a.path(".ssh/config"))
	b.write(".ssh/config", "old\n")
	os.Chmod(b.path(".ssh/config"), 0o600)
	b.init()
	b.expect(".ssh/config", "Host *\n  ServerAliveInterval 60\n")
	if fi, _ := os.Stat(b.path(".ssh/config")); fi.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", fi.Mode().Perm())
	}

	// A target written with $XDG_CONFIG_HOME lands wherever that points on each machine,
	// and os-filtered entries are skipped elsewhere.
	other := "linux"
	if osName() == "linux" {
		other = "darwin"
	}
	manifest := `{"version":1,"entries":[
	  {"source":"git/ignore","target":"$XDG_CONFIG_HOME/git/ignore","description":"global gitignore"},
	  {"source":"only-other","target":"~/.only-other","description":"x","os":["` + other + `"]}]}`
	clone := filepath.Join(w.root, "manual")
	gitCmd(t, w.root, "clone", "-q", w.remote, clone)
	os.WriteFile(filepath.Join(clone, "manifest.json"), []byte(manifest), 0o644)
	os.MkdirAll(filepath.Join(clone, "files/git"), 0o755)
	os.WriteFile(filepath.Join(clone, "files/git/ignore"), []byte(".DS_Store\n"), 0o644)
	os.WriteFile(filepath.Join(clone, "files/only-other"), []byte("x\n"), 0o644)
	gitCmd(t, clone, "add", "-A")
	gitCmd(t, clone, "commit", "-q", "-m", "manual edit")
	gitCmd(t, clone, "push", "-q", "origin", "HEAD:main")

	// A machine whose XDG_CONFIG_HOME points elsewhere.
	g := w.machine("gamma")
	t.Setenv("XDG_CONFIG_HOME", g.path("xdg"))
	g.init()
	g.expect("xdg/git/ignore", ".DS_Store\n")
	if g.exists(".only-other") {
		t.Fatal("os filter ignored")
	}
	if s := entryStatusOf(g.statusJSON(), "only-other"); s != "skipped" {
		t.Fatalf("status = %q", s)
	}
}

func TestBrokenManifestChangesNothing(t *testing.T) {
	w := newWorld(t)
	a := w.machine("alpha")
	a.init()
	a.write(".vimrc", "set nu\n")
	a.ok("add", a.path(".vimrc"))
	clone := filepath.Join(w.root, "manual")
	gitCmd(t, w.root, "clone", "-q", w.remote, clone)
	os.WriteFile(filepath.Join(clone, "manifest.json"), []byte("{ not json"), 0o644)
	gitCmd(t, clone, "commit", "-qam", "oops")
	gitCmd(t, clone, "push", "-q", "origin", "HEAD:main")
	a.write(".vimrc", "set rnu\n")
	if code, out := a.run("sync"); code == 0 || !strings.Contains(out, "not valid JSON") {
		t.Fatalf("exit %d\n%s", code, out)
	}
	a.expect(".vimrc", "set rnu\n")
}

func TestUnifiedDiff(t *testing.T) {
	got := unifiedDiff([]string{"a", "b", "c"}, []string{"a", "x", "c"}, "old", "new")
	want := "--- old\n+++ new\n@@ -1,3 +1,3 @@\n a\n-b\n+x\n c\n"
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestLaunchdPlistIsValid(t *testing.T) {
	if _, err := exec.LookPath("plutil"); err != nil {
		t.Skip("plutil not available")
	}
	w := newWorld(t)
	m := w.machine("alpha")
	m.activate()
	c := &Ctx{Paths: NewPaths(), Config: &Config{Interval: 300}}
	p := filepath.Join(w.root, "agent.plist")
	os.WriteFile(p, []byte(launchdPlistContent(c, []string{"/path with space/dotsync", "sync", "-q"}, 300)), 0o644)
	if out, err := exec.Command("plutil", "-lint", p).CombinedOutput(); err != nil {
		t.Fatalf("invalid plist: %s", out)
	}
}

// ---------------------------------------------------------------- regression tests from review

func TestSymlinkedSubdirectoryIsNotFollowed(t *testing.T) {
	w := newWorld(t)
	a, b := w.machine("alpha"), w.machine("beta")
	a.init()
	a.write(".config/app/sub/a.txt", "a\n")
	a.write(".config/app/top.txt", "top\n")
	a.ok("add", a.path(".config/app"))
	b.init()

	outside := filepath.Join(w.root, "outside")
	os.MkdirAll(outside, 0o755)
	os.WriteFile(filepath.Join(outside, "a.txt"), []byte("not yours\n"), 0o644)
	os.RemoveAll(b.path(".config/app/sub"))
	if err := os.Symlink(outside, b.path(".config/app/sub")); err != nil {
		t.Fatal(err)
	}
	b.run("sync") // reports errors for the sub/ items; must not act on them
	a.write(".config/app/sub/new.txt", "new\n")
	a.ok("sync")
	b.run("sync")

	if got, _ := os.ReadFile(filepath.Join(outside, "a.txt")); string(got) != "not yours\n" {
		t.Fatalf("file outside the managed tree was modified: %q", got)
	}
	if _, err := os.Stat(filepath.Join(outside, "new.txt")); err == nil {
		t.Fatal("file written through a symlinked directory")
	}
	if w.remoteFile("app/sub/a.txt") != "a\n" {
		t.Fatal("remote file deleted because a local parent became a symlink")
	}
}

func TestBackupsAreNeverOverwritten(t *testing.T) {
	w := newWorld(t)
	m := w.machine("alpha")
	m.activate()
	b := &Backups{paths: NewPaths()}
	m.write(".x", "first\n")
	p1, err := b.save(m.path(".x"))
	if err != nil {
		t.Fatal(err)
	}
	m.write(".x", "second\n")
	p2, err := b.save(m.path(".x"))
	if err != nil {
		t.Fatal(err)
	}
	if p1 == p2 {
		t.Fatal("second backup reused the first path")
	}
	if got, _ := os.ReadFile(p1); string(got) != "first\n" {
		t.Fatalf("first backup overwritten: %q", got)
	}
}

func TestLostStateMeansConflictNotReplacement(t *testing.T) {
	w := newWorld(t)
	a, b := w.machine("alpha"), w.machine("beta")
	a.init()
	a.write(".vimrc", "v1\n")
	a.ok("add", a.path(".vimrc"))
	b.init()
	b.write(".vimrc", "beta's unsynced edit\n")
	a.write(".vimrc", "v2\n")
	a.ok("sync")
	os.WriteFile(b.path(".local/state/dotsync/state.json"), []byte("{garbage"), 0o600)
	b.run("sync")
	b.expect(".vimrc", "beta's unsynced edit\n")
	if s := entryStatusOf(b.statusJSON(), "vimrc"); s != "CONFLICT" {
		t.Fatalf("status = %q", s)
	}
}

func TestSameNewFileOnTwoMachinesIsAConflict(t *testing.T) {
	w := newWorld(t)
	a, b := w.machine("alpha"), w.machine("beta")
	a.init()
	a.write(".config/nvim/init.lua", "x\n")
	a.ok("add", a.path(".config/nvim"))
	b.init()
	a.write(".config/nvim/lua/new.lua", "alpha\n")
	b.write(".config/nvim/lua/new.lua", "beta\n")
	a.ok("sync")
	b.run("sync")
	b.expect(".config/nvim/lua/new.lua", "beta\n")
	if s := entryStatusOf(b.statusJSON(), "nvim"); !strings.HasPrefix(s, "CONFLICT") {
		t.Fatalf("status = %q", s)
	}
}

func TestEmptiedDirectoryIsRestoredNotDeletedEverywhere(t *testing.T) {
	w := newWorld(t)
	a, b := w.machine("alpha"), w.machine("beta")
	a.init()
	a.write(".config/nvim/init.lua", "x\n")
	a.write(".config/nvim/lua/y.lua", "y\n")
	a.ok("add", a.path(".config/nvim"))
	b.init()
	os.RemoveAll(b.path(".config/nvim/lua"))
	os.Remove(b.path(".config/nvim/init.lua"))
	b.ok("sync")
	b.expect(".config/nvim/lua/y.lua", "y\n")
	a.ok("sync")
	a.expect(".config/nvim/init.lua", "x\n")
}

func TestIgnoredMatchesAncestors(t *testing.T) {
	pats := append(append([]string(nil), defaultIgnore...), "plugins", "spell/*.spl")
	for rel, want := range map[string]bool{
		"plugins/x.lua": true, "a/plugins/x": true, "spell/en.spl": true, "init.lua": false,
		"lua/.git/config": true, "lua/plugin.lua": false,
	} {
		if got := ignored(rel, pats); got != want {
			t.Errorf("ignored(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestSecretRules(t *testing.T) {
	w := newWorld(t)
	w.machine("alpha").activate()
	h := homeDir()
	paths := map[string]bool{
		".ssh/id_ed25519": true, ".gnupg/private-keys-v1.d/x.key": true, ".aws/credentials": true,
		".config/app/my-secrets.json": true, ".zsh_history": true, ".env": true,
		".gnupg/gpg.conf": false, ".gnupg/gpg-agent.conf": false, ".config/secretive/config": false,
		".ssh/config": false, ".config/fish/config.fish": false,
		".config/mise/age.txt": true, ".config/sops/age/keys.txt": true,
		"Library/Application Support/sops/age/keys.txt": true, ".config/mise/config.toml": false,
	}
	for rel, want := range paths {
		if got := secretPathReason(filepath.Join(h, rel)) != ""; got != want {
			t.Errorf("secretPathReason(%s) = %v, want %v", rel, got, want)
		}
	}
	content := map[string]bool{
		`password = "hunter2!xyz"`:                                            true,
		"export GITHUB_TOKEN=ghp_" + strings.Repeat("a", 36):                  true,
		"token: abcdefgh12345678":                                             true,
		"password_command = pass show mail":                                   false,
		"password = $(pass show mail)":                                        false,
		"set -gx EDITOR nvim":                                                 false,
		"max_tokens: 4096":                                                    false,
		"api_key = sk-" + strings.Repeat("x", 30) + " # dotsync:allow-secret": false,
		fakeAgeKey: true,
		`DATABASE_URL = "postgres://app:hunter2@db.local/app"`: true,
		"redis://:s3cret@cache:6379":                           true,
		`url = "postgres://app:${PGPASSWORD}@db/app"`:          false,
		`url = "postgres://app:{{ env.PW }}@db/app"`:           false,
		"git clone ssh://git@github.com/you/repo":              false,
		"proxy = http://proxy.local:8080/":                     false,
		"# public key: age1" + strings.Repeat("q", 58):         false,
	}
	for line, want := range content {
		if got := secretContentReason([]byte(line)) != ""; got != want {
			t.Errorf("secretContentReason(%q) = %v, want %v", line, got, want)
		}
	}
}

// Built at runtime so secret scanners don't flag the test fixtures themselves.
const sopsYAML = `db_password: ENC[AES256_GCM,data:3q2+7w==,iv:AAAA,tag:BBBB,type:str]
sops:
    age:
        - recipient: age1qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq
          enc: |
            -----BEGIN AGE ENCRYPTED FILE-----
            YWdlLWVuY3J5cHRpb24ub3JnL3YxCg==
            -----END AGE ENCRYPTED FILE-----
    lastmodified: "2026-09-21T00:00:00Z"
    mac: ENC[AES256_GCM,data:bWFj,iv:AAAA,tag:BBBB,type:str]
    version: 3.9.0
`

const sopsJSON = `{
	"api_key": "ENC[AES256_GCM,data:3q2+7w==,iv:AAAA,tag:BBBB,type:str]",
	"sops": {
		"lastmodified": "2026-09-21T00:00:00Z",
		"mac": "ENC[AES256_GCM,data:bWFj,iv:AAAA,tag:BBBB,type:str]",
		"version": "3.9.0"
	}
}
`

const ageArmored = "-----BEGIN AGE ENCRYPTED FILE-----\nYWdlLWVuY3J5cHRpb24ub3JnL3YxCi0+IFgyNTUxOQ==\n-----END AGE ENCRYPTED FILE-----\n"

// formatKind names what cipherFormatOf found, in the terms the test table uses.
func formatKind(data []byte) string {
	switch f := cipherFormatOf(data); {
	case f == nil:
		return ""
	case f.values == nil:
		return "age" // entirely ciphertext
	default:
		return "sops" // encrypted values
	}
}

func TestEncryptedFiles(t *testing.T) {
	w := newWorld(t)
	w.machine("alpha").activate()
	h := homeDir()
	file := func(s string) *Obj { return &Obj{Kind: "file", Data: []byte(s)} }
	for _, c := range []struct {
		rel, content string
		enc          string
		blocked      bool
	}{
		{".config/mise/.env.yaml", sopsYAML, "sops", false},
		{".config/mise/.env.json", sopsJSON, "sops", false},
		{"app/.env.json", `{"k":"ENC[AES256_GCM,data:eA==,iv:A,tag:B,type:str]","sops":{"mac":"ENC[AES256_GCM,data:bQ==,iv:A,tag:B,type:str]"}}`, "sops", false},
		{".env", "API_KEY=ENC[AES256_GCM,data:eA==,iv:A,tag:B,type:str]\nsops_mac=ENC[AES256_GCM,data:bQ==,iv:A,tag:B,type:str]\n", "sops", false},
		{"secrets/prod.env.age", ageArmored, "age", false},
		{"secrets/prod.env.age", "age-encryption.org/v1\n-> X25519 abc\nZGVm\n--- bWFj\n\x00\xff", "age", false},
		// The age header alone isn't enough: plaintext after it is scanned (and the path refused).
		{"secrets/prod.env.age", "age-encryption.org/v1\npassword = hunter2!xyz\n", "", true},
		// In sops files only the ENC[…] values are skipped: a plaintext secret on the same line
		// (e.g. a minified JSON file) is still found.
		{".config/mise/.env.json", `{"k":"ENC[AES256_GCM,data:eA==,iv:A,tag:B,type:str]","token":"ghp_` + strings.Repeat("a", 36) + `","sops":{"mac":"ENC[AES256_GCM,data:bQ==,iv:A,tag:B,type:str]"}}`, "sops", true},
		// sops leaves keys and comments readable; those are still scanned.
		{".config/mise/.env.yaml", "# token: " + "ghp_" + strings.Repeat("a", 36) + "\n" + sopsYAML, "sops", true},
		// ENC[…] lines are only skipped in real sops files.
		{"notes.txt", "ENC[AES256_GCM,data:x] token: ghp_" + strings.Repeat("a", 36) + "\n", "", true},
		// Plaintext between age armor lines isn't ciphertext.
		{"secrets/prod.env.age", "-----BEGIN AGE ENCRYPTED FILE-----\npassword = hunter2!xyz\n-----END AGE ENCRYPTED FILE-----\n", "", true},
		// Unencrypted files named like secrets are still refused.
		{".config/mise/.env.json", `{"API_KEY": "abc"}`, "", true},
	} {
		o := file(c.content)
		if got := formatKind(o.Data); got != c.enc {
			t.Errorf("format of %s = %q, want %q", c.rel, got, c.enc)
		}
		if got := secretReason(filepath.Join(h, c.rel), o); (got != "") != c.blocked {
			t.Errorf("secretReason(%s) = %q, want blocked=%v", c.rel, got, c.blocked)
		}
	}
}

func TestEncryptedSecretsSyncInManagedDirectory(t *testing.T) {
	w := newWorld(t)
	a, b := w.machine("alpha"), w.machine("beta")
	a.init()
	a.write(".config/mise/config.toml", "[env]\n_.file = \".env.yaml\"\n")
	a.write(".config/mise/.env.yaml", sopsYAML)
	a.write(".config/mise/age.txt", fakeAgeKey+"\n")
	out := a.ok("add", a.path(".config/mise"))
	if !strings.Contains(out, "1 file(s) under ~/.config/mise look like secrets") || !strings.Contains(out, "age.txt") {
		t.Fatalf("expected only age.txt to be held back:\n%s", out)
	}
	if w.remoteFile("mise/.env.yaml") != sopsYAML {
		t.Fatal("sops file was not sent")
	}

	b.init()
	b.expect(".config/mise/.env.yaml", sopsYAML)
	if b.exists(".config/mise/age.txt") {
		t.Fatal("age key reached another machine")
	}
}

func TestSourceValidation(t *testing.T) {
	for _, s := range []string{"../x", "/abs", "a/.git/config", ".git", ""} {
		if _, err := normalizeSource(s); err == nil {
			t.Errorf("normalizeSource(%q) accepted", s)
		}
	}
	if got, _ := normalizeSource("./fish//config.fish"); got != "fish/config.fish" {
		t.Errorf("normalizeSource = %q", got)
	}
}

func TestParseArgs(t *testing.T) {
	fs := newFlags("x", "", &bytes.Buffer{})
	d := fs.String("d", "", "")
	pos, err := parseArgs(fs, []string{"a", "-d", "desc", "b", "--", "-c", "--"})
	if err != nil || *d != "desc" || strings.Join(pos, ",") != "a,b,-c,--" {
		t.Fatalf("pos=%q d=%q err=%v", pos, *d, err)
	}
}

func TestSchedulerHelpers(t *testing.T) {
	for in, want := range map[int]string{60: "*/1 * * * *", 300: "*/5 * * * *", 3600: "0 */1 * * *", 86400: "0 0 * * *"} {
		if got, _ := cronSchedule(in); got != want {
			t.Errorf("cronSchedule(%d) = %q, want %q", in, got, want)
		}
	}
	if got := systemdQuote("/home/a b/100%/dotsync"); got != `"/home/a b/100%%/dotsync"` {
		t.Errorf("systemdQuote = %s", got)
	}
}

// Built at runtime so secret scanners don't flag the test fixture itself.
var fakeAWSKey = "AKIA" + strings.Repeat("Z", 16)

var fakeAgeKey = "AGE-SECRET-KEY-1" + strings.Repeat("Q", 58)

func TestGlobNonASCII(t *testing.T) {
	for _, c := range []struct {
		pattern, s string
		want       bool
	}{
		{"café*", "café-notes.md", true},
		{"*.über", "x.über", true},
		{"日本?", "日本語", true},
		{"\xff", "\xff", true}, // invalid UTF-8: literal match, no panic
		{"[\xc8]", "x", false},
	} {
		if got := globMatch(c.pattern, c.s); got != c.want {
			t.Errorf("globMatch(%q, %q) = %v, want %v", c.pattern, c.s, got, c.want)
		}
	}
}

// ---------------------------------------------------------------- 0.2.0: set, exclude/include, backups, doctor

func manifestEntry(t *testing.T, w *world, source string) map[string]any {
	t.Helper()
	var m struct{ Entries []map[string]any }
	if err := json.Unmarshal([]byte(w.remoteManifest()), &m); err != nil {
		t.Fatal(err)
	}
	for _, e := range m.Entries {
		if e["source"] == source {
			return e
		}
	}
	t.Fatalf("no entry %q in manifest", source)
	return nil
}

func TestSetChangesEntryEverywhere(t *testing.T) {
	w := newWorld(t)
	a, b := w.machine("alpha"), w.machine("beta")
	a.init()
	a.write(".config/nvim/init.lua", "x\n")
	a.write(".config/nvim/lazy-lock.json", "{}\n")
	a.ok("add", a.path(".config/nvim"))
	b.init()
	b.expect(".config/nvim/lazy-lock.json", "{}\n")

	a.ok("set", "~/.config/nvim", "--ignore", "lazy-lock.json", "-d", "Neovim", "--mode", "0600")
	e := manifestEntry(t, w, "nvim")
	if e["description"] != "Neovim" || e["mode"] != "0600" || fmt.Sprint(e["ignore"]) != "[lazy-lock.json]" {
		t.Fatalf("entry = %v", e)
	}
	// The ignored file is no longer managed: edits to it stay local on each machine.
	b.write(".config/nvim/lazy-lock.json", "{\"beta\":1}\n")
	b.ok("sync")
	a.ok("sync")
	a.expect(".config/nvim/lazy-lock.json", "{}\n")

	a.ok("set", "nvim", "--unignore", "lazy-lock.json", "--no-mode")
	e = manifestEntry(t, w, "nvim")
	if _, ok := e["ignore"]; ok {
		t.Fatalf("ignore not removed: %v", e)
	}
	if _, ok := e["mode"]; ok {
		t.Fatalf("mode not removed: %v", e)
	}

	// Invalid values are refused before anything is queued.
	if code, out := a.run("set", "nvim", "--mode", "999"); code == 0 || !strings.Contains(out, "octal") {
		t.Fatalf("bad mode accepted (%d): %s", code, out)
	}
	if code, _ := a.run("set", "nvim"); code != 2 {
		t.Fatalf("set with no changes: exit %d", code)
	}
}

func TestSetOSSkipsOtherMachines(t *testing.T) {
	w := newWorld(t)
	a := w.machine("alpha")
	a.init()
	a.write(".other", "x\n")
	a.ok("add", a.path(".other"))
	other := "linux"
	if osName() == "linux" {
		other = "darwin"
	}
	a.ok("set", "other", "--os", other)
	if s := entryStatusOf(a.statusJSON(), "other"); s != "skipped" {
		t.Fatalf("status = %q", s)
	}
	a.ok("set", "other", "--any-os")
	if s := entryStatusOf(a.statusJSON(), "other"); s != "ok" {
		t.Fatalf("status after --any-os = %q", s)
	}
}

func TestConcurrentIgnoreEditsCombine(t *testing.T) {
	w := newWorld(t)
	a, b := w.machine("alpha"), w.machine("beta")
	a.init()
	a.write(".config/app/x", "x\n")
	a.ok("add", a.path(".config/app"))
	b.init()
	a.ok("set", "--no-sync", "app", "--ignore", "cache")
	b.ok("set", "--no-sync", "app", "--ignore", "logs")
	a.ok("sync")
	b.ok("sync")
	if got := fmt.Sprint(manifestEntry(t, w, "app")["ignore"]); got != "[cache logs]" {
		t.Fatalf("ignore = %s", got)
	}
}

func TestExcludeAndInclude(t *testing.T) {
	w := newWorld(t)
	a, b := w.machine("alpha"), w.machine("beta")
	a.init()
	a.write(".tmux.conf", "one\n")
	a.ok("add", a.path(".tmux.conf"))
	b.init()

	b.ok("exclude", "~/.tmux.conf")
	if s := entryStatusOf(b.statusJSON(), "tmux.conf"); s != "skipped" {
		t.Fatalf("status = %q", s)
	}
	a.write(".tmux.conf", "two\n")
	a.ok("sync")
	b.ok("sync")
	b.expect(".tmux.conf", "one\n") // excluded: left alone
	if out := b.ok("exclude"); !strings.Contains(out, "tmux.conf") {
		t.Fatalf("exclude list: %s", out)
	}

	b.ok("include", "tmux.conf")
	b.expect(".tmux.conf", "two\n") // managed again; the old copy was backed up
	if got := b.backups(".tmux.conf"); len(got) != 1 || got[0] != "one\n" {
		t.Fatalf("backups = %q", got)
	}
	if code, _ := b.run("include", "tmux.conf"); code != 0 {
		t.Fatal("include of a non-excluded entry should be a harmless no-op")
	}
	if code, _ := b.run("exclude", "~/.nope"); code == 0 {
		t.Fatal("exclude of an unknown entry accepted")
	}
}

func TestPruneBackups(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 9, 21, 12, 0, 0, 0, time.Local)
	mk := func(run, rel, content string) {
		p := filepath.Join(root, run, rel)
		os.MkdirAll(filepath.Dir(p), 0o700)
		os.WriteFile(p, []byte(content), 0o600)
	}
	mk("20260101-100000-1", ".zshrc", "very old zshrc")    // old, but a newer backup exists → pruned
	mk("20260101-100000-1", ".vimrc", "only vimrc backup") // old, but the only copy → kept
	mk("20260601-100000-2", ".zshrc", "old zshrc")         // old, newer exists → pruned
	mk("20260601-100000-2", ".zshrc.~1~", "old zshrc 2")   // same file, same run → pruned
	mk("20260915-100000-3", ".zshrc", "recent zshrc")      // recent → kept
	mk("not-a-run", "x", "ignored")

	n, err := pruneBackups(root, 90*24*time.Hour, now)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("removed %d files, want 3", n)
	}
	exists := func(p string) bool { _, err := os.Stat(filepath.Join(root, p)); return err == nil }
	for p, want := range map[string]bool{
		"20260101-100000-1/.zshrc": false, "20260101-100000-1/.vimrc": true,
		"20260601-100000-2": false, "20260915-100000-3/.zshrc": true, "not-a-run/x": true,
	} {
		if exists(p) != want {
			t.Errorf("%s exists = %v, want %v", p, !want, want)
		}
	}
}

func TestDoctor(t *testing.T) {
	w := newWorld(t)
	a := w.machine("alpha")
	code, out := a.run("doctor")
	if code != 1 || !strings.Contains(out, "not set up on this machine") {
		t.Fatalf("doctor before init: exit %d\n%s", code, out)
	}
	a.init()
	a.write(".vimrc", "set nu\n")
	a.ok("add", a.path(".vimrc"))
	_, out = a.run("doctor")
	for _, want := range []string{
		"✓ remote reachable without prompts",
		"✓ 1 managed entry, 1 file",
		"background agent: not installed",
		"→ dotsync agent install",
		"backups: 0 file(s)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output missing %q:\n%s", want, out)
		}
	}
	// A broken remote is reported with git's error.
	os.Rename(w.remote, w.remote+".gone")
	_, out = a.run("doctor")
	if !strings.Contains(out, "remote") || strings.Contains(out, "✓ remote reachable") {
		t.Fatalf("unreachable remote not reported:\n%s", out)
	}
}

func TestEmbeddedIcon(t *testing.T) {
	w := newWorld(t)
	w.machine("alpha").activate()
	if len(iconPNG) < 100 || !bytes.HasPrefix(iconPNG, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatal("embedded icon is not a PNG")
	}
	p := installIcon()
	if got, err := os.ReadFile(p); err != nil || !bytes.Equal(got, iconPNG) {
		t.Fatalf("installIcon: %v", err)
	}
	if !isWithin(p, NewPaths().DataDir) {
		t.Fatalf("icon installed outside dotsync's data dir: %s", p)
	}
}

// ---------------------------------------------------------------- efficiency

func TestIdleSyncWritesNothing(t *testing.T) {
	w := newWorld(t)
	a := w.machine("alpha")
	a.init()
	a.write(".config/nvim/init.lua", "x\n")
	a.write(".vimrc", "set nu\n")
	a.ok("add", a.path(".config/nvim"), a.path(".vimrc"))
	a.ok("sync") // settle: housekeeping, caches
	a.ok("sync")

	state := a.path(".local/state/dotsync/state.json")
	logf := a.path(".local/state/dotsync/sync.log")
	repo := a.path(".local/share/dotsync/repo/.git")
	read := func(p string) string { b, _ := os.ReadFile(p); return string(b) }
	mtime := func(p string) time.Time { fi, _ := os.Stat(p); return fi.ModTime() }
	before := map[string]string{"state": read(state), "log": read(logf)}
	indexM, headLog := mtime(filepath.Join(repo, "index")), read(filepath.Join(repo, "logs", "HEAD"))
	os.Remove(filepath.Join(repo, "FETCH_HEAD"))
	time.Sleep(20 * time.Millisecond)

	a.ok("sync")
	if read(state) != before["state"] {
		t.Error("state.json was rewritten by a sync that changed nothing")
	}
	if read(logf) != before["log"] {
		t.Errorf("sync.log grew on a sync that changed nothing:\n%s", strings.TrimPrefix(read(logf), before["log"]))
	}
	if !mtime(filepath.Join(repo, "index")).Equal(indexM) {
		t.Error("git index was rewritten")
	}
	if read(filepath.Join(repo, "logs", "HEAD")) != headLog {
		t.Error("reflog was written")
	}
	if _, err := os.Stat(filepath.Join(repo, "FETCH_HEAD")); err == nil {
		t.Error("FETCH_HEAD was written")
	}

	// And a real change is still picked up immediately.
	a.write(".vimrc", "set rnu\n")
	a.ok("sync")
	if w.remoteFile("vimrc") != "set rnu\n" {
		t.Fatal("change not synced")
	}
}

func TestStatCacheDetectsChangesWithRestoredMtime(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	os.WriteFile(p, []byte("aaaa\n"), 0o644)
	old := time.Now().Add(-time.Hour)
	os.Chtimes(p, old, old)

	sc := newStatCache(nil)
	sc.now = time.Now().Add(time.Minute) // treat current timestamps as settled
	first, _ := sc.obj(p)
	if first.Data == nil {
		t.Fatal("first read must hash the file")
	}
	second, _ := sc.obj(p)
	if second.Data != nil || second.Sig != first.Sig {
		t.Fatal("unchanged file should be answered from the cache")
	}
	// Same size, mtime put back: ctime still changes, so the edit is seen.
	os.WriteFile(p, []byte("bbbb\n"), 0o644)
	os.Chtimes(p, old, old)
	third, _ := sc.obj(p)
	if third.Data == nil || third.Sig == first.Sig {
		t.Fatal("edit with restored mtime was not detected")
	}
	// chmod changes the signature too (executable bit).
	os.Chmod(p, 0o755)
	fourth, _ := sc.obj(p)
	if fourth.Sig == third.Sig {
		t.Fatal("chmod +x not detected")
	}
}

func TestStatCacheIgnoresRecentFiles(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f")
	os.WriteFile(p, []byte("x"), 0o644)
	sc := newStatCache(nil)
	sc.obj(p)
	if again, _ := sc.obj(p); again.Data == nil {
		t.Fatal("a file modified within the racy window must always be re-read")
	}
}

func TestHousekeepingIsScheduled(t *testing.T) {
	w := newWorld(t)
	a := w.machine("alpha")
	a.init()
	a.activate()
	c, _ := newCtx(true, true, io.Discard, io.Discard)
	st, _ := loadState(c)
	if st.LastGC == "" || st.LastBackupPrune == "" || st.RepoConfig != repoConfigVersion {
		t.Fatalf("housekeeping didn't run on first sync: %+v", st)
	}
	day := time.Date(2026, 9, 21, 12, 0, 0, 0, time.Local)
	st.LastGC, st.LastBackupPrune = "2026-09-20", "2026-09-21"
	housekeeping(c, st, day)
	if st.LastGC != "2026-09-20" {
		t.Fatal("gc ran again within a week")
	}
	housekeeping(c, st, day.AddDate(0, 0, 5))
	if st.LastGC != "2026-09-20" || st.LastBackupPrune != "2026-09-26" {
		t.Fatalf("after 5 days: gc=%s prune=%s", st.LastGC, st.LastBackupPrune)
	}
	housekeeping(c, st, day.AddDate(0, 0, 6))
	if st.LastGC != "2026-09-27" || st.LastBackupPrune != "2026-09-27" { // a week later: both due
		t.Fatalf("gc/prune not rescheduled: gc=%s prune=%s", st.LastGC, st.LastBackupPrune)
	}
}

func TestAgentDefinitionDrift(t *testing.T) {
	if osName() != "darwin" {
		t.Skip("exercises the launchd definition; systemd/cron would touch the host's real units")
	}
	w := newWorld(t)
	a := w.machine("alpha")
	a.init()
	a.activate()
	c, _ := newCtx(true, true, io.Discard, io.Discard)
	if installed, _ := agentInstalled(c); installed {
		t.Fatal("no agent was installed yet")
	}
	def, err := desiredAgent(c)
	if err != nil {
		t.Fatal(err)
	}
	for path, content := range def.files { // what `agent install` writes, without loading it
		os.MkdirAll(filepath.Dir(path), 0o755)
		os.WriteFile(path, []byte(content), 0o644)
	}
	if installed, current := agentInstalled(c); !installed || !current {
		t.Fatalf("fresh definition: installed=%v current=%v", installed, current)
	}
	// A definition written by an older version (here: missing the low-priority settings).
	for path, content := range def.files {
		os.WriteFile(path, []byte(strings.Replace(content, "<key>LowPriorityBackgroundIO</key><true/>", "", 1)), 0o644)
	}
	if installed, current := agentInstalled(c); !installed || current {
		t.Fatalf("old definition: installed=%v current=%v", installed, current)
	}
	if _, out := a.run("doctor"); !strings.Contains(out, "definition is from an older dotsync") {
		t.Errorf("doctor didn't flag the outdated agent:\n%s", out)
	}
}

// ---------------------------------------------------------------- actionable git failures

func TestDiagnoseGit(t *testing.T) {
	c := &Ctx{Config: &Config{Remote: "git@github.com:you/dotfiles.git"}}
	for _, tc := range []struct {
		stderr, problem, fix string
	}{
		{"Host key verification failed.\nfatal: Could not read from remote repository.", "host key", "ssh -T git@github.com"},
		{"git@github.com: Permission denied (publickey).\nfatal: Could not read from remote repository.", "rejected the SSH key", "ssh-add"},
		{"fatal: could not read Username for 'https://github.com': terminal prompts disabled", "no stored credentials", "credential helper"},
		{"remote: Invalid username or password.\nfatal: Authentication failed for 'https://github.com/x/y/'", "rejected the stored credentials", "renew them"},
		{"remote: Repository not found.\nfatal: repository 'https://github.com/x/y/' not found", "doesn't exist", "config.json"},
		{"/usr/bin/gh auth git-credential get: /usr/bin/gh: No such file or directory\nfatal: could not read Username for 'https://github.com': terminal prompts disabled",
			"/usr/bin/gh, which isn't installed", "name gh without its path"},
		{"gh auth git-credential get: gh: command not found\nfatal: could not read Username", "run gh, which isn't installed", "install gh"},
	} {
		gp := diagnoseGit(c, tc.stderr)
		if gp == nil || !strings.Contains(gp.Problem, tc.problem) || !strings.Contains(gp.Fix, tc.fix) {
			t.Errorf("diagnoseGit(%q) = %+v; want problem ~%q, fix ~%q", tc.stderr, gp, tc.problem, tc.fix)
		}
	}
	for _, network := range []string{
		"ssh: Could not resolve hostname github.com: nodename nor servname provided, or not known",
		"fatal: unable to access 'https://github.com/x/y/': Could not resolve host: github.com",
		"ssh: connect to host github.com port 22: Operation timed out",
	} {
		if gp := diagnoseGit(c, network); gp != nil {
			t.Errorf("network failure %q diagnosed as %+v; it should just wait", network, gp)
		}
	}
	for remote, want := range map[string]gitRemote{
		"https://git.example.com/me/dots.git":    {url: "https://git.example.com/me/dots.git", host: "git.example.com", sshTarget: "git.example.com"},
		"git@git.example.com:me/dots.git":        {url: "git@git.example.com:me/dots.git", host: "git.example.com", sshTarget: "git@git.example.com"},
		"ssh://me@git.example.com:2222/dots.git": {url: "ssh://me@git.example.com:2222/dots.git", host: "git.example.com", sshTarget: "me@git.example.com"},
	} {
		if got := parseRemote(&Ctx{Config: &Config{Remote: remote}}); got != want {
			t.Errorf("parseRemote(%q) = %+v, want %+v", remote, got, want)
		}
	}
}

// The exact dead end this guards against: a synced git config names a credential helper by an
// absolute path that exists only on the machine that wrote it.
func TestDiagnoseSyncedCredentialHelper(t *testing.T) {
	w := newWorld(t)
	a := w.machine("alpha")
	a.init()
	a.write(".gitconfig", "[user]\n\tname = me\n[credential \"https://github.com\"]\n\thelper =\n\thelper = !/usr/local/nope/gh auth git-credential\n")
	a.ok("add", a.path(".gitconfig"))
	t.Setenv("GIT_CONFIG_GLOBAL", a.path(".gitconfig"))
	a.activate()
	c, err := newCtx(true, true, io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	gp := diagnoseGit(c, "/usr/local/nope/gh auth git-credential get: /usr/local/nope/gh: No such file or directory\n"+
		"fatal: could not read Username for 'https://github.com': terminal prompts disabled")
	if gp == nil {
		t.Fatal("no diagnosis")
	}
	for _, want := range []string{"/usr/local/nope/gh", "(set in ~/.gitconfig)", "synced by dotsync"} {
		if !strings.Contains(gp.Problem, want) {
			t.Errorf("problem %q missing %q", gp.Problem, want)
		}
	}
	if !strings.Contains(gp.Fix, "helper = !gh auth git-credential") {
		t.Errorf("fix = %q", gp.Fix)
	}
}

func TestAgentDefinitionIgnoresCapturedPath(t *testing.T) {
	a := "<key>PATH</key><string>/a/bin:/usr/bin</string><key>HOME</key><string>/h</string>"
	b := "<key>PATH</key><string>/b/bin:/usr/bin:/bin</string><key>HOME</key><string>/h</string>"
	if !sameDefinition(a, b) {
		t.Error("definitions differing only in PATH should match")
	}
	if sameDefinition(a, strings.Replace(b, "/h", "/other", 1)) {
		t.Error("a different HOME is a real difference")
	}
	if !sameDefinition("*/5 * * * * PATH=/a:/b HOME=/h dotsync", "*/5 * * * * PATH=/c HOME=/h dotsync") {
		t.Error("cron lines differing only in PATH should match")
	}
}
