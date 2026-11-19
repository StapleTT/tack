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
				loginPage(w, 200, "")
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
				loginPage(w, 401, "Incorrect admin password.")
				return
			}
			mu.Lock()
			if !bytes.Equal(snapshot, hash) {
				mu.Unlock()
				loginPage(w, 401, "Password changed. Sign in again.")
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

// loginPage writes the sign-in form. msg must be a fixed string: it is not escaped.
func loginPage(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(code)
	if msg != "" {
		msg = `<p class="err" role="alert">` + msg + `</p>`
	}
	w.Write([]byte(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1"><title>Sign in · tack</title><link rel="preconnect" href="https://fonts.googleapis.com"><link rel="preconnect" href="https://fonts.gstatic.com" crossorigin><link href="https://fonts.googleapis.com/css2?family=JetBrains+Mono:wght@400;500;600&display=swap" rel="stylesheet">
<style>
:root{color-scheme:dark}*{box-sizing:border-box}
body{margin:0;min-height:100vh;display:grid;place-items:center;padding:24px;background:#0e0e0e;color:#e6e6e6;font:14px "JetBrains Mono",ui-monospace,Menlo,monospace;-webkit-font-smoothing:antialiased}
form{width:100%;max-width:360px;display:flex;flex-direction:column;gap:28px}
.brand{display:flex;align-items:center;gap:10px;font-size:15px;font-weight:500}.brand i{width:10px;height:10px;background:#e6e6e6}
h1{font-size:22px;font-weight:500;margin:18px 0 8px}p{margin:0;color:#8f8f8f}
label{display:flex;flex-direction:column;gap:6px;font-size:12px;color:#8f8f8f}
input{height:44px;padding:0 14px;border:1px solid #2e2e2e;border-radius:0;background:#141414;color:#e6e6e6;font:inherit;font-size:15px;outline:none}input:focus{border-color:#e6e6e6}
button{height:46px;border:0;border-radius:0;background:#e6e6e6;color:#0e0e0e;font:inherit;font-size:15px;font-weight:500;cursor:pointer}button:hover{background:#fff}button:focus-visible{outline:2px solid #e6e6e6;outline-offset:2px}
.err{color:#e6e6e6;border-left:2px solid #e6e6e6;padding-left:10px}small{font-size:12px;color:#6a6a6a;line-height:1.5}a{color:#c4c4c4}
</style>
<form method="post"><div><div class="brand"><i></i>tack</div><h1>Sign in</h1><p>Admin access to this bridge</p></div>` + msg + `
<label>Admin password<input type="password" name="password" autocomplete="current-password" required autofocus></label>
<button>Sign in</button>
<small>Single admin account. New device? <a href="/ca.pem" download="tack-ca.pem">Download the bridge CA</a> first.</small></form></html>`))
}
