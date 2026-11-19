// Package nvhttp speaks the GameStream HTTP(S) control protocol that Sunshine
// exposes on ports 47989/47984: pairing, server info, app list, launch.
package nvhttp

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

// Identity is this client's self-signed certificate. Sunshine remembers the
// certificate at pairing time and authenticates every later HTTPS request by it.
type Identity struct {
	Cert     tls.Certificate
	CertPEM  []byte
	Leaf     *x509.Certificate
	Key      *rsa.PrivateKey
	UniqueID string
}

// LoadOrCreateIdentity reads client.pem/client.key/uniqueid from dir, creating
// them on first run.
func LoadOrCreateIdentity(dir string) (*Identity, error) {
	certPath := filepath.Join(dir, "client.pem")
	keyPath := filepath.Join(dir, "client.key")
	idPath := filepath.Join(dir, "uniqueid")

	certPEM, err := os.ReadFile(certPath)
	if errors.Is(err, fs.ErrNotExist) {
		if err := createIdentity(dir, certPath, keyPath, idPath); err != nil {
			return nil, err
		}
		certPEM, err = os.ReadFile(certPath)
	}
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, err
	}
	uid, err := os.ReadFile(idPath)
	if err != nil {
		return nil, err
	}

	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("client identity: %w", err)
	}
	key, ok := cert.PrivateKey.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("client identity: key is not RSA")
	}
	return &Identity{Cert: cert, CertPEM: certPEM, Leaf: cert.Leaf, Key: key, UniqueID: string(uid)}, nil
}

func createIdentity(dir, certPath, keyPath, idPath string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	if err != nil {
		return err
	}
	tmpl := &x509.Certificate{
		SerialNumber:       serial,
		Subject:            pkix.Name{CommonName: "NVIDIA GameStream Client"},
		NotBefore:          time.Now().Add(-time.Hour),
		NotAfter:           time.Now().AddDate(20, 0, 0),
		SignatureAlgorithm: x509.SHA256WithRSA,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return err
	}
	uid := make([]byte, 8)
	if _, err := rand.Read(uid); err != nil {
		return err
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(idPath, []byte(hex.EncodeToString(uid)), 0o600); err != nil {
		return err
	}
	return os.WriteFile(certPath, certPEM, 0o600)
}
