package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/seanturner026/f/internal/guard"
)

// guardPaths are where the guard reads rules and writes pi's filtered credentials.
type guardPaths struct {
	rules, cache, awsConfig, awsCreds, kubeconfig string
}

func newGuardPaths() (guardPaths, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return guardPaths{}, err
	}
	cache := filepath.Join(home, ".cache", "f", "guard")
	return guardPaths{
		rules:      filepath.Join(home, ".config", "f", "guard.json"),
		cache:      cache,
		awsConfig:  filepath.Join(cache, "aws-config"),
		awsCreds:   filepath.Join(cache, "aws-credentials"),
		kubeconfig: filepath.Join(cache, "kubeconfig"),
	}, nil
}

// source returns the real file for an env var: its value unless it already
// points into the guard cache (nested pi), else the default path.
func (p guardPaths) source(env, def string) string {
	if v := os.Getenv(env); v != "" && !strings.HasPrefix(v, p.cache) {
		return v
	}
	return def
}

// credsOutput is what the pi guard extension applies to pi's environment.
type credsOutput struct {
	Env             map[string]string `json:"env"`
	RemovedProfiles []string          `json:"removed_profiles"`
	RemovedContexts []string          `json:"removed_contexts"`
}

// creds writes filtered AWS config/credentials and kubeconfig for pi and
// prints the environment pointing at them.
func creds(ctx context.Context) error {
	p, err := newGuardPaths()
	if err != nil {
		return err
	}
	rules, err := guard.LoadRules(p.rules)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(p.cache, 0o700); err != nil {
		return fmt.Errorf("creds: %w", err)
	}
	home, _ := os.UserHomeDir()
	out := credsOutput{Env: map[string]string{
		"AWS_CONFIG_FILE":             p.awsConfig,
		"AWS_SHARED_CREDENTIALS_FILE": p.awsCreds,
		"KUBECONFIG":                  p.kubeconfig,
	}}

	for _, f := range []struct {
		src, dst string
		filter   func(string, guard.Rules) (string, []string)
	}{
		{p.source("AWS_CONFIG_FILE", filepath.Join(home, ".aws", "config")), p.awsConfig, guard.FilterAWSConfig},
		{p.source("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(home, ".aws", "credentials")), p.awsCreds, guard.FilterAWSCredentials},
	} {
		raw, err := os.ReadFile(f.src)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("creds: %w", err)
		}
		filtered, removed := f.filter(string(raw), rules)
		out.RemovedProfiles = append(out.RemovedProfiles, removed...)
		if err := os.WriteFile(f.dst, []byte(filtered), 0o600); err != nil {
			return fmt.Errorf("creds: %w", err)
		}
	}

	kube := "{}"
	if _, err := exec.LookPath("kubectl"); err == nil {
		cmd := exec.CommandContext(ctx, "kubectl", "config", "view", "--raw", "-o", "json")
		cmd.Env = append(os.Environ(), "KUBECONFIG="+p.source("KUBECONFIG", filepath.Join(home, ".kube", "config")))
		if b, err := cmd.Output(); err == nil {
			kube = string(b)
		}
	}
	filtered, removed, err := guard.FilterKubeconfig([]byte(kube), rules)
	if err != nil {
		return err
	}
	out.RemovedContexts = removed
	if err := os.WriteFile(p.kubeconfig, filtered, 0o600); err != nil {
		return fmt.Errorf("creds: %w", err)
	}

	// Keep an allowed AWS_PROFILE; otherwise use the default (or none).
	profile := os.Getenv("AWS_PROFILE")
	if profile == "" || !guard.ProfileAllowed(rules, profile) {
		profile = rules.DefaultProfile
	}
	out.Env["AWS_PROFILE"] = profile
	return json.NewEncoder(os.Stdout).Encode(out)
}

// guardCheck prints the guard's verdict on a bash command as JSON.
func guardCheck(cmd string) error {
	p, err := newGuardPaths()
	if err != nil {
		return err
	}
	rules, err := guard.LoadRules(p.rules)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(guard.CheckCommand(cmd, rules))
}
