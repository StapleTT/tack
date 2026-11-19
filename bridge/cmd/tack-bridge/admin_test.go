package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAdminAccess(t *testing.T) {
	h := adminAccess(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }), "secret")
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
	for _, path := range []string{"/api/status", "/api/session", "/api/pair"} {
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
	request("POST", "/logout", "", "", c)
	if request("GET", "/api/status", "", "", c).Code != 401 {
		t.Fatal("signed-out cookie accepted")
	}
}
