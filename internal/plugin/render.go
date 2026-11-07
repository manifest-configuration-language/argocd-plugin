package plugin

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/manifest-configuration-language/mcl/pkg/mcl"
	"gopkg.in/yaml.v3"
)

type renderedResource struct {
	object   map[string]any
	identity string
	hooks    []string
	cleanup  []string
	wave     int
	waveSet  bool
	regular  bool
}

type operationSpec struct {
	name   string
	phases []string
}

const verifyPhase = "verify"

// Generate evaluates the selected MCL project and writes a complete Argo CD manifest stream.
func Generate(ctx context.Context, encodedParameters string, output io.Writer) error {
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get source directory: %w", err)
	}

	return generateFrom(ctx, root, encodedParameters, output)
}

func generateFrom(ctx context.Context, root, encodedParameters string, output io.Writer) error {
	parameters, entrypoint, options, err := readParameters(encodedParameters, root)
	if err != nil {
		return err
	}
	entry, err := projectFile(root, entrypoint)
	if err != nil {
		return err
	}
	evaluationOptions := mcl.EvaluationOptions{KubernetesVersion: options.kubernetesVersion}
	configureGitCredentialProvider(&evaluationOptions)
	if err := mcl.FetchFileWithParametersAndOptionsContext(ctx, entry, parameters, evaluationOptions); err != nil {
		return fmt.Errorf("fetch locked MCL dependencies: %w", err)
	}
	resources := make(map[string]*renderedResource)
	operations := operationsFor(options)
	for _, operation := range operations {
		bundle, err := mcl.EvaluateFileForOperationWithParametersAndOptionsContext(ctx, entry, operation.name, parameters, evaluationOptions)
		if err != nil {
			return fmt.Errorf("evaluate MCL %s operation: %w", operation.name, err)
		}
		for _, phase := range operation.phases {
			artifacts, err := mcl.RenderPhaseArtifacts(bundle, phase)
			if err != nil {
				return fmt.Errorf("render MCL %s phase: %w", phase, err)
			}
			for _, artifact := range artifacts {
				if strings.Contains(phase, "rollback") {
					return fmt.Errorf("MCL lifecycle event %q has no Argo CD equivalent", phase)
				}
				if operation.name == "delete" && phase == "main" {
					continue
				}
				object, err := decodeResource(artifact.Resource)
				if err != nil {
					return fmt.Errorf("decode MCL resource in phase %q: %w", phase, err)
				}
				identity, err := resourceIdentity(object)
				if err != nil {
					return err
				}
				item := resources[identity]
				if item == nil {
					item = &renderedResource{object: object, identity: identity}
					resources[identity] = item
				} else if !equalYAML(item.object, object) {
					return fmt.Errorf("MCL operations render conflicting contents for %s", identity)
				}
				if err := assignLifecycle(item, phase, artifact.Lifecycle); err != nil {
					return fmt.Errorf("resource %s: %w", identity, err)
				}
			}
		}
	}
	if len(resources) == 0 && !options.allowEmpty {
		return errors.New("MCL produced no Kubernetes resources")
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
	if rendered.Len() == 0 && !options.allowEmpty {
		return errors.New("MCL produced an empty manifest stream")
	}
	_, err = output.Write(rendered.Bytes())

	return err
}

func operationsFor(options pluginOptions) []operationSpec {
	operations := []operationSpec{{"create", []string{"bootstrap", "before.create", "main", "after.create"}}, {"update", []string{"before.update", "main", "after.update"}}, {"delete", []string{"before.delete", "after.delete"}}, {"rollback", []string{"before.rollback", "after.rollback"}}}
	if options.includeVerify {
		operations = append(operations, operationSpec{verifyPhase, []string{verifyPhase}})
	}

	return operations
}

// AnnounceInputFiles writes dynamic Argo CD parameters containing repository-relative MCL inputs.
func AnnounceInputFiles(ctx context.Context, encodedParameters, sourcePath string, output io.Writer) error {
	root, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("get source directory: %w", err)
	}

	return announceInputFilesFrom(ctx, root, encodedParameters, sourcePath, output)
}

func announceInputFilesFrom(ctx context.Context, root, encodedParameters, sourcePath string, output io.Writer) error {
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve source directory: %w", err)
	}
	root = canonicalRoot
	parameters, entrypoint, options, err := readParameters(encodedParameters, root)
	if err != nil {
		return err
	}
	entry, err := projectFile(root, entrypoint)
	if err != nil {
		return err
	}
	evaluationOptions := mcl.EvaluationOptions{KubernetesVersion: options.kubernetesVersion}
	configureGitCredentialProvider(&evaluationOptions)
	if err := mcl.FetchFileWithParametersAndOptionsContext(ctx, entry, parameters, evaluationOptions); err != nil {
		return fmt.Errorf("fetch locked MCL dependencies: %w", err)
	}
	projectRoot := findProjectRoot(filepath.Dir(entry))
	projectRootFromSource, err := filepath.Rel(root, projectRoot)
	if err != nil {
		return fmt.Errorf("resolve MCL project root: %w", err)
	}
	repoSourcePath := path.Clean(filepath.ToSlash(sourcePath))
	if path.IsAbs(repoSourcePath) || repoSourcePath == ".." || strings.HasPrefix(repoSourcePath, "../") {
		return errors.New("plugin source path must be repository-relative")
	}
	inputs := make(map[string]struct{})
	inputs[filepath.ToSlash(entrypoint)] = struct{}{}
	for _, parameterFile := range options.parameterFiles {
		inputs[filepath.ToSlash(parameterFile)] = struct{}{}
	}
	for _, operation := range operationsFor(options) {
		_, readFiles, err := evaluateWithReadFiles(ctx, entry, operation.name, parameters, evaluationOptions)
		if err != nil {
			return fmt.Errorf("evaluate MCL %s operation: %w", operation.name, err)
		}
		for _, readFile := range readFiles {
			inputFromSource := path.Clean(path.Join(filepath.ToSlash(projectRootFromSource), filepath.ToSlash(readFile)))
			repoPath := path.Clean(path.Join(repoSourcePath, inputFromSource))
			if repoPath == ".." || strings.HasPrefix(repoPath, "../") || path.IsAbs(repoPath) {
				return fmt.Errorf("MCL input path %q escapes the Git repository", readFile)
			}
			inputs[inputFromSource] = struct{}{}
		}
	}
	paths := make([]string, 0, len(inputs))
	for input := range inputs {
		paths = append(paths, input)
	}
	slices.Sort(paths)
	encoded, err := json.Marshal([]map[string]any{{"name": "input-files", "title": "MCL input files", "array": paths, "collectionType": "array"}})
	if err != nil {
		return fmt.Errorf("encode dynamic parameter announcement: %w", err)
	}
	_, err = output.Write(encoded)

	return err
}

