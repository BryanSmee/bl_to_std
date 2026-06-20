// Package config persists a small amount of user configuration (the default
// printer profile, printer IP and Moonraker API key) so the CLI does not
// need them passed on every invocation. It lives in the OS user config dir,
// e.g. ~/.config/bl2std/config.json or %AppData%\bl2std\config.json.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const dirName = "bl2std"

// Config is the persisted user configuration. Empty fields mean "unset".
type Config struct {
	Printer   string `json:"printer,omitempty"`
	PrinterIP string `json:"printer_ip,omitempty"`
	APIKey    string `json:"api_key,omitempty"`
}

// Path returns the config file location, honoring BL2STD_CONFIG when set.
func Path() (string, error) {
	if p := os.Getenv("BL2STD_CONFIG"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("config: cannot locate user config dir: %w", err)
	}
	return filepath.Join(dir, dirName, "config.json"), nil
}

// Load reads the config, returning an empty Config when the file is absent.
func Load() (Config, error) {
	path, err := Path()
	if err != nil {
		return Config{}, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("config: %w", err)
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("config %s: %w", path, err)
	}
	return c, nil
}

// Save writes the config, creating the directory if needed.
func Save(c Config) error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	return nil
}

// Clear removes the config file; absence is not an error.
func Clear() error {
	path, err := Path()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("config: %w", err)
	}
	return nil
}
