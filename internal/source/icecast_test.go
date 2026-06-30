package source_test

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darrenwiebe/icestream/internal/source"
)

func basicAuthCreds(authHeader string) (user, password string, ok bool) {
	if !strings.HasPrefix(authHeader, "Basic ") {
		return "", "", false
	}
	raw, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(authHeader, "Basic "))
	if err != nil {
		return "", "", false
	}
	user, password, ok = strings.Cut(string(raw), ":")
	return user, password, ok
}

func TestClientConnectAndWrite(t *testing.T) {
	addr, capture := startRawIcecastServer(t, nil)

	client := source.New(source.Config{
		ServerURL:     addr,
		Mount:         "/stream.mp3",
		Password:      "source-secret",
		AdminUsername: "admin",
		AdminPassword: "admin-secret",
		Name:          "Test",
		Description:   "Desc",
		Genre:         "Test",
		URL:           "http://example.com",
		ContentType:   "audio/mpeg",
		Bitrate:       128000,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	payload := []byte("hello-stream")
	if _, err := client.Write(payload); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	capture.mu.Lock()
	body := append([]byte(nil), capture.body...)
	headers := capture.headers
	capture.mu.Unlock()

	if !strings.HasPrefix(headers, "PUT /stream.mp3 HTTP/1.0") {
		t.Fatalf("request line = %q", strings.Split(headers, "\r\n")[0])
	}
	if !strings.Contains(headers, "Authorization: Basic") {
		t.Fatalf("missing basic auth in headers")
	}
	if !strings.Contains(headers, "Content-Type: audio/mpeg") {
		t.Fatalf("missing content type in headers")
	}
	if !strings.Contains(headers, "Ice-Bitrate: 128000") {
		t.Fatalf("missing Ice-Bitrate in headers")
	}
	if string(body) != string(payload) {
		t.Fatalf("body = %q", body)
	}
}

func TestClientConnectUsesConfiguredSourceCredentials(t *testing.T) {
	headerReady := make(chan string, 1)
	addr, _ := startRawIcecastServer(t, func(headers string) {
		headerReady <- headers
	})

	client := source.New(source.Config{
		ServerURL:   addr,
		Mount:       "/rock.mp3",
		Username:    "rock_source",
		Password:    "rock-pass",
		ContentType: "audio/mpeg",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	var headers string
	select {
	case headers = <-headerReady:
	case <-ctx.Done():
		t.Fatal("timed out waiting for headers")
	}

	var auth string
	for _, line := range strings.Split(headers, "\r\n") {
		if strings.HasPrefix(line, "Authorization: ") {
			auth = strings.TrimPrefix(line, "Authorization: ")
			break
		}
	}
	user, pass, ok := basicAuthCreds(auth)
	if !ok {
		t.Fatalf("invalid auth in headers")
	}
	if user != "rock_source" || pass != "rock-pass" {
		t.Fatalf("auth = %q:%q, want rock_source:rock-pass", user, pass)
	}
}

func TestClientWriteWithoutConnect(t *testing.T) {
	client := source.New(source.Config{ServerURL: "http://localhost", Mount: "/x"})
	if _, err := client.Write([]byte("x")); err == nil {
		t.Fatal("expected error when not connected")
	}
}

func TestClientReconnect(t *testing.T) {
	addr, capture := startRawIcecastLoopServer(t, 2)

	client := source.New(source.Config{
		ServerURL:   addr,
		Mount:       "/stream.mp3",
		Password:    "secret",
		ContentType: "audio/mpeg",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	if _, err := client.Write([]byte("first")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	if err := client.Reconnect(ctx); err != nil {
		t.Fatalf("Reconnect() error = %v", err)
	}

	if _, err := client.Write([]byte("second")); err != nil {
		t.Fatalf("Write() after reconnect error = %v", err)
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	capture.mu.Lock()
	defer capture.mu.Unlock()
	if capture.connectCount != 2 {
		t.Fatalf("connectCount = %d, want 2", capture.connectCount)
	}
	if len(capture.bodies) != 2 {
		t.Fatalf("bodies count = %d, want 2", len(capture.bodies))
	}
	if string(capture.bodies[0]) != "first" || string(capture.bodies[1]) != "second" {
		t.Fatalf("bodies = %q, %q", capture.bodies[0], capture.bodies[1])
	}
}

func TestClientSetMetadataUsesAdminCredentials(t *testing.T) {
	var mu sync.Mutex
	var metaAuth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin/metadata" {
			mu.Lock()
			metaAuth = r.Header.Get("Authorization")
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := source.New(source.Config{
		ServerURL:     server.URL,
		Mount:         "/stream.mp3",
		Password:      "source-secret",
		AdminUsername: "admin",
		AdminPassword: "admin-secret",
	})

	if err := client.SetMetadata("Artist - Song"); err != nil {
		t.Fatalf("SetMetadata() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	user, pass, ok := basicAuthCreds(metaAuth)
	if !ok || user != "admin" || pass != "admin-secret" {
		t.Fatalf("metadata auth = %q:%q, want admin:admin-secret", user, pass)
	}
}

func TestIsDisconnectError(t *testing.T) {
	if !source.IsDisconnectError(source.ErrNotConnected) {
		t.Fatal("ErrNotConnected should be disconnect error")
	}
	if !source.IsDisconnectError(io.ErrClosedPipe) {
		t.Fatal("ErrClosedPipe should be disconnect error")
	}
	if source.IsDisconnectError(nil) {
		t.Fatal("nil should not be disconnect error")
	}
}
