package source_test

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/darrenwiebe/icestream/internal/config"
	"github.com/darrenwiebe/icestream/internal/source"
)

func newTestMulti(t *testing.T, dests []config.Destination, reconnect source.ReconnectSettings) *source.Multi {
	t.Helper()
	m := source.NewMulti(dests, "mp3", 128000, config.MetadataAdmin{Username: "admin", Password: "secret"}, reconnect, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})))
	t.Cleanup(func() { _ = m.Close() })
	return m
}

func TestMultiFanoutIdenticalBytes(t *testing.T) {
	addr, capture := startRawIcecastLoopServer(t, 2)

	dests := []config.Destination{
		{Label: "a", ServerURL: addr, Mount: "/a.mp3", Password: "secret"},
		{Label: "b", ServerURL: addr, Mount: "/b.mp3", Password: "secret"},
	}
	m := newTestMulti(t, dests, source.ReconnectSettings{Enabled: true, InitialDelay: 10 * time.Millisecond, MaxDelay: 50 * time.Millisecond})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := m.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	payload := []byte("same-audio-bytes")
	m.BeginTrack()
	if _, err := m.Write(payload); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := m.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	capture.mu.Lock()
	defer capture.mu.Unlock()
	if capture.connectCount != 2 {
		t.Fatalf("connectCount = %d, want 2", capture.connectCount)
	}
	for i, body := range capture.bodies {
		if string(body) != string(payload) {
			t.Fatalf("body[%d] = %q", i, body)
		}
	}
}

func TestMultiPartialFailureContinuesHealthy(t *testing.T) {
	goodAddr, goodBody := startRawIcecastStreamingServer(t)

	failLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	t.Cleanup(func() { _ = failLn.Close() })
	failAddr := fmt.Sprintf("http://127.0.0.1:%d", failLn.Addr().(*net.TCPAddr).Port)

	var mu sync.Mutex
	failBody := make([]byte, 0)
	failFirst := true

	go func() {
		conn, err := failLn.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		for {
			line, err := br.ReadString('\n')
			if err != nil || line == "\r\n" {
				break
			}
		}
		_, _ = conn.Write([]byte("HTTP/1.0 200 OK\r\n\r\n"))
		buf := make([]byte, 4096)
		total := 0
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				mu.Lock()
				if total < 4 {
					take := n
					if total+take > 4 {
						take = 4 - total
					}
					failBody = append(failBody, buf[:take]...)
					total += take
				}
				first := failFirst && total >= 4
				if first {
					failFirst = false
				}
				mu.Unlock()
				if first {
					_ = conn.Close()
					return
				}
			}
			if err != nil {
				break
			}
		}
	}()

	dests := []config.Destination{
		{Label: "good", ServerURL: goodAddr, Mount: "/good.mp3", Password: "secret"},
		{Label: "fail", ServerURL: failAddr, Mount: "/fail.mp3", Password: "secret"},
	}
	reconnect := source.ReconnectSettings{
		Enabled:      true,
		InitialDelay: 20 * time.Millisecond,
		MaxDelay:     50 * time.Millisecond,
		MaxAttempts:  5,
	}
	m := newTestMulti(t, dests, reconnect)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := m.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	m.BeginTrack()
	if _, err := m.Write([]byte("chunk1")); err != nil {
		t.Fatalf("Write(chunk1) error = %v", err)
	}
	if _, err := m.Write([]byte("chunk2")); err != nil {
		t.Fatalf("Write(chunk2) error = %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if goodBody.string() == "chunk1chunk2" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	good := goodBody.string()
	mu.Lock()
	fail := string(failBody)
	mu.Unlock()

	if good != "chunk1chunk2" {
		t.Fatalf("good body = %q, want chunk1chunk2", good)
	}
	if fail != "chun" {
		t.Fatalf("fail body = %q, want only first 4 bytes before disconnect", fail)
	}

	time.Sleep(500 * time.Millisecond)
	m.BeginTrack()
	if _, err := m.Write([]byte("track2")); err != nil {
		t.Fatalf("Write(track2) error = %v", err)
	}

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if goodBody.string() == "chunk1chunk2track2" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if goodBody.string() != "chunk1chunk2track2" {
		t.Fatalf("good body after track2 = %q, want chunk1chunk2track2", goodBody.string())
	}
}

func TestMultiAllDestinationsFail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.NotFound(w, r)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		conn, bufrw, err := hj.Hijack()
		if err != nil {
			return
		}
		_, _ = bufrw.WriteString("HTTP/1.1 200 OK\r\n\r\n")
		_ = bufrw.Flush()
		one := make([]byte, 1)
		_, _ = conn.Read(one)
		_ = conn.Close()
	}))
	defer server.Close()

	dests := []config.Destination{
		{Label: "a", ServerURL: server.URL, Mount: "/a.mp3", Password: "secret"},
		{Label: "b", ServerURL: server.URL, Mount: "/b.mp3", Password: "secret"},
	}
	m := newTestMulti(t, dests, source.ReconnectSettings{Enabled: false})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := m.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	m.BeginTrack()
	if _, err := m.Write([]byte("x")); err != nil {
		t.Fatalf("first Write() error = %v", err)
	}

	var writeErr error
	for i := 0; i < 20; i++ {
		time.Sleep(50 * time.Millisecond)
		_, writeErr = m.Write([]byte("yyyy"))
		if writeErr != nil {
			break
		}
	}
	if writeErr == nil {
		t.Fatal("expected error when all destinations disconnected")
	}
	if !source.IsDisconnectError(writeErr) {
		t.Fatalf("error = %v, want disconnect class", writeErr)
	}
}

func TestMultiSetMetadataFanout(t *testing.T) {
	var mu sync.Mutex
	counts := make(map[string]int)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/admin/metadata" {
			mu.Lock()
			counts[r.URL.Query().Get("mount")]++
			mu.Unlock()
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodPut {
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	dests := []config.Destination{
		{Label: "a", ServerURL: server.URL, Mount: "/a.mp3", Password: "secret"},
		{Label: "b", ServerURL: server.URL, Mount: "/b.mp3", Password: "secret"},
	}
	m := newTestMulti(t, dests, source.ReconnectSettings{Enabled: true})

	if err := m.SetMetadata("Artist - Title"); err != nil {
		t.Fatalf("SetMetadata() error = %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if counts["/a.mp3"] != 1 || counts["/b.mp3"] != 1 {
		t.Fatalf("metadata counts = %v", counts)
	}
}

func TestMultiConnectRollbackOnPartialFailure(t *testing.T) {
	okAddr, _ := startRawIcecastServer(t, nil)

	dests := []config.Destination{
		{Label: "ok", ServerURL: okAddr, Mount: "/ok.mp3", Password: "secret"},
		{Label: "bad", ServerURL: "http://127.0.0.1:1", Mount: "/bad.mp3", Password: "secret"},
	}
	m := newTestMulti(t, dests, source.ReconnectSettings{Enabled: true})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := m.Connect(ctx); err == nil {
		t.Fatal("expected connect error")
	}
}
