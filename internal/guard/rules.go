// Package guard keeps the agent away from admin AWS profiles and production kube
// contexts. The boundary is credentials: the agent gets filtered copies of the AWS
// config/credentials and kubeconfig without denied entries (FilterAWSConfig,
// FilterKubeconfig), so no tool — aws, kubectl, boto3, terraform — can use
// them. CheckCommand is a speed bump on top for commands that try to point
// back at the real files or name a denied target.
package guard

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"strings"
)

// Rules decide which AWS profiles and kube contexts the agent may use. Patterns are
// case-insensitive globs ("*admin*").
type Rules struct {
	DenyProfiles   []string `json:"deny_profiles"`
	AllowProfiles  []string `json:"allow_profiles"` // exceptions to DenyProfiles
	DenyContexts   []string `json:"deny_contexts"`  // also applied to EKS cluster names
	DefaultProfile string   `json:"default_profile"`
}

// DefaultRules are the generic rules shipped in the repo; machine-specific
// names belong in the local rules file.
var DefaultRules = Rules{
	DenyProfiles: []string{"*admin*", "*prod*"},
	DenyContexts: []string{"*prod*"},
}

// LoadRules returns DefaultRules extended by the JSON file at path (if it
// exists): deny/allow lists are appended, default_profile replaces.
func LoadRules(path string) (Rules, error) {
	r := DefaultRules
	r.DenyProfiles = append([]string(nil), r.DenyProfiles...)
	r.DenyContexts = append([]string(nil), r.DenyContexts...)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return r, nil
	}
	if err != nil {
		return Rules{}, fmt.Errorf("load guard rules: %w", err)
	}
	var local Rules
	if err := json.Unmarshal(raw, &local); err != nil {
		return Rules{}, fmt.Errorf("load guard rules %s: %w", path, err)
	}
	r.DenyProfiles = append(r.DenyProfiles, local.DenyProfiles...)
	r.AllowProfiles = append(r.AllowProfiles, local.AllowProfiles...)
	r.DenyContexts = append(r.DenyContexts, local.DenyContexts...)
	if local.DefaultProfile != "" {
		r.DefaultProfile = local.DefaultProfile
	}
	return r, nil
}

func matchAny(patterns []string, name string) bool {
	name = strings.ToLower(name)
	for _, p := range patterns {
		if ok, _ := path.Match(strings.ToLower(p), name); ok {
			return true
		}
	}
	return false
}

// ProfileAllowed reports whether the agent may use AWS profile name.
func ProfileAllowed(r Rules, name string) bool {
	return matchAny(r.AllowProfiles, name) || !matchAny(r.DenyProfiles, name)
}

// ContextAllowed reports whether the agent may use kube context (or EKS cluster) name.
func ContextAllowed(r Rules, name string) bool { return !matchAny(r.DenyContexts, name) }
