package e2e

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/manifest-configuration-language/mcl/pkg/mcl"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	apps        = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}
	configmaps  = schema.GroupVersionResource{Version: "v1", Resource: "configmaps"}
	deployments = schema.GroupVersionResource{Group: "apps", Version: "v1", Resource: "deployments"}
	jobs        = schema.GroupVersionResource{Group: "batch", Version: "v1", Resource: "jobs"}
	roles       = schema.GroupVersionResource{Group: "rbac.authorization.k8s.io", Version: "v1", Resource: "clusterroles"}
	crds        = schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	widgets     = schema.GroupVersionResource{Group: "e2e.example.com", Version: "v1", Resource: "widgets"}
	authWidgets = schema.GroupVersionResource{Group: "auth.example.com", Version: "v1", Resource: "widgets"}
)

const (
	appName                = "mcl-plugin-e2e"
	apiVersionField        = "apiVersion"
	metadataField          = "metadata"
	gitAuthAdminToken      = "e2e-admin-token" // #nosec G101 -- This token only protects the disposable CI fixture.
	gitAuthApplicationUser = "application"
	gitAuthApplicationPass = "e2e-application-password" // #nosec G101 -- This password only protects the disposable CI fixture.
	gitAuthPrivateUser     = "private"
	gitAuthPrivatePass     = "e2e-private-password" // #nosec G101 -- This password only protects the disposable CI fixture.
)

type suite struct {
	t              *testing.T
	client         dynamic.Interface
	repo, revision string
	syncSequence   uint
}

// Fixture resources have fixed names, so these scenarios must run sequentially.
//
//nolint:paralleltest
func TestArgoIntegration(t *testing.T) {
	if os.Getenv("E2E_RUN") != "1" {
		t.Skip("set E2E_RUN=1 to run against the dedicated Kind cluster")
	}
	repo, rev := os.Getenv("E2E_REPOSITORY"), os.Getenv("E2E_REVISION")
	if repo == "" || rev == "" {
		t.Fatal("E2E_REPOSITORY and E2E_REVISION are required")
	}
	if len(rev) != 40 && len(rev) != 64 {
		t.Fatal("E2E_REVISION must be a full Git object ID")
	}
	loading := clientcmd.NewDefaultClientConfigLoadingRules()
	raw, err := loading.Load()
	if err != nil {
		t.Fatal(err)
	}
	if raw.CurrentContext != "kind-mcl-argocd-e2e" {
		t.Fatalf("refusing context %q", raw.CurrentContext)
	}
	cfg, err := clientcmd.NewNonInteractiveClientConfig(*raw, raw.CurrentContext, &clientcmd.ConfigOverrides{}, loading).ClientConfig()
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(cfg.Host)
	if err != nil {
		t.Fatal(err)
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
		t.Fatalf("refusing non-loopback API server %q", cfg.Host)
	}
	c, err := dynamic.NewForConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	nodes, err := c.Resource(schema.GroupVersionResource{Version: "v1", Resource: "nodes"}).List(t.Context(), metav1.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes.Items) != 1 || nodes.Items[0].GetName() != "mcl-argocd-e2e-control-plane" {
		t.Fatal("refusing cluster without the dedicated Kind control-plane node")
	}
	if _, ok := nodes.Items[0].GetLabels()["node-role.kubernetes.io/control-plane"]; !ok {
		t.Fatal("refusing cluster without the dedicated Kind control-plane node")
	}
	s := &suite{t: t, client: c, repo: repo, revision: rev}
	t.Run("bootstrap", s.bootstrap)
	t.Run("multi-source", s.multiSource)
	t.Run("update-prune", s.updatePrune)
	t.Run("selective-sync", s.selective)
	t.Run("delete-hooks", s.deleteHooks)
	t.Run("generated-name-hooks", s.generatedHooks)
	t.Run("failure-hook", s.failureHook)
	t.Run("git-authentication", s.gitAuthentication)
}

func (s *suite) resource(g schema.GroupVersionResource, ns string) dynamic.ResourceInterface {
	r := s.client.Resource(g)
	if ns != "" {
		return r.Namespace(ns)
	}

	return r
}

