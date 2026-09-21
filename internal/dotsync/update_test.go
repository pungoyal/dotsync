package dotsync

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// fakeRelease serves a GitHub-like API and release download for tag with the given binary.
func fakeRelease(t *testing.T, tag string, binary []byte, corrupt bool) {
	t.Helper()
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	for _, f := range []struct {
		name string
		data []byte
	}{{"LICENSE", []byte("MIT")}, {"dotsync", binary}} {
		tw.WriteHeader(&tar.Header{Name: f.name, Mode: 0o755, Size: int64(len(f.data)), Typeflag: tar.TypeReg})
		tw.Write(f.data)
	}
	tw.Close()
	gz.Close()
	name := fmt.Sprintf("dotsync_%s_%s_%s.tar.gz", strings.TrimPrefix(tag, "v"), runtime.GOOS, runtime.GOARCH)
	sum := sha256.Sum256(archive.Bytes())
	if corrupt {
		sum[0] ^= 0xff
	}
	checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), name)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/" + releases.repo + "/releases/latest":
			fmt.Fprintf(w, `{"tag_name": %q}`, tag)
		case "/" + releases.repo + "/releases/download/" + tag + "/" + name:
			w.Write(archive.Bytes())
		case "/" + releases.repo + "/releases/download/" + tag + "/checksums.txt":
			w.Write([]byte(checksums))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	oldReleases, oldVersion := releases, Version
	releases.api, releases.download, releases.verifyWithGH = srv.URL, srv.URL, false
	t.Cleanup(func() {
		releases, Version = oldReleases, oldVersion
	})
}

func withTarget(t *testing.T) string {
	t.Helper()
	exe := filepath.Join(t.TempDir(), "dotsync")
	os.WriteFile(exe, []byte("old binary"), 0o755)
	old := updateTarget
	updateTarget = func() (string, error) { return exe, nil }
	t.Cleanup(func() { updateTarget = old })
	return exe
}

func TestUpdateInstallsVerifiedRelease(t *testing.T) {
	newWorld(t).machine("alpha").activate()
	fakeRelease(t, "v0.3.0", []byte("new binary"), false)
	exe := withTarget(t)
	Version = "0.2.0"

	var out bytes.Buffer
	if code := Run([]string{"update", "--check"}, &out, &out); code != 0 || !strings.Contains(out.String(), "update available: 0.2.0 → 0.3.0") {
		t.Fatalf("check: %d %s", code, out.String())
	}
	if got, _ := os.ReadFile(exe); string(got) != "old binary" {
		t.Fatal("--check changed the binary")
	}
	out.Reset()
	if code := Run([]string{"update"}, &out, &out); code != 0 {
		t.Fatalf("update: exit %d\n%s", code, out.String())
	}
	if got, _ := os.ReadFile(exe); string(got) != "new binary" {
		t.Fatalf("binary not replaced: %q", got)
	}
	if fi, _ := os.Stat(exe); fi.Mode().Perm()&0o100 == 0 {
		t.Fatal("new binary is not executable")
	}
	if !strings.Contains(out.String(), "checksum verified") || !strings.Contains(out.String(), "0.2.0 → 0.3.0") {
		t.Fatalf("output:\n%s", out.String())
	}
}

func TestUpdateUpToDateAndDevBuilds(t *testing.T) {
	newWorld(t).machine("alpha").activate()
	fakeRelease(t, "v0.3.0", []byte("new binary"), false)
	exe := withTarget(t)

	Version = "0.3.0"
	var out bytes.Buffer
	if code := Run([]string{"update"}, &out, &out); code != 0 || !strings.Contains(out.String(), "up to date") {
		t.Fatalf("%d %s", code, out.String())
	}
	Version = "dev"
	out.Reset()
	if code := Run([]string{"update"}, &out, &out); code == 0 || !strings.Contains(out.String(), "development build") {
		t.Fatalf("dev build updated without --version: %d %s", code, out.String())
	}
	if got, _ := os.ReadFile(exe); string(got) != "old binary" {
		t.Fatal("binary changed")
	}
}

func TestUpdateRefusesBadChecksum(t *testing.T) {
	newWorld(t).machine("alpha").activate()
	fakeRelease(t, "v0.3.0", []byte("evil binary"), true)
	exe := withTarget(t)
	Version = "0.2.0"
	var out bytes.Buffer
	if code := Run([]string{"update"}, &out, &out); code == 0 || !strings.Contains(out.String(), "checksum mismatch") {
		t.Fatalf("%d %s", code, out.String())
	}
	if got, _ := os.ReadFile(exe); string(got) != "old binary" {
		t.Fatal("binary replaced despite bad checksum")
	}
}

