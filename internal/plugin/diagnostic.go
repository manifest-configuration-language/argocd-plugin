package plugin

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/manifest-configuration-language/mcl/pkg/mcl"
)

// FormatError returns a source-located diagnostic suitable for Argo CD's manifest-generation error.
func FormatError(err error) string {
	root, rootErr := os.Getwd()
	if rootErr != nil {
		return err.Error()
	}

	return displayError(root, err)
}

func displayError(root string, err error) string {
	diagnostic, ok := mcl.SourceDiagnosticFromError(err)
	if !ok {
		return err.Error()
	}

	path, sourcePath := diagnosticPath(root, diagnostic.Path)
	location := path
	if line, column, ok := sourcePosition(root, sourcePath, diagnostic.Span.Start); ok {
		location = fmt.Sprintf("%s:%d:%d", path, line, column)
	}
	if location != "" {
		location += ": "
	}
	if diagnostic.Code != "" {
		location += "[" + diagnostic.Code + "] "
	}

	message := diagnostic.DisplayMessage()
	var related []string
	for _, item := range diagnostic.Related {
		relatedPath, relatedSourcePath := diagnosticPath(root, item.Path)
		if line, column, ok := sourcePosition(root, relatedSourcePath, item.Span.Start); ok {
			relatedPath = fmt.Sprintf("%s:%d:%d", relatedPath, line, column)
		}
		if item.Message != "" {
			related = append(related, fmt.Sprintf("  related %s: %s", relatedPath, item.Message))
		}
	}
	if len(related) > 0 {
		message += "\n" + strings.Join(related, "\n")
	}

	return location + message
}

func diagnosticPath(root, diagnosticPath string) (string, string) {
	if diagnosticPath == "" {
		return "", ""
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return filepath.Base(diagnosticPath), ""
	}
	if filepath.IsAbs(diagnosticPath) {
		relative, err := filepath.Rel(root, diagnosticPath)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return filepath.Base(diagnosticPath), ""
		}
		diagnosticPath = relative
	}
	clean := filepath.Clean(diagnosticPath)
	if clean == ".." || filepath.IsAbs(clean) || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return filepath.Base(diagnosticPath), ""
	}

	return filepath.ToSlash(clean), clean
}

func sourcePosition(root, sourcePath string, offset int) (int, int, bool) {
	if sourcePath == "" || offset < 0 || offset > maxParameterFileSize {
		return 0, 0, false
	}
	path, err := projectFile(root, sourcePath)
	if err != nil {
		return 0, 0, false
	}
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, false
	}
	defer file.Close()
	prefix, err := io.ReadAll(io.LimitReader(file, int64(offset)))
	if err != nil || len(prefix) != offset {
		return 0, 0, false
	}
	line := bytes.Count(prefix, []byte{'\n'}) + 1
	lastNewline := bytes.LastIndexByte(prefix, '\n')
	column := utf8.RuneCount(prefix[lastNewline+1:]) + 1

	return line, column, true
}
