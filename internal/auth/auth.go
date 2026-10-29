// Package auth creates and verifies JWT authorization tokens.
package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const tokenLifetime = 24 * time.Hour

// CookieName is the name of the authorization cookie.
const CookieName = "gophermart_session"

type userIDContextKey struct{}

var userIDKey userIDContextKey

// Manager creates and verifies authorization JWTs.
type Manager struct {
	secret []byte
}

// New creates a Manager with a signing secret.
func New(secret []byte) *Manager {
	copied := make([]byte, len(secret))
	copy(copied, secret)
	return &Manager{secret: copied}
}

// TokenForUser creates a signed JWT for userID.
func (m *Manager) TokenForUser(userID int64) (string, error) {
	if userID <= 0 {
		return "", errors.New("user id must be positive")
	}
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Subject:   strconv.FormatInt(userID, 10),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(tokenLifetime)),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(m.secret)
}

// SetToken returns a JWT in the Authorization header and compatibility cookie.
func (m *Manager) SetToken(w http.ResponseWriter, userID int64) error {
	token, err := m.TokenForUser(userID)
	if err != nil {
		return err
	}
	w.Header().Set("Authorization", "Bearer "+token)
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(tokenLifetime / time.Second),
	})
	return nil
}

// SetCookie writes a JWT compatibility cookie for userID.
func (m *Manager) SetCookie(w http.ResponseWriter, userID int64) {
	_ = m.SetToken(w, userID)
}

// UserIDFromRequest reads a Bearer JWT or compatibility cookie.
func (m *Manager) UserIDFromRequest(r *http.Request) (int64, bool) {
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	if strings.HasPrefix(strings.ToLower(authorization), "bearer") {
		value := bearerToken(authorization)
		if value == "" {
			return 0, false
		}
		userID, err := m.verify(value)
		return userID, err == nil
	}
	cookie, err := r.Cookie(CookieName)
	if err != nil {
		return 0, false
	}
	userID, err := m.verify(cookie.Value)
	return userID, err == nil
}

// WithUserID returns a context containing userID.
func WithUserID(ctx context.Context, userID int64) context.Context {
	return context.WithValue(ctx, userIDKey, userID)
}

// UserIDFromContext returns userID from ctx.
func UserIDFromContext(ctx context.Context) (int64, bool) {
	userID, ok := ctx.Value(userIDKey).(int64)
	return userID, ok
}

func (m *Manager) verify(value string) (int64, error) {
	claims := &jwt.RegisteredClaims{}
	token, err := jwt.ParseWithClaims(value, claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, fmt.Errorf("unexpected signing method: %s", token.Method.Alg())
		}
		return m.secret, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))
	if err != nil || token == nil || !token.Valid || claims.Subject == "" || claims.ExpiresAt == nil || claims.IssuedAt == nil {
		return 0, errors.New("invalid jwt")
	}
	userID, err := strconv.ParseInt(claims.Subject, 10, 64)
	if err != nil || userID <= 0 {
		return 0, errors.New("invalid jwt subject")
	}
	return userID, nil
}

func bearerToken(value string) string {
	parts := strings.Fields(value)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		return parts[1]
	}
	return ""
}