func (s *suite) get(g schema.GroupVersionResource, ns, name string) (*unstructured.Unstructured, error) {
	return s.resource(g, ns).Get(s.t.Context(), name, metav1.GetOptions{})
}

func (s *suite) poll(label string, f func() (bool, error)) {
	s.pollContext(s.t.Context(), label, f)
}

func (s *suite) pollContext(ctx context.Context, label string, f func() (bool, error)) {
	s.t.Helper()
	var last error
	err := wait.PollUntilContextTimeout(ctx, 2*time.Second, 5*time.Minute, true, func(context.Context) (bool, error) {
		ok, e := f()
		if e != nil {
			last = e
		}

		return ok, nil
	})
	if err != nil {
		app, _ := s.get(apps, "argocd", appName)
		s.t.Fatalf("%s: %v (last error: %v); application: %v", label, err, last, app)
	}
}

func (s *suite) exists(g schema.GroupVersionResource, ns, name string, want bool) {
	s.t.Helper()
	s.poll(fmt.Sprintf("%s %s exists=%v", g.Resource, name, want), func() (bool, error) {
		_, err := s.get(g, ns, name)
		if apierrors.IsNotFound(err) {
			return !want, nil
		}

		return want && err == nil, err
	})
}

func (s *suite) field(g schema.GroupVersionResource, ns, name, value string, path ...string) {
	s.t.Helper()
	s.poll(fmt.Sprintf("%s %s %v=%s", g.Resource, name, path, value), func() (bool, error) {
		o, e := s.get(g, ns, name)
		if e != nil {
			return false, e
		}
		v, _, e := unstructured.NestedString(o.Object, path...)

		return v == value, e
	})
}

func (s *suite) delete(g schema.GroupVersionResource, ns, name string) {
	s.deleteContext(s.t.Context(), g, ns, name)
}

func (s *suite) deleteContext(ctx context.Context, g schema.GroupVersionResource, ns, name string) {
	s.t.Helper()
	err := s.resource(g, ns).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		s.t.Errorf("delete %s %s: %v", g.Resource, name, err)
	}
}

func (s *suite) cleanup() {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.t.Context()), 5*time.Minute)
	defer cancel()
	s.deleteContext(ctx, apps, "argocd", appName)
	s.pollContext(ctx, "applications "+appName+" exists=false", func() (bool, error) {
		_, err := s.resource(apps, "argocd").Get(ctx, appName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil
		}

		return false, err
	})
}

