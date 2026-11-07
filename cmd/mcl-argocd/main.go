package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/manifest-configuration-language/argocd-plugin/internal/plugin"
)

func main() {
	if len(os.Args) != 2 || os.Args[1] != "generate" {
		fmt.Fprintln(os.Stderr, "usage: mcl-argocd generate")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := plugin.Generate(ctx, os.Getenv("ARGOCD_APP_PARAMETERS"), os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
