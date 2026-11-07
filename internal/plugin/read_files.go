//go:build !mcl_without_read_files

package plugin

import (
	"context"
	"encoding/json/jsontext"

	"github.com/manifest-configuration-language/mcl/pkg/mcl"
)

const readFilesAvailable = true

func evaluateWithReadFiles(ctx context.Context, entry, operation string, parameters map[string]jsontext.Value, options mcl.EvaluationOptions) (mcl.Bundle, []string, error) {
	return mcl.EvaluateFileForOperationWithParametersAndOptionsAndReadFilesContext(ctx, entry, operation, parameters, options)
}
