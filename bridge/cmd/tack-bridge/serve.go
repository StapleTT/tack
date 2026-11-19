package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"tack/bridge/internal/nvhttp"
	"tack/bridge/internal/session"
	"tack/bridge/internal/tlsca"
	"tack/web"
)

func serve(ctx context.Context, dataDir, listen string, udpPort int, app string) error {
	host, err := loadHost(dataDir)
	if err != nil {
		return err
	}
	names := tlsca.LocalNames()
	cert, caPEM, err := tlsca.Load(filepath.Join(dataDir, "tls"), names)
	if err != nil {
		return err
	}
	mgr, err := session.NewManager(host, app, udpPort)
	if err != nil {
		return err
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
		sctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
		defer cancel()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(hostStatus(sctx, host, mgr))
	})
	mux.HandleFunc("GET /ca.pem", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-pem-file")
		w.Write(caPEM)
	})
	mux.HandleFunc("POST /api/session", func(w http.ResponseWriter, r *http.Request) {
		var req session.Request
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		sctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		answer, err := mgr.Start(sctx, req)
		if err != nil {
			log.Printf("session start: %v", err)
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"sdp": answer})
	})

	srv := &http.Server{
		Addr:              listen,
		Handler:           mux,
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
