package git

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Esonhugh/MarketplaceServer/core/kernel"
	"github.com/Esonhugh/MarketplaceServer/pkg/distributionservice"
	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/google/uuid"
	"github.com/juanjiTech/inject/v2"
	jinengine "github.com/juanjiTech/jin"
)

const testRepositoryID = "01J9ZQ2M8K4V7T6P5N3R1X0ABC"

func TestServiceStoresRepositoryByOpaqueIDOnly(t *testing.T) {
	fake := newFakeGit(t)
	root := t.TempDir()

	svc, err := NewService(Config{StorageRoot: root, gitBinary: fake.path})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	repositoryID := testRepositoryID
	if err := svc.InitBareRepository(context.Background(), repositoryID); err != nil {
		t.Fatalf("InitBareRepository() error = %v", err)
	}

	wantRepoPath := filepath.Join(root, "repositories", "01", repositoryID+".git")
	if _, err := os.Stat(wantRepoPath); err != nil {
		t.Fatalf("expected repository at %s: %v", wantRepoPath, err)
	}
	fake.assertInvocation(t, []string{"init", "--bare", wantRepoPath})

	for _, forbidden := range []string{"team-a", "plugin-one"} {
		if strings.Contains(wantRepoPath, forbidden) {
			t.Fatalf("physical repository path %q contains URL slug %q", wantRepoPath, forbidden)
		}
	}
}

func TestServiceRejectsNonOpaqueRepositoryIDsWithoutExecutingGit(t *testing.T) {
	fake := newFakeGit(t)
	svc, err := NewService(Config{StorageRoot: t.TempDir(), gitBinary: fake.path})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	for _, repositoryID := range []string{
		"",
		"a",
		"..",
		"repository.git",
		"namespace/repository",
		`namespace\\repository`,
		"repository.id",
		" repository",
	} {
		t.Run(repositoryID, func(t *testing.T) {
			if err := svc.InitBareRepository(context.Background(), repositoryID); err == nil {
				t.Fatalf("InitBareRepository(%q) succeeded; want error", repositoryID)
			}
		})
	}
	if got := fake.readLog(t); got != "" {
		t.Fatalf("invalid repository IDs executed git: %q", got)
	}
}

func TestServiceRejectsSymlinkEscapeInsideStorageRoot(t *testing.T) {
	fake := newFakeGit(t)
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "repositories")); err != nil {
		t.Fatal(err)
	}

	svc, err := NewService(Config{StorageRoot: root, gitBinary: fake.path})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	if err := svc.InitBareRepository(context.Background(), testRepositoryID); err == nil {
		t.Fatalf("InitBareRepository() succeeded through repositories symlink; want error")
	}
	if _, err := os.Stat(filepath.Join(outside, "01", testRepositoryID+".git")); !os.IsNotExist(err) {
		t.Fatalf("repository was created outside storage root through symlink: %v", err)
	}
	if got := fake.readLog(t); got != "" {
		t.Fatalf("symlink escape executed git: %q", got)
	}
}

func TestAdvertiseRefsWritesSmartHTTPPktLineAndUsesAllowlistedGitArgv(t *testing.T) {
	fake := newFakeGit(t)
	root := t.TempDir()
	repoPath := filepath.Join(root, "repositories", "01", testRepositoryID+".git")
	if err := os.MkdirAll(repoPath, 0o700); err != nil {
		t.Fatal(err)
	}

	svc, err := NewService(Config{StorageRoot: root, gitBinary: fake.path})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	var stdout, stderr bytes.Buffer
	if err := svc.AdvertiseRefs(context.Background(), "git-upload-pack", testRepositoryID, &stdout, &stderr); err != nil {
		t.Fatalf("AdvertiseRefs() error = %v, stderr = %q", err, stderr.String())
	}

	wantPrefix := "001e# service=git-upload-pack\n0000"
	if got := stdout.String(); !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("advertisement prefix = %q, want prefix %q", got, wantPrefix)
	}
	if !strings.Contains(stdout.String(), "FAKE_UPLOAD_ADVERTISE\n") {
		t.Fatalf("advertisement did not include git output: %q", stdout.String())
	}
	fake.assertInvocation(t, []string{"upload-pack", "--stateless-rpc", "--advertise-refs", repoPath})

	before := fake.readLog(t)
	stdout.Reset()
	if err := svc.AdvertiseRefs(context.Background(), "git-status", testRepositoryID, &stdout, io.Discard); err == nil {
		t.Fatalf("AdvertiseRefs() with unsupported service succeeded; want error")
	}
	if after := fake.readLog(t); after != before {
		t.Fatalf("unsupported service executed git; before log %q after log %q", before, after)
	}
}