func (s *suite) create(t *testing.T, path string, auto, finalizer bool, other ...string) *suite {
	t.Helper()

	x := *s
	x.t = t
	source := func(p string, plugin bool) map[string]any {
		repoURL := s.repo
		if !strings.Contains(repoURL, "://") {
			repoURL = "https://github.com/" + repoURL + ".git"
		}
		m := map[string]any{"repoURL": repoURL, "targetRevision": s.revision, "path": p}
		if plugin {
			m["plugin"] = map[string]any{"name": "mcl"}
		}

		return m
	}
	spec := map[string]any{"project": "default", "destination": map[string]any{"server": "https://kubernetes.default.svc", "namespace": "default"}}
	if len(other) > 0 {
		spec["sources"] = []any{source(path, true), source(other[0], false)}
	} else {
		spec["source"] = source(path, true)
	}
	if auto {
		spec["syncPolicy"] = map[string]any{"automated": map[string]any{"prune": true}}
	}
	metadata := map[string]any{"name": appName, "namespace": "argocd"}
	obj := &unstructured.Unstructured{Object: map[string]any{apiVersionField: "argoproj.io/v1alpha1", "kind": "Application", metadataField: metadata, "spec": spec}}
	if finalizer {
		obj.SetFinalizers([]string{"resources-finalizer.argocd.argoproj.io"})
	}
	_, err := x.resource(apps, "argocd").Create(t.Context(), obj, metav1.CreateOptions{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(x.cleanup)

	return &x
}

func (s *suite) patch(payload string) {
	s.t.Helper()
	_, err := s.resource(apps, "argocd").Patch(s.t.Context(), appName, types.MergePatchType, []byte(payload), metav1.PatchOptions{})
	if err != nil {
		s.t.Fatal(err)
	}
}

func (s *suite) sync(path, resources, phase string) *unstructured.Unstructured {
	s.t.Helper()
	revision := s.revision
	if s.syncSequence%2 == 0 {
		revision = revision[:7]
	}
	s.syncSequence++
	extra := ""
	if path != "" {
		extra = fmt.Sprintf(`"spec":{"source":{"path":%q}},`, path)
	}
	if resources != "" {
		resources = `,"resources":` + resources
	}
	s.patch(fmt.Sprintf(`{%s"operation":{"sync":{"revision":%q%s}}}`, extra, revision, resources))
	var result *unstructured.Unstructured
	s.poll("sync operation "+phase, func() (bool, error) {
		o, e := s.get(apps, "argocd", appName)
		if e != nil {
			return false, e
		}
		finished, _, _ := unstructured.NestedString(o.Object, "status", "operationState", "finishedAt")
		p, _, _ := unstructured.NestedString(o.Object, "status", "operationState", "phase")
		operationRevision, _, _ := unstructured.NestedString(o.Object, "status", "operationState", "operation", "sync", "revision")
		if finished != "" && operationRevision == revision {
			if p != phase {
				return false, fmt.Errorf("unexpected phase %s: %v", p, o.Object["status"])
			}
			result = o

			return true, nil
		}

		return false, nil
	})

	return result
}

func results(o *unstructured.Unstructured) []any {
	r, _, _ := unstructured.NestedSlice(o.Object, "status", "operationState", "syncResult", "resources")

	return r
}
func (s *suite) synced() { s.field(apps, "argocd", appName, "Synced", "status", "sync", "status") }
func (s *suite) bootstrap(t *testing.T) {
	t.Cleanup(func() { s.delete(roles, "", "mcl-e2e-bootstrap-review") })
	t.Cleanup(func() { s.delete(crds, "", "widgets.e2e.example.com") })
	x := s.create(t, "examples/e2e/bootstrap", true, true)
	x.synced()
	x.field(apps, "argocd", appName, "Healthy", "status", "health", "status")
	x.field(roles, "", "mcl-e2e-bootstrap-review", appName+":rbac.authorization.k8s.io/ClusterRole:default/mcl-e2e-bootstrap-review", "metadata", "annotations", "argocd.argoproj.io/tracking-id")
	x.cleanup()
	x.exists(roles, "", "mcl-e2e-bootstrap-review", false)
	x.exists(crds, "", "widgets.e2e.example.com", true)
	list, err := x.resource(widgets, "").List(t.Context(), metav1.ListOptions{})
	if err != nil || len(list.Items) != 0 {
		t.Fatalf("remaining widgets: %v, %v", list, err)
	}
}

func (s *suite) multiSource(t *testing.T) {
	t.Cleanup(func() {
		s.delete(configmaps, "default", "mcl-multi-source")
		s.delete(deployments, "default", appName)
		s.delete(jobs, "default", appName+"-migration")
	})
	x := s.create(t, "examples/multi-source", true, true, "examples/helm/chart")
	x.synced()
	x.exists(configmaps, "default", "mcl-multi-source", true)
	x.exists(deployments, "default", appName, true)
	o, err := x.get(apps, "argocd", appName)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, v := range results(o) {
		r := v.(map[string]any)
		if r["kind"] == "Job" && r["name"] == appName+"-migration" && r["hookPhase"] == "Succeeded" {
			found = true
		}
	}
	if !found {
		t.Fatal("Helm PreSync hook did not succeed")
	}
	x.exists(jobs, "default", appName+"-migration", false)
	x.cleanup()
	x.exists(configmaps, "default", "mcl-multi-source", false)
	x.exists(deployments, "default", appName, false)
}

func (s *suite) updatePrune(t *testing.T) {
	t.Cleanup(func() {
		s.delete(configmaps, "default", "mcl-e2e-stable")
		s.delete(configmaps, "default", "mcl-e2e-stale")
	})
	x := s.create(t, "examples/e2e/initial", true, true)
	x.synced()
	x.field(configmaps, "default", "mcl-e2e-stable", "before", "data", "value")
	x.exists(configmaps, "default", "mcl-e2e-stale", true)
	x.patch(`{"spec":{"source":{"path":"examples/e2e/updated"}}}`)
	x.field(configmaps, "default", "mcl-e2e-stable", "after", "data", "value")
	x.synced()
	x.exists(configmaps, "default", "mcl-e2e-stale", false)
	x.cleanup()
	x.exists(configmaps, "default", "mcl-e2e-stable", false)
}

func (s *suite) selective(t *testing.T) {
	t.Cleanup(func() {
		s.delete(configmaps, "default", "mcl-e2e-selective")
		s.delete(jobs, "default", "mcl-e2e-selective-hook")
	})
	x := s.create(t, "examples/e2e/selective-before", false, true)
	x.sync("", "", "Succeeded")
	x.exists(configmaps, "default", "mcl-e2e-selective", true)
	x.exists(jobs, "default", "mcl-e2e-selective-hook", false)
	o := x.sync("examples/e2e/selective-after", `[{"group":"","kind":"ConfigMap","name":"mcl-e2e-selective","namespace":"default"}]`, "Succeeded")
	x.field(configmaps, "default", "mcl-e2e-selective", "after", "data", "value")
	found := false
	for _, v := range results(o) {
		r := v.(map[string]any)
		if r["kind"] == "Job" {
			t.Fatal("selective sync executed a Job")
		}
		if r["kind"] == "ConfigMap" && r["name"] == "mcl-e2e-selective" {
			found = true
		}
	}
	if !found {
		t.Fatal("selective sync omitted ConfigMap")
	}
	x.exists(jobs, "default", "mcl-e2e-selective-hook", false)
	x.cleanup()
	x.exists(configmaps, "default", "mcl-e2e-selective", false)
}

func (s *suite) deleteHooks(t *testing.T) {
	t.Cleanup(func() { s.delete(configmaps, "default", "mcl-e2e-stable") })
	for _, n := range []string{"mcl-e2e-before-delete", "mcl-e2e-after-delete"} {
		t.Cleanup(func() { s.delete(jobs, "default", n) })
	}
	x := s.create(t, "examples/e2e/delete", false, true)
	x.sync("", "", "Succeeded")
	x.exists(configmaps, "default", "mcl-e2e-stable", true)
	x.delete(apps, "argocd", appName)
	for _, n := range []string{"mcl-e2e-before-delete", "mcl-e2e-after-delete"} {
		x.poll("completed job "+n, func() (bool, error) {
			o, e := x.get(jobs, "default", n)
			if e != nil {
				return false, e
			}
			conditions, _, _ := unstructured.NestedSlice(o.Object, "status", "conditions")
			for _, v := range conditions {
				c := v.(map[string]any)
				if c["type"] == "Complete" && c["status"] == "True" {
					return true, nil
				}
			}

			return false, nil
		})
	}
	x.exists(apps, "argocd", appName, false)
	x.exists(configmaps, "default", "mcl-e2e-stable", false)
}

func (s *suite) generatedHooks(t *testing.T) {
	t.Cleanup(func() {
		err := s.resource(jobs, "default").DeleteCollection(context.WithoutCancel(t.Context()), metav1.DeleteOptions{}, metav1.ListOptions{LabelSelector: "e2e=generated-name-hook"})
		if err != nil {
			t.Errorf("delete generated hook jobs: %v", err)
		}
	})
	x := s.create(t, "examples/e2e/generated-name-hook", false, false)
	var previous []string
	for range 2 {
		o := x.sync("", "", "Succeeded")
		names := []string{}
		for _, v := range results(o) {
			r := v.(map[string]any)
			name, _ := r["name"].(string)
			if r["kind"] == "Job" && r["hookPhase"] == "Succeeded" && strings.HasPrefix(name, "mcl-e2e-generated-hook-") {
				names = append(names, name)
			}
		}
		slices.Sort(names)
		if len(names) != 2 || names[0] == names[1] || slices.Equal(names, previous) {
			t.Fatalf("unexpected generated hooks: %v, previous: %v", names, previous)
		}
		previous = names
		x.poll("generated hook cleanup", func() (bool, error) {
			l, e := x.resource(jobs, "default").List(t.Context(), metav1.ListOptions{LabelSelector: "e2e=generated-name-hook"})

			return e == nil && len(l.Items) == 0, e
		})
	}
}

func (s *suite) failureHook(t *testing.T) {
	t.Cleanup(func() { s.delete(jobs, "default", "mcl-failure-hook") })
	x := s.create(t, "examples/e2e/failure-hook", false, true)
	x.sync("", "", "Failed")
	x.exists(jobs, "default", "mcl-failure-hook", false)
}

func (s *suite) gitAuthentication(t *testing.T) {
	baseURL := os.Getenv("E2E_GIT_AUTH_URL")
	parsedURL, err := url.Parse(baseURL)
	if err != nil || parsedURL.Scheme != "https" || !strings.HasSuffix(strings.ToLower(parsedURL.Hostname()), ".trycloudflare.com") || parsedURL.User != nil || parsedURL.Path != "" || parsedURL.RawQuery != "" || parsedURL.Fragment != "" {
		t.Fatalf("E2E_GIT_AUTH_URL must be an HTTPS Cloudflare quick-tunnel origin")
	}

	client := &http.Client{Timeout: 10 * time.Second}
	deadline := time.Now().Add(time.Minute)
	for {
		// #nosec G704 -- The URL is restricted to the public HTTPS origin created for this CI run.
		response, err := client.Get(baseURL + "/healthz")
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("authenticated Git test server is unavailable: %v", err)
		}
		time.Sleep(250 * time.Millisecond)
	}

	createGitTestRepository(t, client, baseURL, "application.git")
	createGitTestRepository(t, client, baseURL, "private.git")

	applicationURL := baseURL + "/application.git"
	privateURL := baseURL + "/private.git"
	application := t.TempDir()
	applicationRevision := initializeGitTestRepository(t, application, applicationURL, gitAuthApplicationUser, gitAuthApplicationPass, map[string]string{
		".gitignore":          ".mcl/\n",
		"schemas/widget.yaml": widgetCRDSchema,
	})
	private := t.TempDir()
	privateRevision := initializeGitTestRepository(t, private, privateURL, gitAuthPrivateUser, gitAuthPrivatePass, map[string]string{
		"schemas/widget.yaml": widgetCRDSchema,
	})
	writeGitProject(t, application, "success", applicationURL, applicationRevision, gitAuthApplicationUser, gitAuthApplicationPass, widgetManifest("mcl-git-auth-success"))
	writeGitProject(t, application, "denied", privateURL, privateRevision, gitAuthPrivateUser, gitAuthPrivatePass, widgetManifest("mcl-git-auth-denied"))
	applicationHead := runGit(t, application, gitAuthApplicationUser, gitAuthApplicationPass, "rev-parse", "HEAD")
	runGit(t, application, gitAuthApplicationUser, gitAuthApplicationPass, "push", "origin", "HEAD:main")
	createGitTestCRD(t, s)

	if err := createGitRepositorySecret(t, s.client, applicationURL); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.resource(apps, "argocd").Delete(context.WithoutCancel(t.Context()), appName, metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("delete authenticated Git Application: %v", err)
		}
		if err := s.resource(schema.GroupVersionResource{Version: "v1", Resource: "secrets"}, "argocd").Delete(context.WithoutCancel(t.Context()), "mcl-git-auth-e2e", metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			t.Errorf("delete authenticated Git repository Secret: %v", err)
		}
	})

	authenticated := *s
	authenticated.repo = applicationURL
	authenticated.revision = applicationHead
	success := authenticated.create(t, "success", false, true)
	success.sync("", "", "Succeeded")
	success.exists(authWidgets, "default", "mcl-git-auth-success", true)
	success.cleanup()
	success.exists(authWidgets, "default", "mcl-git-auth-success", false)

	denied := authenticated.create(t, "denied", false, true)
	result := denied.sync("", "", "Failed")
	messages := []string{}
	if message, _, _ := unstructured.NestedString(result.Object, "status", "operationState", "message"); message != "" {
		messages = append(messages, message)
	}
	conditions, _, _ := unstructured.NestedSlice(result.Object, "status", "conditions")
	for _, rawCondition := range conditions {
		condition, ok := rawCondition.(map[string]any)
		if !ok {
			continue
		}
		if message, ok := condition["message"].(string); ok {
			messages = append(messages, message)
		}
	}
	message := strings.Join(messages, "\n")
	if !strings.Contains(message, "401") && !strings.Contains(strings.ToLower(message), "authentication") {
		t.Fatalf("different-repository Git dependency failed for an unexpected reason: %s", message)
	}
	denied.exists(authWidgets, "default", "mcl-git-auth-denied", false)
}

