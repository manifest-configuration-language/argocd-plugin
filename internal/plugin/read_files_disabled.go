//go:build mcl_without_read_files

package plugin

import (
	"context"
	"encoding/json/jsontext"

	"github.com/manifest-configuration-language/mcl/pkg/mcl"
)

const readFilesAvailable = false

func evaluateWithReadFiles(ctx context.Context, entry, operation string, parameters map[string]jsontext.Value, options mcl.EvaluationOptions) (mcl.Bundle, []string, error) {
	bundle, err := mcl.EvaluateFileForOperationWithParametersAndOptionsContext(ctx, entry, operation, parameters, options)

	return bundle, nil, err
}
