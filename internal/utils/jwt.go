package utils

import (
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type JWTClaims struct {
	UserID string `json:"user_id"`
	jwt.RegisteredClaims
}

func GenerateJWT(userID string, secret string) (string, error) {
	return signClaims(userID, 72*time.Hour, []byte(secret))
}

func ParseJWT(tokenString string, secret string) (*JWTClaims, error) {
	return parseClaims(tokenString, []byte(secret))
}

// Steam link state tokens are signed with a derived key so they can never be
// accepted as access tokens (and vice versa).
func steamLinkKey(secret string) []byte {
	return []byte(secret + ":steam_link")
}

func GenerateSteamLinkState(userID string, secret string) (string, error) {
	return signClaims(userID, 10*time.Minute, steamLinkKey(secret))
}

func ParseSteamLinkState(tokenString string, secret string) (*JWTClaims, error) {
	return parseClaims(tokenString, steamLinkKey(secret))
}

func signClaims(userID string, ttl time.Duration, key []byte) (string, error) {
	claims := JWTClaims{
		UserID: userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(key)
}

func parseClaims(tokenString string, key []byte) (*JWTClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &JWTClaims{}, func(token *jwt.Token) (interface{}, error) {
		return key, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))

	if err != nil {
		return nil, err
	}

	if claims, ok := token.Claims.(*JWTClaims); ok && token.Valid {
		return claims, nil
	}

	return nil, jwt.ErrSignatureInvalid
}
