package notify

import (
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
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeGoogle is a token endpoint and an FCM endpoint in one server.
type fakeGoogle struct {
	t    *testing.T
	pub  *rsa.PublicKey
	mu   sync.Mutex
	asks int
	sent []map[string]any
	// stale is a device token FCM no longer knows.
	stale string
}

func (g *fakeGoogle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	switch {
	case r.URL.Path == "/token":
		g.asks++
		if err := r.ParseForm(); err != nil {
			g.t.Error(err)
		}
		if r.Form.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
			g.t.Errorf("grant_type = %q", r.Form.Get("grant_type"))
		}
		g.checkJWT(r.Form.Get("assertion"))
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "AT1", "expires_in": 3600, "token_type": "Bearer"})
	case r.URL.Path == "/v1/projects/nabu-test/messages:send":
		if r.Header.Get("Authorization") != "Bearer AT1" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body struct {
			Message map[string]any `json:"message"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			g.t.Error(err)
		}
		if body.Message["token"] == g.stale {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":404,"status":"NOT_FOUND","details":[{"errorCode":"UNREGISTERED"}]}}`))
			return
		}
		g.sent = append(g.sent, body.Message)
		_, _ = w.Write([]byte(`{"name":"projects/nabu-test/messages/1"}`))
	default:
		g.t.Errorf("unexpected request to %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

// checkJWT verifies the assertion's signature and claims.
func (g *fakeGoogle) checkJWT(jwt string) {
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		g.t.Fatalf("assertion %q is not a JWT", jwt)
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		g.t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(g.pub, crypto.SHA256, sum[:], sig); err != nil {
		g.t.Errorf("the assertion's signature does not verify: %v", err)
	}
	raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
	var c map[string]any
	if err := json.Unmarshal(raw, &c); err != nil {
		g.t.Fatal(err)
	}
	if c["iss"] != "nabu@nabu-test.iam.gserviceaccount.com" || c["scope"] != fcmScope || !strings.HasSuffix(c["aud"].(string), "/token") ||
		c["exp"].(float64)-c["iat"].(float64) != 3600 {
		g.t.Errorf("claims = %v", c)
	}
}

// testAccount writes a service-account key file with a fresh RSA key, and
// returns its path and the public half.
func testAccount(t *testing.T, tokenURI string) (string, *rsa.PublicKey) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	pemKey := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
	b, _ := json.Marshal(map[string]string{
		"type": "service_account", "project_id": "nabu-test", "client_email": "nabu@nabu-test.iam.gserviceaccount.com",
		"private_key": string(pemKey), "token_uri": tokenURI,
	})
	path := filepath.Join(t.TempDir(), "key.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, &key.PublicKey
}

func TestFCMSendsWithACachedToken(t *testing.T) {
	g := &fakeGoogle{t: t, stale: "GONE"}
	srv := httptest.NewServer(g)
	defer srv.Close()
	path, pub := testAccount(t, srv.URL+"/token")
	g.pub = pub

	acct, err := LoadServiceAccount(path)
	if err != nil {
		t.Fatal(err)
	}
	f, err := NewFCM(acct, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	f.Endpoint = srv.URL
	ctx := context.Background()
	data := map[string]string{"kind": "done", "session_id": "01M4"}
	for range 2 {
		if err := f.Send(ctx, "PHONE", data); err != nil {
			t.Fatal(err)
		}
	}
	if g.asks != 1 {
		t.Errorf("asked for %d access tokens, want 1: the token is cached", g.asks)
	}
	if len(g.sent) != 2 {
		t.Fatalf("sent %d", len(g.sent))
	}
	m := g.sent[0]
	if m["token"] != "PHONE" || m["data"].(map[string]any)["kind"] != "done" || m["android"].(map[string]any)["priority"] != "high" {
		t.Errorf("message = %v", m)
	}
	if _, ok := m["notification"]; ok {
		t.Error("a notification block would show text the phone did not write")
	}

	if err := f.Send(ctx, "GONE", data); !errors.Is(err, ErrStaleToken) {
		t.Errorf("a stale token: %v", err)
	}

	// An hour on, the token is fetched again.
	f.Now = func() time.Time { return time.Now().Add(time.Hour) }
	if err := f.Send(ctx, "PHONE", data); err != nil {
		t.Fatal(err)
	}
	if g.asks != 2 {
		t.Errorf("asks after expiry = %d", g.asks)
	}
}

func TestLoadServiceAccountRefusesOtherFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "google-services.json")
	if err := os.WriteFile(path, []byte(`{"project_info":{"project_id":"x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadServiceAccount(path); err == nil || !strings.Contains(err.Error(), "not a service-account key") {
		t.Errorf("err = %v", err)
	}
}
