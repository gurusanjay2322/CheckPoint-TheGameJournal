package steam

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

const callbackURL = "https://api.example.com/api/v1/auth/steam/callback"

func validParams() url.Values {
	return url.Values{
		"openid.ns":             {"http://specs.openid.net/auth/2.0"},
		"openid.mode":           {"id_res"},
		"openid.op_endpoint":    {openIDEndpoint},
		"openid.claimed_id":     {"https://steamcommunity.com/openid/id/76561197960287930"},
		"openid.identity":       {"https://steamcommunity.com/openid/id/76561197960287930"},
		"openid.return_to":      {callbackURL + "?state=abc"},
		"openid.response_nonce": {"2026-11-11T00:00:00Zxyz"},
		"openid.assoc_handle":   {"1234567890"},
		"openid.signed":         {"signed,op_endpoint,claimed_id,identity,return_to,response_nonce,assoc_handle"},
		"openid.sig":            {"sig"},
	}
}

// fakeSteam answers check_authentication with the given body and records the
// form it received.
func fakeSteam(body string, got *url.Values) *Client {
	return &Client{HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if err := r.ParseForm(); err != nil {
			return nil, err
		}
		if got != nil {
			*got = r.PostForm
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}}, nil
	})}}
}

func TestVerifyOpenIDAccepts(t *testing.T) {
	var sent url.Values
	c := fakeSteam("ns:http://specs.openid.net/auth/2.0\nis_valid:true\n", &sent)

	id, err := c.VerifyOpenID(validParams(), callbackURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "76561197960287930" {
		t.Fatalf("steam id = %q", id)
	}
	if sent.Get("openid.mode") != "check_authentication" || sent.Get("openid.sig") != "sig" {
		t.Fatalf("check_authentication form not forwarded correctly: %v", sent)
	}
}

func TestVerifyOpenIDRejects(t *testing.T) {
	cases := map[string]func(url.Values){
		"wrong mode":        func(p url.Values) { p.Set("openid.mode", "cancel") },
		"foreign endpoint":  func(p url.Values) { p.Set("openid.op_endpoint", "https://evil.example/openid/login") },
		"foreign return_to": func(p url.Values) { p.Set("openid.return_to", "https://evil.example/api/v1/auth/steam/callback") },
		"bad claimed_id":    func(p url.Values) { p.Set("openid.claimed_id", "https://evil.example/openid/id/76561197960287930") },
		"identity mismatch": func(p url.Values) { p.Set("openid.identity", "https://steamcommunity.com/openid/id/76561197960287931") },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			p := validParams()
			mutate(p)
			if _, err := fakeSteam("is_valid:true\n", nil).VerifyOpenID(p, callbackURL); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}

	t.Run("steam says invalid", func(t *testing.T) {
		if _, err := fakeSteam("is_valid:false\n", nil).VerifyOpenID(validParams(), callbackURL); err == nil {
			t.Fatal("expected rejection")
		}
	})
}