func TestUploadAndReceivePackUseStatelessRPCArgv(t *testing.T) {
	fake := newFakeGit(t)
	root := t.TempDir()
	repoPath := filepath.Join(root, "repositories", "01", testRepositoryID+".git")
	if err := os.MkdirAll(repoPath, 0o700); err != nil {
		t.Fatal(err)
	}

	svc, err := NewService(Config{StorageRoot: root, gitBinary: fake.path})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}

	var uploadOut, receiveOut bytes.Buffer
	if err := svc.UploadPack(context.Background(), testRepositoryID, strings.NewReader("upload request"), &uploadOut, io.Discard); err != nil {
		t.Fatalf("UploadPack() error = %v", err)
	}
	if err := svc.ReceivePack(context.Background(), testRepositoryID, strings.NewReader("receive request"), &receiveOut, io.Discard); err != nil {
		t.Fatalf("ReceivePack() error = %v", err)
	}

	fake.assertInvocation(t, []string{"upload-pack", "--stateless-rpc", repoPath})
	fake.assertInvocation(t, []string{"receive-pack", "--stateless-rpc", repoPath})
	if got := uploadOut.String(); got != "FAKE_UPLOAD_RESULT\n" {
		t.Fatalf("UploadPack stdout = %q", got)
	}
	if got := receiveOut.String(); got != "FAKE_RECEIVE_RESULT\n" {
		t.Fatalf("ReceivePack stdout = %q", got)
	}
}

func TestSSHExecCommandParserAcceptsOnlyGitProtocolCommands(t *testing.T) {
	cmd, err := ParseSSHGitCommand("git-upload-pack 'team-a/plugin-one.git'")
	if err != nil {
		t.Fatalf("ParseSSHGitCommand() error = %v", err)
	}
	if cmd.Service != "git-upload-pack" || cmd.Namespace != "team-a" || cmd.Repository != "plugin-one" {
		t.Fatalf("parsed command = %#v", cmd)
	}

	cmd, err = ParseSSHGitCommand("git-receive-pack 'team-a/plugin-one.git'")
	if err != nil {
		t.Fatalf("ParseSSHGitCommand() receive-pack error = %v", err)
	}
	if cmd.Service != "git-receive-pack" || cmd.Namespace != "team-a" || cmd.Repository != "plugin-one" {
		t.Fatalf("parsed receive command = %#v", cmd)
	}

	for _, raw := range []string{
		"git-upload-pack team-a/plugin-one.git",
		"git-upload-pack 'team-a/../plugin-one.git'",
		"git-upload-pack 'team-a/plugin-one.git' extra",
		"git-upload-pack '/team-a/plugin-one.git'",
		"git-upload-pack 'Team/plugin-one.git'",
		"git-upload-pack 'team-a/plugin_one.git'",
		"git status 'team-a/plugin-one.git'",
		"git-receive-pack 'team-a/plugin-one'",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := ParseSSHGitCommand(raw); err == nil {
				t.Fatalf("ParseSSHGitCommand(%q) succeeded; want error", raw)
			}
		})
	}
}

func TestModInitMapsSeparateDistributionCapabilities(t *testing.T) {
	fake := newFakeGit(t)
	hub := kernel.Hub{Injector: inject.New()}
	mod := &Mod{config: Config{StorageRoot: t.TempDir(), gitBinary: fake.path}}
	if err := mod.Init(&hub); err != nil {
		t.Fatalf("Mod.Init() error = %v", err)
	}
	var reader gitservice.DistributionReader
	if err := hub.Load(&reader); err != nil || reader == nil {
		t.Fatalf("load DistributionReader = (%v, %v)", reader, err)
	}
	var builder gitservice.ProjectionBuilder
	if err := hub.Load(&builder); err != nil || builder == nil {
		t.Fatalf("load ProjectionBuilder = (%v, %v)", builder, err)
	}
}

