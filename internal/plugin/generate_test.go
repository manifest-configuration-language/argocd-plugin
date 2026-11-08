package plugin

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
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
let hook = lifecycle.attach(resource, ["before.create"], 2, true, ["on-success"])
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

func TestGenerateRendersGeneratedNameHook(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProjectFile(t, root, "main.mcl", `let hookResource =
  apiVersion: "batch/v1"
  kind: "Job"
  metadata:
    generateName: "migration-"
  spec:
    template:
      spec:
        restartPolicy: "Never"
        containers:
          - name: "migration"
            image: "busybox:1.37.0"
            command: ["sh", "-c", "echo migration"]
let hook = lifecycle.attach(hookResource, ["before.create", "before.update"], 0, true, ["on-success"])
emit hook
`)
	var output bytes.Buffer
	if err := generateFrom(t.Context(), root, "[]", &output); err != nil {
		t.Fatalf("generateFrom() rejected a Hook with metadata.generateName: %v", err)
	}
	if !strings.Contains(output.String(), "generateName: migration-") ||
		!strings.Contains(output.String(), "argocd.argoproj.io/hook: PreSync") ||
		!strings.Contains(output.String(), "argocd.argoproj.io/hook-delete-policy: HookSucceeded") {
		t.Fatalf("generated-name Hook was not rendered with its lifecycle:\n%s", output.String())
	}
	if strings.Contains(output.String(), "name: migration-") {
		t.Fatalf("plugin replaced metadata.generateName with metadata.name:\n%s", output.String())
	}
}

func TestGenerateKeepsBootstrapAsRegularResource(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProjectFile(t, root, "main.mcl", `let bootstrapResource =
  apiVersion: "apiextensions.k8s.io/v1"
  kind: "CustomResourceDefinition"
  metadata:
    name: "widgets.example.test"
  spec:
    group: "example.test"
    scope: "Namespaced"
    names:
      plural: "widgets"
      singular: "widget"
      kind: "Widget"
      listKind: "WidgetList"
    versions:
      - name: "v1"
        served: true
        storage: true
        schema:
          openAPIV3Schema:
            type: "object"
let bootstrap = lifecycle.attach(bootstrapResource, ["bootstrap"], 0, true, [])
emit bootstrap
`)
	var output bytes.Buffer
	if err := generateFrom(t.Context(), root, "[]", &output); err != nil {
		t.Fatalf("generateFrom() error = %v", err)
	}
	if !strings.Contains(output.String(), "kind: CustomResourceDefinition") || strings.Contains(output.String(), "namespace: default") || !strings.Contains(output.String(), "argocd.argoproj.io/sync-wave: \"-1\"") || strings.Contains(output.String(), "argocd.argoproj.io/hook:") {
		t.Fatalf("bootstrap CRD was not kept as a regular resource:\n%s", output.String())
	}
}

func TestGenerateDeduplicatesResourcesUsingArgoDestinationNamespace(t *testing.T) {
	t.Setenv("ARGOCD_APP_NAMESPACE", "default")
	root := t.TempDir()
	writeProjectFile(t, root, "main.mcl", `let createResource =
  apiVersion: "v1"
  kind: "ConfigMap"
  metadata:
    name: "shared"
let updateResource =
  apiVersion: "v1"
  kind: "ConfigMap"
  metadata:
    namespace: "default"
    name: "shared"
let create = lifecycle.attach(createResource, ["before.create"], 0, true, [])
let update = lifecycle.attach(updateResource, ["before.update"], 0, true, [])
emit create
emit update
`)
	var output bytes.Buffer
	if err := generateFrom(t.Context(), root, "[]", &output); err != nil {
		t.Fatal(err)
	}
	if strings.Count(output.String(), "kind: ConfigMap") != 1 {
		t.Fatalf("resources with omitted and explicit destination namespaces were not deduplicated:\n%s", output.String())
	}
}

func TestGenerateRejectsBootstrapAndPreSyncInOneApplication(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProjectFile(t, root, "main.mcl", `let bootstrapResource =
  apiVersion: "v1"
  kind: "ConfigMap"
  metadata:
    name: "bootstrap"
let bootstrap = lifecycle.attach(bootstrapResource, ["bootstrap"], 0, true, [])
let hookResource =
  apiVersion: "v1"
  kind: "ConfigMap"
  metadata:
    name: "pre-sync"
let hook = lifecycle.attach(hookResource, ["before.create"], 0, true, [])
emit bootstrap
emit hook
`)
	err := generateFrom(t.Context(), root, "[]", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "cannot run before PreSync hooks") {
		t.Fatalf("generateFrom() error = %v, want Bootstrap and PreSync ordering diagnostic", err)
	}
}

