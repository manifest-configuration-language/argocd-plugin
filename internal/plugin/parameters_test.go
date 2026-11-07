package plugin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestReadParametersMergesJSONInputsAndRejectsDuplicateKeys(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.mcl"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "params.json"), []byte(`{"replicas":3}`), 0o600); err != nil {
		t.Fatal(err)
	}
	values, entry, _, err := readParameters(`[ {"name":"parameter-files","array":["params.json"]}, {"name":"parameters","string":"{\"region\":\"west\"}"} ]`, root)
	if err != nil {
		t.Fatal(err)
	}
	if entry != "main.mcl" || string(values["replicas"]) != "3" || string(values["region"]) != `"west"` {
		t.Fatalf("unexpected decoded parameters: entry=%q values=%v", entry, values)
	}
	_, _, _, err = readParameters(`[ {"name":"parameters","string":"{\"replicas\":2}"}, {"name":"parameter-files","array":["params.json"]} ]`, root)
	if err == nil {
		t.Fatal("readParameters() accepted a duplicate parameter key")
	}
}

func TestProjectFileRejectsTraversalAndSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	if _, err := projectFile(root, "../outside"); err == nil {
		t.Fatal("projectFile() accepted path traversal")
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.json"), filepath.Join(root, "linked.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := projectFile(root, "linked.json"); err == nil {
		t.Fatal("projectFile() accepted a symlink outside the project")
	}
}

func TestReadParametersRejectsMalformedArgoParameterShapes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.mcl"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{
		`[null]`,
		`[{"name":"parameter-files","string":"params.json"}]`,
		`[{"name":"allow-empty","array":["true"]}]`,
		`[{"name":"entrypoint","string":"main.mcl","extra":true}]`,
	} {
		t.Run(input, func(t *testing.T) {
			if _, _, _, err := readParameters(input, root); err == nil {
				t.Fatalf("readParameters() accepted malformed input %s", input)
			}
		})
	}
}

func TestReadParametersAcceptsNoParameters(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.mcl"), []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, input := range []string{"", "null", "  null  "} {
		values, entry, options, err := readParameters(input, root)
		if err != nil {
			t.Fatalf("readParameters(%q) error = %v", input, err)
		}
		if len(values) != 0 || entry != "main.mcl" || options.allowEmpty || options.includeVerify || options.kubernetesVersion != "" || len(options.parameterFiles) != 0 {
			t.Fatalf("readParameters(%q) = %v, %q, %+v; want defaults", input, values, entry, options)
		}
	}
}
