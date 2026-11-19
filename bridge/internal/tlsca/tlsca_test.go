package tlsca

import (
	"crypto/x509"
	"testing"
)

// The server cert must chain to the CA for both IP and DNS names, and the CA
// must survive a reload, or every imported laptop breaks.
func TestLoadIssuesVerifiableCert(t *testing.T) {
	dir := t.TempDir()
	cert, caPEM, err := Load(dir, []string{"192.168.1.50", "tack.lan"})
	if err != nil {
		t.Fatal(err)
	}
	_, caPEM2, err := Load(dir, nil)
	if err != nil || string(caPEM) != string(caPEM2) {
		t.Fatalf("CA changed on reload: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(caPEM)
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"192.168.1.50", "tack.lan"} {
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: name}); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
