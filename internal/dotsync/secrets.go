package dotsync

import (
	"bytes"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

// secretPaths are lower-cased glob patterns for files that are never uploaded unless the entry
// sets allow_secrets. Patterns containing '/' match the path relative to $HOME ('*' also matches
// '/'); the others match the file name only.
var secretPaths = []string{
	".ssh/id_*", ".ssh/*_key", "*.pem", "*.key", "*.p12", "*.pfx", "*.jks", "*.keystore", "*.kdbx",
	".gnupg/private-keys-v1.d/*", ".gnupg/secring.*", ".gnupg/*.key", ".password-store/*",
	".aws/credentials", ".aws/sso/*", ".azure/*", ".config/gcloud/*", ".kube/config", ".docker/config.json",
	".netrc", ".git-credentials", ".pgpass", ".vault-token", ".config/gh/hosts.yml", ".local/share/keyrings/*",
	".env", ".env.*", "*.env", "*secret*", "*credential*", ".*history",
	".config/mise/age.txt", ".config/sops/age/*", "library/application support/sops/age/*",
}

type secretRule struct {
	name string
	re   *regexp.Regexp
}

// secretContent is never uploaded unless the entry sets allow_secrets. A line containing
// allowMarker is exempt, for known false positives.
var secretContent = []secretRule{
	{"private key", regexp.MustCompile(`-----BEGIN (?:[A-Z0-9]+ )*PRIVATE KEY(?: BLOCK)?-----`)},
	{"private age key", regexp.MustCompile(`AGE-SECRET-KEY-1[02-9AC-HJ-NP-Z]{58}`)},
	{"AWS access key", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"GitHub token", regexp.MustCompile(`\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{22,})`)},
	{"GitLab token", regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}`)},
	{"Slack token", regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9-]{10,}`)},
	{"API key", regexp.MustCompile(`\bsk-(?:ant-|proj-)?[A-Za-z0-9_-]{20,}`)},
	{"Google API key", regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`)},
	{"npm token", regexp.MustCompile(`_authToken\s*=\s*[^\s$]{8,}`)},
	// scheme://user:password@host, unless the password is read from somewhere ($VAR, {{ }}, %s).
	{"password in a URL", regexp.MustCompile(`\b[A-Za-z][A-Za-z0-9+.-]*://[^\s/:@]*:[^\s/@$({<%][^\s/@]*@`)},
	// key = value where the value is a literal of 8+ characters (not a $VAR or $(command)).
	{"password or token assignment", regexp.MustCompile(
		`(?i)(?:password|passwd|secret|token|api[_-]?key|access[_-]?key|client[_-]?secret)` +
			`["']?\s*[:=]\s*["']?[^\s"'$(){}<>]{8,}`)},
}

var allowMarker = []byte("dotsync:allow-secret")

func secretPathReason(path string) string {
	rel := strings.ToLower(homeRel(path))
	name := strings.ToLower(filepath.Base(path))
	for _, p := range secretPaths {
		subject := name
		if strings.Contains(p, "/") {
			subject = rel
		}
		if globMatch(p, subject) {
			return fmt.Sprintf("path looks like a secret (matches '%s')", p)
		}
	}
	return ""
}

func secretContentReason(data []byte) string {
	sops := encryption(data) == "sops"
	for _, line := range splitLines(data) {
		if bytes.Contains(line, allowMarker) {
			continue
		}
		if sops {
			// Remove only the encrypted values: anything else on the line (keys, comments,
			// unencrypted values, or everything in a minified file) is still scanned.
			line = sopsValue.ReplaceAll(line, nil)
		}
		for _, r := range secretContent {
			if r.re.Match(line) {
				return "content looks like a " + r.name
			}
		}
	}
	return ""
}

// secretReason explains why path/obj must not leave this machine, or returns "".
// Encrypted files are judged by what is still readable: age files are pure ciphertext and always
// pass; sops files may be named like secrets (.env.json) but their plaintext lines are still scanned.
func secretReason(path string, o *Obj) string {
	enc := ""
	if o != nil && o.Kind == "file" {
		enc = encryption(o.Data)
	}
	if enc == "age" {
		return ""
	}
	if enc == "" {
		if r := secretPathReason(path); r != "" {
			return r
		}
	}
	if o != nil && o.Kind == "file" {
		return secretContentReason(o.Data)
	}
	return ""
}

var (
	ageBinaryHeader = []byte("age-encryption.org/v1\n")
	ageHeaderMAC    = []byte("\n--- ")
	ageArmorBegin   = []byte("-----BEGIN AGE ENCRYPTED FILE-----")
	ageArmorEnd     = []byte("-----END AGE ENCRYPTED FILE-----")
	base64Line      = regexp.MustCompile(`^[A-Za-z0-9+/]*={0,2}$`)
	// sops writes an encrypted MAC into every file it encrypts, in each of its formats.
	sopsMAC   = regexp.MustCompile(`(?m)(?:"mac"\s*:\s*"|^\s*mac:\s*|^\s*mac\s*=\s*"?|^sops_mac=)ENC\[AES256_GCM,`)
	sopsValue = regexp.MustCompile(`ENC\[AES256_GCM,[^\]]*\]`)
)

// encryption reports "age" when data is entirely age ciphertext, "sops" when it's a
// sops-encrypted file, or "".
func encryption(data []byte) string {
	// Binary age: the version line, recipient stanzas, then the header MAC line ("--- …").
	if bytes.HasPrefix(data, ageBinaryHeader) && bytes.Contains(data[:min(len(data), 64<<10)], ageHeaderMAC) {
		return "age"
	}
	if t := bytes.TrimSpace(data); bytes.HasPrefix(t, ageArmorBegin) && bytes.HasSuffix(t, ageArmorEnd) {
		body := bytes.TrimSuffix(bytes.TrimPrefix(t, ageArmorBegin), ageArmorEnd)
		for _, line := range splitLines(bytes.TrimSpace(body)) {
			if !base64Line.Match(bytes.TrimRight(line, "\r")) {
				return ""
			}
		}
		return "age"
	}
	if sopsMAC.Match(data) {
		return "sops"
	}
	return ""
}