func TestModLoadFailsFastWithoutRepositoryResolver(t *testing.T) {
	fake := newFakeGit(t)
	engine := jinengine.New()
	hub := kernel.Hub{Injector: inject.New()}
	hub.Map(&engine)
	mod := &Mod{config: Config{StorageRoot: t.TempDir(), gitBinary: fake.path}}
	if err := mod.Init(&hub); err != nil {
		t.Fatalf("Mod.Init() error = %v", err)
	}
	if err := mod.Load(&hub); err == nil || !strings.Contains(err.Error(), "RepositoryResolver") {
		t.Fatalf("Mod.Load() error = %v, want RepositoryResolver diagnostic", err)
	}
}

func TestModMapsRepositoryServiceAndRoutesAnonymousUploadPack(t *testing.T) {
	fake := newFakeGit(t)
	root := t.TempDir()
	engine := jinengine.New()
	hub := kernel.Hub{Injector: inject.New()}
	resolver := gitservice.RepositoryResolver(&fakeRepositoryResolver{repositories: map[string]gitservice.Repository{
		"team-a/plugin-one": {ID: testRepositoryID, Visibility: gitservice.VisibilityPublic, Status: gitservice.StatusReady},
	}})
	distributionResolver := distributionservice.Resolver(fakeDistributionResolver{})
	hub.Map(&engine, &resolver, &distributionResolver)

	mod := &Mod{}
	cfg := mod.Config().(*Config)
	cfg.StorageRoot = root
	cfg.gitBinary = fake.path
	cfg.serviceTimeout = time.Second
	cfg.advertiseTimeout = time.Second
	if err := mod.Init(&hub); err != nil {
		t.Fatalf("Mod.Init() error = %v", err)
	}

	var repoSvc gitservice.RepositoryService
	if err := hub.Load(&repoSvc); err != nil {
		t.Fatalf("hub.Load(gitservice.RepositoryService) error = %v", err)
	}
	if err := repoSvc.InitBareRepository(context.Background(), testRepositoryID); err != nil {
		t.Fatalf("InitBareRepository() via DI error = %v", err)
	}
	if err := mod.Load(&hub); err != nil {
		t.Fatalf("Mod.Load() error = %v", err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/git/team-a/plugin-one.git/info/refs?service=git-upload-pack", nil)
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET info/refs status = %d body = %q", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/x-git-upload-pack-advertisement" {
		t.Fatalf("GET info/refs Content-Type = %q", got)
	}
	if got := w.Header().Get("Cache-Control"); !strings.Contains(got, "no-cache") {
		t.Fatalf("GET info/refs Cache-Control = %q", got)
	}
	if got := w.Body.String(); !strings.HasPrefix(got, "001e# service=git-upload-pack\n0000") {
		t.Fatalf("GET info/refs body = %q", got)
	}

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/git/team-a/plugin-one.git/git-upload-pack", strings.NewReader("request"))
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("POST upload-pack status = %d body = %q", w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Type"); got != "application/x-git-upload-pack-result" {
		t.Fatalf("POST upload-pack Content-Type = %q", got)
	}
	if got := w.Body.String(); got != "FAKE_UPLOAD_RESULT\n" {
		t.Fatalf("POST upload-pack body = %q", got)
	}
}

func TestSmartHTTPResolvesURLSlugsToOpaqueRepositoryID(t *testing.T) {
	fake := newFakeGit(t)
	root := t.TempDir()
	repoPath := filepath.Join(root, "repositories", "01", testRepositoryID+".git")
	if err := os.MkdirAll(repoPath, 0o700); err != nil {
		t.Fatal(err)
	}
	resolver := &fakeRepositoryResolver{repositories: map[string]gitservice.Repository{
		"team-a/old-slug": {ID: testRepositoryID, Visibility: gitservice.VisibilityPublic, Status: gitservice.StatusReady},
		"team-a/new-slug": {ID: testRepositoryID, Visibility: gitservice.VisibilityPublic, Status: gitservice.StatusReady},
	}}
	engine := newSmartHTTPTestEngineWithResolver(t, Config{StorageRoot: root, gitBinary: fake.path}, resolver)

	for _, slug := range []string{"old-slug", "new-slug"} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/git/team-a/"+slug+".git/git-upload-pack", strings.NewReader("request"))
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("slug %q status = %d, want 200, body = %q", slug, w.Code, w.Body.String())
		}
	}

	log := fake.readLog(t)
	if strings.Contains(log, "old-slug") || strings.Contains(log, "new-slug") || strings.Contains(log, "team-a") {
		t.Fatalf("URL slugs reached git filesystem/process arguments: %q", log)
	}
	if got := strings.Count(log, "ARG:"+repoPath+"\n"); got != 2 {
		t.Fatalf("opaque repository path invocation count = %d, want 2; log=%q", got, log)
	}
}

