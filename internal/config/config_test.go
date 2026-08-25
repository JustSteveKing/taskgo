package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TASKGO_DATA_DIR is how tests and scripts pin the store, so it has to win
// over everything else — including a config file that names somewhere else.
func TestEnvDataDirWins(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TASKGO_DATA_DIR", dir)
	t.Setenv("XDG_DATA_HOME", filepath.Join(t.TempDir(), "xdg"))

	got, err := DefaultDataDir()
	if err != nil {
		t.Fatalf("DefaultDataDir: %v", err)
	}
	if got != dir {
		t.Errorf("dataDir = %q, want %q", got, dir)
	}
}

func TestXDGDataHomeIsHonoured(t *testing.T) {
	xdg := t.TempDir()
	t.Setenv("TASKGO_DATA_DIR", "")
	t.Setenv("XDG_DATA_HOME", xdg)

	got, err := DefaultDataDir()
	if err != nil {
		t.Fatalf("DefaultDataDir: %v", err)
	}
	if want := filepath.Join(xdg, "taskgo"); got != want {
		t.Errorf("dataDir = %q, want %q", got, want)
	}
}

func TestFallsBackToLocalShare(t *testing.T) {
	home := t.TempDir()
	t.Setenv("TASKGO_DATA_DIR", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("HOME", home)

	got, err := DefaultDataDir()
	if err != nil {
		t.Fatalf("DefaultDataDir: %v", err)
	}
	if want := filepath.Join(home, ".local", "share", "taskgo"); got != want {
		t.Errorf("dataDir = %q, want %q", got, want)
	}
}

// taskgo is meant to work with no configuration at all.
func TestLoadWithoutAConfigFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TASKGO_DATA_DIR", dir)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with no config file: %v", err)
	}
	if cfg.DataDir != dir {
		t.Errorf("dataDir = %q, want %q", cfg.DataDir, dir)
	}
	if cfg.DefaultProject != "" || cfg.Editor != "" {
		t.Errorf("expected an empty config, got %+v", cfg)
	}
}

// A data directory that does not exist yet is the first-run case, not an
// error.
func TestLoadWhenTheDataDirectoryIsMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-created-yet")
	t.Setenv("TASKGO_DATA_DIR", missing)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DataDir != missing {
		t.Errorf("dataDir = %q, want %q", cfg.DataDir, missing)
	}
}

func TestLoadReadsPreferences(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TASKGO_DATA_DIR", dir)
	writeConfig(t, dir, `{"defaultProject":"web","editor":"nvim"}`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DefaultProject != "web" {
		t.Errorf("defaultProject = %q", cfg.DefaultProject)
	}
	if cfg.Editor != "nvim" {
		t.Errorf("editor = %q", cfg.Editor)
	}
}

// The rule that keeps scripts and tests honest: a config file must not be able
// to move the store out from under TASKGO_DATA_DIR.
func TestConfigFileCannotOverrideTheEnvDataDir(t *testing.T) {
	dir := t.TempDir()
	elsewhere := t.TempDir()
	t.Setenv("TASKGO_DATA_DIR", dir)
	writeConfig(t, dir, `{"dataDir":"`+elsewhere+`"}`)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DataDir != dir {
		t.Errorf("dataDir = %q, want the pinned %q", cfg.DataDir, dir)
	}
}

func TestUnparseableConfigIsAnError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("TASKGO_DATA_DIR", dir)
	writeConfig(t, dir, "{ this is not json")

	if _, err := Load(); err == nil {
		t.Error("expected an error for a damaged config file")
	}
}

func TestEditorCommandPrecedence(t *testing.T) {
	cases := []struct {
		name   string
		cfg    Config
		visual string
		editor string
		want   string
	}{
		{name: "config wins", cfg: Config{Editor: "nvim"}, visual: "code", editor: "nano", want: "nvim"},
		{name: "then VISUAL", visual: "code", editor: "nano", want: "code"},
		{name: "then EDITOR", editor: "nano", want: "nano"},
		{name: "then vi", want: "vi"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("VISUAL", tc.visual)
			t.Setenv("EDITOR", tc.editor)

			cfg := tc.cfg
			if got := cfg.EditorCommand(); got != tc.want {
				t.Errorf("EditorCommand() = %q, want %q", got, tc.want)
			}
		})
	}
}

func writeConfig(t *testing.T, dir, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}
