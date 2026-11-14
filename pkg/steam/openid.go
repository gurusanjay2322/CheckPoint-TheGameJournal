package steam

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

const openIDEndpoint = "https://steamcommunity.com/openid/login"

var claimedIDPattern = regexp.MustCompile(`^https://steamcommunity\.com/openid/id/(\d{17})$`)

// OpenIDLoginURL builds the Steam sign-in URL. Steam redirects the browser
// back to returnTo with a signed assertion; realm must be a prefix of returnTo.
func OpenIDLoginURL(returnTo, realm string) string {
	params := url.Values{
		"openid.ns":         {"http://specs.openid.net/auth/2.0"},
		"openid.mode":       {"checkid_setup"},
		"openid.return_to":  {returnTo},
		"openid.realm":      {realm},
		"openid.identity":   {"http://specs.openid.net/auth/2.0/identifier_select"},
		"openid.claimed_id": {"http://specs.openid.net/auth/2.0/identifier_select"},
	}
	return openIDEndpoint + "?" + params.Encode()
}

// VerifyOpenID checks a Steam OpenID callback with Steam itself and returns
// the authenticated 64-bit Steam ID. expectedReturnTo is the callback URL
// without query parameters; the signed return_to must point at it.
func (c *Client) VerifyOpenID(params url.Values, expectedReturnTo string) (string, error) {
	if params.Get("openid.mode") != "id_res" {
		return "", errors.New("openid: unexpected mode")
	}
	if params.Get("openid.op_endpoint") != openIDEndpoint {
		return "", errors.New("openid: unexpected op_endpoint")
	}

	returnTo, err := url.Parse(params.Get("openid.return_to"))
	if err != nil {
		return "", errors.New("openid: invalid return_to")
	}
	returnTo.RawQuery = ""
	returnTo.Fragment = ""
	if returnTo.String() != expectedReturnTo {
		return "", errors.New("openid: return_to mismatch")
	}

	match := claimedIDPattern.FindStringSubmatch(params.Get("openid.claimed_id"))
	if match == nil || params.Get("openid.identity") != params.Get("openid.claimed_id") {
		return "", errors.New("openid: invalid claimed_id")
	}

	check := url.Values{}
	for key, values := range params {
		if strings.HasPrefix(key, "openid.") {
			check[key] = values
		}
	}
	check.Set("openid.mode", "check_authentication")

	resp, err := c.HTTPClient.PostForm(openIDEndpoint, check)
	if err != nil {
		return "", fmt.Errorf("openid: verification request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", fmt.Errorf("openid: reading verification response: %w", err)
	}
	if resp.StatusCode != http.StatusOK || !isValidAssertion(string(body)) {
		return "", errors.New("openid: assertion rejected by steam")
	}

	return match[1], nil
}

func isValidAssertion(body string) bool {
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == "is_valid:true" {
			return true
		}
	}
	return false
}
