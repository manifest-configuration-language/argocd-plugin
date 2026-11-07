package plugin

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/manifest-configuration-language/mcl/pkg/mcl"
	"gopkg.in/yaml.v3"
)

type lifecycleManifest struct {
	Artifacts []lifecycleArtifact `json:"artifacts"`
}
type lifecycleArtifact struct {
	Events    []string            `json:"events"`
	Priority  int                 `json:"priority"`
	Cleanup   []string            `json:"cleanup"`
	Wait      bool                `json:"wait"`
	Documents []documentReference `json:"documents"`
}
type documentReference struct {
	File     string `json:"file"`
	Document int    `json:"document"`
}

type renderedResource struct {
	object   map[string]any
	identity string
	hooks    []string
	cleanup  []string
	wave     int
	waveSet  bool
	regular  bool
}

// Generate evaluates the selected MCL project and writes a complete Argo CD manifest stream.
func Generate(ctx context.Context, encodedParameters string, output io.Writer) error {
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get source directory: %w", err)
	}
	parameters, entrypoint, options, err := readParameters(encodedParameters, root)
	if err != nil {
		return err
	}
	entry, err := projectFile(root, entrypoint)
	if err != nil {
		return err
	}
	if err := mcl.FetchFileWithParametersAndOptionsContext(ctx, entry, parameters, mcl.EvaluationOptions{}); err != nil {
		return fmt.Errorf("fetch locked MCL dependencies: %w", err)
	}
	bundle, err := mcl.EvaluateFileWithParametersAndOptionsContext(ctx, entry, parameters, mcl.EvaluationOptions{})
	if err != nil {
		return fmt.Errorf("evaluate MCL project: %w", err)
	}
	resources := make(map[string]*renderedResource)
	for _, operation := range []string{"create", "update", "delete", "rollback", "verify"} {
		files, err := mcl.RenderOperation(bundle, operation)
		if err != nil {
			return fmt.Errorf("render MCL %s operation: %w", operation, err)
		}
		if err := collectOperation(resources, operation, files, options); err != nil {
			return err
		}
	}
	if len(resources) == 0 && !options.allowEmpty {
		return fmt.Errorf("MCL produced no Kubernetes resources")
	}
	keys := make([]string, 0, len(resources))
	for key := range resources {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var rendered bytes.Buffer
	for _, key := range keys {
		item := resources[key]
		if err := applyLifecycle(item); err != nil {
			return fmt.Errorf("resource %s: %w", key, err)
		}
		data, err := yaml.Marshal(item.object)
		if err != nil {
			return fmt.Errorf("encode resource %s: %w", key, err)
		}
		if rendered.Len() > 0 {
			rendered.WriteString("---\n")
		}
		rendered.Write(data)
	}
	if rendered.Len() == 0 {
		return fmt.Errorf("MCL produced an empty manifest stream")
	}
	_, err = output.Write(rendered.Bytes())
	return err
}

func collectOperation(resources map[string]*renderedResource, operation string, files []mcl.RenderedFile, options pluginOptions) error {
	contents := make(map[string][]map[string]any, len(files))
	var metadata lifecycleManifest
	for _, file := range files {
		if file.Name == "lifecycle.json" {
			if err := json.Unmarshal(file.Content, &metadata); err != nil {
				return fmt.Errorf("decode MCL lifecycle metadata: %w", err)
			}
			continue
		}
		documents, err := decodeDocuments(file.Content)
		if err != nil {
			return fmt.Errorf("decode MCL output %s: %w", file.Name, err)
		}
		contents[file.Name] = documents
	}
	for _, artifact := range metadata.Artifacts {
		for _, event := range artifact.Events {
			if strings.Contains(event, "rollback") {
				return fmt.Errorf("MCL lifecycle event %q has no Argo CD equivalent", event)
			}
		}
		for _, ref := range artifact.Documents {
			if operation == "delete" && ref.File == "main.yaml" {
				continue
			}
			if operation == "rollback" || (ref.File == "verify.yaml" && !options.includeVerify) {
				continue
			}
			documents, exists := contents[ref.File]
			if !exists || ref.Document < 1 || ref.Document > len(documents) {
				return fmt.Errorf("MCL lifecycle metadata references missing document %s#%d", ref.File, ref.Document)
			}
			object := documents[ref.Document-1]
			identity, err := resourceIdentity(object)
			if err != nil {
				return err
			}
			item := resources[identity]
			if item == nil {
				item = &renderedResource{object: object, identity: identity}
				resources[identity] = item
			} else if !equalYAML(item.object, object) {
				return fmt.Errorf("MCL create/update operations render conflicting contents for %s", identity)
			}
			if err := assignLifecycle(item, ref.File, artifact); err != nil {
				return fmt.Errorf("resource %s: %w", identity, err)
			}
		}
	}
	return nil
}

