package config

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"unicode"

	"github.com/BurntSushi/toml"
)

// MigrateResult holds a migrated config and any non-fatal warnings.
type MigrateResult struct {
	Config   *Config
	Warnings []string
}

// ParseLegacyConf reads an IceGenerator icegenerator.conf file into key/value pairs.
// Keys are uppercased; comments and blank lines are ignored. Unknown keys are kept.
func ParseLegacyConf(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read legacy config: %w", err)
	}
	return ParseLegacyConfBytes(data)
}

// ParseLegacyConfBytes parses legacy config content.
func ParseLegacyConfBytes(data []byte) (map[string]string, error) {
	out := make(map[string]string)
	for lineNum, line := range strings.Split(string(data), "\n") {
		key, value, err := parseLegacyLine(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", lineNum+1, err)
		}
		if key == "" {
			continue
		}
		out[key] = value
	}
	return out, nil
}

func parseLegacyLine(line string) (key, value string, err error) {
	const (
		stateInitial = iota
		stateKey
		stateBeforeValue
		stateValue
	)

	state := stateInitial
	keyBuf := strings.Builder{}
	valueBuf := strings.Builder{}

	for i := 0; i < len(line); i++ {
		ch := line[i]
		switch state {
		case stateInitial:
			if ch == '#' {
				return "", "", nil
			}
			if ch == ' ' || ch == '\t' {
				continue
			}
			if !unicode.IsLetter(rune(ch)) {
				return "", "", fmt.Errorf("invalid character %q", ch)
			}
			keyBuf.WriteByte(byte(unicode.ToUpper(rune(ch))))
			state = stateKey
		case stateKey:
			if ch == ' ' || ch == '\t' {
				state = stateBeforeValue
				continue
			}
			if ch == '=' {
				state = stateBeforeValue
				i--
				continue
			}
			if unicode.IsLetter(rune(ch)) || unicode.IsDigit(rune(ch)) || ch == '_' {
				keyBuf.WriteByte(byte(unicode.ToUpper(rune(ch))))
				continue
			}
			return "", "", fmt.Errorf("invalid character %q in key", ch)
		case stateBeforeValue:
			if ch == ' ' || ch == '\t' {
				continue
			}
			if ch != '=' {
				return "", "", fmt.Errorf("expected '=', found %q", ch)
			}
			state = stateValue
		case stateValue:
			if ch == '\r' {
				continue
			}
			if ch == ' ' || ch == '\t' {
				if valueBuf.Len() > 0 {
					valueBuf.WriteByte(ch)
				}
				continue
			}
			if unicode.IsLetter(rune(ch)) || unicode.IsDigit(rune(ch)) || isLegacyValuePunct(ch) {
				valueBuf.WriteByte(ch)
				state = stateValue + 1
				continue
			}
			return "", "", fmt.Errorf("invalid character %q in value", ch)
		case stateValue + 1:
			if ch == '\r' {
				continue
			}
			if unicode.IsLetter(rune(ch)) || unicode.IsDigit(rune(ch)) || isLegacyValuePunct(ch) || ch == ' ' || ch == '\t' {
				valueBuf.WriteByte(ch)
				continue
			}
			return "", "", fmt.Errorf("invalid character %q in value", ch)
		}
	}

	if state == stateInitial {
		return "", "", nil
	}
	if state == stateKey || state == stateBeforeValue {
		return "", "", fmt.Errorf("incomplete key/value pair")
	}

	key = strings.TrimSpace(keyBuf.String())
	value = strings.TrimRight(valueBuf.String(), " \t")
	if key == "" {
		return "", "", nil
	}
	return key, value, nil
}

func isLegacyValuePunct(ch byte) bool {
	return strings.ContainsRune("!\"#$%&'()*+,-./:;<=>?@[\\]^_`{|}~", rune(ch))
}

