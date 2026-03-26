package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const TokenExpiry = 24 * time.Hour

type Claims struct {
	Wallet string `json:"wallet"`
	Chain  string `json:"chain"`
	jwt.RegisteredClaims
}

func GenerateToken(wallet, chain, secret string) (string, time.Time, error) {
	expiresAt := time.Now().Add(TokenExpiry)

	claims := Claims{
		Wallet: wallet,
		Chain:  chain,
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			Issuer:    "orama-invest",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to sign JWT: %w", err)
	}

	return signed, expiresAt, nil
}

func ParseToken(tokenStr, secret string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
		}
		return []byte(secret), nil
	})
	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token claims")
	}

	return claims, nil
}