func TestUpdateRequireAttestation(t *testing.T) {
	newWorld(t).machine("alpha").activate()
	fakeRelease(t, "v0.3.0", []byte("new binary"), false)
	exe := withTarget(t)
	Version = "0.2.0"
	var out bytes.Buffer
	if code := Run([]string{"update", "--require-attestation"}, &out, &out); code == 0 {
		t.Fatalf("installed without attestation: %s", out.String())
	}
	if got, _ := os.ReadFile(exe); string(got) != "old binary" {
		t.Fatal("binary replaced without required attestation")
	}
}

func TestSemver(t *testing.T) {
	a, _ := parseSemver("v0.10.0")
	b, _ := parseSemver("0.9.9")
	if !semverLess(b, a) || semverLess(a, b) {
		t.Fatal("0.9.9 < 0.10.0")
	}
	for _, bad := range []string{"", "1.2", "v1.x.3", "dev"} {
		if _, ok := parseSemver(bad); ok {
			t.Errorf("parseSemver(%q) accepted", bad)
		}
	}
}

func TestPackagedAt(t *testing.T) {
	sources, owned := t.TempDir(), false
	oldSources, oldOwns := aptSourcesDir, dpkgOwns
	aptSourcesDir, dpkgOwns = sources, func() bool { return owned }
	t.Cleanup(func() { aptSourcesDir, dpkgOwns = oldSources, oldOwns })

	for _, exe := range []string{"/home/me/.local/bin/dotsync", "/usr/local/bin/dotsync", "/opt/Cellar/other/1.0/bin/dotsync"} {
		if pm, ok := packagedAt(exe); ok {
			t.Errorf("%s: detected %s", exe, pm.name)
		}
	}
	for exe, bin := range map[string]string{
		"/opt/homebrew/Cellar/dotsync/0.6.0/bin/dotsync":              "/opt/homebrew/bin/dotsync",
		"/home/linuxbrew/.linuxbrew/Cellar/dotsync/1.0.0/bin/dotsync": "/home/linuxbrew/.linuxbrew/bin/dotsync",
	} {
		if pm, ok := packagedAt(exe); !ok || pm.name != "Homebrew" || pm.bin != bin || pm.upgrade != "brew upgrade dotsync" {
			t.Errorf("%s: %+v %v", exe, pm, ok)
		}
	}

	if pm, ok := packagedAt("/usr/bin/dotsync"); !ok || pm.name == "apt" {
		t.Errorf("not installed by dpkg: %+v %v", pm, ok)
	}
	owned = true
	if pm, _ := packagedAt("/usr/bin/dotsync"); pm.name != "apt" || !strings.Contains(pm.upgrade, "releases/latest") {
		t.Errorf("downloaded .deb: %+v", pm)
	}
	os.WriteFile(filepath.Join(sources, "dotsync.sources"), []byte("Types: deb\n"), 0o644)
	if pm, _ := packagedAt("/usr/bin/dotsync"); pm.bin != "/usr/bin/dotsync" || !strings.HasPrefix(pm.upgrade, "sudo apt update") {
		t.Errorf("APT repository: %+v", pm)
	}
}

func TestUpdateDefersToPackageManager(t *testing.T) {
	newWorld(t).machine("alpha").activate()
	fakeRelease(t, "v0.3.0", []byte("new binary"), false)
	prefix := t.TempDir()
	exe := filepath.Join(prefix, "Cellar", "dotsync", "0.2.0", "bin", "dotsync")
	os.MkdirAll(filepath.Dir(exe), 0o755)
	os.WriteFile(exe, []byte("old binary"), 0o755)
	old := updateTarget
	updateTarget = func() (string, error) { return exe, nil }
	t.Cleanup(func() { updateTarget = old })
	Version = "0.2.0"

	if bin := NewPaths().Bin; bin != filepath.Join(prefix, "bin", "dotsync") {
		t.Errorf("the agent would run %s, not Homebrew's stable link", bin)
	}
	var out bytes.Buffer
	if code := Run([]string{"update", "--check"}, &out, &out); code != 0 || !strings.Contains(out.String(), "run `brew upgrade dotsync`") {
		t.Fatalf("check: %d %s", code, out.String())
	}
	out.Reset()
	if code := Run([]string{"update"}, &out, &out); code == 0 || !strings.Contains(out.String(), "installed with Homebrew") {
		t.Fatalf("update: %d %s", code, out.String())
	}
	if got, _ := os.ReadFile(exe); string(got) != "old binary" {
		t.Fatal("replaced a binary owned by Homebrew")
	}
}
