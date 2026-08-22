// Package config resolves where taskgo keeps its data and reads user
// preferences.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config is small on purpose. Anything that belongs to a task belongs in the
// task file, not here.
type Config struct {
	// DataDir is the store root.
	DataDir string `json:"dataDir" mapstructure:"dataDir"`
	// DefaultProject is applied by `taskgo add` when --project is omitted.
	DefaultProject string `json:"defaultProject,omitempty" mapstructure:"defaultProject"`
	// Editor overrides $EDITOR for `taskgo edit`.
	Editor string `json:"editor,omitempty" mapstructure:"editor"`
}

// DefaultDataDir follows the XDG spec, falling back to ~/.local/share.
func DefaultDataDir() (string, error) {
	if dir := os.Getenv("TASKGO_DATA_DIR"); dir != "" {
		return dir, nil
	}
	if dir := os.Getenv("XDG_DATA_HOME"); dir != "" {
		return filepath.Join(dir, "taskgo"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locate home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", "taskgo"), nil
}

// Load reads config.json from the data directory if present. A missing file is
// not an error — taskgo is meant to work with no configuration at all.
func Load() (*Config, error) {
	dataDir, err := DefaultDataDir()
	if err != nil {
		return nil, err
	}

	cfg := &Config{DataDir: dataDir}

	v := viper.New()
	v.SetConfigName("config")
	v.SetConfigType("json")
	v.AddConfigPath(dataDir)

	v.SetEnvPrefix("TASKGO")
	v.AutomaticEnv()

	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !isNotFound(err, &notFound) {
			return nil, fmt.Errorf("read config: %w", err)
		}
		return cfg, nil
	}

	if err := v.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	// A config file must not be able to move the data directory out from under
	// TASKGO_DATA_DIR, which is how tests and scripts pin the store.
	if dir := os.Getenv("TASKGO_DATA_DIR"); dir != "" {
		cfg.DataDir = dir
	}
	if cfg.DataDir == "" {
		cfg.DataDir = dataDir
	}
	return cfg, nil
}

func isNotFound(err error, target *viper.ConfigFileNotFoundError) bool {
	_, ok := err.(viper.ConfigFileNotFoundError)
	if ok {
		return true
	}
	// viper also returns a plain *fs.PathError when AddConfigPath points at a
	// directory that does not exist yet.
	return os.IsNotExist(err)
}

// EditorCommand picks the editor for `taskgo edit`.
func (c *Config) EditorCommand() string {
	if c.Editor != "" {
		return c.Editor
	}
	if e := os.Getenv("VISUAL"); e != "" {
		return e
	}
	if e := os.Getenv("EDITOR"); e != "" {
		return e
	}
	return "vi"
}
