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
	var mu sync.Mutex
	var method, auth, contentType, iceBitrate string
	body := make([]byte, 0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stream.mp3" && r.Method == http.MethodPut {
			mu.Lock()
			method = r.Method
			auth = r.Header.Get("Authorization")
			contentType = r.Header.Get("Content-Type")
			iceBitrate = r.Header.Get("Ice-Bitrate")
			mu.Unlock()

			data, _ := io.ReadAll(r.Body)
			mu.Lock()
			body = append(body, data...)
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/admin/metadata" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := source.New(source.Config{
		ServerURL:   server.URL,
		Mount:       "/stream.mp3",
		Password:    "secret",
		Name:        "Test",
		Description: "Desc",
		Genre:       "Test",
		URL:         "http://example.com",
		ContentType: "audio/mpeg",
		Bitrate:     128000,
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

	if err := client.SetMetadata("Artist - Song"); err != nil {
		t.Fatalf("SetMetadata() error = %v", err)
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if method != http.MethodPut {
		t.Fatalf("method = %q", method)
	}
	if !strings.HasPrefix(auth, "Basic ") {
		t.Fatalf("missing basic auth")
	}
	if contentType != "audio/mpeg" {
		t.Fatalf("content type = %q", contentType)
	}
	if iceBitrate != "128000" {
		t.Fatalf("Ice-Bitrate = %q, want 128000", iceBitrate)
	}
	if string(body) != string(payload) {
		t.Fatalf("body = %q", body)
	}
}

func TestClientConnectUsesConfiguredSourceCredentials(t *testing.T) {
	var mu sync.Mutex
	var auth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/rock.mp3" && r.Method == http.MethodPut {
			mu.Lock()
			auth = r.Header.Get("Authorization")
			mu.Unlock()
			io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := source.New(source.Config{
		ServerURL:   server.URL,
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

	mu.Lock()
	defer mu.Unlock()
	user, pass, ok := basicAuthCreds(auth)
	if !ok {
		t.Fatalf("invalid auth header %q", auth)
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
	var mu sync.Mutex
	connectCount := 0
	body := make([]byte, 0)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stream.mp3" && r.Method == http.MethodPut {
			mu.Lock()
			connectCount++
			mu.Unlock()

			data, _ := io.ReadAll(r.Body)
			mu.Lock()
			body = append(body, data...)
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	client := source.New(source.Config{
		ServerURL:   server.URL,
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

	mu.Lock()
	defer mu.Unlock()
	if connectCount != 2 {
		t.Fatalf("connectCount = %d, want 2", connectCount)
	}
	if string(body) != "firstsecond" {
		t.Fatalf("body = %q", body)
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
