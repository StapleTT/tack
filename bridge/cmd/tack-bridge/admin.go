package main

import (
	"bytes"
	"crypto/rand"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"sync"
	"time"
)

func adminAccess(next http.Handler, path string, hash []byte) http.Handler {
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
				w.Write([]byte(`<!doctype html><html lang="en"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Sign in · tack</title><style>:root{color-scheme:dark;font:1rem/1.6 system-ui;background:#101217}main{max-width:24rem;margin:12vh auto;padding:2rem}input,button{font:inherit;padding:.5rem}a{color:#9ac1ff}</style><main><h1>tack</h1><form method="post"><label>Admin password<br><input type="password" name="password" autocomplete="current-password" required autofocus></label> <button>Sign in</button></form><a href="/ca.pem">Download bridge CA</a></main></html>`))
				return
			}
			if r.Method != "POST" {
				w.WriteHeader(405)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			if r.ParseForm() != nil {
				http.Error(w, "Invalid form", 400)
				return
			}
			mu.Lock()
			snapshot := hash
			mu.Unlock()
			password := []byte(r.PostForm.Get("password"))
			if len(password) > 72 || bcrypt.CompareHashAndPassword(snapshot, password) != nil {
				http.Error(w, "Incorrect admin password. Go back to try again.", 401)
				return
			}
			mu.Lock()
			if !bytes.Equal(snapshot, hash) {
				mu.Unlock()
				http.Error(w, "Password changed. Sign in again.", 401)
				return
			}
			session := rand.Text()
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
		if r.URL.Path == "/api/password" {
			if r.Method != "POST" {
				w.WriteHeader(405)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			if err := r.ParseForm(); err != nil {
				http.Error(w, "Invalid form", 400)
				return
			}
			newPassword := r.PostForm.Get("new")
			if len(newPassword) == 0 || len(newPassword) > 72 || newPassword != r.PostForm.Get("confirm") {
				http.Error(w, "Enter matching new passwords of up to 72 UTF-8 bytes.", 400)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			if !sessions[c.Value].After(time.Now()) || len(r.PostForm.Get("current")) > 72 || bcrypt.CompareHashAndPassword(hash, []byte(r.PostForm.Get("current"))) != nil {
				http.Error(w, "Current password is incorrect or your session expired.", 403)
				return
			}
			updated, err := savePassword(path, []byte(newPassword))
			if err != nil {
				http.Error(w, "Could not save the password.", 500)
				return
			}
			hash = updated
			clear(sessions)
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	}))
}
