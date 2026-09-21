package dotsync

import (
	"archive/tar"
	"bufio"
	"cmp"
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

// releaseHost is where releases are published and how their provenance is checked.
type releaseHost struct {
	repo         string // owner/name
	api          string // REST API base URL
	download     string // base URL of release downloads
	verifyWithGH bool   // check build provenance with the GitHub CLI, when it's installed
}

func (h releaseHost) latestURL() string { return h.api + "/repos/" + h.repo + "/releases/latest" }

func (h releaseHost) assetURL(tag, name string) string {
	return h.download + "/" + h.repo + "/releases/download/" + tag + "/" + name
}

// Overridable in tests.
var (
	releases     = releaseHost{repo: "pungoyal/dotsync", api: "https://api.github.com", download: "https://github.com", verifyWithGH: true}
	updateTarget = os.Executable
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
	if strings.HasPrefix(url, releases.api) {
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
	resp, err := httpGet(ctx, releases.latestURL())
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

// refreshAgent re-installs the background agent with the new binary, so its definition (e.g.
// scheduling and priority settings) matches the new version. Running the *new* binary matters:
// this process is still the old one.
func refreshAgent(say func(string, ...any), newBinary string) {
	c, err := newCtx(true, true, io.Discard, io.Discard)
	if err != nil {
		return // not set up on this machine: nothing to refresh
	}
	if installed, _ := agentInstalled(c); !installed {
		return
	}
	out, err := exec.Command(newBinary, "agent", "install").CombinedOutput()
	if err != nil {
		say("note: could not refresh the background agent (%s); run `dotsync agent install`", lastLine(string(out)))
		return
	}
	say("background agent refreshed: %s", strings.TrimSpace(string(out)))
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
	tag, err := releaseTag(ctx, *want)
	if err != nil && *want == "" {
		return 1, err // couldn't reach the release host
	} else if err != nil {
		return 2, err
	}
	target, _ := parseSemver(tag)
	if cur, ok := parseSemver(current); ok && *want == "" && !semverLess(cur, target) {
		say("dotsync %s is up to date", current)
		return 0, nil
	}
	version := strings.TrimPrefix(tag, "v")
	switch {
	case *check && current == "":
		say("latest release: %s (this is a development build)", tag)
		return 0, nil
	case *check:
		say("update available: %s → %s (%s)", current, version, runHint(upgradeHint()))
		return 0, nil
	}
	if pm, ok := packaged(); ok {
		return 1, fmt.Errorf("dotsync was installed with %s, which also updates it: %s", pm.name, pm.upgrade)
	}
	if current == "" && *want == "" {
		return 1, errors.New("this is a development build; pass --version to replace it with a release")
	}
	targets, err := installTargets()
	if err != nil {
		return 1, err
	}
	tmp, err := os.MkdirTemp("", "dotsync-update-")
	if err != nil {
		return 1, err
	}
	defer os.RemoveAll(tmp)
	bin, err := downloadRelease(ctx, tag, tmp, *requireAttestation, say)
	if err != nil {
		return 1, err
	}
	for _, t := range targets {
		if err := replaceBinary(bin, t); err != nil {
			return 1, fmt.Errorf("replacing %s: %w", tilde(t), err)
		}
		say("installed %s", tilde(t))
	}
	say("dotsync %s → %s", cmp.Or(current, "development build"), version)
	refreshAgent(say, targets[len(targets)-1])
	return 0, nil
}

// releaseTag is the tag to install: the one asked for, or the latest release.
func releaseTag(ctx context.Context, want string) (string, error) {
	tag := want
	if tag == "" {
		latest, err := latestRelease(ctx)
		if err != nil {
			return "", fmt.Errorf("can't check for updates: %w", err)
		}
		tag = latest
	} else if !strings.HasPrefix(tag, "v") {
		tag = "v" + tag
	}
	if _, ok := parseSemver(tag); !ok {
		return "", fmt.Errorf("%q is not a release version like v0.3.0", tag)
	}
	return tag, nil
}

// installTargets are the binaries to replace: this one, and the copy the background agent runs
// if that's a different file.
func installTargets() ([]string, error) {
	exe, err := updateTarget()
	if err != nil {
		return nil, err
	}
	if real, err := filepath.EvalSymlinks(exe); err == nil {
		exe = real
	}
	targets := []string{exe}
	if p := NewPaths().Bin; p != exe {
		if _, err := os.Stat(p); err == nil {
			targets = append(targets, p)
		}
	}
	return targets, nil
}

// downloadRelease downloads this platform's archive of tag into dir, verifies its checksum and
// (when possible) its build provenance, and returns the path of the extracted binary.
func downloadRelease(ctx context.Context, tag, dir string, requireAttestation bool, say func(string, ...any)) (string, error) {
	name := fmt.Sprintf("dotsync_%s_%s_%s.tar.gz", strings.TrimPrefix(tag, "v"), runtime.GOOS, runtime.GOARCH)
	archive := filepath.Join(dir, name)
	say("downloading dotsync %s (%s/%s)", strings.TrimPrefix(tag, "v"), runtime.GOOS, runtime.GOARCH)
	if err := download(ctx, releases.assetURL(tag, name), archive); err != nil {
		return "", err
	}
	sums := filepath.Join(dir, "checksums.txt")
	if err := download(ctx, releases.assetURL(tag, "checksums.txt"), sums); err != nil {
		return "", err
	}
	wantSum, err := expectedChecksum(sums, name)
	if err != nil {
		return "", err
	}
	gotSum, err := fileSHA256(archive)
	if err != nil {
		return "", err
	}
	if gotSum != wantSum {
		return "", fmt.Errorf("checksum mismatch for %s: expected %s, got %s; nothing was changed", name, wantSum, gotSum)
	}
	say("checksum verified")

	switch {
	case verifyProvenance(ctx, archive):
		say("build provenance verified (built by %s's release workflow)", releases.repo)
	case requireAttestation:
		return "", errors.New("could not verify build provenance (is the GitHub CLI installed and logged in?); nothing was changed")
	default:
		say("note: build provenance not checked (install the GitHub CLI to verify it automatically)")
	}
	bin := filepath.Join(dir, "dotsync")
	return bin, extractBinary(archive, bin)
}

// verifyProvenance checks the archive's signed build provenance with the GitHub CLI.
func verifyProvenance(ctx context.Context, archive string) bool {
	gh, err := exec.LookPath("gh")
	if err != nil || !releases.verifyWithGH {
		return false
	}
	return exec.CommandContext(ctx, gh, "attestation", "verify", archive, "--repo", releases.repo).Run() == nil
}
