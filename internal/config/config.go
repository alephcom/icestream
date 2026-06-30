package config

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Server   ServerConfig   `toml:"server"`
	Audio    AudioConfig    `toml:"audio"`
	Playlist PlaylistConfig `toml:"playlist"`
	Metadata MetadataConfig `toml:"metadata"`
	Logging  LoggingConfig  `toml:"logging"`
}

type ServerConfig struct {
	Host                    string              `toml:"host"`
	Port                    int                 `toml:"port"`
	Mount                   string              `toml:"mount"`
	Username                string              `toml:"username"`
	Password                string              `toml:"password"`
	Name                    string              `toml:"name"`
	Genre                   string              `toml:"genre"`
	Description             string              `toml:"description"`
	URL                     string              `toml:"url"`
	Public                  bool                `toml:"public"`
	Reconnect               bool                `toml:"reconnect"`
	ReconnectInitialDelay   string              `toml:"reconnect_initial_delay"`
	ReconnectMaxDelay       string              `toml:"reconnect_max_delay"`
	ReconnectMaxAttempts    int                 `toml:"reconnect_max_attempts"`
	Destinations            []DestinationConfig `toml:"destinations"`
}

// DestinationConfig is one Icecast mount; fields omitted here inherit from [server].
type DestinationConfig struct {
	Name        string `toml:"name"`
	Host        string `toml:"host"`
	Port        int    `toml:"port"`
	Mount       string `toml:"mount"`
	Username    string `toml:"username"`
	Password    string `toml:"password"`
	Genre       string `toml:"genre"`
	Description string `toml:"description"`
	URL         string `toml:"url"`
	Public      *bool  `toml:"public"`
}

// Destination is a fully resolved stream target.
type Destination struct {
	Label       string
	ServerURL   string
	Mount       string
	Username    string
	Password    string
	Name        string
	Genre       string
	Description string
	URL         string
	Public      bool
}

type AudioConfig struct {
	Format  string `toml:"format"`
	Bitrate int    `toml:"bitrate"`
}

type PlaylistConfig struct {
	Paths              []string `toml:"paths"`
	Recursive          bool     `toml:"recursive"`
	Shuffle            bool     `toml:"shuffle"`
	Loop               bool     `toml:"loop"`
	MissingFileBackoff string   `toml:"missing_file_backoff"`
}

type MetadataConfig struct {
	UpdateInterval string `toml:"update_interval"`
	AdminUsername  string `toml:"admin_username"`
	AdminPassword  string `toml:"admin_password"`
}

// MetadataAdmin holds resolved Icecast admin credentials for /admin/metadata.
type MetadataAdmin struct {
	Username string
	Password string
}

