package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type server struct {
	root  string
	admin string
	users map[string]string
}

func main() {
	s := server{
		root:  "/repositories",
		admin: required("GIT_TEST_ADMIN_TOKEN"),
		users: map[string]string{
			"application": required("GIT_TEST_APPLICATION_PASSWORD"),
			"private":     required("GIT_TEST_PRIVATE_PASSWORD"),
		},
	}
	httpServer := &http.Server{
		Addr:              ":8443",
		Handler:           s,
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := httpServer.ListenAndServeTLS("/tls/tls.crt", "/tls/tls.key"); err != nil && !errors.Is(err, http.ErrServerClosed) {
		fmt.Fprintln(os.Stderr, "git test server stopped")
		os.Exit(1)
	}
}

func required(name string) string {
	value := os.Getenv(name)
	if value == "" {
		fmt.Fprintf(os.Stderr, "required environment variable %s is empty\n", name)
		os.Exit(1)
	}

	return value
}

func (s server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/healthz" {
		w.WriteHeader(http.StatusOK)

		return
	}
	if strings.HasPrefix(r.URL.Path, "/admin/") {
		s.adminRequest(w, r)

		return
	}

	repository, ok := gitRepository(r.URL.Path)
	if !ok {
		http.NotFound(w, r)

		return
	}
	username, password, ok := r.BasicAuth()
	if !ok || s.users[repository] != password || username != repository {
		w.Header().Set("WWW-Authenticate", `Basic realm="MCL Git E2E"`)
		http.Error(w, "authentication required", http.StatusUnauthorized)

		return
	}
	s.serveGit(w, r)
}

func (s server) adminRequest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+s.admin {
		http.Error(w, "forbidden", http.StatusForbidden)

		return
	}
	repository := strings.TrimPrefix(r.URL.Path, "/admin/repositories/")
	var path string
	switch repository {
	case "application.git":
		path = filepath.Join(s.root, "application.git")
	case "private.git":
		path = filepath.Join(s.root, "private.git")
	default:
		http.NotFound(w, r)

		return
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		http.Error(w, "repository initialization failed", http.StatusInternalServerError)

		return
	}
	if _, err := os.Stat(path); err == nil {
		http.Error(w, "repository already exists", http.StatusConflict)

		return
	} else if !errors.Is(err, os.ErrNotExist) {
		http.Error(w, "repository initialization failed", http.StatusInternalServerError)

		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	// #nosec G702 -- The executable and arguments are fixed; the repository path is selected from a closed allowlist.
	command := exec.CommandContext(ctx, "git", "init", "--bare", "--initial-branch=main", path)
	if err := command.Run(); err != nil {
		http.Error(w, "repository initialization failed", http.StatusInternalServerError)

		return
	}
	for _, setting := range [][2]string{
		{"http.uploadpack", "true"},
		{"http.receivepack", "true"},
		{"uploadpack.allowReachableSHA1InWant", "true"},
	} {
		command = exec.CommandContext(ctx, "git", "-C", path, "config", setting[0], setting[1])
		if err := command.Run(); err != nil {
			http.Error(w, "repository initialization failed", http.StatusInternalServerError)

			return
		}
	}
	w.WriteHeader(http.StatusCreated)
}

func (s server) serveGit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)

		return
	}
	repository, operation, ok := gitOperation(r.URL.Path, r.Method, r.URL.Query().Get("service"))
	if !ok {
		http.NotFound(w, r)

		return
	}
	repositoryPath := filepath.Join(s.root, repository+".git")
	args := []string{operation, "--stateless-rpc"}
	advertisement := r.Method == http.MethodGet
	if advertisement {
		args = append(args, "--advertise-refs")
	}
	args = append(args, repositoryPath)
	// #nosec G702 -- The executable and operation are allowlisted; the repository path comes from a closed map.
	command := exec.CommandContext(r.Context(), "git", args...)
	command.Env = append(os.Environ(), "GIT_PROTOCOL="+r.Header.Get("Git-Protocol"))
	if r.Method == http.MethodPost {
		command.Stdin = r.Body
	}
	var output bytes.Buffer
	command.Stdout = &output
	if err := command.Run(); err != nil {
		http.Error(w, "Git request failed", http.StatusBadGateway)

		return
	}
	if advertisement {
		var body bytes.Buffer
		service := "git-" + operation
		fmt.Fprintf(&body, "%04x# service=%s\n0000", 4+len("# service=")+len(service)+1, service)
		body.Write(output.Bytes())
		output = body
	}
	contentType := "application/x-git-" + operation + "-result"
	if advertisement {
		contentType = "application/x-git-" + operation + "-advertisement"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(output.Bytes())
}

func gitOperation(path, method, service string) (string, string, bool) {
	repositoryPath, endpoint, ok := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if !ok || !strings.HasSuffix(repositoryPath, ".git") {
		return "", "", false
	}
	repository := strings.TrimSuffix(repositoryPath, ".git")
	if repository != "application" && repository != "private" {
		return "", "", false
	}
	if method == http.MethodGet && endpoint == "info/refs" {
		if service == "git-upload-pack" {
			return repository, "upload-pack", true
		}
		if service == "git-receive-pack" {
			return repository, "receive-pack", true
		}
	}
	if method == http.MethodPost && endpoint == "git-upload-pack" {
		return repository, "upload-pack", true
	}
	if method == http.MethodPost && endpoint == "git-receive-pack" {
		return repository, "receive-pack", true
	}

	return "", "", false
}

func gitRepository(path string) (string, bool) {
	repositoryPath, _, ok := strings.Cut(strings.TrimPrefix(path, "/"), "/")
	if !ok || !strings.HasSuffix(repositoryPath, ".git") {
		return "", false
	}
	repository := strings.TrimSuffix(repositoryPath, ".git")

	return repository, repository == "application" || repository == "private"
}