func findProjectRoot(directory string) string {
	for current := filepath.Clean(directory); ; current = filepath.Dir(current) {
		info, err := os.Lstat(filepath.Join(current, "mcl.toml"))
		if err == nil && info.Mode().IsRegular() {
			return current
		}
		parent := filepath.Dir(current)
		if parent == current {
			return directory
		}
	}
}

func decodeResource(data []byte) (map[string]any, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, err
	}
	var extra map[string]any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("MCL artifact contains multiple JSON values")
	}

	return object, nil
}

func resourceIdentity(object map[string]any) (string, error) {
	apiVersion, _ := object["apiVersion"].(string)
	kind, _ := object["kind"].(string)
	metadata, _ := object["metadata"].(map[string]any)
	name, _ := metadata["name"].(string)
	namespace, _ := metadata["namespace"].(string)
	if apiVersion == "" || kind == "" || name == "" {
		return "", errors.New("MCL output contains a resource without apiVersion, kind, or metadata.name")
	}

	return strings.Join([]string{apiVersion, kind, namespace, name}, "/"), nil
}

func assignLifecycle(item *renderedResource, phase string, lifecycle mcl.Lifecycle) error {
	for _, event := range lifecycle.Events {
		if phase == "main" && event == "main" {
			item.regular = true
		}
		if phase == "bootstrap" && event == "bootstrap" {
			item.regular = true
			wave := -1000 + lifecycle.Priority
			if item.waveSet && wave != item.wave {
				return fmt.Errorf("MCL lifecycle events assign conflicting sync waves %d and %d", item.wave, wave)
			}
			if !item.waveSet || wave < item.wave {
				item.wave = wave
				item.waveSet = true
			}
		}
		var hook string
		switch {
		case (phase == "before.create" || phase == "before.update") && (event == "before.create" || event == "before.update"):
			hook = "PreSync"
		case (phase == "after.create" || phase == "after.update") && (event == "after.create" || event == "after.update"):
			hook = "PostSync"
		case phase == "before.delete" && event == "before.delete":
			hook = "PreDelete"
		case phase == "after.delete" && event == "after.delete":
			hook = "PostDelete"
		case phase == verifyPhase && event == verifyPhase:
			hook = "PostSync"
		}
		if hook != "" && !slices.Contains(item.hooks, hook) {
			if item.waveSet && item.wave != lifecycle.Priority {
				return fmt.Errorf("MCL lifecycle events assign conflicting sync waves %d and %d", item.wave, lifecycle.Priority)
			}
			item.hooks = append(item.hooks, hook)
			item.wave = lifecycle.Priority
			item.waveSet = true
		}
	}
	if strings.HasPrefix(phase, "before.") || strings.HasPrefix(phase, "after.") || phase == verifyPhase {
		item.cleanup = append(item.cleanup, lifecycle.Cleanup...)
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
		return errors.New("resource is both a regular resource and an Argo CD hook")
	}
	if item.waveSet {
		if err := setAnnotation(annotations, "argocd.argoproj.io/sync-wave", strconv.Itoa(item.wave)); err != nil {
			return err
		}
	}
	if len(item.cleanup) > 0 && len(item.hooks) == 0 {
		return errors.New("MCL cleanup policy is set on a resource without an Argo CD Hook")
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
		if !ok || !equivalentAnnotationValue(key, existing, value) {
			return fmt.Errorf("annotation %s conflicts with MCL lifecycle value %q", key, value)
		}
	}
	annotations[key] = value

	return nil
}

func equivalentAnnotationValue(key, existing, expected string) bool {
	if key != "argocd.argoproj.io/hook" && key != "argocd.argoproj.io/hook-delete-policy" {
		return existing == expected
	}
	values := func(value string) []string {
		items := strings.Split(value, ",")
		for index := range items {
			items[index] = strings.TrimSpace(items[index])
		}
		slices.Sort(items)

		return slices.Compact(items)
	}

	return slices.Equal(values(existing), values(expected))
}

func equalYAML(left, right map[string]any) bool {
	a, errA := yaml.Marshal(left)
	b, errB := yaml.Marshal(right)

	return errA == nil && errB == nil && bytes.Equal(a, b)
}