type LoggingConfig struct {
	Level       string `toml:"level"`
	Destination string `toml:"destination"`
	File        string `toml:"file"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := &Config{
		Server: ServerConfig{
			Host:                  "127.0.0.1",
			Port:                  8000,
			Username:              "source",
			Reconnect:             true,
			ReconnectInitialDelay: "1s",
			ReconnectMaxDelay:     "60s",
		},
		Audio: AudioConfig{
			Format:  "mp3",
			Bitrate: 128000,
		},
		Playlist: PlaylistConfig{
			Recursive:          true,
			Shuffle:              true,
			Loop:                 true,
			MissingFileBackoff:   "0",
		},
		Metadata: MetadataConfig{
			UpdateInterval: "5s",
		},
		Logging: LoggingConfig{
			Level:       "info",
			Destination: "stderr",
		},
	}

	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) Validate() error {
	if c.Server.Host == "" {
		return fmt.Errorf("server.host is required")
	}
	if c.Server.Port <= 0 || c.Server.Port > 65535 {
		return fmt.Errorf("server.port must be between 1 and 65535")
	}
	if c.Server.Username == "" {
		c.Server.Username = "source"
	}

	hasDestinations := len(c.Server.Destinations) > 0
	if !hasDestinations {
		if c.Server.Mount == "" {
			return fmt.Errorf("server.mount is required")
		}
		if !strings.HasPrefix(c.Server.Mount, "/") {
			return fmt.Errorf("server.mount must start with /")
		}
		if c.Server.Password == "" {
			return fmt.Errorf("server.password is required")
		}
	} else {
		if c.Server.Password == "" {
			hasPassword := false
			for _, d := range c.Server.Destinations {
				if d.Password != "" {
					hasPassword = true
					break
				}
			}
			if !hasPassword {
				return fmt.Errorf("server.password is required when no destination sets password")
			}
		}
		for i, d := range c.Server.Destinations {
			if d.Mount == "" {
				return fmt.Errorf("server.destinations[%d].mount is required", i)
			}
			if !strings.HasPrefix(d.Mount, "/") {
				return fmt.Errorf("server.destinations[%d].mount must start with /", i)
			}
			password := d.Password
			if password == "" {
				password = c.Server.Password
			}
			if password == "" {
				return fmt.Errorf("server.destinations[%d] has no password", i)
			}
			port := d.Port
			if port == 0 {
				port = c.Server.Port
			}
			if port <= 0 || port > 65535 {
				return fmt.Errorf("server.destinations[%d].port must be between 1 and 65535", i)
			}
		}
	}

	dests := c.Destinations()
	if len(dests) == 0 {
		return fmt.Errorf("at least one stream destination is required")
	}

	format := strings.ToLower(c.Audio.Format)
	if format != "mp3" && format != "ogg" {
		return fmt.Errorf("audio.format must be mp3 or ogg")
	}
	c.Audio.Format = format

	if c.Audio.Bitrate <= 0 {
		return fmt.Errorf("audio.bitrate must be greater than 0")
	}

	if len(c.Playlist.Paths) == 0 {
		return fmt.Errorf("playlist.paths must contain at least one path")
	}
	for _, p := range c.Playlist.Paths {
		if p == "" {
			return fmt.Errorf("playlist.paths contains an empty path")
		}
	}

	if c.Metadata.UpdateInterval != "" && c.Metadata.UpdateInterval != "0" {
		if _, err := time.ParseDuration(c.Metadata.UpdateInterval); err != nil {
			return fmt.Errorf("metadata.update_interval: %w", err)
		}
	}

	if c.Server.ReconnectInitialDelay != "" {
		if _, err := time.ParseDuration(c.Server.ReconnectInitialDelay); err != nil {
			return fmt.Errorf("server.reconnect_initial_delay: %w", err)
		}
	}
	if c.Server.ReconnectMaxDelay != "" {
		if _, err := time.ParseDuration(c.Server.ReconnectMaxDelay); err != nil {
			return fmt.Errorf("server.reconnect_max_delay: %w", err)
		}
	}
	if c.Server.ReconnectMaxAttempts < 0 {
		return fmt.Errorf("server.reconnect_max_attempts must be >= 0")
	}

	if c.Playlist.MissingFileBackoff != "" && c.Playlist.MissingFileBackoff != "0" {
		if _, err := time.ParseDuration(c.Playlist.MissingFileBackoff); err != nil {
			return fmt.Errorf("playlist.missing_file_backoff: %w", err)
		}
	}

	level := strings.ToLower(c.Logging.Level)
	switch level {
	case "debug", "info", "warn", "error":
		c.Logging.Level = level
	default:
		return fmt.Errorf("logging.level must be debug, info, warn, or error")
	}

	dest := strings.ToLower(c.Logging.Destination)
	if dest == "" {
		dest = "stderr"
	}
	switch dest {
	case "stderr", "file", "syslog", "none":
		c.Logging.Destination = dest
	default:
		return fmt.Errorf("logging.destination must be stderr, file, syslog, or none")
	}

	return nil
}

func (c *Config) MetadataUpdateInterval() time.Duration {
	if c.Metadata.UpdateInterval == "" || c.Metadata.UpdateInterval == "0" {
		return 0
	}
	d, _ := time.ParseDuration(c.Metadata.UpdateInterval)
	return d
}

func (c *Config) MetadataAdmin() MetadataAdmin {
	user := c.Metadata.AdminUsername
	if user == "" {
		user = "admin"
	}
	pass := c.Metadata.AdminPassword
	if pass == "" {
		pass = c.Server.Password
	}
	return MetadataAdmin{Username: user, Password: pass}
}

func (c *Config) ReconnectInitialDelay() time.Duration {
	if c.Server.ReconnectInitialDelay == "" {
		return time.Second
	}
	d, _ := time.ParseDuration(c.Server.ReconnectInitialDelay)
	return d
}

func (c *Config) ReconnectMaxDelay() time.Duration {
	if c.Server.ReconnectMaxDelay == "" {
		return 60 * time.Second
	}
	d, _ := time.ParseDuration(c.Server.ReconnectMaxDelay)
	return d
}

func (c *Config) MissingFileBackoff() time.Duration {
	if c.Playlist.MissingFileBackoff == "" || c.Playlist.MissingFileBackoff == "0" {
		return 0
	}
	d, _ := time.ParseDuration(c.Playlist.MissingFileBackoff)
	return d
}

func (c *Config) ContentType() string {
	if c.Audio.Format == "ogg" {
		return "application/ogg"
	}
	return "audio/mpeg"
}

func (c *Config) FileExtension() string {
	if c.Audio.Format == "ogg" {
		return ".ogg"
	}
	return ".mp3"
}

func (c *Config) ServerURL() string {
	return fmt.Sprintf("http://%s:%d", c.Server.Host, c.Server.Port)
}

func (c *Config) Destinations() []Destination {
	if len(c.Server.Destinations) == 0 {
		return []Destination{c.resolveDestination(DestinationConfig{
			Mount:       c.Server.Mount,
			Host:        c.Server.Host,
			Port:        c.Server.Port,
			Username:    c.Server.Username,
			Password:    c.Server.Password,
			Name:        c.Server.Name,
			Genre:       c.Server.Genre,
			Description: c.Server.Description,
			URL:         c.Server.URL,
		}, c.Server.Public)}
	}

	out := make([]Destination, 0, len(c.Server.Destinations))
	for _, d := range c.Server.Destinations {
		public := c.Server.Public
		if d.Public != nil {
			public = *d.Public
		}
		out = append(out, c.resolveDestination(d, public))
	}
	return out
}

func (c *Config) resolveDestination(d DestinationConfig, public bool) Destination {
	host := d.Host
	if host == "" {
		host = c.Server.Host
	}
	port := d.Port
	if port == 0 {
		port = c.Server.Port
	}
	username := d.Username
	if username == "" {
		username = c.Server.Username
	}
	password := d.Password
	if password == "" {
		password = c.Server.Password
	}
	name := d.Name
	if name == "" {
		name = c.Server.Name
	}
	genre := d.Genre
	if genre == "" {
		genre = c.Server.Genre
	}
	description := d.Description
	if description == "" {
		description = c.Server.Description
	}
	url := d.URL
	if url == "" {
		url = c.Server.URL
	}

	label := d.Name
	if label == "" {
		label = fmt.Sprintf("%s:%d%s", host, port, d.Mount)
	}

	return Destination{
		Label:       label,
		ServerURL:   fmt.Sprintf("http://%s:%d", host, port),
		Mount:       d.Mount,
		Username:    username,
		Password:    password,
		Name:        name,
		Genre:       genre,
		Description: description,
		URL:         url,
		Public:      public,
	}
}
