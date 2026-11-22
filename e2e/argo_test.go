package e2e

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

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
)

const appName = "mcl-plugin-e2e"

type suite struct {
	t              *testing.T
	client         dynamic.Interface
	repo, revision string
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
	s := &suite{t, c, repo, rev}
	t.Run("bootstrap", s.bootstrap)
	t.Run("multi-source", s.multiSource)
	t.Run("update-prune", s.updatePrune)
	t.Run("selective-sync", s.selective)
	t.Run("delete-hooks", s.deleteHooks)
	t.Run("generated-name-hooks", s.generatedHooks)
	t.Run("failure-hook", s.failureHook)
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
	s.t.Helper()
	var last error
	err := wait.PollUntilContextTimeout(s.t.Context(), 2*time.Second, 5*time.Minute, true, func(context.Context) (bool, error) {
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
	s.t.Helper()
	err := s.resource(g, ns).Delete(s.t.Context(), name, metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		s.t.Errorf("delete %s %s: %v", g.Resource, name, err)
	}
}

func (s *suite) cleanup() {
	s.delete(apps, "argocd", appName)
	s.exists(apps, "argocd", appName, false)
}

func (s *suite) create(t *testing.T, path string, auto, finalizer bool, other ...string) *suite {
	t.Helper()
	x := *s
	x.t = t
	source := func(p string, plugin bool) map[string]any {
		m := map[string]any{"repoURL": "https://github.com/" + s.repo + ".git", "targetRevision": s.revision, "path": p}
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
	obj := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "argoproj.io/v1alpha1", "kind": "Application", "metadata": map[string]any{"name": appName, "namespace": "argocd"}, "spec": spec}}
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
	old, _ := s.get(apps, "argocd", appName)
	previous, _, _ := unstructured.NestedString(old.Object, "status", "operationState", "finishedAt")
	extra := ""
	if path != "" {
		extra = fmt.Sprintf(`"spec":{"source":{"path":%q}},`, path)
	}
	if resources != "" {
		resources = `,"resources":` + resources
	}
	s.patch(fmt.Sprintf(`{%s"operation":{"sync":{"revision":%q%s}}}`, extra, s.revision, resources))
	var result *unstructured.Unstructured
	s.poll("sync operation "+phase, func() (bool, error) {
		o, e := s.get(apps, "argocd", appName)
		if e != nil {
			return false, e
		}
		finished, _, _ := unstructured.NestedString(o.Object, "status", "operationState", "finishedAt")
		p, _, _ := unstructured.NestedString(o.Object, "status", "operationState", "phase")
		if finished != "" && finished != previous {
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
