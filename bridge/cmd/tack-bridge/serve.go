package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/websocket"

	"tack/bridge/internal/nvhttp"
	"tack/bridge/internal/session"
	"tack/bridge/internal/tlsca"
	"tack/internal/agentproto"
	"tack/web"
)

func serve(ctx context.Context, dataDir, listen string, udpPort int, app string) error {
	host, err := loadHost(dataDir)
	if err != nil && !errors.Is(err, errNoHost) {
		return err
	}
	names := tlsca.LocalNames()
	cert, caPEM, err := tlsca.Load(filepath.Join(dataDir, "tls"), names)
	if err != nil {
		return err
	}
	mgr, err := session.NewManager(host, app, udpPort, dataDir)
	if err != nil {
		return err
	}
	tokenPath := filepath.Join(dataDir, "agent-token")
	agentToken, err := loadOrCreateToken(tokenPath)
	if err != nil {
		return err
	}

	passwordPath := filepath.Join(dataDir, "admin-password")
	password, err := loadPassword(passwordPath)
	if err != nil {
		return err
	}
	var hostMu sync.RWMutex
	var savedHost nvhttp.Client
	if host != nil {
		savedHost = *host
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, web.Files, "index.html")
	})
	mux.HandleFunc("GET /stream", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, web.Files, "stream.html")
	})
	mux.Handle("GET /dist/", http.FileServerFS(web.Files))
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		if !hostMu.TryRLock() {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(status{Error: "Pairing in progress"})
			return
		}
		current := savedHost
		hostMu.RUnlock()
		if current.Host == "" {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(status{Error: "No host paired"})
			return
		}
		sctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(hostStatus(sctx, &current, mgr))
	})
	mux.HandleFunc("GET /ca.pem", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Write(caPEM)
	})
	mux.Handle("GET /agent", agentHandler(mgr, agentToken))
	mux.HandleFunc("POST /api/pair", func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Address, PIN string }
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
			http.Error(w, "Invalid pairing request", 400)
			return
		}
		ip, err := netip.ParseAddr(req.Address)
		if err != nil || !(ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast()) || !pinPattern.MatchString(req.PIN) {
			http.Error(w, "Enter a LAN IP address and a four-digit PIN", 400)
			return
		}
		if !hostMu.TryLock() {
			http.Error(w, "Host is busy", 409)
			return
		}
		defer hostMu.Unlock()
		if mgr.Streaming() {
			http.Error(w, "Disconnect the active stream before pairing", 409)
			return
		}
		id, err := nvhttp.LoadOrCreateIdentity(dataDir)
		if err != nil {
			http.Error(w, "Cannot read bridge identity", 500)
			return
		}
		c := nvhttp.NewClient(ip.String(), id)
		si, err := c.ServerInfo(r.Context())
		if err == nil {
			err = pairAndSave(r.Context(), dataDir, c, si.AppVersion, req.PIN)
		}
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		mgr.SetHost(c)
		savedHost = *c
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("POST /api/session", func(w http.ResponseWriter, r *http.Request) {
		var req session.Request
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		sctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if !hostMu.TryRLock() {
			http.Error(w, "Pairing in progress", 409)
			return
		}
		defer hostMu.RUnlock()
		if savedHost.Host == "" {
			http.Error(w, "Pair a host first", 409)
			return
		}
		answer, err := mgr.Start(sctx, req)
		if err != nil {
			log.Printf("session start: %v", err)
			if errors.Is(err, session.ErrResumeExpired) {
				http.Error(w, err.Error(), http.StatusConflict)
				return
			}
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"sdp": answer})
	})

	srv := &http.Server{
		Addr:              listen,
		Handler:           adminAccess(mux, passwordPath, password),
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}},
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
		mgr.Close()
	}()
	log.Printf("serving https on %s (cert for %s), media on udp/%d", listen, strings.Join(names, ", "), udpPort)
	log.Printf("host agent: copy %s and %s to the gaming PC", tokenPath, filepath.Join(dataDir, "tls", "ca.pem"))
	if err := srv.ListenAndServeTLS("", ""); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	mgr.Close()
	return nil
}

type status struct {
	Host      string `json:"host"`
	Online    bool   `json:"online"`
	Paired    bool   `json:"paired"`
	App       string `json:"app"`
	Streaming bool   `json:"streaming"`
	Error     string `json:"error,omitempty"`
}

var pinPattern = regexp.MustCompile(`^[0-9]{4}$`)

func hostStatus(ctx context.Context, host *nvhttp.Client, mgr *session.Manager) status {
	st := status{Host: host.Host, Streaming: mgr.Streaming()}
	si, err := host.ServerInfo(ctx)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.Online, st.Paired = true, si.Paired
	if si.Hostname != "" {
		st.Host = si.Hostname
	}
	if si.CurrentGame != 0 && si.Paired {
		st.App = "an app"
		if apps, err := host.AppList(ctx); err == nil {
			for _, a := range apps {
				if a.ID == si.CurrentGame {
					st.App = a.Title
				}
			}
		}
	}
	return st
}

// agentHandler accepts the host agent's WebSocket. The agent authenticates
// with a bearer token; a new connection replaces the old one.
func agentHandler(mgr *session.Manager, token string) http.Handler {
	want := []byte("Bearer " + token)
	return websocket.Server{
		Handshake: func(_ *websocket.Config, r *http.Request) error {
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), want) != 1 {
				return errors.New("bad agent token")
			}
			return nil
		},
		Handler: func(ws *websocket.Conn) {
			ws.MaxPayloadBytes = 2 * agentproto.MaxClipboard
			send := func(m agentproto.Message) error {
				ws.SetWriteDeadline(time.Now().Add(5 * time.Second))
				return websocket.JSON.Send(ws, m)
			}
			id := mgr.SetAgent(send, func() { ws.Close() })
			defer mgr.ClearAgent(id)
			log.Printf("agent %d: connected from %s", id, ws.Request().RemoteAddr)
			for {
				var m agentproto.Message
				if err := websocket.JSON.Receive(ws, &m); err != nil {
					log.Printf("agent %d: disconnected: %v", id, err)
					return
				}
				mgr.FromAgent(m)
			}
		},
	}
}

func loadOrCreateToken(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err == nil {
		return strings.TrimSpace(string(b)), nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	tok := hex.EncodeToString(raw)
	return tok, os.WriteFile(path, []byte(tok+"\n"), 0o600)
}
