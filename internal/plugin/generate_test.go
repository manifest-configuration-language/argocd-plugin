package plugin

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGenerateRendersLifecycleAndOrdinaryResources(t *testing.T) {
	t.Parallel()

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

func TestGenerateResolvesMonorepoImportsFromProjectRoot(t *testing.T) {
	t.Parallel()

	project := t.TempDir()
	app := filepath.Join(project, "apps", "worker")
	writeProjectFile(t, project, "mcl.toml", "")
	writeProjectFile(t, project, "cluster.mcl", `export let clusterName = "monorepo-worker"
`)
	writeProjectFile(t, project, "apps/worker/main.mcl", `import "../../cluster.mcl" as cluster
let resource =
  apiVersion: "v1"
  kind: "ConfigMap"
  metadata:
    name: cluster.clusterName
emit resource
`)

	var output bytes.Buffer
	if err := generateFrom(t.Context(), app, "[]", &output); err != nil {
		t.Fatalf("generateFrom() error = %v", err)
	}
	if !strings.Contains(output.String(), "name: monorepo-worker") {
		t.Fatalf("generated manifests do not include the project-root import:\n%s", output.String())
	}
}

func TestGenerateMapsHelmHooksToArgoCDHooks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProjectFile(t, root, "main.mcl", `let options =
  kubeVersion: "1.36.0"
  apiVersions:
    - "apps/v1"
  releaseName: "sample"
  namespace: "default"
let resources = helm.render("chart", options)
emit resources
`)
	writeProjectFile(t, root, "chart/Chart.yaml", "apiVersion: v2\nname: sample\nversion: 0.1.0\n")
	writeProjectFile(t, root, "chart/templates/migration.yaml", `apiVersion: batch/v1
kind: Job
metadata:
  name: {{ .Release.Name }}-migration
  annotations:
    helm.sh/hook: pre-install,pre-upgrade
    helm.sh/hook-weight: "-2"
    helm.sh/hook-delete-policy: before-hook-creation,hook-succeeded
spec:
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: migration
          image: busybox:1.37.0
          command: ["sh", "-c", "echo migrate"]
`)
	writeProjectFile(t, root, "chart/templates/configmap.yaml", `apiVersion: v1
kind: ConfigMap
metadata:
  name: {{ .Release.Name }}
`)

	var output bytes.Buffer
	if err := generateFrom(t.Context(), root, "[]", &output); err != nil {
		t.Fatalf("generateFrom() error = %v", err)
	}
	manifest := output.String()
	for _, expected := range []string{
		"argocd.argoproj.io/hook: PreSync",
		"argocd.argoproj.io/hook-delete-policy: BeforeHookCreation,HookSucceeded",
		"argocd.argoproj.io/sync-wave: \"-2\"",
		"name: sample-migration",
		"name: sample",
	} {
		if !strings.Contains(manifest, expected) {
			t.Errorf("generated manifests do not contain %q:\n%s", expected, manifest)
		}
	}
	if strings.Contains(manifest, "helm.sh/hook:") {
		t.Fatalf("generated manifests retain Helm hook annotations:\n%s", manifest)
	}
}

func TestAnnounceInputFilesIncludesMCLAndParameterInputs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProjectFile(t, root, "mcl.toml", "")
	writeProjectFile(t, root, "apps/worker/main.mcl", `import "../../cluster.mcl" as cluster
let resource =
  apiVersion: "v1"
  kind: "ConfigMap"
  metadata:
    name: cluster.name
emit resource
`)
	writeProjectFile(t, root, "cluster.mcl", `export let name = "worker"
`)
	writeProjectFile(t, root, "apps/worker/params.json", `{}`)
	var output bytes.Buffer
	parameters := `[{"name":"parameter-files","array":["params.json"]}]`
	if err := announceInputFilesFrom(t.Context(), filepath.Join(root, "apps/worker"), parameters, "apps/worker", &output); err != nil {
		t.Fatalf("announceInputFilesFrom() error = %v", err)
	}
	inputs := []string{"main.mcl", "params.json"}
	if readFilesAvailable {
		inputs = append(inputs, "../../cluster.mcl", "../../mcl.toml")
	}
	for _, input := range inputs {
		if !strings.Contains(output.String(), input) {
			t.Errorf("dynamic announcement %s does not contain input %q", output.String(), input)
		}
	}
}

func TestGenerateRejectsCreateUpdateManifestDrift(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
