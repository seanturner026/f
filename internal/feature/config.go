package feature

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config is ~/.config/f/config.json, per machine: where f keeps features, and
// which repos it offers for them.
//
//	{"home": "~/code/features", "repos": ["~/code/work/*", "~/code/f"]}
type Config struct {
	Home  string   `json:"home"`  // default ~/code/features
	Repos []string `json:"repos"` // repo paths, or dir/* for every repo in dir; default <home>/*
}

// DefaultLayout reads the config ($F_BASE overrides its home).
func DefaultLayout() (Layout, error) {
	userHome, err := os.UserHomeDir()
	if err != nil {
		return Layout{}, fmt.Errorf("default layout: %w", err)
	}
	cfg, err := loadConfig(filepath.Join(userHome, ".config", "f", "config.json"))
	if err != nil {
		return Layout{}, err
	}
	home := filepath.Join(userHome, "code", "features")
	if cfg.Home != "" {
		home = expandHome(cfg.Home, userHome)
	}
	if env := os.Getenv("F_BASE"); env != "" {
		home = env
	}
	l := NewLayout(home)
	if len(cfg.Repos) > 0 {
		l.Sources = nil
		for _, r := range cfg.Repos {
			l.Sources = append(l.Sources, expandHome(r, userHome))
		}
	}
	return l, nil
}

// loadConfig reads path; a missing file is the zero Config.
func loadConfig(path string) (Config, error) {
	var cfg Config
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, nil
}

func expandHome(p, userHome string) string {
	if rest, ok := strings.CutPrefix(p, "~/"); ok {
		return filepath.Join(userHome, rest)
	}
	return p
}
