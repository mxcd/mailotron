package config

import (
	"os"
	"path/filepath"
)

// Env vars that influence where mailotron looks for its config and store.
const (
	EnvConfigFile = "MAILOTRON_CONFIG"     // full path to config.yml
	EnvConfigDir  = "MAILOTRON_CONFIG_DIR" // directory holding config + store
)

// ConfigFileName is the canonical config filename written by `config init`.
const ConfigFileName = "config.yml"

// Dir resolves the mailotron config/store directory. Precedence:
// MAILOTRON_CONFIG_DIR > ~/.mailotron.
func Dir() (string, error) {
	if v := os.Getenv(EnvConfigDir); v != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".mailotron"), nil
}

// FilePath resolves the config file path. An explicit override (the --config
// flag value or MAILOTRON_CONFIG) wins. Otherwise it is <Dir>/config.yml, with
// a fallback to <Dir>/config.yaml if only the .yaml variant exists on disk.
func FilePath(override string) (string, error) {
	if override != "" {
		return override, nil
	}
	if v := os.Getenv(EnvConfigFile); v != "" {
		return v, nil
	}
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	primary := filepath.Join(dir, ConfigFileName)
	if _, err := os.Stat(primary); os.IsNotExist(err) {
		legacy := filepath.Join(dir, "config.yaml")
		if _, err := os.Stat(legacy); err == nil {
			return legacy, nil
		}
	}
	return primary, nil
}

// TemplatesDir returns the on-disk template store directory.
func TemplatesDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "templates"), nil
}

// SignaturesDir returns the on-disk signature store directory.
func SignaturesDir() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "signatures"), nil
}
