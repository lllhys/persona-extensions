package x

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// OAuth 1.0a user-context signing (RFC 5849 section 3, HMAC-SHA1), as X's API v2 takes it for the user's own
// developer app: the consumer key / secret of the app and the access token / secret it generated for its owner.
// Written out here (it is small) rather than taking a library: nothing else in the module needs one.

// Keys are the four values X's developer console gives the app's owner. They never leave the connector: not in a
// log, a status, an error, a tool result or the page.
type Keys struct {
	APIKey            string `json:"api_key"`
	APISecret         string `json:"api_secret"`
	AccessToken       string `json:"access_token"`
	AccessTokenSecret string `json:"access_token_secret"`
}

// complete says whether all four are there.
func (k Keys) complete() bool {
	return k.APIKey != "" && k.APISecret != "" && k.AccessToken != "" && k.AccessTokenSecret != ""
}

// secrets are the values that must never show anywhere (scrub).
func (k Keys) secrets() []string {
	return []string{k.APIKey, k.APISecret, k.AccessToken, k.AccessTokenSecret}
}

// percentEncode is RFC 5849 section 3.6: every byte but the unreserved characters as %XX, upper-case hex.
func percentEncode(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
			continue
		}
		b.WriteByte('%')
		b.WriteString(strings.ToUpper(hex.EncodeToString([]byte{c})))
	}
	return b.String()
}

// baseURI is RFC 5849 section 3.4.1.2: scheme and host lower-case, the default port left out, no query or fragment.
func baseURI(u *url.URL) string {
	scheme, host := strings.ToLower(u.Scheme), strings.ToLower(u.Host)
	if h, p, ok := strings.Cut(host, ":"); ok && (scheme == "http" && p == "80" || scheme == "https" && p == "443") {
		host = h
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	return scheme + "://" + host + path
}

// signatureBase is RFC 5849 section 3.4.1: the method, the base URI and every parameter (the URL's query, extra —
// a form body's — and the oauth_* ones but the signature), each encoded, sorted by name then value, and joined.
func signatureBase(method string, u *url.URL, extra url.Values, oauth map[string]string) string {
	type pair struct{ k, v string }
	var ps []pair
	for k, vs := range u.Query() {
		for _, v := range vs {
			ps = append(ps, pair{percentEncode(k), percentEncode(v)})
		}
	}
	for k, vs := range extra {
		for _, v := range vs {
			ps = append(ps, pair{percentEncode(k), percentEncode(v)})
		}
	}
	for k, v := range oauth {
		if k != "oauth_signature" {
			ps = append(ps, pair{percentEncode(k), percentEncode(v)})
		}
	}
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].k != ps[j].k {
			return ps[i].k < ps[j].k
		}
		return ps[i].v < ps[j].v
	})
	parts := make([]string, len(ps))
	for i, p := range ps {
		parts[i] = p.k + "=" + p.v
	}
	return strings.ToUpper(method) + "&" + percentEncode(baseURI(u)) + "&" + percentEncode(strings.Join(parts, "&"))
}

// hmacSign is RFC 5849 section 3.4.2: HMAC-SHA1 of the base string keyed by both secrets, in base64.
func hmacSign(base, consumerSecret, tokenSecret string) string {
	mac := hmac.New(sha1.New, []byte(percentEncode(consumerSecret)+"&"+percentEncode(tokenSecret)))
	mac.Write([]byte(base))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

// oauthParams are the protocol parameters of one request (without the signature).
func oauthParams(k Keys, nonce string, at time.Time) map[string]string {
	return map[string]string{
		"oauth_consumer_key":     k.APIKey,
		"oauth_nonce":            nonce,
		"oauth_signature_method": "HMAC-SHA1",
		"oauth_timestamp":        strconv.FormatInt(at.Unix(), 10),
		"oauth_token":            k.AccessToken,
		"oauth_version":          "1.0",
	}
}

// authorization is the Authorization header for a request signed with k (RFC 5849 section 3.5.1).
func authorization(k Keys, method string, u *url.URL, nonce string, at time.Time) string {
	p := oauthParams(k, nonce, at)
	p["oauth_signature"] = hmacSign(signatureBase(method, u, nil, p), k.APISecret, k.AccessTokenSecret)
	return header(p)
}

// header writes the parameters as an OAuth Authorization header, sorted for a stable order.
func header(p map[string]string) string {
	keys := make([]string, 0, len(p))
	for k := range p {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = percentEncode(k) + `="` + percentEncode(p[k]) + `"`
	}
	return "OAuth " + strings.Join(parts, ", ")
}

// nonce is a fresh random nonce.
func nonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Verify says whether header is a valid signature by k of a request (method, the full URL as sent): the check X
// makes, for fake servers in tests.
func Verify(k Keys, method, rawURL, header string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || !strings.HasPrefix(header, "OAuth ") {
		return false
	}
	p := map[string]string{}
	for _, part := range strings.Split(strings.TrimPrefix(header, "OAuth "), ", ") {
		name, quoted, ok := strings.Cut(part, "=")
		if !ok {
			return false
		}
		v, err := url.PathUnescape(strings.Trim(quoted, `"`))
		if err != nil {
			return false
		}
		p[name] = v
	}
	if p["oauth_consumer_key"] != k.APIKey || p["oauth_token"] != k.AccessToken || p["oauth_signature_method"] != "HMAC-SHA1" {
		return false
	}
	want := hmacSign(signatureBase(method, u, nil, p), k.APISecret, k.AccessTokenSecret)
	return hmac.Equal([]byte(want), []byte(p["oauth_signature"]))
}