const widgetCRDSchema = `apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: widgets.auth.example.com
spec:
  group: auth.example.com
  names:
    kind: Widget
    plural: widgets
    singular: widget
  scope: Namespaced
  versions:
    - name: v1
      served: true
      storage: true
      schema:
        openAPIV3Schema:
          type: object
          properties:
            apiVersion:
              type: string
            kind:
              type: string
            metadata:
              type: object
            spec:
              type: object
              properties:
                value:
                  type: string
`

func createGitTestRepository(t *testing.T, client *http.Client, baseURL, name string) {
	t.Helper()
	path := ""
	switch name {
	case "application.git":
		path = "application.git"
	case "private.git":
		path = "private.git"
	default:
		t.Fatalf("unsupported Git test repository %q", name)
	}
	// #nosec G704 -- The URL is restricted to the public HTTPS origin created for this CI run.
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, baseURL+"/admin/repositories/"+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+gitAuthAdminToken)
	// #nosec G704 -- The request targets the validated public HTTPS CI fixture origin.
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("create Git test repository: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create Git test repository returned %s", response.Status)
	}
}

func initializeGitTestRepository(t *testing.T, directory, remote, username, password string, files map[string]string) string {
	t.Helper()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	runGit(t, directory, username, password, "init", "--initial-branch=main")
	runGit(t, directory, username, password, "config", "user.name", "MCL E2E")
	runGit(t, directory, username, password, "config", "user.email", "mcl-e2e@example.invalid")
	for name, content := range files {
		path := filepath.Join(directory, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, directory, username, password, "add", ".")
	runGit(t, directory, username, password, "commit", "-m", "initial repository content")
	revision := runGit(t, directory, username, password, "rev-parse", "HEAD")
	runGit(t, directory, username, password, "remote", "add", "origin", remote)
	runGit(t, directory, username, password, "push", "origin", "HEAD:main")

	return revision
}

func writeGitProject(t *testing.T, repository, directory, source, revision, username, password, manifest string) {
	t.Helper()
	if !filepath.IsLocal(directory) || directory == "." {
		t.Fatalf("Git project path %q must be local", directory)
	}
	project := filepath.Join(repository, directory)
	if err := os.MkdirAll(project, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "main.mcl"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("[[crd_schemas]]\nsource = %q\nrevision = %q\ndirectory = \"schemas\"\nfiles = [\"widget.yaml\"]\n", "git+"+source, revision)
	// #nosec G703 -- directory is constrained to a local path before constructing the fixture path.
	if err := os.WriteFile(filepath.Join(project, "mcl.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	options := mcl.EvaluationOptions{
		GitCredentialProvider: func(_ context.Context, _ string) (mcl.GitCredentials, error) {
			return mcl.GitCredentials{Username: username, Password: password}, nil
		},
	}
	if err := mcl.UpdateFileWithParametersAndOptionsContext(t.Context(), filepath.Join(project, "main.mcl"), nil, options); err != nil {
		t.Fatalf("create locked Git dependency for %s: %v", directory, err)
	}
	runGit(t, repository, gitAuthApplicationUser, gitAuthApplicationPass, "add", ".")
	runGit(t, repository, gitAuthApplicationUser, gitAuthApplicationPass, "commit", "-m", "add "+directory+" project")
}

func widgetManifest(name string) string {
	return fmt.Sprintf(`let resource =
  apiVersion: "auth.example.com/v1"
  kind: "Widget"
  metadata:
    name: %q
  spec:
    value: "authenticated"
emit resource
`, name)
}

func createGitTestCRD(t *testing.T, s *suite) {
	t.Helper()
	crd := &unstructured.Unstructured{Object: map[string]any{
		apiVersionField: "apiextensions.k8s.io/v1",
		"kind":          "CustomResourceDefinition",
		metadataField:   map[string]any{"name": "widgets.auth.example.com"},
		"spec": map[string]any{
			"group": "auth.example.com",
			"names": map[string]any{
				"plural":   "widgets",
				"singular": "widget",
				"kind":     "Widget",
				"listKind": "WidgetList",
			},
			"scope": "Namespaced",
			"versions": []any{map[string]any{
				"name":    "v1",
				"served":  true,
				"storage": true,
				"schema": map[string]any{"openAPIV3Schema": map[string]any{
					"type": "object",
					"properties": map[string]any{
						apiVersionField: map[string]any{"type": "string"},
						"kind":          map[string]any{"type": "string"},
						metadataField:   map[string]any{"type": "object"},
						"spec": map[string]any{
							"type":       "object",
							"properties": map[string]any{"value": map[string]any{"type": "string"}},
						},
					},
				}},
			}},
		},
	}}
	if _, err := s.resource(crds, "").Create(t.Context(), crd, metav1.CreateOptions{}); err != nil {
		t.Fatalf("create Git authentication test CRD: %v", err)
	}
	t.Cleanup(func() { s.delete(crds, "", "widgets.auth.example.com") })
	s.poll("established Git authentication test CRD", func() (bool, error) {
		created, err := s.get(crds, "", "widgets.auth.example.com")
		if err != nil {
			return false, err
		}
		conditions, _, _ := unstructured.NestedSlice(created.Object, "status", "conditions")
		for _, rawCondition := range conditions {
			condition, ok := rawCondition.(map[string]any)
			if ok && condition["type"] == "Established" && condition["status"] == "True" {
				return true, nil
			}
		}

		return false, nil
	})
}

func runGit(t *testing.T, directory, username, password string, args ...string) string {
	t.Helper()
	credentials := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
	// #nosec G702 -- Git receives argument-vector values directly and never invokes a shell.
	command := exec.CommandContext(t.Context(), "git", args...)
	command.Dir = directory
	command.Env = append(os.Environ(),
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=http.extraHeader",
		"GIT_CONFIG_VALUE_0=Authorization: Basic "+credentials,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}

	return strings.TrimSpace(string(output))
}

func createGitRepositorySecret(t *testing.T, client dynamic.Interface, repoURL string) error {
	t.Helper()
	encoded := func(value string) string { return base64.StdEncoding.EncodeToString([]byte(value)) }
	secret := &unstructured.Unstructured{Object: map[string]any{
		apiVersionField: "v1",
		"kind":          "Secret",
		metadataField: map[string]any{
			"name":      "mcl-git-auth-e2e",
			"namespace": "argocd",
			"labels":    map[string]any{"argocd.argoproj.io/secret-type": "repository"},
		},
		"type": "Opaque",
		"data": map[string]any{
			"url":      encoded(repoURL),
			"username": encoded(gitAuthApplicationUser),
			"password": encoded(gitAuthApplicationPass),
			"type":     encoded("git"),
			"name":     encoded("mcl-git-auth-e2e"),
		},
	}}
	_, err := client.Resource(schema.GroupVersionResource{Version: "v1", Resource: "secrets"}).Namespace("argocd").Create(t.Context(), secret, metav1.CreateOptions{})

	return err
}
