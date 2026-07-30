package ci

import (
	"os"
	"strings"
	"testing"
)

func TestWorkflowPinsToolsAndExposesEveryRequiredGate(t *testing.T) {
	content, err := os.ReadFile("../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(content)
	for _, fragment := range []string{
		"GO_VERSION: 1.26.5",
		"actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd",
		"actions/setup-go@4a3601121dd01d1626a1e23e37211e3254c1c06c",
		"actions/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a",
		"browser-actions/setup-chrome@2e1d749697dd1612b833dba4a722266286fbefcd",
		"chrome-version: 151.0.7922.71",
		"postgres:18.4-alpine3.23@sha256:996d0920e4ff9df1fc19dacb904492f3c1ec0ec1cc338f0ad7123be7731c5f5e",
		"scripts/ci.sh format",
		"scripts/ci.sh dependencies",
		"scripts/ci.sh licenses",
		"scripts/ci.sh build",
		"scripts/ci.sh vet",
		"scripts/ci.sh race",
		"scripts/ci.sh generated",
		"scripts/ci.sh integration",
		"scripts/ci.sh e2e",
		"if: failure()",
		"test-results/auth-e2e/",
	} {
		if !strings.Contains(workflow, fragment) {
			t.Errorf("workflow missing %q", fragment)
		}
	}
}

func TestWorkflowNeverUsesFloatingActionTags(t *testing.T) {
	content, err := os.ReadFile("../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	for lineNumber, line := range strings.Split(string(content), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "uses:") && strings.Contains(line, "@v") {
			t.Errorf("line %d uses floating action tag: %s", lineNumber+1, line)
		}
	}
}
