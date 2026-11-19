package main

import (
	"crypto/rand"
	"crypto/subtle"
	"net/http"
	"sync"
	"time"
)

func adminAccess(next http.Handler, password string) http.Handler {
	var mu sync.Mutex
	sessions := make(map[string]time.Time)
	const cookie = "__Host-tack-admin"
	return http.NewCrossOriginProtection().Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Frame-Options", "DENY")
		switch r.URL.Path {
		case "/agent", "/ca.pem":
			next.ServeHTTP(w, r)
			return
		case "/login":
			if r.Method == "GET" {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Write([]byte(`<!doctype html><html lang="en"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Sign in · tack</title><style>:root{color-scheme:dark;font:1rem/1.6 system-ui;background:#101217}main{max-width:24rem;margin:12vh auto;padding:2rem}input,button{font:inherit;padding:.5rem}a{color:#9ac1ff}</style><main><h1>tack</h1><form method="post"><label>Admin password<br><input type="password" name="password" autocomplete="current-password" required autofocus></label> <button>Sign in</button></form><p>Use the password in the bridge’s admin-password file.</p><a href="/ca.pem">Download bridge CA</a></main></html>`))
				return
			}
			if r.Method != "POST" {
				w.WriteHeader(405)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			if r.ParseForm() != nil || subtle.ConstantTimeCompare([]byte(r.PostForm.Get("password")), []byte(password)) != 1 {
				http.Error(w, "Incorrect admin password. Go back to try again.", 401)
				return
			}
			session := rand.Text()
			mu.Lock()
			now := time.Now()
			for key, expiry := range sessions {
				if !expiry.After(now) {
					delete(sessions, key)
				}
			}
			sessions[session] = now.Add(24 * time.Hour)
			mu.Unlock()
			http.SetCookie(w, &http.Cookie{Name: cookie, Value: session, Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, "/", 303)
			return
		case "/logout":
			if r.Method != "POST" {
				w.WriteHeader(405)
				return
			}
			if c, err := r.Cookie(cookie); err == nil {
				mu.Lock()
				delete(sessions, c.Value)
				mu.Unlock()
			}
			http.SetCookie(w, &http.Cookie{Name: cookie, Path: "/", Secure: true, HttpOnly: true, MaxAge: -1, SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, "/login", 303)
			return
		}
		c, err := r.Cookie(cookie)
		valid := false
		if err == nil {
			mu.Lock()
			valid = sessions[c.Value].After(time.Now())
			mu.Unlock()
		}
		if !valid {
			if r.Method == "GET" && (r.URL.Path == "/" || r.URL.Path == "/stream") {
				http.Redirect(w, r, "/login", 303)
				return
			}
			http.Error(w, "Sign in to the bridge", 401)
			return
		}
		next.ServeHTTP(w, r)
	}))
}
