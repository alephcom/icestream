package source

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const defaultBufferSize = 16 * 1024

var ErrNotConnected = errors.New("not connected")

type Config struct {
	ServerURL   string
	Mount       string
	Username    string
	Password    string
	Name        string
	Genre       string
	Description string
	URL         string
	Public      bool
	ContentType string
	Bitrate     int
}

type Client struct {
	cfg    Config
	client *http.Client

	mu     sync.Mutex
	pw     *io.PipeWriter
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

	if c.pw != nil {
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
	pr, pw := io.Pipe()
	c.pw = pw
	c.done = make(chan struct{})

	mount := c.cfg.Mount
	if !strings.HasPrefix(mount, "/") {
		mount = "/" + mount
	}

	reqURL := strings.TrimRight(c.cfg.ServerURL, "/") + mount
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, reqURL, pr)
	if err != nil {
		pw.Close()
		c.pw = nil
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Authorization", basicAuth(c.sourceUsername(), c.cfg.Password))
	req.Header.Set("Content-Type", c.cfg.ContentType)
	req.Header.Set("Ice-Name", c.cfg.Name)
	req.Header.Set("Ice-Description", c.cfg.Description)
	req.Header.Set("Ice-Genre", c.cfg.Genre)
	req.Header.Set("Ice-Url", c.cfg.URL)
	if c.cfg.Bitrate > 0 {
		req.Header.Set("Ice-Bitrate", fmt.Sprintf("%d", c.cfg.Bitrate))
	}
	if c.cfg.Public {
		req.Header.Set("Ice-Public", "1")
	} else {
		req.Header.Set("Ice-Public", "0")
	}

	started := make(chan error, 1)
	go func() {
		defer close(c.done)
		resp, err := c.client.Do(req)
		if err != nil {
			if !c.closed {
				_ = pr.CloseWithError(err)
			}
			started <- err
			return
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			rejectErr := fmt.Errorf("icecast rejected source: %s", resp.Status)
			if !c.closed {
				_ = pr.CloseWithError(rejectErr)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			started <- rejectErr
			return
		}
		started <- nil
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	const connectFastFail = 100 * time.Millisecond
	select {
	case <-ctx.Done():
		_ = pw.Close()
		select {
		case <-started:
		case <-time.After(5 * time.Second):
		}
		c.pw = nil
		c.done = nil
		return ctx.Err()
	case err := <-started:
		if err != nil {
			_ = pw.Close()
			c.pw = nil
			c.done = nil
			return err
		}
	case <-time.After(connectFastFail):
	}
	return nil
}


func (c *Client) closeConnectionLocked() error {
	if c.pw == nil {
		return nil
	}

	err := c.pw.Close()
	c.pw = nil

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
	pw := c.pw
	c.mu.Unlock()

	if pw == nil {
		return 0, ErrNotConnected
	}
	return pw.Write(p)
}

func (c *Client) SetMetadata(title string) error {
	mount := c.cfg.Mount
	if !strings.HasPrefix(mount, "/") {
		mount = "/" + mount
	}

	params := url.Values{}
	params.Set("mount", mount)
	params.Set("mode", "updinfo")
	params.Set("song", title)

	reqURL := strings.TrimRight(c.cfg.ServerURL, "/") + "/admin/metadata?" + params.Encode()
	req, err := http.NewRequest(http.MethodGet, reqURL, nil)
	if err != nil {
		return fmt.Errorf("create metadata request: %w", err)
	}

	req.Header.Set("Authorization", basicAuth("admin", c.cfg.Password))

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
		strings.Contains(msg, "icecast rejected source")
}
