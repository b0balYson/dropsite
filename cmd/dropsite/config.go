package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/b0balYson/dropsite/internal/brand"
	"github.com/b0balYson/dropsite/internal/client"
)

const (
	envServer      = "DROPSITE_SERVER"
	envToken       = "DROPSITE_TOKEN"
	envCode        = "DROPSITE_CODE"    // one-time connect code for login
	envCAFile      = "DROPSITE_CA_FILE" // extra trusted root CA (PEM) for private installations
	configFileName = "config.json"
	configDirPerm  = 0o700
	configFilePerm = 0o600
)

// config is what login stores and every other command reads.
type config struct {
	Server string `json:"server"`
	Token  string `json:"token"`
}

func (a *app) configPath() (string, error) {
	dir, err := a.configDir()
	if err != nil {
		return "", fmt.Errorf("locate config directory: %w", err)
	}
	return filepath.Join(dir, brand.Name, configFileName), nil
}

// loadConfig reads the saved config; a missing file yields a zero config.
func (a *app) loadConfig() (config, error) {
	path, err := a.configPath()
	if err != nil {
		return config{}, err
	}
	b, err := os.ReadFile(path) //nolint:gosec // path is the CLI's own config file
	if errors.Is(err, fs.ErrNotExist) {
		return config{}, nil
	}
	if err != nil {
		return config{}, fmt.Errorf("read config: %w", err)
	}
	var c config
	if err := json.Unmarshal(b, &c); err != nil {
		return config{}, withHint(fmt.Errorf("config %s is not valid JSON: %w", path, err),
			fmt.Sprintf("Run '%s login' again to rewrite it.", brand.Name))
	}
	return c, nil
}

// saveConfig writes the config atomically with owner-only permissions.
func (a *app) saveConfig(c config) (string, error) {
	path, err := a.configPath()
	if err != nil {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, configDirPerm); err != nil {
		return "", fmt.Errorf("create config directory: %w", err)
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode config: %w", err)
	}
	if err := writeFileAtomic(path, append(b, '\n'), configFilePerm); err != nil {
		return "", fmt.Errorf("save config: %w", err)
	}
	return path, nil
}

// writeFileAtomic writes to a temp file in the same directory and renames it.
func writeFileAtomic(path string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(name, perm); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// connect returns a client from env vars (preferred) or the saved config.
func (a *app) connect() (*client.Client, error) {
	c, err := a.loadConfig()
	if err != nil {
		return nil, err
	}
	if v := strings.TrimSpace(a.getenv(envServer)); v != "" {
		c.Server = v
	}
	if v := strings.TrimSpace(a.getenv(envToken)); v != "" {
		c.Token = v
	}
	if c.Server == "" || c.Token == "" {
		return nil, errNotLoggedIn
	}
	server, err := normalizeServer(c.Server)
	if err != nil {
		return nil, err
	}
	return a.newClient(server, c.Token), nil
}

// normalizeServer defaults to https, drops trailing slashes and the API
// prefix, and allows plain http only for the local machine.
func normalizeServer(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("server URL is empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid server URL %q: %w", raw, err)
	}
	if err := checkServerURL(u); err != nil {
		return "", err
	}
	path := strings.TrimSuffix(strings.TrimRight(u.Path, "/"), "/api/v1")
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host) + strings.TrimRight(path, "/"), nil
}

func checkServerURL(u *url.URL) error {
	if u.Host == "" || u.Hostname() == "" {
		return fmt.Errorf("invalid server URL %q: no host", u.String())
	}
	if u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("invalid server URL %q: credentials, query and fragment are not allowed", u.String())
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return nil
	case "http":
		if isLoopback(u.Hostname()) {
			return nil
		}
		return fmt.Errorf("refusing plain http for %s: use https (http is allowed only for localhost)", u.Host)
	default:
		return fmt.Errorf("invalid server URL %q: scheme must be https", u.String())
	}
}

func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
