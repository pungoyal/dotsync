package dotsync

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func mkfile(t *testing.T, path, content string, perm os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), perm); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, perm); err != nil {
		t.Fatal(err)
	}
}

func symlink(t *testing.T, text, link string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(text, link); err != nil {
		t.Fatal(err)
	}
}

// buildStow creates home/dotfiles with several packages and the links GNU Stow would have made:
// plain file links, a folded directory, an unfolded directory, a --dotfiles name, a directory
// shared by two packages, and a directory that also holds a file Stow does not own.
func buildStow(t *testing.T, home string) string {
	t.Helper()
	stow := filepath.Join(home, "dotfiles")
	files := map[string]string{
		"git/.gitconfig":                 "[user]\n\tname = r\n",
		"fish/.config/fish/config.fish":  "set -x EDITOR nvim\n",
		"nvim/.config/nvim/init.lua":     "-- init\n",
		"nvim/.config/nvim/lua/opts.lua": "-- opts\n",
		"zsh/dot-zshrc":                  "# zsh\n",
		"ssh/.ssh/config":                "Host *\n",
		"tools/bin/b":                    "#!/bin/sh\necho b\n",
		"unstowed/.tmux.conf":            "# never stowed\n",
		".git/config":                    "",
	}
	for rel, content := range files {
		mkfile(t, filepath.Join(stow, filepath.FromSlash(rel)), content, 0o644)
	}
	mkfile(t, filepath.Join(stow, "scripts", "bin", "a"), "#!/bin/sh\necho a\n", 0o755)

	symlink(t, "dotfiles/git/.gitconfig", filepath.Join(home, ".gitconfig"))                                    // relative
	symlink(t, "../dotfiles/fish/.config/fish", filepath.Join(home, ".config/fish"))                            // folded dir
	symlink(t, filepath.Join(stow, "nvim/.config/nvim/init.lua"), filepath.Join(home, ".config/nvim/init.lua")) // absolute
	symlink(t, "../../../dotfiles/nvim/.config/nvim/lua/opts.lua", filepath.Join(home, ".config/nvim/lua/opts.lua"))
	symlink(t, "dotfiles/zsh/dot-zshrc", filepath.Join(home, ".zshrc")) // stow --dotfiles
	symlink(t, "../dotfiles/ssh/.ssh/config", filepath.Join(home, ".ssh/config"))
	symlink(t, "../dotfiles/scripts/bin/a", filepath.Join(home, "bin/a"))
	symlink(t, "../dotfiles/tools/bin/b", filepath.Join(home, "bin/b"))

	mkfile(t, filepath.Join(home, ".ssh/known_hosts"), "example.com ssh-ed25519 AAAA\n", 0o644) // not Stow's
	mkfile(t, filepath.Join(home, ".tmux.conf"), "# a real file\n", 0o644)                      // package exists, never stowed
	symlink(t, "/etc/hosts", filepath.Join(home, ".hosts"))                                     // someone else's link
	return stow
}

