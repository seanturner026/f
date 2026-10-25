package guard_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/seanturner026/f/internal/guard"
)

// rules mirror a typical local setup: generic defaults plus one allowed admin
// profile needed for EKS dev/staging.
var rules = guard.Rules{
	DenyProfiles:   []string{"*admin*", "*prod*"},
	AllowProfiles:  []string{"team-eks-admin"},
	DenyContexts:   []string{"*prod*"},
	DefaultProfile: "team-read-only",
}

func TestProfileAndContextAllowed(t *testing.T) {
	tests := []struct {
		name string
		got  bool
		want bool
	}{
		{name: "read-only profile", got: guard.ProfileAllowed(rules, "team-read-only"), want: true},
		{name: "admin profile", got: guard.ProfileAllowed(rules, "mgmt-admin"), want: false},
		{name: "administrators", got: guard.ProfileAllowed(rules, "uni-administrators"), want: false},
		{name: "case-insensitive", got: guard.ProfileAllowed(rules, "Backup-ADMIN"), want: false},
		{name: "allow list beats deny", got: guard.ProfileAllowed(rules, "team-eks-admin"), want: true},
		{name: "dev context", got: guard.ContextAllowed(rules, "k8s-1-usw2-dev"), want: true},
		{name: "staging context", got: guard.ContextAllowed(rules, "k8s-1-usw2-staging"), want: true},
		{name: "production context", got: guard.ContextAllowed(rules, "k8s-public-1-usw2-production"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %v, want %v", tt.got, tt.want)
			}
		})
	}
}

func TestLoadRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "guard.json")
	if err := os.WriteFile(path, []byte(`{"allow_profiles":["team-eks-admin"],"deny_contexts":["*secret*"],"default_profile":"ro"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, path string
		want       guard.Rules
	}{
		{name: "missing file = defaults", path: filepath.Join(t.TempDir(), "nope.json"), want: guard.DefaultRules},
		{
			name: "local file extends defaults",
			path: path,
			want: guard.Rules{
				DenyProfiles:   []string{"*admin*", "*prod*"},
				AllowProfiles:  []string{"team-eks-admin"},
				DenyContexts:   []string{"*prod*", "*secret*"},
				DefaultProfile: "ro",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := guard.LoadRules(tt.path)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestFilterAWSConfig(t *testing.T) {
	in := `[default]
region = us-west-2

[profile team-read-only]
sso_session = team

[profile mgmt-admin]
sso_session = team
role_name = Admin

[profile team-eks-admin]
sso_session = team

[sso-session team]
sso_start_url = https://example.awsapps.com/start
`
	out, removed := guard.FilterAWSConfig(in, rules)
	tests := []struct {
		name string
		ok   bool
	}{
		{name: "keeps default", ok: strings.Contains(out, "[default]")},
		{name: "keeps read-only", ok: strings.Contains(out, "[profile team-read-only]")},
		{name: "keeps allowed eks admin", ok: strings.Contains(out, "[profile team-eks-admin]")},
		{name: "keeps sso-session", ok: strings.Contains(out, "[sso-session team]") && strings.Contains(out, "sso_start_url")},
		{name: "drops admin and its lines", ok: !strings.Contains(out, "mgmt-admin") && !strings.Contains(out, "role_name = Admin")},
		{name: "reports removed", ok: reflect.DeepEqual(removed, []string{"mgmt-admin"})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ok {
				t.Errorf("filtered config:\n%s\nremoved: %v", out, removed)
			}
		})
	}
}

func TestFilterAWSCredentials(t *testing.T) {
	in := "[demo-user]\naws_access_key_id = A\n[prod-deployer]\naws_access_key_id = B\n"
	out, removed := guard.FilterAWSCredentials(in, rules)
	if strings.Contains(out, "prod-deployer") || strings.Contains(out, "= B") || !strings.Contains(out, "[demo-user]") {
		t.Errorf("filtered:\n%s", out)
	}
	if !reflect.DeepEqual(removed, []string{"prod-deployer"}) {
		t.Errorf("removed = %v", removed)
	}
}

func TestFilterKubeconfig(t *testing.T) {
	in := `{
	  "current-context": "k8s-1-usw2-production",
	  "contexts": [
	    {"name": "k8s-1-usw2-dev", "context": {"cluster": "dev", "user": "u-dev"}},
	    {"name": "k8s-1-usw2-production", "context": {"cluster": "prod", "user": "u-prod"}}
	  ],
	  "clusters": [{"name": "dev"}, {"name": "prod"}],
	  "users": [{"name": "u-dev"}, {"name": "u-prod"}]
	}`
	out, removed, err := guard.FilterKubeconfig([]byte(in), rules)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		Current  string `json:"current-context"`
		Contexts []struct {
			Name string `json:"name"`
		} `json:"contexts"`
		Clusters []struct {
			Name string `json:"name"`
		} `json:"clusters"`
		Users []struct {
			Name string `json:"name"`
		} `json:"users"`
	}
	if err := json.Unmarshal(out, &cfg); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name      string
		got, want any
	}{
		{name: "removed", got: removed, want: []string{"k8s-1-usw2-production"}},
		{name: "current-context cleared", got: cfg.Current, want: ""},
		{name: "contexts", got: len(cfg.Contexts) == 1 && cfg.Contexts[0].Name == "k8s-1-usw2-dev", want: true},
		{name: "clusters", got: len(cfg.Clusters) == 1 && cfg.Clusters[0].Name == "dev", want: true},
		{name: "users", got: len(cfg.Users) == 1 && cfg.Users[0].Name == "u-dev", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !reflect.DeepEqual(tt.got, tt.want) {
				t.Errorf("got %v, want %v", tt.got, tt.want)
			}
		})
	}
}

func TestCheckCommand(t *testing.T) {
	tests := []struct {
		name  string
		cmd   string
		block bool
	}{
		// allowed
		{name: "plain read", cmd: "kubectl get pods -n argocd"},
		{name: "dev context", cmd: "kubectl --context k8s-1-usw2-dev get pods"},
		{name: "apply on dev via pipe", cmd: "kustomize build overlays/dev | kubectl --context=k8s-1-usw2-dev apply -f -"},
		{name: "allowed profile", cmd: "aws --profile team-read-only s3 ls"},
		{name: "eks dev kubeconfig", cmd: "aws eks update-kubeconfig --name k8s-1-usw2-dev"},
		{name: "unrelated", cmd: "go test ./... && git status"},
		{name: "python can't be inspected", cmd: `python3 -c "import boto3; boto3.Session(profile_name='mgmt-admin')"`},

		// blocked: denied targets
		{name: "prod context flag", cmd: "kubectl --context k8s-1-usw2-production get pods", block: true},
		{name: "prod context = form", cmd: "kubectl get pods --context=k8s-public-1-usw2-production", block: true},
		{name: "prod in pipe", cmd: "helm template x | kubectl --context k8s-1-afs1-production apply -f -", block: true},
		{name: "prod after &&", cmd: "cd apps && k --context k8s-1-usw2-production delete pod x", block: true},
		{name: "use-context prod", cmd: "kubectl config use-context k8s-1-usw2-production", block: true},
		{name: "admin profile flag", cmd: "aws s3 ls --profile mgmt-admin", block: true},
		{name: "admin profile = form", cmd: "aws --profile=backup-admin sts get-caller-identity", block: true},
		{name: "AWS_PROFILE prefix", cmd: "AWS_PROFILE=mgmt-admin terraform plan", block: true},
		{name: "export AWS_PROFILE", cmd: "export AWS_PROFILE=security-admin; aws s3 ls", block: true},
		{name: "eks prod kubeconfig", cmd: "aws eks update-kubeconfig --name k8s-1-usw2-production", block: true},
		{name: "eks prod token", cmd: "aws eks get-token --cluster-name k8s-1-usw2-production", block: true},
		{name: "in $(...)", cmd: `echo $(kubectl --context k8s-1-usw2-production get ns)`, block: true},
		{name: "in bash -c", cmd: `bash -c "kubectl --context k8s-1-usw2-production get ns"`, block: true},
		{name: "in subshell", cmd: "(cd x; aws --profile mgmt-admin s3 ls)", block: true},

		// blocked: bypassing the filtered files
		{name: "KUBECONFIG override", cmd: "KUBECONFIG=~/.kube/config kubectl get pods", block: true},
		{name: "export AWS_CONFIG_FILE", cmd: "export AWS_CONFIG_FILE=/tmp/x", block: true},
		{name: "--kubeconfig", cmd: "kubectl --kubeconfig /tmp/k get pods", block: true},
		{name: "reading real aws config", cmd: "cat ~/.aws/config", block: true},
		{name: "reading real credentials", cmd: `grep key "$HOME/.aws/credentials"`, block: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := guard.CheckCommand(tt.cmd, rules)
			if v.Block != tt.block {
				t.Errorf("block = %v (%s), want %v", v.Block, v.Reason, tt.block)
			}
			if v.Block && v.Reason == "" {
				t.Error("blocked without a reason")
			}
		})
	}
}