func decodeDocuments(data []byte) ([]map[string]any, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var documents []map[string]any
	for {
		var document map[string]any
		err := decoder.Decode(&document)
		if err == io.EOF {
			return documents, nil
		}
		if err != nil {
			return nil, err
		}
		if len(document) > 0 {
			documents = append(documents, document)
		}
	}
}

func resourceIdentity(object map[string]any) (string, error) {
	apiVersion, _ := object["apiVersion"].(string)
	kind, _ := object["kind"].(string)
	metadata, _ := object["metadata"].(map[string]any)
	name, _ := metadata["name"].(string)
	namespace, _ := metadata["namespace"].(string)
	if apiVersion == "" || kind == "" || name == "" {
		return "", fmt.Errorf("MCL output contains a resource without apiVersion, kind, or metadata.name")
	}
	return strings.Join([]string{apiVersion, kind, namespace, name}, "/"), nil
}

func assignLifecycle(item *renderedResource, file string, artifact lifecycleArtifact) error {
	eventByFile := map[string]string{"bootstrap.yaml": "bootstrap", "before.yaml": "before", "main.yaml": "main", "after.yaml": "after", "verify.yaml": "verify"}
	phase := eventByFile[file]
	for _, event := range artifact.Events {
		if phase == "main" && event == "main" {
			item.regular = true
		}
		if phase == "bootstrap" && event == "bootstrap" {
			item.regular = true
			wave := -1000 + artifact.Priority
			if !item.waveSet || wave < item.wave {
				item.wave = wave
				item.waveSet = true
			}
		}
		var hook string
		switch {
		case phase == "before" && (event == "before.create" || event == "before.update"):
			hook = "PreSync"
		case phase == "after" && (event == "after.create" || event == "after.update"):
			hook = "PostSync"
		case phase == "before" && event == "before.delete":
			hook = "PreDelete"
		case phase == "after" && event == "after.delete":
			hook = "PostDelete"
		case phase == "verify" && event == "verify":
			hook = "PostSync"
		}
		if hook != "" && !slices.Contains(item.hooks, hook) {
			if item.waveSet && item.wave != artifact.Priority {
				return fmt.Errorf("MCL lifecycle events assign conflicting sync waves %d and %d", item.wave, artifact.Priority)
			}
			item.hooks = append(item.hooks, hook)
			item.wave = artifact.Priority
			item.waveSet = true
		}
	}
	if phase == "before" || phase == "after" || phase == "verify" {
		item.cleanup = append(item.cleanup, artifact.Cleanup...)
	}
	return nil
}

func applyLifecycle(item *renderedResource) error {
	metadata, _ := item.object["metadata"].(map[string]any)
	if metadata == nil {
		metadata = make(map[string]any)
		item.object["metadata"] = metadata
	}
	annotations, _ := metadata["annotations"].(map[string]any)
	if annotations == nil {
		annotations = make(map[string]any)
		metadata["annotations"] = annotations
	}
	if len(item.hooks) > 0 {
		slices.Sort(item.hooks)
		if err := setAnnotation(annotations, "argocd.argoproj.io/hook", strings.Join(item.hooks, ",")); err != nil {
			return err
		}
	}
	if item.regular && len(item.hooks) > 0 {
		return fmt.Errorf("resource is both a regular resource and an Argo CD hook")
	}
	if item.regular || len(item.hooks) == 0 || item.wave != 0 {
		if err := setAnnotation(annotations, "argocd.argoproj.io/sync-wave", fmt.Sprint(item.wave)); err != nil {
			return err
		}
	}
	var policies []string
	for _, cleanup := range item.cleanup {
		policy := map[string]string{"before-next-run": "BeforeHookCreation", "on-success": "HookSucceeded", "on-failure": "HookFailed"}[cleanup]
		if policy == "" {
			return fmt.Errorf("unsupported MCL cleanup policy %q", cleanup)
		}
		if !slices.Contains(policies, policy) {
			policies = append(policies, policy)
		}
	}
	if len(policies) > 0 {
		slices.Sort(policies)
		if err := setAnnotation(annotations, "argocd.argoproj.io/hook-delete-policy", strings.Join(policies, ",")); err != nil {
			return err
		}
	}
	if len(annotations) == 0 {
		delete(metadata, "annotations")
	}
	return nil
}

func setAnnotation(annotations map[string]any, key, value string) error {
	if raw, exists := annotations[key]; exists {
		existing, ok := raw.(string)
		if !ok || existing != value {
			return fmt.Errorf("annotation %s conflicts with MCL lifecycle value %q", key, value)
		}
	}
	annotations[key] = value
	return nil
}

func equalYAML(left, right map[string]any) bool {
	a, errA := yaml.Marshal(left)
	b, errB := yaml.Marshal(right)
	return errA == nil && errB == nil && bytes.Equal(a, b)
}