func TestSmartHTTPAnonymousReadRequiresPublicReadyResolvedRepository(t *testing.T) {
	fake := newFakeGit(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "repositories", "01", testRepositoryID+".git"), 0o700); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name       string
		repository gitservice.Repository
		err        error
	}{
		{name: "private", repository: gitservice.Repository{ID: testRepositoryID, Visibility: "private", Status: gitservice.StatusReady}},
		{name: "non-ready", repository: gitservice.Repository{ID: testRepositoryID, Visibility: gitservice.VisibilityPublic, Status: "provisioning"}},
		{name: "invalid opaque id", repository: gitservice.Repository{ID: "team-a/plugin-one", Visibility: gitservice.VisibilityPublic, Status: gitservice.StatusReady}},
		{name: "unknown", err: gitservice.ErrRepositoryNotFound},
		{name: "resolver unavailable", err: gitservice.ErrRepositoryUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := fake.readLog(t)
			resolver := &fakeRepositoryResolver{repositories: map[string]gitservice.Repository{"team-a/plugin-one": tt.repository}, err: tt.err}
			engine := newSmartHTTPTestEngineWithResolver(t, Config{StorageRoot: root, gitBinary: fake.path}, resolver)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/git/team-a/plugin-one.git/git-upload-pack", strings.NewReader("request"))
			engine.ServeHTTP(w, req)
			if w.Code != http.StatusNotFound || w.Body.String() != "repository not found\n" {
				t.Fatalf("response = (%d, %q), want fail-closed 404", w.Code, w.Body.String())
			}
			if after := fake.readLog(t); after != before {
				t.Fatalf("denied resolver result executed git; before=%q after=%q", before, after)
			}
		})
	}
}

func TestSmartHTTPDoesNotWriteGitStderrIntoProtocolResponse(t *testing.T) {
	gitBinary := newScriptGit(t, `#!/bin/sh
printf 'PROTOCOL_BYTES'
printf 'sensitive path failure: /srv/git/secret.git' >&2
`)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "repositories", "01", testRepositoryID+".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	engine := newSmartHTTPTestEngine(t, Config{
		StorageRoot: root,
		gitBinary:   gitBinary,
	})

	for _, tc := range []struct {
		name     string
		method   string
		path     string
		wantBody string
	}{
		{
			name:     "advertisement",
			method:   http.MethodGet,
			path:     "/git/team-a/plugin-one.git/info/refs?service=git-upload-pack",
			wantBody: "001e# service=git-upload-pack\n0000PROTOCOL_BYTES",
		},
		{
			name:     "upload-pack result",
			method:   http.MethodPost,
			path:     "/git/team-a/plugin-one.git/git-upload-pack",
			wantBody: "PROTOCOL_BYTES",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader("request"))
			engine.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200, body = %q", w.Code, w.Body.String())
			}
			if got := w.Body.String(); got != tc.wantBody {
				t.Fatalf("protocol response = %q, want %q", got, tc.wantBody)
			}
		})
	}
}

