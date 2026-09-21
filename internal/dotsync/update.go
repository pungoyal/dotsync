package dotsync

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const releaseRepo = "pungoyal/dotsync"

// Overridable in tests.
var (
	updateAPIBase      = "https://api.github.com"
	updateDownloadBase = "https://github.com"
	updateTarget       = os.Executable
	updateVerifyWithGH = true
)

var httpClient = &http.Client{Timeout: 5 * time.Minute}

// parseSemver turns "v1.2.3", "1.2.3" or "1.2.3-rc.1" into comparable numbers.
func parseSemver(v string) ([3]int, bool) {
	var out [3]int
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+ "); i >= 0 {
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func semverLess(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}

// currentVersion is this binary's release version, or "" for a development build.
func currentVersion() string {
	s := strings.TrimPrefix(versionString(), "dotsync ")
	v := strings.SplitN(s, " ", 2)[0]
	if _, ok := parseSemver(v); !ok || strings.Contains(v, "-") {
		return ""
	}
	return strings.TrimPrefix(v, "v")
}

func httpGet(ctx context.Context, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "dotsync/"+Version)
	if strings.HasPrefix(url, updateAPIBase) {
		req.Header.Set("Accept", "application/vnd.github+json")
		if tok := os.Getenv("GITHUB_TOKEN"); tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	return resp, nil
}

// latestRelease returns the newest release tag, e.g. "v0.3.0".
func latestRelease(ctx context.Context) (string, error) {
	resp, err := httpGet(ctx, updateAPIBase+"/repos/"+releaseRepo+"/releases/latest")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var rel struct {
		TagName string `json:"tag_name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", fmt.Errorf("reading release information: %w", err)
	}
	if _, ok := parseSemver(rel.TagName); !ok {
		return "", fmt.Errorf("unexpected release tag %q", rel.TagName)
	}
	return rel.TagName, nil
}

func download(ctx context.Context, url, dest string) error {
	resp, err := httpGet(ctx, url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func expectedChecksum(checksums, name string) (string, error) {
	f, err := os.Open(checksums)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if fields := strings.Fields(sc.Text()); len(fields) == 2 && fields[1] == name {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("%s is not listed in checksums.txt", name)
}

// extractBinary copies the "dotsync" entry of a .tar.gz archive to dest.
func extractBinary(archive, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return errors.New("the archive contains no dotsync binary")
		}
		if err != nil {
			return err
		}
		if h.Typeflag != tar.TypeReg || filepath.Base(h.Name) != "dotsync" || h.Size > 200<<20 {
			continue
		}
		out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.CopyN(out, tr, h.Size); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	}
}

// replaceBinary atomically replaces path with the file at src, keeping it executable.
func replaceBinary(src, path string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return atomicWrite(path, data, 0o755)
}

func cmdUpdate(args []string, stdout, stderr io.Writer) (int, error) {
	fs := newFlags("update", "[--check] [--version vX.Y.Z] [--require-attestation]", stderr)
	check := fs.Bool("check", false, "only report whether an update is available")
	want := fs.String("version", "", "install this release instead of the latest (also allows downgrades)")
	requireAttestation := fs.Bool("require-attestation", false, "fail unless the build provenance can be verified with the GitHub CLI")
	if _, err := parseArgs(fs, args); err != nil {
		return 2, err
	}
	say := func(format string, a ...any) { fmt.Fprintf(stdout, format+"\n", a...) }
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	current := currentVersion()
	tag := *want
	if tag == "" {
		latest, err := latestRelease(ctx)
		if err != nil {
			return 1, fmt.Errorf("can't check for updates: %w", err)
		}
		tag = latest
	} else if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	target, ok := parseSemver(tag)
	if !ok {
		return 2, fmt.Errorf("%q is not a release version like v0.3.0", tag)
	}
	if current != "" {
		cur, _ := parseSemver(current)
		if *want == "" && !semverLess(cur, target) {
			say("dotsync %s is up to date", current)
			return 0, nil
		}
	}
	if *check {
		if current == "" {
			say("latest release: %s (this is a development build)", tag)
		} else {
			say("update available: %s → %s (run `dotsync update`)", current, strings.TrimPrefix(tag, "v"))
		}
		return 0, nil
	}
	if current == "" && *want == "" {
		return 1, errors.New("this is a development build; pass --version to replace it with a release")
	}

	exe, err := updateTarget()
	if err != nil {
		return 1, err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	tmp, err := os.MkdirTemp("", "dotsync-update-")
	if err != nil {
		return 1, err
	}
	defer os.RemoveAll(tmp)

	version := strings.TrimPrefix(tag, "v")
	name := fmt.Sprintf("dotsync_%s_%s_%s.tar.gz", version, runtime.GOOS, runtime.GOARCH)
	base := fmt.Sprintf("%s/%s/releases/download/%s/", updateDownloadBase, releaseRepo, tag)
	archive := filepath.Join(tmp, name)
	say("downloading dotsync %s (%s/%s)", version, runtime.GOOS, runtime.GOARCH)
	if err := download(ctx, base+name, archive); err != nil {
		return 1, err
	}
	sums := filepath.Join(tmp, "checksums.txt")
	if err := download(ctx, base+"checksums.txt", sums); err != nil {
		return 1, err
	}
	wantSum, err := expectedChecksum(sums, name)
	if err != nil {
		return 1, err
	}
	gotSum, err := fileSHA256(archive)
	if err != nil {
		return 1, err
	}
	if gotSum != wantSum {
		return 1, fmt.Errorf("checksum mismatch for %s: expected %s, got %s; nothing was changed", name, wantSum, gotSum)
	}
	say("checksum verified")

	verified := false
	if gh, err := exec.LookPath("gh"); err == nil && updateVerifyWithGH {
		cmd := exec.CommandContext(ctx, gh, "attestation", "verify", archive, "--repo", releaseRepo)
		if cmd.Run() == nil {
			verified = true
			say("build provenance verified (built by %s's release workflow)", releaseRepo)
		}
	}
	if !verified {
		if *requireAttestation {
			return 1, errors.New("could not verify build provenance (is the GitHub CLI installed and logged in?); nothing was changed")
		}
		say("note: build provenance not checked (install the GitHub CLI to verify it automatically)")
	}

	bin := filepath.Join(tmp, "dotsync")
	if err := extractBinary(archive, bin); err != nil {
		return 1, err
	}
	targets := []string{exe}
	// Keep the copy the background agent runs in step, if it's a different file.
	if p := NewPaths().Bin; p != exe {
		if _, err := os.Stat(p); err == nil {
			targets = append(targets, p)
		}
	}
	for _, t := range targets {
		if err := replaceBinary(bin, t); err != nil {
			return 1, fmt.Errorf("replacing %s: %w", tilde(t), err)
		}
		say("installed %s", tilde(t))
	}
	from := current
	if from == "" {
		from = "development build"
	}
	say("dotsync %s → %s", from, version)
	return 0, nil
}
