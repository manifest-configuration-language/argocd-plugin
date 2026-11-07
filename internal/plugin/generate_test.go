package plugin

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateRendersLifecycleAndOrdinaryResources(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "main.mcl", `let resource =
  apiVersion: "v1"
  kind: "ConfigMap"
  metadata:
    name: "hook"
let hook = lifecycle.attach(resource, ["before.create"], 2, false, ["on-success"])
let regular =
  apiVersion: "v1"
  kind: "ConfigMap"
  metadata:
    name: "regular"
emit hook
emit regular
`)
	var output bytes.Buffer
	if err := generateFrom(t.Context(), root, "[]", &output); err != nil {
		t.Fatal(err)
	}
	manifest := output.String()
	if !strings.Contains(manifest, "argocd.argoproj.io/hook: PreSync") || !strings.Contains(manifest, "argocd.argoproj.io/hook-delete-policy: HookSucceeded") {
		t.Fatalf("lifecycle annotations missing from output:\n%s", manifest)
	}
	if strings.Contains(manifest, "argocd.argoproj.io/sync-wave: \"0\"") {
		t.Fatalf("ordinary resource received an unnecessary Sync Wave:\n%s", manifest)
	}
}

func TestGenerateRejectsCreateUpdateManifestDrift(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "main.mcl", `let options =
  kubeVersion: "1.36.0"
  apiVersions:
    - "v1"
  releaseName: "sample"
let resources = helm.render("chart", options)
emit resources
`)
	writeProjectFile(t, root, "chart/Chart.yaml", "apiVersion: v2\nname: sample\nversion: 0.1.0\n")
	writeProjectFile(t, root, "chart/templates/configmap.yaml", `apiVersion: v1
kind: ConfigMap
metadata:
  name: sample
data:
  operation: {{ if .Release.IsInstall }}create{{ else }}update{{ end }}
`)
	err := generateFrom(t.Context(), root, "[]", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "render conflicting contents") {
		t.Fatalf("generateFrom() error = %v, want operation drift error", err)
	}
}

func TestGenerateRequiresExplicitEmptyOutputOptIn(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "main.mcl", "let unused = 1\n")
	if err := generateFrom(t.Context(), root, "[]", &bytes.Buffer{}); err == nil {
		t.Fatal("generateFrom() accepted empty output without opt-in")
	}
	var output bytes.Buffer
	if err := generateFrom(t.Context(), root, `[{"name":"allow-empty","string":"true"}]`, &output); err != nil {
		t.Fatalf("generateFrom() rejected explicitly allowed empty output: %v", err)
	}
	if output.Len() != 0 {
		t.Fatalf("allowed empty output contains %q", output.String())
	}
}

func writeProjectFile(t *testing.T, root, name, contents string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
