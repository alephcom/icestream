package source

import (
	"bufio"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrNotConnected = errors.New("not connected")

type Config struct {
	ServerURL      string
	Mount          string
	Username       string
	Password       string
	AdminUsername  string
	AdminPassword  string
	Name           string
	Genre          string
	Description    string
	URL            string
	Public         bool
	ContentType    string
	Bitrate        int
}

type Client struct {
	cfg    Config
	client *http.Client

	mu     sync.Mutex
	conn   net.Conn
	done   chan struct{}
	closed bool
}

func New(cfg Config) *Client {
	return &Client{
		cfg: cfg,
		client: &http.Client{
			Timeout: 0,
		},
	}
}

func (c *Client) Connect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.conn != nil {
		return fmt.Errorf("already connected")
	}
	return c.connectLocked(ctx)
}

func (c *Client) Reconnect(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if err := c.closeConnectionLocked(); err != nil {
		return err
	}
	return c.connectLocked(ctx)
}

func (c *Client) connectLocked(ctx context.Context) error {
	mount := normalizeMount(c.cfg.Mount)
	host, port, err := icecastDialTarget(c.cfg.ServerURL)
	if err != nil {
		return err
	}

	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(host, port))
	if err != nil {
		return fmt.Errorf("dial icecast: %w", err)
	}

	if _, err := conn.Write([]byte(buildSourcePUTHeaders(c.cfg, mount, host, port))); err != nil {
		_ = conn.Close()
		return fmt.Errorf("write PUT headers: %w", err)
	}

	br := bufio.NewReader(conn)
	status, err := readHTTPResponseStatus(br)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("read icecast response: %w", err)
	}
	if status < 200 || status >= 300 {
		_ = conn.Close()
		return fmt.Errorf("icecast rejected source: HTTP/1.0 %d", status)
	}

	c.conn = conn
	c.done = make(chan struct{})
	close(c.done)

	return nil
}

func (c *Client) closeConnectionLocked() error {
	if c.conn == nil {
		return nil
	}

	err := c.conn.Close()
	c.conn = nil

	if c.done != nil {
		select {
		case <-c.done:
		case <-time.After(5 * time.Second):
		}
		c.done = nil
	}
	return err
}

func (c *Client) BeginTrack() {}

func (c *Client) Write(p []byte) (int, error) {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()

	if conn == nil {
		return 0, ErrNotConnected
	}
	return conn.Write(p)
}

func (c *Client) SetMetadata(title string) error {
	mount := normalizeMount(c.cfg.Mount)

	params := url.Values{}
	params.Set("mount", mount)
	params.Set("mode", "updinfo")
	params.Set("song", title)

	reqURL := strings.TrimRight(c.cfg.ServerURL, "/") + "/admin/metadata?" + params.Encode()
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return fmt.Errorf("create metadata request: %w", err)
	}

	req.Header.Set("Authorization", basicAuth(c.adminUsername(), c.adminPassword()))

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("metadata update: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("metadata update rejected: %s", resp.Status)
	}
	return nil
}

func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.closed = true
	return c.closeConnectionLocked()
}

func (c *Client) sourceUsername() string {
	if c.cfg.Username != "" {
		return c.cfg.Username
	}
	return "source"
}

func (c *Client) adminUsername() string {
	if c.cfg.AdminUsername != "" {
		return c.cfg.AdminUsername
	}
	return "admin"
}

func (c *Client) adminPassword() string {
	if c.cfg.AdminPassword != "" {
		return c.cfg.AdminPassword
	}
	return c.cfg.Password
}

func normalizeMount(mount string) string {
	if !strings.HasPrefix(mount, "/") {
		return "/" + mount
	}
	return mount
}

func icecastDialTarget(serverURL string) (host, port string, err error) {
	u, err := url.Parse(serverURL)
	if err != nil {
		return "", "", fmt.Errorf("parse server URL: %w", err)
	}
	if u.Host == "" {
		return "", "", fmt.Errorf("invalid server URL: %q", serverURL)
	}
	host = u.Hostname()
	port = u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	return host, port, nil
}

func buildSourcePUTHeaders(cfg Config, mount, host, port string) string {
	user := cfg.Username
	if user == "" {
		user = "source"
	}

	var b strings.Builder
	b.WriteString("PUT ")
	b.WriteString(mount)
	b.WriteString(" HTTP/1.0\r\n")
	b.WriteString("Host: ")
	b.WriteString(host)
	if port != "" && port != "80" {
		b.WriteString(":")
		b.WriteString(port)
	}
	b.WriteString("\r\n")
	b.WriteString("Authorization: ")
	b.WriteString(basicAuth(user, cfg.Password))
	b.WriteString("\r\n")
	b.WriteString("Content-Type: ")
	b.WriteString(cfg.ContentType)
	b.WriteString("\r\n")
	b.WriteString("Ice-Name: ")
	b.WriteString(cfg.Name)
	b.WriteString("\r\n")
	b.WriteString("Ice-Description: ")
	b.WriteString(cfg.Description)
	b.WriteString("\r\n")
	b.WriteString("Ice-Genre: ")
	b.WriteString(cfg.Genre)
	b.WriteString("\r\n")
	b.WriteString("Ice-Url: ")
	b.WriteString(cfg.URL)
	b.WriteString("\r\n")
	if cfg.Bitrate > 0 {
		b.WriteString("Ice-Bitrate: ")
		b.WriteString(strconv.Itoa(cfg.Bitrate))
		b.WriteString("\r\n")
	}
	if cfg.Public {
		b.WriteString("Ice-Public: 1\r\n")
	} else {
		b.WriteString("Ice-Public: 0\r\n")
	}
	b.WriteString("Connection: close\r\n")
	b.WriteString("\r\n")
	return b.String()
}

func readHTTPResponseStatus(br *bufio.Reader) (int, error) {
	for {
		statusLine, err := br.ReadString('\n')
		if err != nil {
			return 0, err
		}
		statusLine = strings.TrimSpace(statusLine)
		if statusLine == "" {
			continue
		}
		parts := strings.SplitN(statusLine, " ", 3)
		if len(parts) < 2 {
			return 0, fmt.Errorf("invalid HTTP status line: %q", statusLine)
		}
		code, err := strconv.Atoi(parts[1])
		if err != nil {
			return 0, fmt.Errorf("invalid HTTP status code: %q", parts[1])
		}
		if code == http.StatusContinue {
			continue
		}
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return 0, err
			}
			if line == "\r\n" {
				return code, nil
			}
		}
	}
}

func basicAuth(user, password string) string {
	creds := user + ":" + password
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(creds))
}

// IsDisconnectError reports whether err indicates the stream connection was lost.
func IsDisconnectError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrNotConnected) {
		return true
	}
	if errors.Is(err, io.ErrClosedPipe) {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "broken pipe") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "icecast rejected source")
}
