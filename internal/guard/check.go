package guard

import (
	"fmt"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// Verdict is CheckCommand's answer.
type Verdict struct {
	Block  bool   `json:"block"`
	Reason string `json:"reason,omitempty"`
}

// scopedEnv are the variables the guard points at filtered files; commands
// must not override them.
var scopedEnv = map[string]bool{"KUBECONFIG": true, "AWS_CONFIG_FILE": true, "AWS_SHARED_CREDENTIALS_FILE": true}

// realCredentialPaths are the unfiltered files commands must not point at.
var realCredentialPaths = []string{".aws/config", ".aws/credentials", ".kube/config"}

// CheckCommand inspects a bash command (every command in it: pipelines,
// chains, subshells, $(...), and bash -c strings) and blocks ones that
// reach for denied profiles/contexts or the unfiltered credential files.
// It can't see inside scripts (python, etc.); the filtered credentials are
// what stop those.
func CheckCommand(cmd string, r Rules) Verdict {
	return checkDepth(cmd, r, 0)
}

func checkDepth(cmd string, r Rules, depth int) Verdict {
	f, err := syntax.NewParser().Parse(strings.NewReader(cmd), "")
	if err != nil {
		return Verdict{} // not shell we can parse; the credentials boundary still holds
	}
	var v Verdict
	syntax.Walk(f, func(node syntax.Node) bool {
		if v.Block {
			return false
		}
		switch n := node.(type) {
		case *syntax.Assign:
			v = checkAssign(n.Name.Value, wordLit(n.Value), r)
		case *syntax.CallExpr:
			for _, a := range n.Assigns {
				if v = checkAssign(a.Name.Value, wordLit(a.Value), r); v.Block {
					return false
				}
			}
			v = checkCall(words(n.Args), r, depth)
		}
		return !v.Block
	})
	return v
}

func checkAssign(name, value string, r Rules) Verdict {
	switch {
	case scopedEnv[name]:
		return Verdict{Block: true, Reason: fmt.Sprintf("%s is managed by the guard; don't override it", name)}
	case name == "AWS_PROFILE" && value != "" && !ProfileAllowed(r, value):
		return Verdict{Block: true, Reason: fmt.Sprintf("AWS profile %q is not allowed for pi", value)}
	}
	return Verdict{}
}

func checkCall(args []string, r Rules, depth int) Verdict {
	for _, a := range args {
		for _, p := range realCredentialPaths {
			if strings.Contains(a, p) {
				return Verdict{Block: true, Reason: fmt.Sprintf("%s holds unfiltered credentials; pi uses filtered copies", p)}
			}
		}
	}
	if len(args) == 0 {
		return Verdict{}
	}
	switch filepath.Base(args[0]) {
	case "aws":
		return checkAWS(args[1:], r)
	case "kubectl", "k":
		return checkKubectl(args[1:], r)
	case "bash", "sh", "zsh":
		for i, a := range args {
			if a == "-c" && i+1 < len(args) && depth < 3 {
				return checkDepth(args[i+1], r, depth+1)
			}
		}
	}
	return Verdict{}
}

// flagValue returns the value of --name (as "--name v" or "--name=v").
func flagValue(args []string, name string) (string, bool) {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1], true
		}
		if v, ok := strings.CutPrefix(a, name+"="); ok {
			return v, true
		}
	}
	return "", false
}

func checkAWS(args []string, r Rules) Verdict {
	if p, ok := flagValue(args, "--profile"); ok && !ProfileAllowed(r, p) {
		return Verdict{Block: true, Reason: fmt.Sprintf("AWS profile %q is not allowed for pi", p)}
	}
	if len(args) >= 2 && args[0] == "eks" {
		var cluster string
		switch args[1] {
		case "update-kubeconfig":
			cluster, _ = flagValue(args, "--name")
		case "get-token":
			cluster, _ = flagValue(args, "--cluster-name")
		}
		if cluster != "" && !ContextAllowed(r, cluster) {
			return Verdict{Block: true, Reason: fmt.Sprintf("EKS cluster %q is not allowed for pi", cluster)}
		}
	}
	return Verdict{}
}

func checkKubectl(args []string, r Rules) Verdict {
	if _, ok := flagValue(args, "--kubeconfig"); ok {
		return Verdict{Block: true, Reason: "--kubeconfig bypasses the guard's filtered kubeconfig"}
	}
	if c, ok := flagValue(args, "--context"); ok && !ContextAllowed(r, c) {
		return Verdict{Block: true, Reason: fmt.Sprintf("kube context %q is not allowed for pi", c)}
	}
	if len(args) >= 3 && args[0] == "config" && (args[1] == "use-context" || args[1] == "set-context") && !ContextAllowed(r, args[2]) {
		return Verdict{Block: true, Reason: fmt.Sprintf("kube context %q is not allowed for pi", args[2])}
	}
	return Verdict{}
}

func words(ws []*syntax.Word) []string {
	out := make([]string, 0, len(ws))
	for _, w := range ws {
		out = append(out, wordLit(w))
	}
	return out
}

// wordLit returns a word's literal text (quotes removed; expansions dropped).
func wordLit(w *syntax.Word) string {
	if w == nil {
		return ""
	}
	var b strings.Builder
	var parts func([]syntax.WordPart)
	parts = func(ps []syntax.WordPart) {
		for _, p := range ps {
			switch p := p.(type) {
			case *syntax.Lit:
				b.WriteString(p.Value)
			case *syntax.SglQuoted:
				b.WriteString(p.Value)
			case *syntax.DblQuoted:
				parts(p.Parts)
			}
		}
	}
	parts(w.Parts)
	return b.String()
}