func TestSmartHTTPReturnsErrorOnlyBeforeProtocolOutputStarts(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "repositories", "01", testRepositoryID+".git"), 0o700); err != nil {
		t.Fatal(err)
	}

	t.Run("advertisement failure before output", func(t *testing.T) {
		gitBinary := newScriptGit(t, `#!/bin/sh
printf 'fatal: cannot open /srv/git/private.git' >&2
exit 9
`)
		engine := newSmartHTTPTestEngine(t, Config{StorageRoot: root, gitBinary: gitBinary})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/git/team-a/plugin-one.git/info/refs?service=git-upload-pack", nil)
		engine.ServeHTTP(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500, body = %q", w.Code, w.Body.String())
		}
		if got := w.Body.String(); got != "git service unavailable\n" {
			t.Fatalf("body = %q, want stable generic error", got)
		}
	})

	t.Run("failure before output", func(t *testing.T) {
		gitBinary := newScriptGit(t, `#!/bin/sh
printf 'fatal: cannot open /srv/git/private.git' >&2
exit 9
`)
		engine := newSmartHTTPTestEngine(t, Config{StorageRoot: root, gitBinary: gitBinary})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/git/team-a/plugin-one.git/git-upload-pack", strings.NewReader("request"))
		engine.ServeHTTP(w, req)

		if w.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500, body = %q", w.Code, w.Body.String())
		}
		if got := w.Body.String(); got != "git service unavailable\n" {
			t.Fatalf("body = %q, want stable generic error", got)
		}
	})

	t.Run("failure after output", func(t *testing.T) {
		gitBinary := newScriptGit(t, `#!/bin/sh
printf 'PROTOCOL_BYTES'
printf 'fatal: cannot open /srv/git/private.git' >&2
exit 9
`)
		engine := newSmartHTTPTestEngine(t, Config{StorageRoot: root, gitBinary: gitBinary})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/git/team-a/plugin-one.git/git-upload-pack", strings.NewReader("request"))
		engine.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want committed 200, body = %q", w.Code, w.Body.String())
		}
		if got := w.Body.String(); got != "PROTOCOL_BYTES" {
			t.Fatalf("body = %q, want protocol bytes without appended HTTP error", got)
		}
	})
}

