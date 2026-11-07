package plugin

import (
	"bytes"
	"gopkg.in/yaml.v3"
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
let bootstrapResource =
  apiVersion: "v1"
  kind: "ConfigMap"
  metadata:
    name: "bootstrap"
let bootstrap = lifecycle.attach(bootstrapResource, ["bootstrap"], 0, false, [])
let regular =
  apiVersion: "v1"
  kind: "ConfigMap"
  metadata:
    name: "regular"
emit hook
emit bootstrap
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
	if !strings.Contains(manifest, "argocd.argoproj.io/sync-wave: \"-1000\"") {
		t.Fatalf("bootstrap resource did not receive the leading Sync Wave:\n%s", manifest)
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

func TestGenerateMapsDeleteHooksAndOptionalVerification(t *testing.T) {
	root := t.TempDir()
	writeProjectFile(t, root, "main.mcl", `let beforeDeleteResource =
  apiVersion: "v1"
  kind: "ConfigMap"
  metadata:
    name: "before-delete"
let afterDeleteResource =
  apiVersion: "v1"
  kind: "ConfigMap"
  metadata:
    name: "after-delete"
let verifyResource =
  apiVersion: "v1"
  kind: "ConfigMap"
  metadata:
    name: "verify"
let beforeDelete = lifecycle.attach(beforeDeleteResource, ["before.delete"], 1, false, [])
let afterDelete = lifecycle.attach(afterDeleteResource, ["after.delete"], 2, false, [])
let verify = lifecycle.attach(verifyResource, ["verify"], 3, false, [])
emit beforeDelete
emit afterDelete
emit verify
`)
	var output bytes.Buffer
	if err := generateFrom(t.Context(), root, "[]", &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "argocd.argoproj.io/hook: PreDelete") || !strings.Contains(output.String(), "argocd.argoproj.io/hook: PostDelete") || strings.Contains(output.String(), "name: verify") {
		t.Fatalf("default delete/verify lifecycle output is incorrect:\n%s", output.String())
	}
	output.Reset()
	if err := generateFrom(t.Context(), root, `[{"name":"include-verify","string":"true"}]`, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "argocd.argoproj.io/hook: PostSync") || !strings.Contains(output.String(), "name: verify") {
		t.Fatalf("enabled verify lifecycle output is incorrect:\n%s", output.String())
	}
}

func TestDecodeResourcePreservesLargeIntegers(t *testing.T) {
	const value = "9223372036854775807"
	resource, err := decodeResource([]byte(`{"apiVersion":"v1","kind":"ConfigMap","metadata":{"name":"integer"},"data":{"value":` + value + `}}`))
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := yaml.Marshal(resource)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), value) {
		t.Fatalf("encoded resource lost integer precision: %s", encoded)
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