func TestDiscoverStow(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	stow := buildStow(t, home)

	got, err := discoverStow(stow, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []stowLink{
		{Package: "fish", Target: filepath.Join(home, ".config/fish"), Source: filepath.Join(stow, "fish/.config/fish"), Dir: true},
		{Package: "nvim", Target: filepath.Join(home, ".config/nvim/init.lua"), Source: filepath.Join(stow, "nvim/.config/nvim/init.lua")},
		{Package: "nvim", Target: filepath.Join(home, ".config/nvim/lua/opts.lua"), Source: filepath.Join(stow, "nvim/.config/nvim/lua/opts.lua")},
		{Package: "git", Target: filepath.Join(home, ".gitconfig"), Source: filepath.Join(stow, "git/.gitconfig")},
		{Package: "ssh", Target: filepath.Join(home, ".ssh/config"), Source: filepath.Join(stow, "ssh/.ssh/config")},
		{Package: "zsh", Target: filepath.Join(home, ".zshrc"), Source: filepath.Join(stow, "zsh/dot-zshrc")},
		{Package: "scripts", Target: filepath.Join(home, "bin/a"), Source: filepath.Join(stow, "scripts/bin/a")},
		{Package: "tools", Target: filepath.Join(home, "bin/b"), Source: filepath.Join(stow, "tools/bin/b")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("links:\n got %+v\nwant %+v", got, want)
	}
}

func TestDiscoverStowPackageFilter(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	stow := buildStow(t, home)

	got, err := discoverStow(stow, home, []string{"git"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Package != "git" {
		t.Fatalf("got %+v, want only the git link", got)
	}
	if _, err := discoverStow(stow, home, []string{"nope"}); err == nil {
		t.Fatal("an unknown package must be an error")
	}
	for _, bad := range []string{"..", ".", "../x", "git/..", ""} {
		if _, err := discoverStow(stow, home, []string{bad}); err == nil {
			t.Fatalf("package name %q must be rejected", bad)
		}
	}
}

func TestDiscoverStowTriesBothDotSpellings(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	stow := filepath.Join(home, "dotfiles")

	// Test case 1: file with dot- spelling should not block the plain spelling link
	if err := os.MkdirAll(filepath.Join(stow, "zsh"), 0o755); err != nil {
		t.Fatal(err)
	}
	mkfile(t, filepath.Join(stow, "zsh/dot-zshrc"), "# zsh\n", 0o644)
	mkfile(t, filepath.Join(home, "dot-zshrc"), "# stray file\n", 0o644)
	symlink(t, "dotfiles/zsh/dot-zshrc", filepath.Join(home, ".zshrc"))

	got, err := discoverStow(stow, home, []string{"zsh"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d links, want 1: %+v", len(got), got)
	}
	if got[0].Target != filepath.Join(home, ".zshrc") {
		t.Fatalf("got Target %s, want %s", got[0].Target, filepath.Join(home, ".zshrc"))
	}
	if got[0].Source != filepath.Join(stow, "zsh/dot-zshrc") {
		t.Fatalf("got Source %s, want %s", got[0].Source, filepath.Join(stow, "zsh/dot-zshrc"))
	}

	// Test case 2: directory with dot- spelling should not block descending into plain spelling
	home2 := filepath.Join(t.TempDir(), "home2")
	stow2 := filepath.Join(home2, "dotfiles")
	if err := os.MkdirAll(filepath.Join(stow2, "cfg/dot-config/app"), 0o755); err != nil {
		t.Fatal(err)
	}
	mkfile(t, filepath.Join(stow2, "cfg/dot-config/app/rc"), "# rc\n", 0o644)
	mkfile(t, filepath.Join(home2, "dot-config"), "# stray file\n", 0o644)
	if err := os.MkdirAll(filepath.Join(home2, ".config/app"), 0o755); err != nil {
		t.Fatal(err)
	}
	symlink(t, filepath.Join(stow2, "cfg/dot-config/app/rc"), filepath.Join(home2, ".config/app/rc"))

	got2, err := discoverStow(stow2, home2, []string{"cfg"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got2) != 1 {
		t.Fatalf("got %d links, want 1: %+v", len(got2), got2)
	}
	if got2[0].Target != filepath.Join(home2, ".config/app/rc") {
		t.Fatalf("got Target %s, want %s", got2[0].Target, filepath.Join(home2, ".config/app/rc"))
	}
}

func TestGroupStowLinks(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	stow := buildStow(t, home)
	links, err := discoverStow(stow, home, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := groupStowLinks(links, home, []string{filepath.Join(home, ".config"), filepath.Join(home, ".local")})

	type row struct{ pkg, target, kind, label string }
	var rows []row
	for _, e := range got {
		rel, _ := filepath.Rel(home, e.Target)
		rows = append(rows, row{e.Package, filepath.ToSlash(rel), e.Kind, e.kindLabel()})
	}
	want := []row{
		{"fish", ".config/fish", "dir", "dir (folded)"},
		{"nvim", ".config/nvim", "dir", "dir (2 links, unfolded)"}, // promoted: only nvim links below it
		{"git", ".gitconfig", "file", "file"},
		{"ssh", ".ssh/config", "file", "file"}, // ~/.ssh also holds known_hosts, which is not Stow's
		{"zsh", ".zshrc", "file", "file"},
		{"scripts", "bin/a", "file", "file"}, // ~/bin is shared by two packages
		{"tools", "bin/b", "file", "file"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("entries:\n got %+v\nwant %+v", rows, want)
	}
	if n := len(got[1].Links); n != 2 {
		t.Fatalf("nvim entry carries %d links, want 2", n)
	}
}

func TestKindLabelSingularLink(t *testing.T) {
	e := importEntry{Kind: "dir", Target: "/home/x/.config/nvim", Links: []stowLink{
		{Target: "/home/x/.config/nvim", Dir: false},
	}}
	if got := e.kindLabel(); got != "dir (1 link, unfolded)" {
		t.Fatalf("kindLabel() = %q, want %q", got, "dir (1 link, unfolded)")
	}
}

func TestGroupStowLinksNeverPromotesBaseDirs(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	stow := filepath.Join(home, "dotfiles")
	mkfile(t, filepath.Join(stow, "starship/.config/starship.toml"), "add_newline = false\n", 0o644)
	symlink(t, "../dotfiles/starship/.config/starship.toml", filepath.Join(home, ".config/starship.toml"))
	links, err := discoverStow(stow, home, nil)
	if err != nil {
		t.Fatal(err)
	}

	// ~/.config holds nothing but this one link, yet it must not become a dir entry.
	got := groupStowLinks(links, home, []string{filepath.Join(home, ".config")})
	if len(got) != 1 || got[0].Kind != "file" || got[0].Target != filepath.Join(home, ".config/starship.toml") {
		t.Fatalf("got %+v, want one file entry for starship.toml", got)
	}
	// The same holds for an ancestor of a protected directory.
	got = groupStowLinks(links, home, []string{filepath.Join(home, ".config/some/xdg")})
	if len(got) != 1 || got[0].Kind != "file" {
		t.Fatalf("an ancestor of a protected dir was promoted: %+v", got)
	}
}

func TestGroupStowLinksFoldedDirInsidePromotedDir(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	stow := filepath.Join(home, "dotfiles")
	mkfile(t, filepath.Join(stow, "nvim/.config/nvim/init.lua"), "-- init\n", 0o644)
	mkfile(t, filepath.Join(stow, "nvim/.config/nvim/lua/opts.lua"), "-- opts\n", 0o644)

	// Create real directory ~/.config/nvim with a file link to init.lua
	if err := os.MkdirAll(filepath.Join(home, ".config/nvim"), 0o755); err != nil {
		t.Fatal(err)
	}
	symlink(t, filepath.Join(stow, "nvim/.config/nvim/init.lua"), filepath.Join(home, ".config/nvim/init.lua"))
	// Create a folded directory link lua -> package's lua directory
	symlink(t, filepath.Join(stow, "nvim/.config/nvim/lua"), filepath.Join(home, ".config/nvim/lua"))

	links, err := discoverStow(stow, home, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Expect 2 links: init.lua (file) and lua (dir)
	if len(links) != 2 {
		t.Fatalf("discoverStow: got %d links, want 2: %+v", len(links), links)
	}

	// .config is protected, so .config/nvim should be promoted
	got := groupStowLinks(links, home, []string{filepath.Join(home, ".config")})

	// Should result in exactly ONE entry
	if len(got) != 1 {
		t.Fatalf("groupStowLinks: got %d entries, want 1: %+v", len(got), got)
	}

	e := got[0]
	target := filepath.Join(home, ".config/nvim")

	if e.Target != target {
		t.Fatalf("Target: got %s, want %s", e.Target, target)
	}
	if e.Kind != "dir" {
		t.Fatalf("Kind: got %s, want dir", e.Kind)
	}
	if len(e.Links) != 2 {
		t.Fatalf("Links count: got %d, want 2", len(e.Links))
	}

	// Check links are sorted by Target
	if e.Links[0].Target != filepath.Join(home, ".config/nvim/init.lua") {
		t.Fatalf("Links[0].Target: got %s, want %s", e.Links[0].Target, filepath.Join(home, ".config/nvim/init.lua"))
	}
	if e.Links[1].Target != filepath.Join(home, ".config/nvim/lua") {
		t.Fatalf("Links[1].Target: got %s, want %s", e.Links[1].Target, filepath.Join(home, ".config/nvim/lua"))
	}

	// Check that the folded link has Dir == true
	if !e.Links[1].Dir {
		t.Fatalf("Links[1].Dir: got false, want true (folded directory)")
	}

	// Check kindLabel
	if label := e.kindLabel(); label != "dir (2 links, unfolded)" {
		t.Fatalf("kindLabel: got %s, want 'dir (2 links, unfolded)'", label)
	}
}

func TestGroupStowLinksNeverPromotesDirWithSecretLink(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	t.Setenv("HOME", home) // the secret path rules are relative to $HOME
	stow := filepath.Join(home, "dotfiles")
	mkfile(t, filepath.Join(stow, "aws/.aws/config"), "[default]\nregion = eu-west-1\n", 0o644)
	mkfile(t, filepath.Join(stow, "aws/.aws/credentials"), "[default]\n", 0o600)
	symlink(t, "../dotfiles/aws/.aws/config", filepath.Join(home, ".aws/config"))
	symlink(t, "../dotfiles/aws/.aws/credentials", filepath.Join(home, ".aws/credentials"))
	links, err := discoverStow(stow, home, nil)
	if err != nil {
		t.Fatal(err)
	}

	// ~/.aws holds nothing but links of one package, yet one of them is a secret by path: the
	// directory must not become an entry, so the secret can be skipped on its own.
	got := groupStowLinks(links, home, nil)
	if len(got) != 2 || got[0].Kind != "file" || got[1].Kind != "file" {
		t.Fatalf("got %+v, want two file entries", got)
	}
	if got[0].Target != filepath.Join(home, ".aws/config") || got[1].Target != filepath.Join(home, ".aws/credentials") {
		t.Fatalf("targets = %s, %s", got[0].Target, got[1].Target)
	}
}
