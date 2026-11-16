package verify

import (
	"regexp"
	"strings"
	"unicode"
)

// SecretFound rejects an item that would carry a credential: a password, token, API key, private key or a connection
// string with a password in it (design 5.5). The tools facet keeps which script, alias or variable holds a secret,
// never the secret itself.
const SecretFound = "SECRET"

// secretPatterns are the shapes credentials come in. Each is specific enough that ordinary text does not match: a
// git commit hash, a UUID or a version number is not a secret.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`),
	regexp.MustCompile(`\b(AKIA|ASIA)[0-9A-Z]{16}\b`),                                        // AWS access key id
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{30,}\b|\bgithub_pat_[A-Za-z0-9_]{30,}`),      // GitHub
	regexp.MustCompile(`\bsk-(ant-|proj-|live-|test-)?[A-Za-z0-9_\-]{20,}`),                  // OpenAI, Anthropic, Stripe-style keys
	regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9\-]{10,}`),                                    // Slack
	regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}\b`),                                         // Google API key
	regexp.MustCompile(`\bglpat-[A-Za-z0-9_\-]{20,}`),                                        // GitLab
	regexp.MustCompile(`\beyJ[A-Za-z0-9_\-]{8,}\.eyJ[A-Za-z0-9_\-]{8,}\.[A-Za-z0-9_\-]{8,}`), // JWT
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9_\-\.=]{16,}`),
	// scheme://user:password@host: a connection string or URL with a password in it.
	regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.\-]{1,20}://[^\s:/@]*:[^\s@/]{2,}@[^\s]+`),
}

// keyValue are a credential's name followed by its value ("password: hunter22", "api_key=…", "密码是 abc123"); the
// value (group 1) is then checked by literal.
var keyValue = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:password|passwd|pwd|passphrase|secret|client_secret|app_secret|api[_\-]?key|access[_\-]?key|secret[_\-]?key|private[_\-]?key|auth[_\-]?token|access[_\-]?token|refresh[_\-]?token|token)\b["']?\s*[:=]\s*["']?([\x21-\x7e]{6,})`),
	regexp.MustCompile(`(?i)(?:密码|口令|密钥|令牌|私钥|秘钥|token)\s*(?:是|为|[:：=])\s*["'“]?([\x21-\x7e]{6,})`),
}

// literal says whether a value after a credential's name is the credential itself, not a reference to where it is
// kept ($VAR, ${VAR}, <placeholder>, ***) or a number (a token count).
func literal(v string) bool {
	v = strings.Trim(v, `"'“”,;.)`)
	if len(v) < 6 || strings.HasPrefix(v, Redacted) || strings.HasPrefix(v, "$") || strings.HasPrefix(v, "<") || strings.HasPrefix(v, "%") ||
		strings.Trim(v, "*xX.") == "" {
		return false
	}
	digits := 0
	for _, r := range v {
		if unicode.IsDigit(r) {
			digits++
		}
	}
	return digits < len(v)
}

// opaque finds long random-looking strings: 32 or more letters and digits mixing upper case, lower case and digits.
// Pure hex (commit hashes, digests) and words are left alone.
var opaque = regexp.MustCompile(`[A-Za-z0-9_\-+/]{32,}={0,2}`)

func randomLooking(s string) bool {
	var up, low, dig int
	for _, r := range s {
		switch {
		case unicode.IsUpper(r):
			up++
		case unicode.IsLower(r):
			low++
		case unicode.IsDigit(r):
			dig++
		}
	}
	// A key mixes all three throughout; an identifier made of words ("parseObserveTestHelpers2") has a digit or
	// two at most, and a path has separators between its words.
	return up >= 3 && low >= 3 && dig >= 3 && strings.Count(s, "/") < 2
}

// Secret says whether a text carries a credential.
func Secret(s string) bool {
	for _, re := range secretPatterns {
		if re.MatchString(s) {
			return true
		}
	}
	for _, re := range keyValue {
		for _, m := range re.FindAllStringSubmatch(s, -1) {
			if literal(m[1]) {
				return true
			}
		}
	}
	for _, m := range opaque.FindAllString(s, -1) {
		if randomLooking(m) {
			return true
		}
	}
	return false
}

// Redacted is what a credential is replaced with in a quote that is kept.
const Redacted = "[secret]"

// Redact replaces every credential in s with Redacted: evidence keeps pointing at its line without carrying the
// secret.
func Redact(s string) string {
	for _, re := range secretPatterns {
		s = re.ReplaceAllString(s, Redacted)
	}
	for _, re := range keyValue {
		s = re.ReplaceAllStringFunc(s, func(m string) string {
			if sub := re.FindStringSubmatch(m); sub != nil && literal(sub[1]) {
				return strings.Replace(m, sub[1], Redacted, 1)
			}
			return m
		})
	}
	return opaque.ReplaceAllStringFunc(s, func(m string) string {
		if randomLooking(m) {
			return Redacted
		}
		return m
	})
}
