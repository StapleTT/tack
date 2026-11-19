package main

import (
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAdminAccess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "admin-password")
	if err := os.WriteFile(path, []byte("secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	hash, err := loadPassword(path)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := os.ReadFile(path)
	if err != nil || bcrypt.CompareHashAndPassword(stored, []byte("secret")) != nil {
		t.Fatal("legacy password migration failed")
	}
	h := adminAccess(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }), path, hash)
	request := func(method, path, body, origin string, cookie *http.Cookie) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "https://bridge"+path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if cookie != nil {
			r.AddCookie(cookie)
		}
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, path := range []string{"/api/status", "/api/session", "/api/pair", "/api/password"} {
		if got := request("POST", path, "", "", nil).Code; got != 401 {
			t.Fatalf("%s: %d", path, got)
		}
	}
	if request("POST", "/login", "password=wrong", "", nil).Code != 401 {
		t.Fatal("bad password accepted")
	}
	login := request("POST", "/login", "password=secret", "", nil)
	c := login.Result().Cookies()[0]
	if login.Code != 303 || !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Fatal("invalid login cookie")
	}
	if request("GET", "/api/status", "", "", c).Code != 204 {
		t.Fatal("valid login rejected")
	}
	if request("POST", "/api/pair", "", "https://other", c).Code != 403 {
		t.Fatal("cross-origin pairing allowed")
	}
	other := request("POST", "/login", "password=secret", "", nil).Result().Cookies()[0]
	if request("POST", "/api/password", "current=bad&new=fresh&confirm=fresh", "", c).Code != 403 {
		t.Fatal("accepted wrong current password")
	}
	if request("POST", "/api/password", "current=secret&new=fresh&confirm=typo", "", c).Code != 400 {
		t.Fatal("accepted mismatched confirmation")
	}
	if request("POST", "/api/password", "current=secret&new=fresh&confirm=fresh", "", c).Code != 204 {
		t.Fatal("rotation failed")
	}
	if request("GET", "/api/status", "", "", other).Code != 401 || request("GET", "/api/status", "", "", c).Code != 401 {
		t.Fatal("old session survived rotation")
	}
	reloaded, err := loadPassword(path)
	if err != nil || bcrypt.CompareHashAndPassword(reloaded, []byte("fresh")) != nil || request("POST", "/login", "password=secret", "", nil).Code != 401 {
		t.Fatal("password change not persisted")
	}
	c = request("POST", "/login", "password=fresh", "", nil).Result().Cookies()[0]
	request("POST", "/logout", "", "", c)
	if request("GET", "/api/status", "", "", c).Code != 401 {
		t.Fatal("signed-out cookie accepted")
	}
}
