package source_test

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darrenwiebe/icestream/internal/source"
)

type rawIcecastCapture struct {
	mu      sync.Mutex
	headers string
	body    []byte
}

func startRawIcecastServer(t *testing.T, onHeaders func(headers string)) (addr string, capture *rawIcecastCapture) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	capture = &rawIcecastCapture{}
	done := make(chan struct{})

	go func() {
		defer close(done)
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		go func(c net.Conn) {
			defer c.Close()
			br := bufio.NewReader(c)
			var headerBuf bytes.Buffer
			for {
				line, err := br.ReadString('\n')
				if err != nil {
					return
				}
				headerBuf.WriteString(line)
				if line == "\r\n" {
					break
				}
			}
			headers := headerBuf.String()
			capture.mu.Lock()
			capture.headers = headers
			capture.mu.Unlock()
			if onHeaders != nil {
				onHeaders(headers)
			}
			_, _ = c.Write([]byte("HTTP/1.0 200 OK\r\n\r\n"))
			body, _ := io.ReadAll(c)
			capture.mu.Lock()
			capture.body = append(capture.body, body...)
			capture.mu.Unlock()
		}(conn)
	}()

	t.Cleanup(func() {
		_ = ln.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	})

	return fmt.Sprintf("http://127.0.0.1:%d", ln.Addr().(*net.TCPAddr).Port), capture
}

type rawIcecastLoopCapture struct {
	mu           sync.Mutex
	bodies       [][]byte
	connectCount int
}

func startRawIcecastLoopServer(t *testing.T, maxConns int) (addr string, capture *rawIcecastLoopCapture) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	capture = &rawIcecastLoopCapture{}
	done := make(chan struct{})

	go func() {
		defer close(done)
		for {
			capture.mu.Lock()
			full := capture.connectCount >= maxConns
			capture.mu.Unlock()
			if full {
				return
			}
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				br := bufio.NewReader(c)
				for {
					line, err := br.ReadString('\n')
					if err != nil {
						return
					}
					if line == "\r\n" {
						break
					}
				}
				_, _ = c.Write([]byte("HTTP/1.0 200 OK\r\n\r\n"))
				body, _ := io.ReadAll(c)
				capture.mu.Lock()
				capture.bodies = append(capture.bodies, append([]byte(nil), body...))
				capture.connectCount++
				capture.mu.Unlock()
			}(conn)
		}
	}()

	t.Cleanup(func() {
		_ = ln.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	})

	return fmt.Sprintf("http://127.0.0.1:%d", ln.Addr().(*net.TCPAddr).Port), capture
}

func startRawIcecastStreamingServer(t *testing.T) (addr string, body *safeBuffer) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}

	body = &safeBuffer{}
	done := make(chan struct{})

	go func() {
		defer close(done)
		conn, err := ln.Accept()
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
		for {
			n, err := conn.Read(buf)
			if n > 0 {
				body.append(buf[:n])
			}
			if err != nil {
				break
			}
		}
	}()

	t.Cleanup(func() {
		_ = ln.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	})

	return fmt.Sprintf("http://127.0.0.1:%d", ln.Addr().(*net.TCPAddr).Port), body
}

type safeBuffer struct {
	mu   sync.Mutex
	data []byte
}

func (b *safeBuffer) append(p []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data = append(b.data, p...)
}

func (b *safeBuffer) string() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.data)
}

func TestClientPUTUsesHTTP10Identity(t *testing.T) {
	headerReady := make(chan string, 1)
	addr, _ := startRawIcecastServer(t, func(headers string) {
		headerReady <- headers
	})

	client := source.New(source.Config{
		ServerURL:   addr,
		Mount:       "/stream.mp3",
		Password:    "secret",
		ContentType: "audio/mpeg",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}
	defer client.Close()

	var headers string
	select {
	case headers = <-headerReady:
	case <-ctx.Done():
		t.Fatal("timed out waiting for request headers")
	}

	if !strings.HasPrefix(headers, "PUT /stream.mp3 HTTP/1.0") {
		t.Fatalf("request line = %q, want HTTP/1.0 PUT", strings.Split(headers, "\r\n")[0])
	}
	lower := strings.ToLower(headers)
	if strings.Contains(lower, "transfer-encoding: chunked") {
		t.Fatalf("request uses chunked encoding:\n%s", headers)
	}
	if !strings.Contains(lower, "connection: close") {
		t.Fatalf("missing Connection: close in headers:\n%s", headers)
	}
}

func TestClientSmallWritesNoChunkMarkersInBody(t *testing.T) {
	addr, capture := startRawIcecastServer(t, nil)

	client := source.New(source.Config{
		ServerURL:   addr,
		Mount:       "/stream.mp3",
		Password:    "secret",
		ContentType: "audio/mpeg",
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := client.Connect(ctx); err != nil {
		t.Fatalf("Connect() error = %v", err)
	}

	// Simulate MP3-sized frames (~104 bytes).
	frame := bytes.Repeat([]byte{0xFF, 0xF3, 0x40, 0xC0}, 26) // 104 bytes
	for i := 0; i < 8; i++ {
		if _, err := client.Write(frame); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}

	if err := client.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	capture.mu.Lock()
	body := append([]byte(nil), capture.body...)
	capture.mu.Unlock()

	wantLen := 8 * len(frame)
	if len(body) != wantLen {
		t.Fatalf("body len = %d, want %d", len(body), wantLen)
	}
	for i := 0; i < 8; i++ {
		offset := i * len(frame)
		if !bytes.HasPrefix(body[offset:], frame) {
			t.Fatalf("frame %d at offset %d does not match expected MP3 frame prefix", i, offset)
		}
	}
	if bytes.Contains(body, []byte("\r\n68\r\n")) || bytes.Contains(body, []byte("\r\n69\r\n")) {
		t.Fatalf("body contains HTTP chunk markers: % x", body[:min(128, len(body))])
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
