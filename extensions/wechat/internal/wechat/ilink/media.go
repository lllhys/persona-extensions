package ilink

import (
	"context"
	"crypto/aes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"time"
)

// cdnOrigin serves media that has an encrypted query but no full URL.
const cdnOrigin = "https://novac2c.cdn.weixin.qq.com"

// MaxMedia is the largest media Download accepts (decrypted).
const MaxMedia = 8 << 20

var hex32 = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

// Download fetches m from WeChat's CDN and decrypts it (AES-128-ECB, PKCS#7).
func (c *Client) Download(ctx context.Context, m Media) ([]byte, error) {
	src, err := mediaURL(m)
	if err != nil {
		return nil, err
	}
	key, err := mediaKey(m)
	if err != nil {
		return nil, err
	}
	cctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(cctx, http.MethodGet, src, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &Error{Code: CodeNetwork, Msg: "media download failed", Retry: true, err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &Error{Code: CodeHTTP, Msg: "media download answered HTTP " + resp.Status, Retry: resp.StatusCode >= 500}
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, MaxMedia+aes.BlockSize+1))
	if err != nil {
		return nil, &Error{Code: CodeNetwork, Msg: "media download failed", Retry: true, err: err}
	}
	if len(data) > MaxMedia+aes.BlockSize {
		return nil, &Error{Code: CodeBadResponse, Msg: "media too large"}
	}
	return DecryptECB(data, key)
}

func mediaURL(m Media) (string, error) {
	if m.URL != "" {
		u, err := url.Parse(m.URL)
		if err != nil || u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") || !tencentHost(u.Hostname()) {
			return "", &Error{Code: CodeProtocol, Msg: "media URL is not a Tencent HTTPS URL"}
		}
		return u.String(), nil
	}
	if m.Query == "" {
		return "", &Error{Code: CodeProtocol, Msg: "media has no download reference"}
	}
	return cdnOrigin + "/c2c/download?" + url.Values{"encrypted_query_param": {m.Query}}.Encode(), nil
}

// mediaKey is the 16-byte key: an image's hex key, else base64 of the raw key or of its hex text.
func mediaKey(m Media) ([]byte, error) {
	if hex32.MatchString(m.KeyHex) {
		return hex.DecodeString(m.KeyHex)
	}
	if m.KeyB64 == "" {
		return nil, &Error{Code: CodeProtocol, Msg: "media has no key"}
	}
	b, err := base64.StdEncoding.DecodeString(m.KeyB64)
	switch {
	case err != nil:
	case len(b) == 16:
		return b, nil
	case len(b) == 32 && hex32.Match(b):
		return hex.DecodeString(string(b))
	}
	return nil, &Error{Code: CodeProtocol, Msg: "media key has an invalid encoding"}
}

var errPadding = errors.New("bad padding")

// DecryptECB decrypts AES-128-ECB with PKCS#7 padding.
func DecryptECB(data, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil || len(key) != 16 {
		return nil, &Error{Code: CodeProtocol, Msg: "media key must be 16 bytes"}
	}
	if len(data) == 0 || len(data)%aes.BlockSize != 0 {
		return nil, &Error{Code: CodeBadResponse, Msg: "media is not whole AES blocks"}
	}
	out := make([]byte, len(data))
	for i := 0; i < len(data); i += aes.BlockSize {
		block.Decrypt(out[i:i+aes.BlockSize], data[i:i+aes.BlockSize])
	}
	n := int(out[len(out)-1])
	if n == 0 || n > aes.BlockSize {
		return nil, &Error{Code: CodeBadResponse, Msg: "media could not be decrypted", err: errPadding}
	}
	for _, p := range out[len(out)-n:] {
		if int(p) != n {
			return nil, &Error{Code: CodeBadResponse, Msg: "media could not be decrypted", err: errPadding}
		}
	}
	return out[:len(out)-n], nil
}

// EncryptECB is DecryptECB's inverse (tests, and uploading media later).
func EncryptECB(data, key []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil || len(key) != 16 {
		return nil, &Error{Code: CodeProtocol, Msg: "media key must be 16 bytes"}
	}
	n := aes.BlockSize - len(data)%aes.BlockSize
	in := append(append([]byte{}, data...), make([]byte, n)...)
	for i := len(data); i < len(in); i++ {
		in[i] = byte(n)
	}
	out := make([]byte, len(in))
	for i := 0; i < len(in); i += aes.BlockSize {
		block.Encrypt(out[i:i+aes.BlockSize], in[i:i+aes.BlockSize])
	}
	return out, nil
}
