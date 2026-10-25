package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestTokenRoundTrip(t *testing.T) {
	manager := New([]byte("test-secret"))
	token, err := manager.TokenForUser(42)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	userID, ok := manager.UserIDFromRequest(request)
	if !ok || userID != 42 {
		t.Fatalf("userID = %d, ok = %v", userID, ok)
	}
}

func TestCookieRoundTrip(t *testing.T) {
	manager := New([]byte("test-secret"))
	response := httptest.NewRecorder()
	manager.SetCookie(response, 42)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(response.Result().Cookies()[0])
	if userID, ok := manager.UserIDFromRequest(request); !ok || userID != 42 {
		t.Fatalf("userID = %d, ok = %v", userID, ok)
	}
}

func TestInvalidTokens(t *testing.T) {
	manager := New([]byte("test-secret"))
	cases := []string{"42.bad-signature", "", makeToken(t, []byte("other"), jwt.RegisteredClaims{Subject: "42", ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}), makeToken(t, []byte("test-secret"), jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}), makeToken(t, []byte("test-secret"), jwt.RegisteredClaims{Subject: "42"})}
	for _, value := range cases {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set("Authorization", "Bearer "+value)
		if _, ok := manager.UserIDFromRequest(request); ok {
			t.Fatalf("invalid token was accepted: %q", value)
		}
	}
}

func TestBearerTokenTakesPriority(t *testing.T) {
	manager := New([]byte("test-secret"))
	token, err := manager.TokenForUser(42)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer broken")
	request.AddCookie(&http.Cookie{Name: CookieName, Value: token})
	if _, ok := manager.UserIDFromRequest(request); ok {
		t.Fatal("invalid bearer token fell back to cookie")
	}
}

func makeToken(t *testing.T, secret []byte, claims jwt.RegisteredClaims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	value, err := token.SignedString(secret)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestExpiredToken(t *testing.T) {
	manager := New([]byte("test-secret"))
	value := makeToken(t, []byte("test-secret"), jwt.RegisteredClaims{Subject: "42", IssuedAt: jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)), ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour))})
	if strings.TrimSpace(value) == "" {
		t.Fatal("empty token")
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer "+value)
	if _, ok := manager.UserIDFromRequest(request); ok {
		t.Fatal("expired token was accepted")
	}
}
