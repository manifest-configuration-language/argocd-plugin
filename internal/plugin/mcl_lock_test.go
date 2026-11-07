package plugin

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/manifest-configuration-language/mcl/pkg/mcl"
)

func TestGenerateLoadsMCLCLIGeneratedLock(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	source, err := os.ReadFile(filepath.Join("testdata", "mcl-cli-lock", "main.mcl"))
	if err != nil {
		t.Fatalf("read CLI-generated MCL fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "main.mcl"), source, 0o600); err != nil { // #nosec G703 -- root is an isolated t.TempDir directory.
		t.Fatalf("write CLI-generated MCL fixture: %v", err)
	}
	lock, err := os.ReadFile(filepath.Join("testdata", "mcl-cli-lock", "mcl.lock"))
	if err != nil {
		t.Fatalf("read CLI-generated lock fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "mcl.lock"), lock, 0o600); err != nil { // #nosec G703 -- root is an isolated t.TempDir directory.
		t.Fatalf("write CLI-generated lock fixture: %v", err)
	}
	var output bytes.Buffer
	if err := generateFrom(t.Context(), root, "[]", &output); err != nil {
		t.Fatalf("generateFrom() rejected the MCL CLI-generated lock: %v", err)
	}
	if !strings.Contains(output.String(), "name: cli-lock-fixture") {
		t.Fatalf("generateFrom() output = %s, want the CLI-locked ConfigMap", output.String())
	}
}

func TestGenerateUsesMCLToolchainLockAndSharedCache(t *testing.T) {
	root := t.TempDir()
	lockPath := filepath.Join(root, "mcl.lock")
	entry := filepath.Join(root, "main.mcl")
	source := `let resource =
  apiVersion: "v1"
  kind: "ConfigMap"
  metadata:
    name: "sample"
emit resource
`
	if err := os.WriteFile(entry, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(t.TempDir(), "mcl-cache")
	options := mcl.EvaluationOptions{CacheDirectory: cache}
	if err := mcl.UpdateFileWithParametersAndOptionsContext(t.Context(), entry, nil, options); err != nil {
		t.Fatalf("MCL SDK lock update error = %v", err)
	}
	lock, err := os.ReadFile(lockPath)
	if err != nil {
		t.Fatalf("read MCL SDK lock: %v", err)
	}
	if !bytes.Contains(lock, []byte(`"mcl": "`+mcl.ToolchainVersion+`"`)) {
		t.Fatalf("MCL SDK lock does not record the plugin SDK version %q: %s", mcl.ToolchainVersion, lock)
	}

	t.Setenv("MCL_CACHE_DIRECTORY", cache)
	var output bytes.Buffer
	if err := generateFrom(t.Context(), root, "[]", &output); err != nil {
		t.Fatalf("generateFrom() rejected a compatible MCL SDK lock: %v", err)
	}
	if !strings.Contains(output.String(), "name: sample") {
		t.Fatalf("generateFrom() output = %s, want the locked ConfigMap", output.String())
	}

	wrongVersion := []byte(`"mcl": "` + mcl.ToolchainVersion + `"`)
	if !bytes.Contains(lock, wrongVersion) {
		t.Fatalf("MCL lock does not contain toolchain version %q", mcl.ToolchainVersion)
	}
	lock = bytes.Replace(lock, wrongVersion, []byte(`"mcl": "v0.0.0"`), 1)
	if err := os.WriteFile(lockPath, lock, 0o600); err != nil { // #nosec G703 -- lockPath uses a fixed filename inside t.TempDir.
		t.Fatal(err)
	}
	if err := generateFrom(t.Context(), root, "[]", &bytes.Buffer{}); err == nil || !strings.Contains(err.Error(), "dependency lock toolchain does not match") {
		t.Fatalf("generateFrom() error = %v, want a clear incompatible toolchain diagnostic", err)
	}
}
