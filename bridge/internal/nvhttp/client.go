package nvhttp

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	DefaultHTTPPort  = 47989
	DefaultHTTPSPort = 47984
)

// Client talks to one Sunshine host. ServerCert is the host's DER certificate
// pinned at pairing; HTTPS requests fail if the host presents anything else.
type Client struct {
	Host       string
	HTTPPort   int
	HTTPSPort  int
	ServerCert []byte
	ID         *Identity
}

func NewClient(host string, id *Identity) *Client {
	return &Client{Host: host, HTTPPort: DefaultHTTPPort, HTTPSPort: DefaultHTTPSPort, ID: id}
}

// response is the common <root status_code=".."> envelope. Fields holds the
// text of each direct child element.
type response struct {
	StatusCode    int    `xml:"status_code,attr"`
	StatusMessage string `xml:"status_message,attr"`
	Children      []struct {
		XMLName xml.Name
		Value   string `xml:",chardata"`
	} `xml:",any"`
	raw []byte
}

func (r *response) get(name string) string {
	for _, c := range r.Children {
		if c.XMLName.Local == name {
			return c.Value
		}
	}
	return ""
}

func (r *response) hex(name string) ([]byte, error) {
	return hex.DecodeString(r.get(name))
}

func (c *Client) httpClient(secure bool, timeout time.Duration) *http.Client {
	tr := &http.Transport{
		DisableKeepAlives: true,
		DialContext:       (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
	}
	if secure {
		pinned := c.ServerCert
		tr.TLSClientConfig = &tls.Config{
			Certificates: []tls.Certificate{c.ID.Cert},
			// Sunshine's cert is self-signed; trust comes from pinning it at pairing.
			InsecureSkipVerify: true,
			VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
				if len(pinned) == 0 {
					return errors.New("no pinned server certificate; pair first")
				}
				if len(raw) == 0 || !bytes.Equal(raw[0], pinned) {
					return errors.New("server certificate does not match pinned certificate")
				}
				return nil
			},
		}
	}
	return &http.Client{Transport: tr, Timeout: timeout}
}

func (c *Client) request(ctx context.Context, secure bool, cmd string, args url.Values, timeout time.Duration) (*response, error) {
	if args == nil {
		args = url.Values{}
	}
	uuid := make([]byte, 16)
	_, _ = rand.Read(uuid)
	args.Set("uniqueid", c.ID.UniqueID)
	args.Set("uuid", hex.EncodeToString(uuid))

	scheme, port := "http", c.HTTPPort
	if secure {
		scheme, port = "https", c.HTTPSPort
	}
	u := url.URL{Scheme: scheme, Host: net.JoinHostPort(c.Host, strconv.Itoa(port)), Path: "/" + cmd, RawQuery: args.Encode()}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient(secure, timeout).Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", cmd, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", cmd, err)
	}
	var r response
	if err := xml.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("%s: bad XML (HTTP %d): %w", cmd, resp.StatusCode, err)
	}
	r.raw = body
	if r.StatusCode != 200 {
		return nil, fmt.Errorf("%s: host returned %d %s", cmd, r.StatusCode, r.StatusMessage)
	}
	return &r, nil
}

type ServerInfo struct {
	Hostname         string
	AppVersion       string
	GfeVersion       string
	CodecModeSupport int
	CurrentGame      int
	Paired           bool
	HTTPSPort        int
}

// ServerInfo queries /serverinfo, over HTTPS when a cert is pinned (Sunshine
// only reports pairing status and current game to authenticated clients).
func (c *Client) ServerInfo(ctx context.Context) (*ServerInfo, error) {
	r, err := c.request(ctx, len(c.ServerCert) > 0, "serverinfo", nil, 10*time.Second)
	if err != nil {
		return nil, err
	}
	atoi := func(s string) int { n, _ := strconv.Atoi(s); return n }
	si := &ServerInfo{
		Hostname:         r.get("hostname"),
		AppVersion:       r.get("appversion"),
		GfeVersion:       r.get("GfeVersion"),
		CodecModeSupport: atoi(r.get("ServerCodecModeSupport")),
		CurrentGame:      atoi(r.get("currentgame")),
		Paired:           r.get("PairStatus") == "1",
		HTTPSPort:        atoi(r.get("HttpsPort")),
	}
	if si.HTTPSPort != 0 {
		c.HTTPSPort = si.HTTPSPort
	}
	return si, nil
}

type App struct {
	ID    int
	Title string
}

func (c *Client) AppList(ctx context.Context) ([]App, error) {
	r, err := c.request(ctx, true, "applist", nil, 10*time.Second)
	if err != nil {
		return nil, err
	}
	var list struct {
		Apps []struct {
			Title string `xml:"AppTitle"`
			ID    int    `xml:"ID"`
		} `xml:"App"`
	}
	if err := xml.Unmarshal(r.raw, &list); err != nil {
		return nil, err
	}
	apps := make([]App, len(list.Apps))
	for i, a := range list.Apps {
		apps[i] = App{ID: a.ID, Title: a.Title}
	}
	return apps, nil
}

// LaunchParams carries what /launch and /resume need. RIKey/RIKeyID must match
// the remote input AES key/IV given to moonlight-common-c.
type LaunchParams struct {
	AppID             int
	Width, Height     int
	FPS               int
	RIKey             [16]byte
	RIKeyID           int32
	SurroundAudioInfo int
	GamepadMask       int
	// ExtraQuery is LiGetLaunchUrlQueryParameters() from moonlight-common-c.
	ExtraQuery string
}

// Launch starts the app (or resumes it when resume is true) and returns the
// RTSP session URL.
func (c *Client) Launch(ctx context.Context, p LaunchParams, resume bool) (string, error) {
	v := url.Values{}
	v.Set("appid", strconv.Itoa(p.AppID))
	v.Set("mode", fmt.Sprintf("%dx%dx%d", p.Width, p.Height, p.FPS))
	v.Set("additionalStates", "1")
	v.Set("sops", "0")
	v.Set("rikey", hex.EncodeToString(p.RIKey[:]))
	v.Set("rikeyid", strconv.Itoa(int(p.RIKeyID)))
	v.Set("localAudioPlayMode", "0")
	v.Set("surroundAudioInfo", strconv.Itoa(p.SurroundAudioInfo))
	v.Set("remoteControllersBitmap", strconv.Itoa(p.GamepadMask))
	v.Set("gcmap", strconv.Itoa(p.GamepadMask))
	v.Set("gcpersist", "0")
	q := v.Encode() + p.ExtraQuery // ExtraQuery starts with '&'

	verb := "launch"
	if resume {
		verb = "resume"
	}
	vals, err := url.ParseQuery(q)
	if err != nil {
		return "", err
	}
	r, err := c.request(ctx, true, verb, vals, 60*time.Second)
	if err != nil {
		return "", err
	}
	if s := r.get("sessionUrl0"); s != "" {
		return s, nil
	}
	return "", fmt.Errorf("%s: no sessionUrl0 in response", verb)
}

// Cancel quits the running app on the host.
func (c *Client) Cancel(ctx context.Context) error {
	_, err := c.request(ctx, true, "cancel", nil, 30*time.Second)
	return err
}

func (c *Client) unpair() {
	_, _ = c.request(context.Background(), false, "unpair", nil, 5*time.Second)
}

// NewInputKey returns a random remote-input AES key and key ID for a launch.
func NewInputKey() (key [16]byte, id int32) {
	copy(key[:], randBytes(16))
	b := randBytes(4)
	return key, int32(uint32(b[0])<<24 | uint32(b[1])<<16 | uint32(b[2])<<8 | uint32(b[3]))
}
