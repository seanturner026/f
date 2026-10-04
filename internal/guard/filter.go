package guard

import (
	"encoding/json"
	"fmt"
	"strings"
)

// iniProfile returns the AWS profile a section header names ("" for
// non-profile sections like [sso-session x], which are always kept).
// configStyle: ~/.aws/config uses [profile x] (and [default]); credentials uses [x].
func iniProfile(header string, configStyle bool) (string, bool) {
	name := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(header), "["), "]"))
	if !configStyle {
		return name, true
	}
	if name == "default" {
		return name, true
	}
	if p, ok := strings.CutPrefix(name, "profile "); ok {
		return strings.TrimSpace(p), true
	}
	return "", false
}

// filterINI drops sections for denied profiles. Returns the filtered text and
// the removed profile names.
func filterINI(raw string, r Rules, configStyle bool) (string, []string) {
	var out strings.Builder
	var removed []string
	keep := true
	for _, line := range strings.SplitAfter(raw, "\n") {
		if t := strings.TrimSpace(line); strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			profile, isProfile := iniProfile(t, configStyle)
			keep = !isProfile || ProfileAllowed(r, profile)
			if !keep {
				removed = append(removed, profile)
			}
		}
		if keep {
			out.WriteString(line)
		}
	}
	return out.String(), removed
}

// FilterAWSConfig removes denied profiles from ~/.aws/config content.
func FilterAWSConfig(raw string, r Rules) (string, []string) { return filterINI(raw, r, true) }

// FilterAWSCredentials removes denied profiles from ~/.aws/credentials content.
func FilterAWSCredentials(raw string, r Rules) (string, []string) { return filterINI(raw, r, false) }

type kubeNamed struct {
	Name string `json:"name"`
}

// FilterKubeconfig removes denied contexts (and clusters/users only they used)
// from a kubeconfig in JSON form (`kubectl config view --raw -o json`). A
// denied current-context is cleared. Returns JSON and the removed contexts.
func FilterKubeconfig(raw []byte, r Rules) ([]byte, []string, error) {
	var cfg map[string]any
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, nil, fmt.Errorf("filter kubeconfig: %w", err)
	}
	contexts, _ := cfg["contexts"].([]any)
	var kept []any
	var removed []string
	usedClusters, usedUsers := map[string]bool{}, map[string]bool{}
	for _, c := range contexts {
		m, _ := c.(map[string]any)
		name, _ := m["name"].(string)
		if !ContextAllowed(r, name) {
			removed = append(removed, name)
			continue
		}
		kept = append(kept, c)
		if ctx, ok := m["context"].(map[string]any); ok {
			if s, ok := ctx["cluster"].(string); ok {
				usedClusters[s] = true
			}
			if s, ok := ctx["user"].(string); ok {
				usedUsers[s] = true
			}
		}
	}
	cfg["contexts"] = kept
	cfg["clusters"] = keepNamed(cfg["clusters"], usedClusters)
	cfg["users"] = keepNamed(cfg["users"], usedUsers)
	if cur, _ := cfg["current-context"].(string); cur != "" && !ContextAllowed(r, cur) {
		cfg["current-context"] = ""
	}
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, nil, fmt.Errorf("filter kubeconfig: %w", err)
	}
	return out, removed, nil
}

func keepNamed(list any, used map[string]bool) []any {
	items, _ := list.([]any)
	var kept []any
	for _, it := range items {
		if m, ok := it.(map[string]any); ok {
			if name, _ := m["name"].(string); used[name] {
				kept = append(kept, it)
			}
		}
	}
	return kept
}
