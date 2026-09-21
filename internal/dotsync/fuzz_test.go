package dotsync

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Fuzz tests for the code that turns untrusted text (a manifest someone else pushed, file names
// found on disk) into paths. Run one with: go test -fuzz=FuzzExpandTarget ./internal/dotsync

func FuzzNormalizeSource(f *testing.F) {
	for _, s := range []string{"fish/config.fish", "./a//b", "../x", "/abs", "a/.git/b", `a\b`, "", ".", "a/./b/"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got, err := normalizeSource(s)
		if err != nil {
			return
		}
		if got == "" || strings.HasPrefix(got, "/") || strings.Contains(got, `\`) {
			t.Fatalf("normalizeSource(%q) = %q", s, got)
		}
		for _, part := range strings.Split(got, "/") {
			if part == "" || part == "." || part == ".." || strings.EqualFold(part, ".git") {
				t.Fatalf("normalizeSource(%q) = %q has bad component %q", s, got, part)
			}
		}
		if again, err := normalizeSource(got); err != nil || again != got {
			t.Fatalf("not idempotent: %q -> %q -> %q (%v)", s, got, again, err)
		}
	})
}

func FuzzExpandTarget(f *testing.F) {
	for _, s := range []string{"~/.gitconfig", "~", "$XDG_CONFIG_HOME/nvim", "${HOME}/x", "~/../etc/passwd", "/etc/passwd", "$NOPE/x", "~/.local/state/dotsync/x"} {
		f.Add(s)
	}
	home := f.TempDir()
	f.Fuzz(func(t *testing.T, spec string) {
		t.Setenv("HOME", home)
		t.Setenv("XDG_CONFIG_HOME", "")
		t.Setenv("XDG_DATA_HOME", "")
		t.Setenv("XDG_STATE_HOME", "")
		p := NewPaths()
		got, err := expandTarget(spec, p)
		if err != nil {
			return
		}
		// The one property that matters: an accepted target is strictly inside $HOME and never
		// inside dotsync's own directories.
		if !isWithin(got, home) || filepath.Clean(got) == filepath.Clean(home) {
			t.Fatalf("expandTarget(%q) = %q escapes %q", spec, got, home)
		}
		for _, d := range p.OwnDirs() {
			if isWithin(got, d) {
				t.Fatalf("expandTarget(%q) = %q is inside dotsync's %q", spec, got, d)
			}
		}
	})
}

func FuzzGlobMatch(f *testing.F) {
	f.Add("*.swp", "init.lua.swp")
	f.Add("[!a]*", "b")
	f.Add("[", "[")
	f.Add("spell/*.spl", "spell/en.utf-8.spl")
	f.Fuzz(func(t *testing.T, pattern, s string) {
		_ = globMatch(pattern, s) // must never panic, whatever the pattern
		if !strings.ContainsAny(pattern, "*?[") && globMatch(pattern, s) != (pattern == s) {
			t.Fatalf("literal pattern %q vs %q", pattern, s)
		}
	})
}

func FuzzLoadManifest(f *testing.F) {
	f.Add(`{"version":1,"entries":[{"source":"a","target":"~/.a","description":"x"}]}`)
	f.Add(`[{"source":"a","target":"~/.a"}]`)
	f.Add(`{"entries":[1,"x",null,{"os":5,"mode":"999","ignore":[1]}]}`)
	f.Add(`{`)
	f.Fuzz(func(t *testing.T, data string) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, manifestName), []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
		m, err := loadManifest(dir)
		if err != nil {
			return
		}
		for _, raw := range m.Entries {
			_, _ = parseEntry(raw) // must never panic
		}
		// Whatever loads must survive a save/load round trip unchanged.
		if err := saveManifest(dir, m); err != nil {
			t.Fatalf("save: %v", err)
		}
		m2, err := loadManifest(dir)
		if err != nil {
			t.Fatalf("reload after save: %v", err)
		}
		if m.canonical() != m2.canonical() {
			t.Fatalf("round trip changed entries:\n%s\n%s", m.canonical(), m2.canonical())
		}
	})
}
