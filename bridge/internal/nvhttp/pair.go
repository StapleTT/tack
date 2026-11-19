package nvhttp

import (
	"bytes"
	"context"
	"crypto"
	"crypto/aes"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var ErrWrongPIN = errors.New("wrong PIN")

// NewPIN returns a random 4-digit PIN for the user to type into Sunshine.
func NewPIN() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(10000))
	return fmt.Sprintf("%04d", n.Int64())
}

// Pair runs the GameStream PIN pairing handshake. The first request blocks
// until the user enters pin in Sunshine's web UI, so ctx should allow minutes.
// On success it pins and returns the host's DER certificate.
func (c *Client) Pair(ctx context.Context, appVersion, pin string) ([]byte, error) {
	if major, _ := strconv.Atoi(strings.SplitN(appVersion, ".", 2)[0]); major < 7 {
		// Pre-gen-7 hosts (old GFE) use SHA-1 pairing; Sunshine is always 7.x.
		return nil, fmt.Errorf("unsupported host version %q", appVersion)
	}

	salt := randBytes(16)
	keyHash := sha256.Sum256(append(append([]byte{}, salt...), pin...))
	aesKey := keyHash[:16]

	pair := func(secure bool, timeout time.Duration, kv ...string) (*response, error) {
		v := url.Values{"devicename": {"tack"}, "updateState": {"1"}}
		for i := 0; i+1 < len(kv); i += 2 {
			v.Set(kv[i], kv[i+1])
		}
		r, err := c.request(ctx, secure, "pair", v, timeout)
		if err != nil {
			return nil, err
		}
		if r.get("paired") != "1" {
			return nil, errors.New("host rejected pairing step")
		}
		return r, nil
	}
	fail := func(stage int, err error) ([]byte, error) {
		c.unpair()
		c.ServerCert = nil
		if errors.Is(err, ErrWrongPIN) {
			return nil, err
		}
		return nil, fmt.Errorf("pairing stage %d: %w", stage, err)
	}

	// 1. Exchange certificates. Blocks until the PIN is entered on the host.
	r, err := pair(false, 0, "phrase", "getservercert", "salt", hex.EncodeToString(salt),
		"clientcert", hex.EncodeToString(c.ID.CertPEM))
	if err != nil {
		return fail(1, err)
	}
	serverPEM, err := r.hex("plaincert")
	if err != nil || len(serverPEM) == 0 {
		return fail(1, errors.New("no server certificate (pairing already in progress?)"))
	}
	block, _ := pem.Decode(serverPEM)
	if block == nil {
		return fail(1, errors.New("bad server certificate PEM"))
	}
	serverCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fail(1, err)
	}
	serverKey, ok := serverCert.PublicKey.(*rsa.PublicKey)
	if !ok {
		return fail(1, errors.New("server key is not RSA"))
	}
	// Pin for the HTTPS step at the end; only kept if pairing succeeds.
	c.ServerCert = block.Bytes

	// 2. Send our challenge, receive the server's hash and its challenge.
	clientChallenge := randBytes(16)
	r, err = pair(false, 10*time.Second, "clientchallenge", hex.EncodeToString(ecb(aesKey, clientChallenge, true)))
	if err != nil {
		return fail(2, err)
	}
	enc, err := r.hex("challengeresponse")
	if err != nil || len(enc) < 48 || len(enc)%16 != 0 {
		return fail(2, errors.New("bad challengeresponse"))
	}
	dec := ecb(aesKey, enc, false)
	serverHash, serverChallenge := dec[:32], dec[32:48]

	// 3. Answer the server's challenge, receive its secret and signature.
	clientSecret := randBytes(16)
	h := sha256.Sum256(concat(serverChallenge, c.ID.Leaf.Signature, clientSecret))
	r, err = pair(false, 10*time.Second, "serverchallengeresp", hex.EncodeToString(ecb(aesKey, h[:], true)))
	if err != nil {
		return fail(3, err)
	}
	pairingSecret, err := r.hex("pairingsecret")
	if err != nil || len(pairingSecret) <= 16 {
		return fail(3, errors.New("bad pairingsecret"))
	}
	serverSecret, serverSig := pairingSecret[:16], pairingSecret[16:]
	ss := sha256.Sum256(serverSecret)
	if rsa.VerifyPKCS1v15(serverKey, crypto.SHA256, ss[:], serverSig) != nil {
		return fail(3, errors.New("server signature invalid (possible MITM)"))
	}
	expect := sha256.Sum256(concat(clientChallenge, serverCert.Signature, serverSecret))
	if !bytes.Equal(expect[:], serverHash) {
		return fail(3, ErrWrongPIN)
	}

	// 4. Prove we hold our private key.
	cs := sha256.Sum256(clientSecret)
	sig, err := rsa.SignPKCS1v15(rand.Reader, c.ID.Key, crypto.SHA256, cs[:])
	if err != nil {
		return fail(4, err)
	}
	if _, err := pair(false, 10*time.Second, "clientpairingsecret", hex.EncodeToString(concat(clientSecret, sig))); err != nil {
		return fail(4, err)
	}

	// 5. Confirm over HTTPS with the pinned cert and our client cert.
	if _, err := pair(true, 10*time.Second, "phrase", "pairchallenge"); err != nil {
		return fail(5, err)
	}
	return c.ServerCert, nil
}

// ecb is AES-128-ECB without padding; GameStream pairing uses it on
// block-aligned buffers only.
func ecb(key, in []byte, encrypt bool) []byte {
	b, err := aes.NewCipher(key)
	if err != nil {
		panic(err) // key is always 16 bytes
	}
	out := make([]byte, len(in))
	for i := 0; i < len(in); i += aes.BlockSize {
		if encrypt {
			b.Encrypt(out[i:], in[i:i+aes.BlockSize])
		} else {
			b.Decrypt(out[i:], in[i:i+aes.BlockSize])
		}
	}
	return out
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return b
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}
