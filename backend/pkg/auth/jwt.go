package auth

import (
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type Claims struct {
	UserID   uuid.UUID
	TenantID uuid.UUID
}

func Sign(secret string, claims Claims) (string, error) {
	return jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{"sub": claims.UserID.String(), "tenant_id": claims.TenantID.String()}).SignedString([]byte(secret))
}

func Verify(secret, tokenString string) (Claims, error) {
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (any, error) { return []byte(secret), nil })
	if err != nil || !token.Valid {
		return Claims{}, jwt.ErrTokenInvalidClaims
	}
	values := token.Claims.(jwt.MapClaims)
	userID, err := uuid.Parse(values["sub"].(string))
	if err != nil {
		return Claims{}, err
	}
	tenantID, err := uuid.Parse(values["tenant_id"].(string))
	if err != nil {
		return Claims{}, err
	}
	return Claims{UserID: userID, TenantID: tenantID}, nil
}
