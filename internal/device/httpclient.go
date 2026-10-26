package device

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxBody bounds any response read from a device (configuration backups
// included).
const maxBody = 64 << 20

// HTTPError is a non-2xx answer from a device API.
type HTTPError struct {
	Status int
	Body   string
}

func (e HTTPError) Error() string {
	switch e.Status {
	case http.StatusUnauthorized:
		return "device API: authentication failed (check the API token and trusted hosts)"
	case http.StatusForbidden:
		return "device API: permission denied (the API profile needs read access, and write access for restore)"
	case http.StatusNotFound:
		return "device API: endpoint not found on this firmware"
	}
	return fmt.Sprintf("device API: HTTP %d %s", e.Status, strings.TrimSpace(e.Body))
}

// pinnedTLS returns a TLS config that accepts exactly the certificate whose
// SHA-256 is pin, or verifies against system CAs when pin is empty.
func pinnedTLS(host, pin string) *tls.Config {
	if pin == "" {
		return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	}
	want, _ := hex.DecodeString(strings.ReplaceAll(strings.ToLower(pin), ":", ""))
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		// Devices use self-signed certificates; the pin replaces chain and
		// name verification.
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
			if len(raw) == 0 {
				return errors.New("device presented no certificate")
			}
			got := sha256.Sum256(raw[0])
			if len(want) != len(got) || subtle.ConstantTimeCompare(want, got[:]) != 1 {
				return CertMismatchError{Got: hex.EncodeToString(got[:])}
			}
			return nil
		},
	}
}

// CertMismatchError means the device certificate differs from the pinned one.
type CertMismatchError struct{ Got string }

func (e CertMismatchError) Error() string {
	return "device certificate does not match the pinned fingerprint (got " + e.Got + ")"
}

// FetchCertificate connects to host:port and returns the SHA-256 (hex) of the
// certificate it presents, for trust-on-first-use pinning.
func FetchCertificate(ctx context.Context, host string, port int) (string, error) {
	d := tls.Dialer{Config: &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS12}}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return "", err
	}
	defer conn.Close()
	certs := conn.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", errors.New("device presented no certificate")
	}
	sum := sha256.Sum256(certs[0].Raw)
	return hex.EncodeToString(sum[:]), nil
}

// restClient is the HTTP plumbing shared by the REST drivers.
type restClient struct {
	base   string
	client *http.Client
	auth   func(*http.Request)
}

func newRESTClient(c Config) *restClient {
	port := c.HTTPSPort
	if port == 0 {
		port = 443
	}
	tr := &http.Transport{
		TLSClientConfig:     pinnedTLS(c.Host, c.PinnedSHA256),
		Proxy:               nil, // devices are reached directly
		DialContext:         (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second,
		MaxIdleConnsPerHost: 2,
		IdleConnTimeout:     90 * time.Second,
	}
	return &restClient{
		base:   "https://" + net.JoinHostPort(c.Host, strconv.Itoa(port)),
		client: &http.Client{Transport: tr, Timeout: 120 * time.Second},
	}
}

// do sends a request and returns the raw body of a 2xx answer.
func (rc *restClient) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, rc.base+path, r)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if rc.auth != nil {
		rc.auth(req)
	}
	resp, err := rc.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		if len(data) > 300 {
			data = data[:300]
		}
		return nil, HTTPError{Status: resp.StatusCode, Body: string(data)}
	}
	return data, nil
}

func (rc *restClient) getJSON(ctx context.Context, path string, out any) error {
	data, err := rc.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("device API %s: unexpected response: %w", path, err)
	}
	return nil
}

// num reads a JSON number that firmware versions encode as either a number
// or a string.
type num float64

func (n *num) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(b), `"`)
	if s == "" || s == "null" {
		*n = 0
		return nil
	}
	f, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
	if err != nil {
		return nil // tolerate odd values; they read as zero
	}
	*n = num(f)
	return nil
}