func TestGenerateRejectsUnsupportedLifecycleWaitSemantics(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProjectFile(t, root, "main.mcl", `let resource =
  apiVersion: "v1"
  kind: "ConfigMap"
  metadata:
    name: "pre-sync"
let hook = lifecycle.attach(resource, ["before.create"], 0, false, [])
emit hook
`)
	err := generateFrom(t.Context(), root, "[]", &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "wait=false") {
		t.Fatalf("generateFrom() error = %v, want unsupported wait semantics diagnostic", err)
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

func TestListInputFilesIncludesMCLAndParameterInputs(t *testing.T) {
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
	if err := listInputFilesFrom(t.Context(), filepath.Join(root, "apps/worker"), parameters, "apps/worker", &output); err != nil {
		t.Fatalf("listInputFilesFrom() error = %v", err)
	}
	inputs := []string{"main.mcl", "params.json"}
	if readFilesAvailable {
		inputs = append(inputs, "../../cluster.mcl", "../../mcl.toml")
	}
	for _, input := range inputs {
		if !strings.Contains(output.String(), input) {
			t.Errorf("input list %s does not contain input %q", output.String(), input)
		}
	}
	if !strings.HasSuffix(output.String(), "\n") {
		t.Fatalf("input list %q does not end in a newline", output.String())
	}
	if readFilesAvailable && !strings.Contains(output.String(), "../../cluster.mcl") {
		t.Errorf("input list %q does not contain the monorepo import relative to the Application source", output.String())
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

func TestGenerateIsDeterministic(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProjectFile(t, root, "main.mcl", `let first =
  apiVersion: "v1"
  kind: "ConfigMap"
  metadata:
    name: "first"
  data:
    value: "stable"
let second =
  apiVersion: "v1"
  kind: "ConfigMap"
  metadata:
    name: "second"
  data:
    value: "stable"
emit second
emit first
`)

	var first, second bytes.Buffer
	if err := generateFrom(t.Context(), root, "[]", &first); err != nil {
		t.Fatalf("first generateFrom() error = %v", err)
	}
	if err := generateFrom(t.Context(), root, "[]", &second); err != nil {
		t.Fatalf("second generateFrom() error = %v", err)
	}
	if !bytes.Equal(first.Bytes(), second.Bytes()) {
		t.Fatalf("generateFrom() output changed for identical input:\nfirst:\n%s\nsecond:\n%s", first.String(), second.String())
	}
}

func TestGenerateConcurrentProjectsShareCache(t *testing.T) {
	cache := filepath.Join(t.TempDir(), "mcl-cache")
	t.Setenv("MCL_CACHE_DIRECTORY", cache)

	const projectCount = 8
	roots := make([]string, projectCount)
	for index := range roots {
		roots[index] = t.TempDir()
		writeProjectFile(t, roots[index], "main.mcl", `let resource =
  apiVersion: "v1"
  kind: "ConfigMap"
  metadata:
    name: "concurrent"
emit resource
`)
	}

	errors := make(chan error, projectCount)
	var workers sync.WaitGroup
	for _, root := range roots {
		workers.Go(func() {
			var output bytes.Buffer
			if err := generateFrom(t.Context(), root, "[]", &output); err != nil {
				errors <- err

				return
			}
			if !strings.Contains(output.String(), "name: concurrent") {
				errors <- fmt.Errorf("generateFrom() output is missing the ConfigMap: %s", output.String())
			}
		})
	}
	workers.Wait()
	close(errors)
	for err := range errors {
		t.Error(err)
	}
}

func TestGenerateCanceledContextDoesNotWritePartialManifest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProjectFile(t, root, "main.mcl", `let resource =
  apiVersion: "v1"
  kind: "ConfigMap"
  metadata:
    name: "canceled"
emit resource
`)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	var output bytes.Buffer
	if err := generateFrom(ctx, root, "[]", &output); err == nil {
		t.Fatal("generateFrom() accepted a canceled Context")
	}
	if output.Len() != 0 {
		t.Fatalf("generateFrom() wrote partial output after cancellation: %s", output.String())
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
let beforeDelete = lifecycle.attach(beforeDeleteResource, ["before.delete"], 1, true, [])
let afterDelete = lifecycle.attach(afterDeleteResource, ["after.delete"], 2, true, [])
let verify = lifecycle.attach(verifyResource, ["verify"], 3, true, [])
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
