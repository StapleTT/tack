package main

import (
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/net/websocket"
)

// The stream WebSocket carries the session cookie, so other sites must not open it.
func TestStreamSocketSameOrigin(t *testing.T) {
	srv := httptest.NewServer(websocket.Server{Handshake: sameOrigin, Handler: func(ws *websocket.Conn) {}})
	defer srv.Close()
	u := "ws" + strings.TrimPrefix(srv.URL, "http")
	if ws, err := websocket.Dial(u, "", srv.URL); err != nil {
		t.Fatalf("same origin refused: %v", err)
	} else {
		ws.Close()
	}
	if _, err := websocket.Dial(u, "", "https://evil.example"); err == nil {
		t.Fatal("cross-origin handshake accepted")
	}
}
