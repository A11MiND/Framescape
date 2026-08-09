package httpapi

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// tokenType distinguishes access vs refresh JWTs so a leaked/expired refresh
// token can't be replayed as an access token and vice versa.
type tokenType string

const (
	tokenAccess  tokenType = "access"
	tokenRefresh tokenType = "refresh"

	accessTTL  = 7 * 24 * time.Hour // F1.1: "token 7 天"
	refreshTTL = 30 * 24 * time.Hour
)

type claims struct {
	UserID uint64    `json:"uid"`
	Type   tokenType `json:"typ"`
	jwt.RegisteredClaims
}

func signToken(secret string, userID uint64, typ tokenType, ttl time.Duration) (string, error) {
	c := claims{
		UserID: userID,
		Type:   typ,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(secret))
}

func parseToken(secret string, raw string, want tokenType) (*claims, error) {
	var c claims
	token, err := jwt.ParseWithClaims(raw, &c, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, err
	}
	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}
	if c.Type != want {
		return nil, fmt.Errorf("wrong token type: want %s got %s", want, c.Type)
	}
	return &c, nil
}
