package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Opt-in image verification; ordinary go test must not require Docker.
func TestDockerImage(t *testing.T) {
	image := os.Getenv("GHREPORT_DOCKER_IMAGE")
	if image == "" {
		t.Skip("set GHREPORT_DOCKER_IMAGE to test a built Linux image")
	}
	var images []struct {
		Config struct {
			User       string
			WorkingDir string
			Entrypoint []string
			Env        []string
		}
	}
	if err := json.Unmarshal(dockerCommand(t, "image", "inspect", image), &images); err != nil || len(images) != 1 {
		t.Fatalf("image metadata: %v", err)
	}
	cfg := images[0].Config
	if cfg.User != "65532:65532" || cfg.WorkingDir != "/work" || !reflect.DeepEqual(cfg.Entrypoint, []string{"/ghreport"}) {
		t.Fatalf("unexpected runtime configuration: %+v", cfg)
	}
	for _, env := range cfg.Env {
		if strings.HasPrefix(env, "GITHUB_TOKEN=") || strings.HasPrefix(env, "GITHUB_API_BASE_URL=") {
			t.Fatal("API configuration must be supplied at runtime")
		}
	}
	requests := make(chan string, 8)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requests <- r.URL.RequestURI() + ":" + r.Header.Get("If-None-Match"):
		default:
			t.Error("container made too many requests")
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("Authorization") != "Bearer docker-test-token" || r.Header.Get("User-Agent") != "ghreport/1.0" ||
			r.Header.Get("X-GitHub-Api-Version") != "2026-03-10" || r.Header.Get("Accept") != "application/vnd.github+json" {
			t.Error("container lost required request headers")
		}
		etag := `W/"first"`
		switch r.URL.Path {
		case "/api/repos/stub/alpha/issues":
			if r.URL.Query().Get("state") != "open" || r.URL.Query().Get("sort") != "created" ||
				r.URL.Query().Get("direction") != "asc" || r.URL.Query().Get("per_page") != "100" {
				t.Error("container lost required query parameters")
			}
		case "/api/next":
			etag = `"second"`
			if r.URL.RawQuery != "cursor=a%2Cb&keep=yes" {
				t.Errorf("container rewrote next URL: %s", r.URL)
			}
		default:
			t.Errorf("container used unexpected API path: %s", r.URL)
			w.WriteHeader(404)
			return
		}
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(304) // The second process must recover Link from disk.
			return
		}
		if r.Header.Get("If-None-Match") != "" {
			t.Error("validator was transferred to another URL")
		}
		w.Header().Set("ETag", etag)
		if r.URL.Path == "/api/repos/stub/alpha/issues" {
			w.Header().Set("Link", `</api/next?cursor=a%2Cb&keep=yes>; rel=next`)
			fmt.Fprint(w, testPage(`{"pull_request":null}`, testIssue(1, "2026-01-01T00:00:00Z", "first")))
		} else {
			fmt.Fprint(w, testPage(testIssue(2, "2026-01-02T00:00:00Z", "second")))
		}
	})
	listener, err := net.Listen("tcp4", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.Listener.Close()
	server.Listener = listener
	server.Start()
	t.Cleanup(server.Close)
	port := listener.Addr().(*net.TCPAddr).Port
	base := fmt.Sprintf("http://host.docker.internal:%d/api/", port)
	id := strings.TrimSpace(string(dockerCommand(t, "create", "--add-host", "host.docker.internal:host-gateway",
		"--env", "GITHUB_TOKEN=docker-test-token", "--env", "GITHUB_API_BASE_URL="+base,
		image, "--repos", "stub/alpha", "--limit", "2", "--out", "report.json", "--trace", "trace.jsonl")))
	t.Cleanup(func() { dockerCommand(t, "rm", "--force", id) })
	dir := testTempDir(t)
	var first []repoReport
	for run := 0; run < 2; run++ {
		// Restarting the same container starts a fresh process and retains its
		// writable layer. No host files or volumes are required for this check.
		if output := dockerCommand(t, "start", "--attach", id); len(bytes.TrimSpace(output)) != 0 {
			t.Fatalf("file report produced stdout: %s", output)
		}
		if code := strings.TrimSpace(string(dockerCommand(t, "inspect", "--format", "{{.State.ExitCode}}", id))); code != "0" {
			t.Fatalf("container exit code: %s", code)
		}
		outPath, tracePath := filepath.Join(dir, fmt.Sprintf("report-%d.json", run)), filepath.Join(dir, fmt.Sprintf("trace-%d.jsonl", run))
		dockerCommand(t, "cp", id+":/work/report.json", outPath)
		dockerCommand(t, "cp", id+":/work/trace.jsonl", tracePath)
		data, err := os.ReadFile(outPath)
		if err != nil {
			t.Fatal(err)
		}
		var result report
		if err := json.Unmarshal(data, &result); err != nil || len(result.Repos) != 1 || result.Repos[0].Count != 2 || result.Repos[0].Error != nil {
			t.Fatalf("container report: %s, %v", data, err)
		}
		if len(result.Repos[0].Issues) != 2 || result.Repos[0].Issues[0].Number != 1 || result.Repos[0].Issues[1].Number != 2 {
			t.Fatalf("container issue selection: %+v", result.Repos)
		}
		if run == 1 && !reflect.DeepEqual(first, result.Repos) {
			t.Fatal("report changed after container restart")
		}
		first = result.Repos
		trace, err := os.ReadFile(tracePath)
		if err != nil {
			t.Fatal(err)
		}
		records := parseTrace(t, string(trace))
		if len(records) != 2 || strings.Contains(string(trace)+string(data), "docker-test-token") {
			t.Fatal("container trace lost records or exposed the token")
		}
		for _, record := range records {
			status := 200
			if run == 1 {
				status = 304
			}
			if record.Status != status || record.FromCache != (run == 1) || record.Error != nil || record.Attempt != 1 || !strings.HasPrefix(record.URL, base) {
				t.Fatalf("container trace: %+v", record)
			}
		}
	}
	var got []string
	for len(requests) > 0 {
		got = append(got, <-requests)
	}
	want := []string{"/api/repos/stub/alpha/issues?direction=asc&per_page=100&sort=created&state=open:",
		"/api/next?cursor=a%2Cb&keep=yes:", `/api/repos/stub/alpha/issues?direction=asc&per_page=100&sort=created&state=open:W/"first"`,
		`/api/next?cursor=a%2Cb&keep=yes:"second"`}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("container requests=%v, want=%v", got, want)
	}
	certPath := filepath.Join(dir, "ca-certificates.crt")
	dockerCommand(t, "cp", id+":/etc/ssl/certs/ca-certificates.crt", certPath)
	certs, err := os.ReadFile(certPath)
	if err != nil || !x509.NewCertPool().AppendCertsFromPEM(certs) {
		t.Fatalf("image has no usable CA bundle: %v", err)
	}
	// A new named volume must inherit writable /work ownership for the default
	// non-root user, as in the documented docker run --rm workflow.
	volume := strings.TrimSpace(string(dockerCommand(t, "volume", "create")))
	t.Cleanup(func() { dockerCommand(t, "volume", "rm", volume) })
	mounted := strings.TrimSpace(string(dockerCommand(t, "create", "--add-host", "host.docker.internal:host-gateway",
		"--mount", "type=volume,source="+volume+",target=/work",
		"--env", "GITHUB_TOKEN=docker-test-token", "--env", "GITHUB_API_BASE_URL="+base,
		image, "--repos", "stub/alpha", "--limit", "1", "--trace", "trace.jsonl")))
	t.Cleanup(func() { dockerCommand(t, "rm", "--force", mounted) })
	var mountedReport report
	output := dockerCommand(t, "start", "--attach", mounted)
	if err := json.Unmarshal(output, &mountedReport); err != nil || len(mountedReport.Repos) != 1 || mountedReport.Repos[0].Count != 1 {
		t.Fatalf("non-root named-volume run: %s, %v", output, err)
	}
	if code := strings.TrimSpace(string(dockerCommand(t, "inspect", "--format", "{{.State.ExitCode}}", mounted))); code != "0" {
		t.Fatalf("named-volume exit code: %s", code)
	}
	if len(requests) != 1 || <-requests != want[0] {
		t.Fatal("named-volume run did not make exactly one initial request")
	}
}

func dockerCommand(t *testing.T, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("docker %s: %v; stdout=%s; stderr=%s", args[0], err, &stdout, &stderr)
	}
	if stderr.Len() != 0 {
		t.Fatalf("docker %s wrote stderr: %s", args[0], &stderr)
	}
	return stdout.Bytes()
}
