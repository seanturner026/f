package feature_test

import (
	"os"
	"testing"
)

// TestMain sets git's identity once and keeps the user's git config out, so
// tests that commit can run in parallel (t.Setenv forbids t.Parallel).
func TestMain(m *testing.M) {
	for k, v := range map[string]string{
		"GIT_AUTHOR_NAME": "t", "GIT_AUTHOR_EMAIL": "t@t",
		"GIT_COMMITTER_NAME": "t", "GIT_COMMITTER_EMAIL": "t@t",
		"GIT_CONFIG_GLOBAL": os.DevNull, "GIT_CONFIG_NOSYSTEM": "1",
	} {
		os.Setenv(k, v)
	}
	os.Exit(m.Run())
}
