package plugin

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

type announcedParameter struct {
	Name   string             `json:"name"`
	String *string            `json:"string"`
	Array  *[]string          `json:"array"`
	Map    *map[string]string `json:"map"`
}

type pluginOptions struct {
	allowEmpty        bool
	includeVerify     bool
	kubernetesVersion string
	parameterFiles    []string
}

const maxParameterFileSize = 64 << 20

func readParameters(encoded, root string) (map[string]jsontext.Value, pluginOptions, error) {
	values := make(map[string]jsontext.Value)
	options := pluginOptions{}
	if strings.TrimSpace(encoded) == "" || strings.TrimSpace(encoded) == "null" {
		if _, err := projectFile(root, "main.mcl"); err != nil {
			return nil, options, err
		}

		return values, options, nil
	}
	var supplied []announcedParameter
	if err := json.Unmarshal([]byte(encoded), &supplied, json.RejectUnknownMembers(true)); err != nil {
		return nil, options, fmt.Errorf("decode Argo CD parameters: %w", err)
	}
	if supplied == nil {
		return nil, options, errors.New("plugin parameters must be a JSON array")
	}
	var parameterFiles []string
	seen := make(map[string]bool, len(supplied))
	for _, parameter := range supplied {
		if seen[parameter.Name] {
			return nil, options, fmt.Errorf("parameter %q is supplied more than once", parameter.Name)
		}
		seen[parameter.Name] = true
		switch parameter.Name {
		case "parameter-files":
			if err := requireParameterShape(parameter, "array"); err != nil {
				return nil, options, err
			}
			parameterFiles = append(parameterFiles, (*parameter.Array)...)
		case "parameters":
			if err := requireParameterShape(parameter, "string"); err != nil {
				return nil, options, err
			}
			if len(*parameter.String) > maxParameterFileSize {
				return nil, options, fmt.Errorf("parameters JSON exceeds %d bytes", maxParameterFileSize)
			}
			var object map[string]jsontext.Value
			if err := json.Unmarshal([]byte(*parameter.String), &object); err != nil {
				return nil, options, fmt.Errorf("decode parameters JSON: %w", err)
			}
			if object == nil {
				return nil, options, errors.New("parameters must be a JSON object")
			}
			if err := mergeParameters(values, object); err != nil {
				return nil, options, err
			}
		case "allow-empty", "include-verify":
			if err := requireParameterShape(parameter, "string"); err != nil {
				return nil, options, err
			}
			value := strings.EqualFold(*parameter.String, "true")
			if !value && !strings.EqualFold(*parameter.String, "false") {
				return nil, options, fmt.Errorf("parameter %q must be true or false", parameter.Name)
			}
			if parameter.Name == "allow-empty" {
				options.allowEmpty = value
			} else {
				options.includeVerify = value
			}
		case "kubernetes-version":
			if err := requireParameterShape(parameter, "string"); err != nil {
				return nil, options, err
			}
			if *parameter.String == "" {
				return nil, options, errors.New("kubernetes-version must not be empty")
			}
			options.kubernetesVersion = *parameter.String
		default:
			return nil, options, fmt.Errorf("unsupported Argo CD parameter %q", parameter.Name)
		}
	}
	for _, name := range parameterFiles {
		path, err := projectFile(root, name)
		if err != nil {
			return nil, options, err
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, options, fmt.Errorf("read parameter file %q: %w", name, err)
		}
		data, readErr := io.ReadAll(io.LimitReader(file, maxParameterFileSize+1))
		closeErr := file.Close()
		if readErr != nil {
			return nil, options, fmt.Errorf("read parameter file %q: %w", name, readErr)
		}
		if closeErr != nil {
			return nil, options, fmt.Errorf("close parameter file %q: %w", name, closeErr)
		}
		if len(data) > maxParameterFileSize {
			return nil, options, fmt.Errorf("parameter file %q exceeds %d bytes", name, maxParameterFileSize)
		}
		var object map[string]jsontext.Value
		if err := json.Unmarshal(data, &object); err != nil {
			return nil, options, fmt.Errorf("decode parameter file %q: %w", name, err)
		}
		if object == nil {
			return nil, options, fmt.Errorf("parameter file %q must contain a JSON object", name)
		}
		if err := mergeParameters(values, object); err != nil {
			return nil, options, err
		}
	}
	options.parameterFiles = slices.Clone(parameterFiles)
	if _, err := projectFile(root, "main.mcl"); err != nil {
		return nil, options, err
	}

	return values, options, nil
}

func requireParameterShape(parameter announcedParameter, shape string) error {
	present := 0
	if parameter.String != nil {
		present++
	}
	if parameter.Array != nil {
		present++
	}
	if parameter.Map != nil {
		present++
	}
	valid := (shape == "string" && parameter.String != nil) || (shape == "array" && parameter.Array != nil) || (shape == "map" && parameter.Map != nil)
	if present != 1 || !valid {
		return fmt.Errorf("parameter %q must use the %s field", parameter.Name, shape)
	}

	return nil
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
		return "", errors.New("project path must be relative")
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
