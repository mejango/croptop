// Package config is config.json in the data directory.
package config

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
)

type Config struct {
	Listen       string `json:"listen"`
	PasscodeSalt string `json:"passcodeSalt,omitempty"`
	PasscodeHash string `json:"passcodeHash,omitempty"`
	KuboBin      string `json:"kuboBin,omitempty"` // use a system kubo instead of the downloaded one
}

const DefaultListen = "127.0.0.1:8086"

func path(dir string) string { return filepath.Join(dir, "config.json") }

func Load(dir string) (*Config, error) {
	c := &Config{Listen: DefaultListen}
	b, err := os.ReadFile(path(dir))
	if os.IsNotExist(err) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, c); err != nil {
		return nil, err
	}
	if c.Listen == "" {
		c.Listen = DefaultListen
	}
	return c, nil
}

func (c *Config) Save(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path(dir), append(b, '\n'), 0o600)
}

func (c *Config) HasPasscode() bool { return c.PasscodeHash != "" }

func (c *Config) SetPasscode(plain string) {
	salt := make([]byte, 16)
	rand.Read(salt)
	c.PasscodeSalt = hex.EncodeToString(salt)
	c.PasscodeHash = hash(c.PasscodeSalt, plain)
}

func (c *Config) CheckPasscode(plain string) bool {
	if c.PasscodeHash == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(hash(c.PasscodeSalt, plain)), []byte(c.PasscodeHash)) == 1
}

func hash(salt, plain string) string {
	sum := sha256.Sum256([]byte(salt + ":" + plain))
	return hex.EncodeToString(sum[:])
}

// DefaultDir is os.UserConfigDir()/croptop.
func DefaultDir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "croptop"), nil
}
