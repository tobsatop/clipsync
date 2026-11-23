// Package config stores this computer's identity and the computer it is
// paired with, in a small JSON file under the user's config directory.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	DefaultPort       = 47800 // TCP: encrypted clipboard connection
	DefaultBeaconPort = 47801 // UDP: "I am here" broadcasts on the local network
	fileName          = "config.json"
)

// Peer is the other computer this one syncs with.
type Peer struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Key  []byte `json:"key"`            // shared secret agreed during pairing
	Addr string `json:"addr,omitempty"` // last known host:port
}

type Config struct {
	DeviceID   string `json:"device_id"`
	Name       string `json:"name"`
	Port       int    `json:"port"`
	BeaconPort int    `json:"beacon_port"`
	Peer       *Peer  `json:"peer,omitempty"`
}

// Dir is where the config lives. CLIPSYNC_HOME overrides it, which lets two
// copies run side by side for testing.
func Dir() (string, error) {
	if d := os.Getenv("CLIPSYNC_HOME"); d != "" {
		return d, nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "clipsync"), nil
}

// Path is the config file inside dir.
func Path(dir string) string { return filepath.Join(dir, fileName) }

// Load reads the config in dir, creating a fresh one on first run.
func Load(dir string) (*Config, error) {
	b, err := os.ReadFile(Path(dir))
	if errors.Is(err, fs.ErrNotExist) {
		c := &Config{}
		if err := c.fillDefaults(); err != nil {
			return nil, err
		}
		return c, c.Save(dir)
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("read %s: %w", Path(dir), err)
	}
	if err := c.fillDefaults(); err != nil {
		return nil, err
	}
	return &c, nil
}

func (c *Config) fillDefaults() error {
	if c.DeviceID == "" {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		c.DeviceID = hex.EncodeToString(b)
	}
	if c.Name == "" {
		c.Name = hostName()
	}
	if c.Port == 0 {
		c.Port = DefaultPort
	}
	if c.BeaconPort == 0 {
		c.BeaconPort = DefaultBeaconPort
	}
	return nil
}

// Save writes the config atomically, readable only by the current user
// because it holds the pairing key.
func (c *Config) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, fileName+".*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(b, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), Path(dir))
}

func hostName() string {
	h, err := os.Hostname()
	if err != nil || h == "" {
		return "computer"
	}
	h = strings.TrimSuffix(h, ".local")
	h = strings.TrimSuffix(h, ".lan")
	return h
}