// MigrateLegacy maps a parsed icegenerator.conf into icestream TOML config.
func MigrateLegacy(legacy map[string]string) (*MigrateResult, error) {
	var warnings []string

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
			Recursive:        false,
			Shuffle:          true,
			Loop:             true,
			MissingFileBackoff: "0",
		},
		Metadata: MetadataConfig{
			UpdateInterval: "0",
		},
		Logging: LoggingConfig{
			Level:       "info",
			Destination: "stderr",
		},
	}

	if v, ok := legacy["IP"]; ok && v != "" {
		cfg.Server.Host = v
	}
	if v, ok := legacy["PORT"]; ok && v != "" {
		port, err := strconv.Atoi(v)
		if err != nil || port <= 0 || port > 65535 {
			return nil, fmt.Errorf("invalid PORT %q", v)
		}
		cfg.Server.Port = port
	}
	if v, ok := legacy["MOUNT"]; ok && v != "" {
		cfg.Server.Mount = v
	} else {
		return nil, fmt.Errorf("MOUNT is required")
	}
	if v, ok := legacy["SOURCE"]; ok && v != "" {
		cfg.Server.Username = v
	}
	if v, ok := legacy["PASSWORD"]; ok && v != "" {
		cfg.Server.Password = v
	} else {
		return nil, fmt.Errorf("PASSWORD is required")
	}

	if v, ok := legacy["SERVER"]; ok && v != "" {
		switch v {
		case "2":
			// Icecast 2 HTTP — supported.
		case "1":
			return nil, fmt.Errorf("SERVER=1 (Shoutcast/Icecast 1 ICY) is not supported; icestream requires Icecast 2 (SERVER=2)")
		default:
			return nil, fmt.Errorf("invalid SERVER %q (expected 1 or 2)", v)
		}
	} else {
		warnings = append(warnings, "SERVER not set; assuming Icecast 2 (HTTP)")
	}

	if v, ok := legacy["FORMAT"]; ok && v != "" {
		switch v {
		case "0":
			cfg.Audio.Format = "ogg"
		case "1":
			cfg.Audio.Format = "mp3"
		default:
			return nil, fmt.Errorf("invalid FORMAT %q (expected 0 for OGG or 1 for MP3)", v)
		}
	}

	if v, ok := legacy["BITRATE"]; ok && v != "" {
		bitrate, err := strconv.Atoi(v)
		if err != nil || bitrate <= 0 {
			return nil, fmt.Errorf("invalid BITRATE %q", v)
		}
		cfg.Audio.Bitrate = normalizeLegacyBitrate(bitrate)
	} else {
		warnings = append(warnings, "BITRATE not set; defaulting audio.bitrate to 128000")
	}

	paths, pathWarnings, err := parseLegacyMP3Path(legacy["MP3PATH"])
	if err != nil {
		return nil, err
	}
	warnings = append(warnings, pathWarnings...)
	if len(paths) == 0 {
		return nil, fmt.Errorf("MP3PATH is required")
	}
	cfg.Playlist.Paths = paths

	if v, ok := legacy["RECURSIVE"]; ok && v != "" {
		recursive, err := legacyFlag(v)
		if err != nil {
			return nil, fmt.Errorf("invalid RECURSIVE %q", v)
		}
		cfg.Playlist.Recursive = recursive
	}

	if v, ok := legacy["LOOP"]; ok && v != "" {
		loop, err := legacyFlag(v)
		if err != nil {
			return nil, fmt.Errorf("invalid LOOP %q", v)
		}
		cfg.Playlist.Loop = loop
	}

	if v, ok := legacy["SHUFFLE"]; ok && v != "" {
		shuffle, err := legacyFlag(v)
		if err != nil {
			return nil, fmt.Errorf("invalid SHUFFLE %q", v)
		}
		cfg.Playlist.Shuffle = shuffle
	}

	if v, ok := legacy["NAME"]; ok {
		cfg.Server.Name = v
	}
	if v, ok := legacy["GENRE"]; ok {
		cfg.Server.Genre = v
	}
	if v, ok := legacy["DESCRIPTION"]; ok {
		cfg.Server.Description = v
	}
	if v, ok := legacy["URL"]; ok {
		cfg.Server.URL = v
	}

	if v, ok := legacy["PUBLIC"]; ok && v != "" {
		public, err := legacyFlag(v)
		if err != nil {
			return nil, fmt.Errorf("invalid PUBLIC %q", v)
		}
		cfg.Server.Public = public
	}

	if v, ok := legacy["METAUPDATE"]; ok && v != "" {
		seconds, err := strconv.Atoi(v)
		if err != nil || seconds < 0 {
			return nil, fmt.Errorf("invalid METAUPDATE %q", v)
		}
		if seconds == 0 {
			cfg.Metadata.UpdateInterval = "0"
		} else {
			cfg.Metadata.UpdateInterval = fmt.Sprintf("%ds", seconds)
		}
	}

	if v, ok := legacy["LOG"]; ok && v != "" {
		level, dest, file, logWarnings := mapLegacyLog(v, legacy["LOGPATH"])
		warnings = append(warnings, logWarnings...)
		cfg.Logging.Level = level
		cfg.Logging.Destination = dest
		cfg.Logging.File = file
	}

	if v, ok := legacy["DUMPFILE"]; ok && v != "" {
		warnings = append(warnings, fmt.Sprintf("DUMPFILE=%q is not supported in icestream (ignored)", v))
	}
	if v, ok := legacy["MDFPATH"]; ok && v != "" {
		warnings = append(warnings, fmt.Sprintf("MDFPATH=%q is not supported in icestream (IceMetaL deferred to v2)", v))
	}
	if v, ok := legacy["DATAPORT"]; ok && v != "" {
		warnings = append(warnings, fmt.Sprintf("DATAPORT=%q is not supported in icestream (telnet control deferred to v2)", v))
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("migrated config invalid: %w", err)
	}

	return &MigrateResult{Config: cfg, Warnings: warnings}, nil
}

