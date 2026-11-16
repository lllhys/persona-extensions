package verify

import (
	"strings"
	"testing"
)

// Credentials of every common shape are found and cut out; what only names where a secret lives, and ordinary
// identifiers (commit hashes, UUIDs, versions, token counts), are not.
func TestSecrets(t *testing.T) {
	for _, s := range []string{
		"-----BEGIN OPENSSH PRIVATE KEY-----",
		"AKIAABCDEFGHIJKLMNOP",
		"export GH=ghp_ABCDEFGHIJKLMNOPQRSTUVWXYZabcdef123456",
		"github_pat_11ABCDEFG0123456789_abcdefghijklmnopqrstuvwxyz",
		"key sk-ant-api03-abcdefghijklmnopqrstuvwxyz0123",
		"xoxb-123456789012-abcdefghijkl",
		"AIzaSyA-abcdefghijklmnopqrstuvwxyz0123456",
		"Authorization: Bearer abcdefghijklmnopqrstuvwxyz012345",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiJ1c2VyLWEifQ.abcdefghijklmnop",
		"postgres://dev:hunter22@192.0.2.5:5432/app",
		"mongodb+srv://user-a:s3cretPass@cluster0.example.net/db",
		"redis://:pa55word@cache.example.com:6379",
		"password: Tr0ub4dor3x",
		"api_key=abcd1234efgh",
		`"access_token": "q8fJ2kLm9xYz"`,
		"测试库密码是 Abc12345!",
		"令牌：x9Yk2LmQ7pR4",
		"Kq7Zp2Lx9Vb4Nc8Md3Fg6Hj1Ws5Er0TyA",
	} {
		if !Secret(s) {
			t.Errorf("not found: %q", s)
		}
		if r := Redact(s); Secret(r) || !strings.Contains(r, Redacted) {
			t.Errorf("redact %q → %q", s, r)
		}
	}
	for _, s := range []string{
		"用 jump-dev 这个别名连测试机，密码在钥匙串的 dev-db 条目",
		"token: $GITHUB_TOKEN",
		"api_key=${OPENAI_API_KEY}",
		"password: <hub secret>",
		"commit 3f2a9c1e8b7d6a5f4e3d2c1b0a9f8e7d6c5b4a39 merged",
		"session 067856ad-b6db-4705-b5db-19d314c66131",
		"model-x via provider-x, max tokens: 6000",
		"token: 120000",
		"https://example.com/docs/setup",
		"ssh -p 2222 user-a@host-x",
		"internal/curate/observeTestHelpers.go",
	} {
		if Secret(s) {
			t.Errorf("false positive: %q", s)
		}
		if Redact(s) != s {
			t.Errorf("redacted without a secret: %q", s)
		}
	}
}
