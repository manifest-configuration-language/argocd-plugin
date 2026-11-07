package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/manifest-configuration-language/argocd-plugin/internal/plugin"
)

func main() {
	if len(os.Args) != 2 || os.Args[1] != "generate" && os.Args[1] != "parameters" {
		fmt.Fprintln(os.Stderr, "usage: mcl-argocd generate|parameters")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	var err error
	if os.Args[1] == "generate" {
		err = plugin.Generate(ctx, os.Getenv("ARGOCD_APP_PARAMETERS"), os.Stdout)
	} else {
		err = plugin.AnnounceInputFiles(ctx, os.Getenv("ARGOCD_APP_PARAMETERS"), os.Getenv("ARGOCD_APP_SOURCE_PATH"), os.Stdout)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, plugin.FormatError(err))
		os.Exit(1)
	}
}