func TestSmartHTTPErrorsDoNotExposeFilesystemOrProcessDetails(t *testing.T) {
	root := t.TempDir()
	gitBinary := newScriptGit(t, `#!/bin/sh
printf 'fatal: cannot open /srv/git/private.git: permission denied' >&2
exit 9
`)
	if err := os.MkdirAll(filepath.Join(root, "repositories", "01", testRepositoryID+".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	engine := newSmartHTTPTestEngine(t, Config{StorageRoot: root, gitBinary: gitBinary})

	for _, tc := range []struct {
		name     string
		path     string
		wantCode int
		wantBody string
	}{
		{name: "missing repository", path: "/git/team-a/missing.git/git-upload-pack", wantCode: http.StatusNotFound, wantBody: "repository not found\n"},
		{name: "subprocess failure", path: "/git/team-a/plugin-one.git/git-upload-pack", wantCode: http.StatusInternalServerError, wantBody: "git service unavailable\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader("request"))
			engine.ServeHTTP(w, req)
			if w.Code != tc.wantCode || w.Body.String() != tc.wantBody {
				t.Fatalf("response = (%d, %q), want (%d, %q)", w.Code, w.Body.String(), tc.wantCode, tc.wantBody)
			}
			for _, secret := range []string{"/srv/git/private.git", "permission denied", "exit status"} {
				if strings.Contains(w.Body.String(), secret) {
					t.Fatalf("response leaked %q: %q", secret, w.Body.String())
				}
			}
		})
	}
}

func TestSmartHTTPRejectsKnownOversizedContentLengthBeforeExecutingGit(t *testing.T) {
	fake := newFakeGit(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "repositories", "01", testRepositoryID+".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	engine := newSmartHTTPTestEngine(t, Config{
		StorageRoot:     root,
		gitBinary:       fake.path,
		maxRequestBytes: 4,
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/git/team-a/plugin-one.git/git-upload-pack", strings.NewReader("12345"))
	engine.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413, body = %q", w.Code, w.Body.String())
	}
	if got := fake.readLog(t); got != "" {
		t.Fatalf("oversized known-length request executed git: %q", got)
	}
}

func TestSmartHTTPUnknownLengthLimitDoesNotLeakReaderError(t *testing.T) {
	fake := newFakeGit(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "repositories", "01", testRepositoryID+".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	engine := newSmartHTTPTestEngine(t, Config{
		StorageRoot:     root,
		gitBinary:       fake.path,
		maxRequestBytes: 4,
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/git/team-a/plugin-one.git/git-upload-pack", io.NopCloser(strings.NewReader("12345")))
	if req.ContentLength != -1 {
		t.Fatalf("test request ContentLength = %d, want unknown", req.ContentLength)
	}
	engine.ServeHTTP(w, req)

	if got := fake.readLog(t); got == "" {
		t.Fatal("unknown-length request did not execute git; test must document streaming boundary")
	}
	// Unknown-length bodies are streamed to git, so the subprocess may execute and
	// may still return protocol output after MaxBytesReader stops forwarding input.
	// The handler's guarantee here is non-disclosure, not pre-execution rejection.
	if got := w.Body.String(); got != "FAKE_UPLOAD_RESULT\n" {
		t.Fatalf("response body = %q, want only protocol output", got)
	}
	for _, leaked := range []string{"request body too large", "http: request body too large", "read |0", "exit status"} {
		if strings.Contains(w.Body.String(), leaked) {
			t.Fatalf("response leaked request reader/process error %q: %q", leaked, w.Body.String())
		}
	}
}

func TestModConfigExposesOnlyStorageRoot(t *testing.T) {
	mod := &Mod{}
	cfg := mod.Config().(*Config)
	if cfg.gitBinary != defaultGitBinary {
		t.Fatalf("internal Git binary default = %q, want %q", cfg.gitBinary, defaultGitBinary)
	}
	if cfg.maxRequestBytes != defaultMaxRequestBytes {
		t.Fatalf("internal request limit = %d, want %d", cfg.maxRequestBytes, defaultMaxRequestBytes)
	}
	if cfg.advertiseTimeout != defaultAdvertiseTimeout {
		t.Fatalf("internal advertise timeout = %s, want %s", cfg.advertiseTimeout, defaultAdvertiseTimeout)
	}
	if cfg.serviceTimeout != defaultServiceTimeout {
		t.Fatalf("internal service timeout = %s, want %s", cfg.serviceTimeout, defaultServiceTimeout)
	}

	typeOfConfig := reflect.TypeOf(Config{})
	var exported []reflect.StructField
	for i := 0; i < typeOfConfig.NumField(); i++ {
		field := typeOfConfig.Field(i)
		if field.IsExported() {
			exported = append(exported, field)
		}
	}
	if len(exported) != 1 || exported[0].Name != "StorageRoot" {
		t.Fatalf("exported Git config fields = %#v, want only StorageRoot", exported)
	}
	if got := exported[0].Tag.Get("yaml"); got != "storageRoot" {
		t.Fatalf("Config.StorageRoot yaml tag = %q, want storageRoot", got)
	}
	if got := exported[0].Tag.Get("mapstructure"); got != "storageRoot" {
		t.Fatalf("Config.StorageRoot mapstructure tag = %q, want storageRoot", got)
	}
}

func TestSmartHTTPValidatesRouteBeforeRepositoryResolution(t *testing.T) {
	fake := newFakeGit(t)
	root := t.TempDir()
	engine := jinengine.New()
	hub := kernel.Hub{Injector: inject.New()}
	resolverImpl := &fakeRepositoryResolver{repositories: map[string]gitservice.Repository{}}
	resolver := gitservice.RepositoryResolver(resolverImpl)
	distributionResolver := distributionservice.Resolver(fakeDistributionResolver{})
	hub.Map(&engine, &resolver, &distributionResolver)
	mod := &Mod{config: Config{StorageRoot: root, gitBinary: fake.path}}
	if err := mod.Init(&hub); err != nil {
		t.Fatalf("Mod.Init() error = %v", err)
	}
	if err := mod.Load(&hub); err != nil {
		t.Fatalf("Mod.Load() error = %v", err)
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/git/Team/plugin_one.git/git-upload-pack", strings.NewReader("request"))
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("invalid route status = %d, want 404 before anonymous policy, body=%q", w.Code, w.Body.String())
	}
	if resolverImpl.calls != 0 {
		t.Fatalf("invalid route called resolver %d times, want 0", resolverImpl.calls)
	}
}

func TestSmartHTTPRoutesAllowOnlyPublicReadsAndFailClosedForWrites(t *testing.T) {
	fake := newFakeGit(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "repositories", "01", testRepositoryID+".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	engine := jinengine.New()
	hub := kernel.Hub{Injector: inject.New()}
	resolver := gitservice.RepositoryResolver(&fakeRepositoryResolver{repositories: map[string]gitservice.Repository{
		"team-a/plugin-one": {ID: testRepositoryID, Visibility: gitservice.VisibilityPublic, Status: gitservice.StatusReady},
	}})
	distributionResolver := distributionservice.Resolver(fakeDistributionResolver{})
	hub.Map(&engine, &resolver, &distributionResolver)

	mod := &Mod{}
	cfg := mod.Config().(*Config)
	cfg.StorageRoot = root
	cfg.gitBinary = fake.path
	if err := mod.Init(&hub); err != nil {
		t.Fatalf("Mod.Init() error = %v", err)
	}
	if err := mod.Load(&hub); err != nil {
		t.Fatalf("Mod.Load() error = %v", err)
	}

	for _, tc := range []struct {
		name   string
		method string
		path   string
		want   int
	}{
		{name: "public upload-pack advertisement", method: http.MethodGet, path: "/git/team-a/plugin-one.git/info/refs?service=git-upload-pack", want: http.StatusOK},
		{name: "public upload-pack rpc", method: http.MethodPost, path: "/git/team-a/plugin-one.git/git-upload-pack", want: http.StatusOK},
		{name: "receive-pack advertisement fail closed", method: http.MethodGet, path: "/git/team-a/plugin-one.git/info/refs?service=git-receive-pack", want: http.StatusForbidden},
		{name: "receive-pack rpc fail closed", method: http.MethodPost, path: "/git/team-a/plugin-one.git/git-receive-pack", want: http.StatusForbidden},
		{name: "unknown advertised service not routed to git", method: http.MethodGet, path: "/git/team-a/plugin-one.git/info/refs?service=git-status", want: http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader("request"))
			engine.ServeHTTP(w, req)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d, body = %q", w.Code, tc.want, w.Body.String())
			}
		})
	}
	if got := fake.readLog(t); strings.Contains(got, "receive-pack") {
		t.Fatalf("write requests executed git: %q", got)
	}
}

