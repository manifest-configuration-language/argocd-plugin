package plugin

import (
	"strings"
	"testing"
)

func TestFormatErrorIncludesRepositoryRelativeSourceLocation(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeProjectFile(t, root, "main.mcl", `let greeting = "雪"
let message: String = 1
emit message
`)
	err := generateFrom(t.Context(), root, "[]", &strings.Builder{})
	if err == nil {
		t.Fatal("generateFrom() succeeded with an invalid type")
	}
	formatted := displayError(root, err)
	if !strings.Contains(formatted, "main.mcl:2:") {
		t.Fatalf("formatted diagnostic has no source position: %s", formatted)
	}
	if strings.Contains(formatted, root) {
		t.Fatalf("formatted diagnostic exposes the absolute source directory: %s", formatted)
	}
}
