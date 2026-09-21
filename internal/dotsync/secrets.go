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

// secretContentReason scans data line by line. For a format that encrypts values within a
// readable file, the encrypted values are removed first and everything else is still scanned.
func secretContentReason(data []byte) string {
	f := cipherFormatOf(data)
	for _, line := range splitLines(data) {
		if bytes.Contains(line, allowMarker) {
			continue
		}
		if f != nil && f.values != nil {
			line = f.values.ReplaceAll(line, nil)
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
// Encrypted files are judged by what is still readable: a file that is entirely ciphertext always
// passes; one that only encrypts values may be named like a secret, but its readable parts are
// still scanned.
func secretReason(path string, o *Obj) string {
	if o == nil || o.Kind != kindFile {
		return secretPathReason(path)
	}
	f := cipherFormatOf(o.Data)
	if f != nil && f.values == nil {
		return ""
	}
	if f == nil {
		if r := secretPathReason(path); r != "" {
			return r
		}
	}
	return secretContentReason(o.Data)
}

// cipherFormat recognises an encrypted file format.
type cipherFormat struct {
	detect func(data []byte) bool
	values *regexp.Regexp // encrypted values in an otherwise readable file; nil when the whole file is ciphertext
}

// cipherFormats are the encrypted formats dotsync recognises. They're data: the code above
// never refers to a format by name.
var cipherFormats = []cipherFormat{
	// age, binary: the version line, recipient stanzas, then the header MAC line ("--- …").
	{detect: func(d []byte) bool {
		return bytes.HasPrefix(d, []byte("age-encryption.org/v1\n")) && bytes.Contains(d[:min(len(d), 64<<10)], []byte("\n--- "))
	}},
	// age, ASCII armor: nothing but base64 between the armor lines.
	{detect: func(d []byte) bool {
		return armored(d, []byte("-----BEGIN AGE ENCRYPTED FILE-----"), []byte("-----END AGE ENCRYPTED FILE-----"))
	}},
	// sops: every encrypted file carries an encrypted MAC, in each of sops's formats; values are
	// encrypted individually.
	{
		detect: regexp.MustCompile(`(?m)(?:"mac"\s*:\s*"|^\s*mac:\s*|^\s*mac\s*=\s*"?|^sops_mac=)ENC\[AES256_GCM,`).Match,
		values: regexp.MustCompile(`ENC\[AES256_GCM,[^\]]*\]`),
	},
}

func cipherFormatOf(data []byte) *cipherFormat {
	for i := range cipherFormats {
		if cipherFormats[i].detect(data) {
			return &cipherFormats[i]
		}
	}
	return nil
}

var base64Line = regexp.MustCompile(`^[A-Za-z0-9+/]*={0,2}$`)

// armored reports whether data is a single ASCII-armored block containing only base64.
func armored(data, begin, end []byte) bool {
	t := bytes.TrimSpace(data)
	if !bytes.HasPrefix(t, begin) || !bytes.HasSuffix(t, end) {
		return false
	}
	for _, line := range splitLines(bytes.TrimSpace(t[len(begin) : len(t)-len(end)])) {
		if !base64Line.Match(bytes.TrimRight(line, "\r")) {
			return false
		}
	}
	return true
}