func TestRealGitSmartHTTPAnonymousCloneInteroperability(t *testing.T) {
	gitBinary, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git binary is not available")
	}
	root := t.TempDir()
	svc, err := NewService(Config{StorageRoot: root, gitBinary: gitBinary})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	if err := svc.InitBareRepository(context.Background(), testRepositoryID); err != nil {
		t.Fatalf("InitBareRepository() error = %v", err)
	}

	worktree := t.TempDir()
	runGitCommand(t, gitBinary, worktree, "init")
	runGitCommand(t, gitBinary, worktree, "config", "user.name", "Marketplace Test")
	runGitCommand(t, gitBinary, worktree, "config", "user.email", "marketplace@example.invalid")
	if err := os.WriteFile(filepath.Join(worktree, "README"), []byte("interop\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitCommand(t, gitBinary, worktree, "add", "README")
	runGitCommand(t, gitBinary, worktree, "commit", "-m", "initial")
	repoPath := filepath.Join(root, "repositories", "01", testRepositoryID+".git")
	runGitCommand(t, gitBinary, worktree, "push", repoPath, "HEAD:refs/heads/main")

	engine := newSmartHTTPTestEngine(t, Config{StorageRoot: root, gitBinary: gitBinary})
	server := httptest.NewServer(engine)
	defer server.Close()
	cloneDir := filepath.Join(t.TempDir(), "clone")
	runGitCommand(t, gitBinary, "", "clone", server.URL+"/git/team-a/plugin-one.git", cloneDir)
	got, err := os.ReadFile(filepath.Join(cloneDir, "README"))
	if err != nil {
		t.Fatalf("read cloned README: %v", err)
	}
	if string(got) != "interop\n" {
		t.Fatalf("cloned README = %q", got)
	}
}

func runGitCommand(t *testing.T, gitBinary, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command(gitBinary, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func newSmartHTTPTestEngine(t *testing.T, config Config) *jinengine.Engine {
	t.Helper()
	return newSmartHTTPTestEngineWithResolver(t, config, &fakeRepositoryResolver{repositories: map[string]gitservice.Repository{
		"team-a/plugin-one": {ID: testRepositoryID, Visibility: gitservice.VisibilityPublic, Status: gitservice.StatusReady},
	}})
}

func newSmartHTTPTestEngineWithResolver(t *testing.T, config Config, repositoryResolver gitservice.RepositoryResolver) *jinengine.Engine {
	t.Helper()
	engine := jinengine.New()
	hub := kernel.Hub{Injector: inject.New()}
	resolver := repositoryResolver
	distributionResolver := distributionservice.Resolver(fakeDistributionResolver{})
	hub.Map(&engine, &resolver, &distributionResolver)
	mod := &Mod{config: config}
	if err := mod.Init(&hub); err != nil {
		t.Fatalf("Mod.Init() error = %v", err)
	}
	if err := mod.Load(&hub); err != nil {
		t.Fatalf("Mod.Load() error = %v", err)
	}
	return engine
}

type fakeRepositoryResolver struct {
	repositories map[string]gitservice.Repository
	err          error
	calls        int
}

func (r *fakeRepositoryResolver) Resolve(_ context.Context, namespace, repository string) (gitservice.Repository, error) {
	r.calls++
	if r.err != nil {
		return gitservice.Repository{}, r.err
	}
	resolved, ok := r.repositories[namespace+"/"+repository]
	if !ok {
		return gitservice.Repository{}, gitservice.ErrRepositoryNotFound
	}
	return resolved, nil
}

var _ gitservice.RepositoryResolver = (*fakeRepositoryResolver)(nil)

type fakeDistributionResolver struct{}

func (fakeDistributionResolver) ResolvePlugin(context.Context, uuid.UUID) (distributionservice.PluginGrant, error) {
	return distributionservice.PluginGrant{}, distributionservice.ErrNotFound
}
func (fakeDistributionResolver) ResolveMarketplace(context.Context, distributionservice.MarketplacePublicKey) (distributionservice.MarketplaceGrant, error) {
	return distributionservice.MarketplaceGrant{}, distributionservice.ErrNotFound
}

func newScriptGit(t *testing.T, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake-git")
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

type fakeGit struct {
	path string
	log  string
}

func newFakeGit(t *testing.T) fakeGit {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "git.log")
	scriptPath := filepath.Join(dir, "fake-git")
	script := `#!/bin/sh
set -eu
log=` + shellQuote(logPath) + `
{
  echo BEGIN
  for arg in "$@"; do
    printf 'ARG:%s\n' "$arg"
  done
  printf 'ENV_GIT_CONFIG_NOSYSTEM:%s\n' "${GIT_CONFIG_NOSYSTEM-}"
  printf 'ENV_GIT_TERMINAL_PROMPT:%s\n' "${GIT_TERMINAL_PROMPT-}"
  echo END
} >> "$log"
case "${1-}" in
  init)
    if [ "${2-}" = "--bare" ]; then
      mkdir -p "${3-}"
      exit 0
    fi
    ;;
  upload-pack)
    if [ "${2-}" = "--stateless-rpc" ] && [ "${3-}" = "--advertise-refs" ]; then
      printf 'FAKE_UPLOAD_ADVERTISE\n'
      exit 0
    fi
    if [ "${2-}" = "--stateless-rpc" ]; then
      cat >/dev/null
      printf 'FAKE_UPLOAD_RESULT\n'
      exit 0
    fi
    ;;
  receive-pack)
    if [ "${2-}" = "--stateless-rpc" ] && [ "${3-}" = "--advertise-refs" ]; then
      printf 'FAKE_RECEIVE_ADVERTISE\n'
      exit 0
    fi
    if [ "${2-}" = "--stateless-rpc" ]; then
      cat >/dev/null
      printf 'FAKE_RECEIVE_RESULT\n'
      exit 0
    fi
    ;;
esac
printf 'unexpected fake git argv:' >&2
for arg in "$@"; do printf ' [%s]' "$arg" >&2; done
printf '\n' >&2
exit 9
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return fakeGit{path: scriptPath, log: logPath}
}

func (f fakeGit) readLog(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(f.log)
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func (f fakeGit) assertInvocation(t *testing.T, wantArgs []string) {
	t.Helper()
	log := f.readLog(t)
	want := "BEGIN\n"
	for _, arg := range wantArgs {
		want += "ARG:" + arg + "\n"
	}
	if !strings.Contains(log, want) {
		t.Fatalf("git invocation with args %#v not found in log:\n%s", wantArgs, log)
	}
	if !strings.Contains(log, "ENV_GIT_CONFIG_NOSYSTEM:1\n") {
		t.Fatalf("git command did not receive minimal GIT_CONFIG_NOSYSTEM env; log:\n%s", log)
	}
	if !strings.Contains(log, "ENV_GIT_TERMINAL_PROMPT:0\n") {
		t.Fatalf("git command did not receive disabled terminal prompt env; log:\n%s", log)
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
