package x

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// The signature base string of RFC 5849 section 3.4.1.1's example: a query with encoded and empty values, a
// repeated name and a form body's parameters, sorted by name and value.
func TestSignatureBaseRFC5849(t *testing.T) {
	u, _ := url.Parse("http://example.com/request?b5=%3D%253D&a3=a&c%40=&a2=r%20b")
	body := url.Values{"c2": {""}, "a3": {"2 q"}}
	oauth := map[string]string{
		"oauth_consumer_key":     "9djdj82h48djs9d2",
		"oauth_token":            "kkk9d7dh3k39sjv7",
		"oauth_signature_method": "HMAC-SHA1",
		"oauth_timestamp":        "137131201",
		"oauth_nonce":            "7d8f3e4a",
		"oauth_signature":        "left out",
	}
	want := "POST&http%3A%2F%2Fexample.com%2Frequest&a2%3Dr%2520b%26a3%3D2%2520q%26a3%3Da%26b5%3D%253D%25253D%26c%2540%3D%26c2%3D%26oauth_consumer_key%3D9djdj82h48djs9d2%26oauth_nonce%3D7d8f3e4a%26oauth_signature_method%3DHMAC-SHA1%26oauth_timestamp%3D137131201%26oauth_token%3Dkkk9d7dh3k39sjv7"
	if got := signatureBase("POST", u, body, oauth); got != want {
		t.Fatalf("base string\n got %s\nwant %s", got, want)
	}
}

// The OAuth Core 1.0 example (Appendix A.5, the request RFC 5849 builds on): its HMAC-SHA1 signature is the known
// one, and the header carries it encoded.
func TestSignatureVector(t *testing.T) {
	k := Keys{APIKey: "dpf43f3p2l4k3l03", APISecret: "kd94hf93k423kf44", AccessToken: "nnch734d00sl2jdk", AccessTokenSecret: "pfkkdhi9sl3r4s00"}
	u, _ := url.Parse("http://photos.example.net/photos?file=vacation.jpg&size=original")
	at := time.Unix(1191242096, 0)
	p := oauthParams(k, "kllo9940pd9333jh", at)
	base := signatureBase("GET", u, nil, p)
	wantBase := "GET&http%3A%2F%2Fphotos.example.net%2Fphotos&file%3Dvacation.jpg%26oauth_consumer_key%3Ddpf43f3p2l4k3l03%26oauth_nonce%3Dkllo9940pd9333jh%26oauth_signature_method%3DHMAC-SHA1%26oauth_timestamp%3D1191242096%26oauth_token%3Dnnch734d00sl2jdk%26oauth_version%3D1.0%26size%3Doriginal"
	if base != wantBase {
		t.Fatalf("base string\n got %s\nwant %s", base, wantBase)
	}
	if got := hmacSign(base, k.APISecret, k.AccessTokenSecret); got != "tR3+Ty81lMeYAr/Fid0kMTYa/WM=" {
		t.Fatalf("signature %s", got)
	}
	h := authorization(k, "GET", u, "kllo9940pd9333jh", at)
	if !strings.HasPrefix(h, "OAuth ") || !strings.Contains(h, `oauth_signature="tR3%2BTy81lMeYAr%2FFid0kMTYa%2FWM%3D"`) {
		t.Fatalf("header %s", h)
	}
}

func TestPercentEncode(t *testing.T) {
	for in, want := range map[string]string{"Ladies + Gentlemen": "Ladies%20%2B%20Gentlemen", "An encoded string!": "An%20encoded%20string%21",
		"Dogs, Cats & Mice": "Dogs%2C%20Cats%20%26%20Mice", "☃": "%E2%98%83", "-._~": "-._~"} {
		if got := percentEncode(in); got != want {
			t.Errorf("%q: %s, want %s", in, got, want)
		}
	}
}
