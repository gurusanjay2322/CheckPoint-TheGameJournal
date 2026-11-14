package utils

import (
	"testing"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "0123456789abcdef0123456789abcdef"

func TestSteamLinkStateIsNotAnAccessToken(t *testing.T) {
	state, err := GenerateSteamLinkState("user-1", testSecret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseJWT(state, testSecret); err == nil {
		t.Fatal("link state accepted as access token")
	}
	claims, err := ParseSteamLinkState(state, testSecret)
	if err != nil || claims.UserID != "user-1" {
		t.Fatalf("link state round-trip failed: %v", err)
	}
}

func TestAccessTokenIsNotALinkState(t *testing.T) {
	token, err := GenerateJWT("user-1", testSecret)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseSteamLinkState(token, testSecret); err == nil {
		t.Fatal("access token accepted as link state")
	}
}

func TestParseJWTRejectsOtherAlgorithms(t *testing.T) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS512, JWTClaims{UserID: "user-1"})
	signed, err := token.SignedString([]byte(testSecret))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ParseJWT(signed, testSecret); err == nil {
		t.Fatal("HS512 token accepted")
	}
}
