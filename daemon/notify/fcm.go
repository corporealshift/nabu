package notify

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// ServiceAccount is the part of a Google service-account key file the
// sender needs.
type ServiceAccount struct {
	ProjectID   string `json:"project_id"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

// LoadServiceAccount reads a service-account key file.
func LoadServiceAccount(path string) (ServiceAccount, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return ServiceAccount{}, fmt.Errorf("notify: %w", err)
	}
	var a ServiceAccount
	if err := json.Unmarshal(b, &a); err != nil {
		return ServiceAccount{}, fmt.Errorf("notify: %s: %w", path, err)
	}
	if a.ProjectID == "" || a.ClientEmail == "" || a.PrivateKey == "" {
		return ServiceAccount{}, fmt.Errorf("notify: %s is not a service-account key: it needs project_id, client_email and private_key", path)
	}
	if a.TokenURI == "" {
		a.TokenURI = "https://oauth2.googleapis.com/token"
	}
	return a, nil
}

// ErrStaleToken is a device token FCM no longer knows: the app was
// uninstalled, or its token rotated. The device should be dropped.
var ErrStaleToken = errors.New("notify: the device token is no longer valid")

// fcmScope is the OAuth scope that may send messages.
const fcmScope = "https://www.googleapis.com/auth/firebase.messaging"

// FCM sends data messages through the Firebase Cloud Messaging HTTP v1 API.
// It signs its own OAuth assertion with the standard library, so the daemon
// pulls in no Google SDK.
type FCM struct {
	account ServiceAccount
	key     *rsa.PrivateKey
	http    *http.Client
	// Endpoint is FCM's base URL; tests point it elsewhere.
	Endpoint string
	Now      func() time.Time

	mu      sync.Mutex
	token   string
	expires time.Time
}

// NewFCM makes a sender for a service account. hc may be nil.
func NewFCM(a ServiceAccount, hc *http.Client) (*FCM, error) {
	block, _ := pem.Decode([]byte(a.PrivateKey))
	if block == nil {
		return nil, errors.New("notify: the service account's private_key is not PEM")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		if k, err1 := x509.ParsePKCS1PrivateKey(block.Bytes); err1 == nil {
			parsed = k
		} else {
			return nil, fmt.Errorf("notify: the service account's private_key: %w", err)
		}
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("notify: the service account's private_key is not RSA")
	}
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	return &FCM{account: a, key: key, http: hc, Endpoint: "https://fcm.googleapis.com", Now: time.Now}, nil
}

// Send delivers one data message to one device. It returns ErrStaleToken
// when FCM says the device is gone.
func (f *FCM) Send(ctx context.Context, device string, data map[string]string) error {
	access, err := f.accessToken(ctx)
	if err != nil {
		return err
	}
	body, err := json.Marshal(map[string]any{"message": map[string]any{
		"token": device,
		"data":  data,
		// Data-only messages at normal priority wait out Doze; these are
		// few, and each is shown.
		"android": map[string]any{"priority": "high"},
	}})
	if err != nil {
		return err
	}
	u := fmt.Sprintf("%s/v1/projects/%s/messages:send", strings.TrimRight(f.Endpoint, "/"), url.PathEscape(f.account.ProjectID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+access)
	req.Header.Set("Content-Type", "application/json")
	resp, err := f.http.Do(req)
	if err != nil {
		return fmt.Errorf("notify: sending: %w", err)
	}
	defer resp.Body.Close()
	reply, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	switch {
	case resp.StatusCode == http.StatusOK:
		return nil
	case resp.StatusCode == http.StatusNotFound || bytes.Contains(reply, []byte("UNREGISTERED")):
		return ErrStaleToken
	case resp.StatusCode == http.StatusBadRequest && bytes.Contains(reply, []byte("registration token")):
		return ErrStaleToken
	case resp.StatusCode == http.StatusUnauthorized:
		// The cached token was refused; the next send asks for another.
		f.mu.Lock()
		f.token = ""
		f.mu.Unlock()
	}
	return fmt.Errorf("notify: FCM answered %d: %s", resp.StatusCode, strings.TrimSpace(string(reply)))
}

// accessToken is a cached OAuth access token, fetched again a minute before
// it expires.
func (f *FCM) accessToken(ctx context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.Now()
	if f.token != "" && now.Before(f.expires.Add(-time.Minute)) {
		return f.token, nil
	}
	assertion, err := f.assertion(now)
	if err != nil {
		return "", err
	}
	form := url.Values{"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"}, "assertion": {assertion}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.account.TokenURI, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := f.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("notify: asking for an access token: %w", err)
	}
	defer resp.Body.Close()
	reply, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("notify: the token endpoint answered %d: %s", resp.StatusCode, strings.TrimSpace(string(reply)))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(reply, &tok); err != nil || tok.AccessToken == "" {
		return "", fmt.Errorf("notify: the token endpoint gave no access token: %s", strings.TrimSpace(string(reply)))
	}
	f.token, f.expires = tok.AccessToken, now.Add(time.Duration(tok.ExpiresIn)*time.Second)
	return f.token, nil
}

// assertion is the signed JWT that asks for an access token (RFC 7523).
func (f *FCM) assertion(now time.Time) (string, error) {
	enc := base64.RawURLEncoding
	header, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	claims, err := json.Marshal(map[string]any{
		"iss": f.account.ClientEmail, "scope": fcmScope, "aud": f.account.TokenURI,
		"iat": now.Unix(), "exp": now.Add(time.Hour).Unix(),
	})
	if err != nil {
		return "", err
	}
	signing := enc.EncodeToString(header) + "." + enc.EncodeToString(claims)
	sum := sha256.Sum256([]byte(signing))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("notify: signing: %w", err)
	}
	return signing + "." + enc.EncodeToString(sig), nil
}
