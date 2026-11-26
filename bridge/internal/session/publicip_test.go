package session

import (
	"strings"
	"testing"

	"github.com/pion/webrtc/v4"
)

// The public IP must be offered in addition to the LAN addresses.
func TestPublicIPCandidate(t *testing.T) {
	m, err := NewManager(nil, "Desktop", 0, "203.0.113.7", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	pc, err := m.api.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	if _, err := pc.CreateDataChannel("x", nil); err != nil {
		t.Fatal(err)
	}
	offer, err := pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(offer); err != nil {
		t.Fatal(err)
	}
	<-gathered
	sdp := pc.LocalDescription().SDP
	var public, lan int
	for _, l := range strings.Split(sdp, "\n") {
		if strings.HasPrefix(l, "a=candidate:") {
			if strings.Contains(l, " 203.0.113.7 ") {
				public++
			} else {
				lan++
			}
		}
	}
	if public == 0 || lan == 0 {
		t.Fatalf("want public and LAN candidates, got %d and %d:\n%s", public, lan, sdp)
	}

	if _, err := NewManager(nil, "Desktop", 0, "example.com", t.TempDir()); err == nil {
		t.Fatal("hostname accepted as public IP")
	}
}
