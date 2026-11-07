package plugin

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type announcedParameter struct {
	Name   string            `json:"name"`
	String string            `json:"string,omitempty"`
	Array  []string          `json:"array,omitempty"`
	Map    map[string]string `json:"map,omitempty"`
}

type pluginOptions struct {
	allowEmpty    bool
	includeVerify bool
}

func readParameters(encoded, root string) (map[string]jsontext.Value, string, pluginOptions, error) {
	values := make(map[string]jsontext.Value)
	entry := "main.mcl"
	options := pluginOptions{}
	if encoded == "" {
		return values, entry, options, nil
	}
	var supplied []announcedParameter
	if err := json.Unmarshal([]byte(encoded), &supplied); err != nil {
		return nil, "", options, fmt.Errorf("decode Argo CD parameters: %w", err)
	}
	var parameterFiles []string
	seen := make(map[string]bool, len(supplied))
	for _, parameter := range supplied {
		if seen[parameter.Name] {
			return nil, "", options, fmt.Errorf("Argo CD parameter %q is supplied more than once", parameter.Name)
		}
		seen[parameter.Name] = true
		switch parameter.Name {
		case "entrypoint":
			if parameter.String == "" {
				return nil, "", options, fmt.Errorf("entrypoint must not be empty")
			}
			entry = parameter.String
		case "parameter-files":
			parameterFiles = append(parameterFiles, parameter.Array...)
		case "parameters":
			var object map[string]jsontext.Value
			if err := json.Unmarshal([]byte(parameter.String), &object); err != nil {
				return nil, "", options, fmt.Errorf("decode parameters JSON: %w", err)
			}
			if object == nil {
				return nil, "", options, fmt.Errorf("parameters must be a JSON object")
			}
			if err := mergeParameters(values, object); err != nil {
				return nil, "", options, err
			}
		case "allow-empty", "include-verify":
			value := strings.EqualFold(parameter.String, "true")
			if parameter.String != "" && !value && !strings.EqualFold(parameter.String, "false") {
				return nil, "", options, fmt.Errorf("parameter %q must be true or false", parameter.Name)
			}
			if parameter.Name == "allow-empty" {
				options.allowEmpty = value
			} else {
				options.includeVerify = value
			}
		default:
			return nil, "", options, fmt.Errorf("unsupported Argo CD parameter %q", parameter.Name)
		}
	}
	for _, name := range parameterFiles {
		path, err := projectFile(root, name)
		if err != nil {
			return nil, "", options, err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, "", options, fmt.Errorf("read parameter file %q: %w", name, err)
		}
		var object map[string]jsontext.Value
		if err := json.Unmarshal(data, &object); err != nil {
			return nil, "", options, fmt.Errorf("decode parameter file %q: %w", name, err)
		}
		if object == nil {
			return nil, "", options, fmt.Errorf("parameter file %q must contain a JSON object", name)
		}
		if err := mergeParameters(values, object); err != nil {
			return nil, "", options, err
		}
	}
	if _, err := projectFile(root, entry); err != nil {
		return nil, "", options, err
	}
	return values, entry, options, nil
}

func mergeParameters(dst, src map[string]jsontext.Value) error {
	for name, value := range src {
		if _, exists := dst[name]; exists {
			return fmt.Errorf("parameter %q is supplied more than once", name)
		}
		dst[name] = value
	}
	return nil
}

func projectFile(root, name string) (string, error) {
	if name == "" || filepath.IsAbs(name) {
		return "", fmt.Errorf("project path must be relative")
	}
	clean := filepath.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("project path %q escapes the source directory", name)
	}
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("resolve source directory: %w", err)
	}
	path, err := filepath.EvalSymlinks(filepath.Join(base, clean))
	if err != nil {
		return "", fmt.Errorf("resolve project path %q: %w", name, err)
	}
	rel, err := filepath.Rel(base, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("project path %q escapes the source directory", name)
	}
	return path, nil
}