// MigrateLegacyFile reads icegenerator.conf and returns icestream config.
func MigrateLegacyFile(path string) (*MigrateResult, error) {
	legacy, err := ParseLegacyConf(path)
	if err != nil {
		return nil, err
	}
	return MigrateLegacy(legacy)
}

// EncodeTOML encodes the config as TOML bytes.
func (c *Config) EncodeTOML() ([]byte, error) {
	var buf bytes.Buffer
	enc := toml.NewEncoder(&buf)
	if err := enc.Encode(c); err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}
	return buf.Bytes(), nil
}

func parseLegacyMP3Path(raw string) (paths []string, warnings []string, err error) {
	if raw == "" {
		return nil, nil, fmt.Errorf("MP3PATH is required")
	}

	colon := strings.Index(raw, ":")
	if colon < 0 {
		return nil, nil, fmt.Errorf("invalid MP3PATH %q (expected type:data, e.g. pth:/music)", raw)
	}

	typ := strings.ToUpper(strings.TrimSpace(raw[:colon]))
	data := strings.TrimSpace(raw[colon+1:])
	if data == "" {
		return nil, nil, fmt.Errorf("invalid MP3PATH %q (missing playlist data)", raw)
	}

	switch typ {
	case "PTH":
		for _, part := range strings.Split(data, ";") {
			part = strings.TrimSpace(part)
			if part != "" {
				paths = append(paths, part)
			}
		}
		if len(paths) == 0 {
			return nil, nil, fmt.Errorf("MP3PATH pth entry has no directories")
		}
	case "SQL":
		return nil, nil, fmt.Errorf("MP3PATH sql playlists are not supported in icestream")
	case "PQL":
		return nil, nil, fmt.Errorf("MP3PATH pql playlists are not supported in icestream")
	case "M3U":
		return nil, nil, fmt.Errorf("MP3PATH m3u playlists are not supported in icestream (deferred to v2)")
	case "PLS":
		return nil, nil, fmt.Errorf("MP3PATH pls playlists are not supported in icestream (deferred to v2)")
	default:
		return nil, nil, fmt.Errorf("unknown MP3PATH type %q", typ)
	}

	return paths, warnings, nil
}

func legacyFlag(v string) (bool, error) {
	n, err := strconv.Atoi(v)
	if err != nil || (n != 0 && n != 1) {
		return false, fmt.Errorf("expected 0 or 1")
	}
	return n == 1, nil
}

func mapLegacyLog(v, logPath string) (level, destination, file string, warnings []string) {
	switch v {
	case "0":
		return "error", "none", "", nil
	case "1":
		return "info", "syslog", "", nil
	case "2":
		path := logPath
		if path == "" {
			path = "/var/log/icegenerator.log"
		}
		return "info", "file", path, nil
	default:
		return "info", "stderr", "", []string{fmt.Sprintf("unknown LOG=%q; defaulting to stderr/info", v)}
	}
}

// Values below 1000 are treated as kbps (e.g. legacy BITRATE=128 -> 128000).
func normalizeLegacyBitrate(v int) int {
	if v < 1000 {
		return v * 1000
	}
	return v
}
