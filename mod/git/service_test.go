package git

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
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
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/Esonhugh/MarketplaceServer/pkg/distributionservice"
	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/google/uuid"
	"github.com/juanjiTech/inject/v2"
	jinengine "github.com/juanjiTech/jin"
)

const testRepositoryID = "01J9ZQ2M8K4V7T6P5N3R1X0ABC"

func TestProtectedReceiveHookProcess(t *testing.T) {
	if _, ok := ProtectedReceiveHookMode(); !ok {
		return
	}
	if err := RunProtectedReceiveHook(context.Background(), os.Stdin, os.Stdout); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

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

func TestModLoadRejectsTypedNilDependencies(t *testing.T) {
	fake := newFakeGit(t)
	tests := []struct {
		name          string
		mapDependency func(*kernel.Hub)
		want          string
	}{
		{name: "resolver", mapDependency: func(hub *kernel.Hub) {
			var value *fakeRepositoryResolver
			dependency := gitservice.RepositoryResolver(value)
			hub.Map(&dependency)
		}, want: "RepositoryResolver"},
		{name: "authenticator", mapDependency: func(hub *kernel.Hub) {
			resolver := gitservice.RepositoryResolver(&fakeRepositoryResolver{})
			var value *typedNilAuthenticator
			dependency := auth.GitPATAuthenticator(value)
			hub.Map(&resolver, &dependency)
		}, want: "GitPATAuthenticator"},
		{name: "authorizer", mapDependency: func(hub *kernel.Hub) {
			resolver := gitservice.RepositoryResolver(&fakeRepositoryResolver{})
			authenticator := auth.GitPATAuthenticator(fakeGitAuthenticator{})
			var value *typedNilAuthorizer
			dependency := auth.Authorizer(value)
			hub.Map(&resolver, &authenticator, &dependency)
		}, want: "Authorizer"},
		{name: "distribution resolver", mapDependency: func(hub *kernel.Hub) {
			resolver := gitservice.RepositoryResolver(&fakeRepositoryResolver{})
			authenticator := auth.GitPATAuthenticator(fakeGitAuthenticator{})
			authorizer := auth.Authorizer(fakeGitAuthorizer{})
			var value *typedNilDistributionResolver
			dependency := distributionservice.Resolver(value)
			hub.Map(&resolver, &authenticator, &authorizer, &dependency)
		}, want: "distributionservice.Resolver"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := jinengine.New()
			hub := kernel.Hub{Injector: inject.New()}
			hub.Map(&engine)
			tt.mapDependency(&hub)
			mod := &Mod{config: Config{StorageRoot: t.TempDir(), gitBinary: fake.path}}
			if err := mod.Init(&hub); err != nil {
				t.Fatal(err)
			}
			if err := mod.Load(&hub); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Mod.Load() error = %v, want %q diagnostic", err, tt.want)
			}
		})
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
	gitPATAuthenticator := auth.GitPATAuthenticator(fakeGitAuthenticator{})
	authorizer := auth.Authorizer(fakeGitAuthorizer{})
	receiveCoordinator := gitservice.ReceiveCoordinator(&recordingReceiveCoordinator{coordination: &recordingReceiveCoordination{}})
	hub.Map(&engine, &resolver, &distributionResolver, &gitPATAuthenticator, &authorizer, &receiveCoordinator)

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
			wantStatus, wantBody := http.StatusNotFound, "repository not found\n"
			if tt.name == "private" {
				wantStatus, wantBody = http.StatusUnauthorized, "authentication required\n"
			}
			if w.Code != wantStatus || w.Body.String() != wantBody {
				t.Fatalf("response = (%d, %q), want (%d, %q)", w.Code, w.Body.String(), wantStatus, wantBody)
			}
			if after := fake.readLog(t); after != before {
				t.Fatalf("denied resolver result executed git; before=%q after=%q", before, after)
			}
		})
	}
}

func TestSmartHTTPRepositoryStatusMatrix(t *testing.T) {
	fake := newFakeGit(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "repositories", "01", testRepositoryID+".git"), 0o700); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		status      string
		readStatus  int
		writeStatus int
	}{
		{status: "provisioning", readStatus: http.StatusNotFound, writeStatus: http.StatusNotFound},
		{status: gitservice.StatusReady, readStatus: http.StatusOK, writeStatus: http.StatusOK},
		{status: gitservice.StatusReadOnly, readStatus: http.StatusOK, writeStatus: http.StatusNotFound},
		{status: "error", readStatus: http.StatusNotFound, writeStatus: http.StatusNotFound},
		{status: "deleting", readStatus: http.StatusNotFound, writeStatus: http.StatusNotFound},
		{status: "deleted", readStatus: http.StatusNotFound, writeStatus: http.StatusNotFound},
		{status: "active", readStatus: http.StatusNotFound, writeStatus: http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			for _, operation := range []struct {
				name, path string
				want       int
			}{
				{name: "read", path: "git-upload-pack", want: tt.readStatus},
				{name: "write", path: "git-receive-pack", want: tt.writeStatus},
			} {
				t.Run(operation.name, func(t *testing.T) {
					repository := gitservice.Repository{ID: testRepositoryID, NamespaceID: "namespace-a", OwnerUserID: "user-alice", Visibility: "private", Status: tt.status}
					engine := newSmartHTTPTestEngineWithResolver(t, Config{StorageRoot: root, gitBinary: fake.path}, &fakeRepositoryResolver{repositories: map[string]gitservice.Repository{"team-a/plugin-one": repository}})
					before := fake.readLog(t)
					request := httptest.NewRequest(http.MethodPost, "/git/team-a/plugin-one.git/"+operation.path, strings.NewReader("request"))
					request.SetBasicAuth("alice", "git-write-pat")
					response := httptest.NewRecorder()
					engine.ServeHTTP(response, request)
					if response.Code != operation.want {
						t.Fatalf("status = %d, want %d, body = %q", response.Code, operation.want, response.Body.String())
					}
					after := fake.readLog(t)
					if operation.want == http.StatusOK {
						if after == before {
							t.Fatal("allowed request did not invoke git")
						}
					} else if after != before {
						t.Fatalf("denied request invoked git; before=%q after=%q", before, after)
					}
				})
			}
		})
	}
}

func TestSmartHTTPMapsGitServicesToPATOperations(t *testing.T) {
	fake := newFakeGit(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "repositories", "01", testRepositoryID+".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	repository := gitservice.Repository{ID: testRepositoryID, NamespaceID: "namespace-a", OwnerUserID: "user-alice", Visibility: "private", Status: gitservice.StatusReady}
	authenticator := &recordingGitPATAuthenticator{principal: gitPATPrincipal(t, auth.ActionPluginRead, auth.ActionPluginWrite)}
	engine := newSmartHTTPTestEngineWithDependencies(t, Config{StorageRoot: root, gitBinary: fake.path}, &fakeRepositoryResolver{repositories: map[string]gitservice.Repository{"team-a/plugin-one": repository}}, authenticator)

	for _, test := range []struct {
		name      string
		method    string
		path      string
		operation auth.GitOperation
	}{
		{name: "upload advertisement", method: http.MethodGet, path: "/git/team-a/plugin-one.git/info/refs?service=git-upload-pack", operation: auth.GitOperationRead},
		{name: "upload RPC", method: http.MethodPost, path: "/git/team-a/plugin-one.git/git-upload-pack", operation: auth.GitOperationRead},
		{name: "receive advertisement", method: http.MethodGet, path: "/git/team-a/plugin-one.git/info/refs?service=git-receive-pack", operation: auth.GitOperationWrite},
		{name: "receive RPC", method: http.MethodPost, path: "/git/team-a/plugin-one.git/git-receive-pack", operation: auth.GitOperationWrite},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := len(authenticator.operations)
			request := httptest.NewRequest(test.method, test.path, strings.NewReader("request"))
			request.SetBasicAuth("alice", "git-write-pat")
			response := httptest.NewRecorder()
			engine.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %q", response.Code, response.Body.String())
			}
			if len(authenticator.operations) != before+1 || authenticator.operations[before] != test.operation {
				t.Fatalf("operations = %v, want appended %q", authenticator.operations, test.operation)
			}
		})
	}
}

func TestSmartHTTPAppliesCredentialsAuthorizationAndReadinessBeforeGit(t *testing.T) {
	fake := newFakeGit(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "repositories", "01", testRepositoryID+".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	private := gitservice.Repository{ID: testRepositoryID, NamespaceID: "namespace-a", OwnerUserID: "user-alice", Visibility: "private", Status: gitservice.StatusReady}

	t.Run("private anonymous read challenges before git", func(t *testing.T) {
		engine := newSmartHTTPTestEngineWithResolver(t, Config{StorageRoot: root, gitBinary: fake.path}, &fakeRepositoryResolver{repositories: map[string]gitservice.Repository{"team-a/plugin-one": private}})
		w := httptest.NewRecorder()
		engine.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/git/team-a/plugin-one.git/git-upload-pack", strings.NewReader("request")))
		if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") == "" {
			t.Fatalf("anonymous private read = (%d, %q), want 401 challenge", w.Code, w.Body.String())
		}
		if got := fake.readLog(t); got != "" {
			t.Fatalf("anonymous private read invoked git: %q", got)
		}
	})

	t.Run("invalid supplied credentials do not downgrade to anonymous", func(t *testing.T) {
		public := private
		public.Visibility = gitservice.VisibilityPublic
		for _, test := range []struct {
			name          string
			authorization string
		}{
			{name: "wrong PAT", authorization: basicAuthorization("alice", "wrong")},
			{name: "wrong username", authorization: basicAuthorization("mallory", "git-clone-pat")},
			{name: "revoked PAT", authorization: basicAuthorization("alice", "revoked-pat")},
			{name: "expired PAT", authorization: basicAuthorization("alice", "expired-pat")},
			{name: "disabled user PAT", authorization: basicAuthorization("alice", "disabled-user-pat")},
			{name: "subscription PAT", authorization: basicAuthorization("alice", "sub-read-pat")},
			{name: "account password", authorization: basicAuthorization("alice", "account-password")},
			{name: "JWT bearer", authorization: "Bearer jwt-token"},
			{name: "malformed basic", authorization: "Basic !!!"},
		} {
			t.Run(test.name, func(t *testing.T) {
				engine := newSmartHTTPTestEngineWithResolver(t, Config{StorageRoot: root, gitBinary: fake.path}, &fakeRepositoryResolver{repositories: map[string]gitservice.Repository{"team-a/plugin-one": public}})
				before := fake.readLog(t)
				response := httptest.NewRecorder()
				request := httptest.NewRequest(http.MethodPost, "/git/team-a/plugin-one.git/git-upload-pack", strings.NewReader("request"))
				request.Header.Set("Authorization", test.authorization)
				engine.ServeHTTP(response, request)
				if response.Code != http.StatusUnauthorized {
					t.Fatalf("status = %d, want 401", response.Code)
				}
				if after := fake.readLog(t); after != before {
					t.Fatalf("invalid credentials invoked git; before=%q after=%q", before, after)
				}
			})
		}
	})

	t.Run("authorized private read and write invoke correct services", func(t *testing.T) {
		engine := newSmartHTTPTestEngineWithResolver(t, Config{StorageRoot: root, gitBinary: fake.path}, &fakeRepositoryResolver{repositories: map[string]gitservice.Repository{"team-a/plugin-one": private}})
		for _, tc := range []struct {
			name string
			path string
			want string
		}{
			{name: "read", path: "/git/team-a/plugin-one.git/git-upload-pack", want: "upload-pack"},
			{name: "write", path: "/git/team-a/plugin-one.git/git-receive-pack", want: "receive-pack"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				w := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader("request"))
				req.SetBasicAuth("alice", "git-write-pat")
				engine.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					t.Fatalf("authorized %s status = %d body=%q", tc.name, w.Code, w.Body.String())
				}
				if !strings.Contains(fake.readLog(t), "ARG:"+tc.want+"\n") {
					t.Fatalf("authorized %s did not invoke %s: %q", tc.name, tc.want, fake.readLog(t))
				}
			})
		}
	})

	t.Run("git clone PAT reads but cannot push", func(t *testing.T) {
		engine := newSmartHTTPTestEngineWithResolver(t, Config{StorageRoot: root, gitBinary: fake.path}, &fakeRepositoryResolver{repositories: map[string]gitservice.Repository{"team-a/plugin-one": private}})
		read := httptest.NewRecorder()
		readRequest := httptest.NewRequest(http.MethodPost, "/git/team-a/plugin-one.git/git-upload-pack", strings.NewReader("request"))
		readRequest.SetBasicAuth("alice", "git-clone-pat")
		engine.ServeHTTP(read, readRequest)
		if read.Code != http.StatusOK {
			t.Fatalf("git-clone PAT read status = %d, want 200", read.Code)
		}

		before := fake.readLog(t)
		write := httptest.NewRecorder()
		writeRequest := httptest.NewRequest(http.MethodPost, "/git/team-a/plugin-one.git/git-receive-pack", strings.NewReader("request"))
		writeRequest.SetBasicAuth("alice", "git-clone-pat")
		engine.ServeHTTP(write, writeRequest)
		if write.Code != http.StatusUnauthorized {
			t.Fatalf("git-clone PAT push status = %d, want 401", write.Code)
		}
		if after := fake.readLog(t); after != before {
			t.Fatalf("credential denied push invoked git; before=%q after=%q", before, after)
		}
	})

	t.Run("read only status denies pushes before git", func(t *testing.T) {
		readOnly := private
		readOnly.Status = gitservice.StatusReadOnly
		engine := newSmartHTTPTestEngineWithResolver(t, Config{StorageRoot: root, gitBinary: fake.path}, &fakeRepositoryResolver{repositories: map[string]gitservice.Repository{"team-a/plugin-one": readOnly}})
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/git/team-a/plugin-one.git/git-receive-pack", strings.NewReader("request"))
		req.SetBasicAuth("alice", "git-write-pat")
		engine.ServeHTTP(w, req)
		if w.Code != http.StatusNotFound {
			t.Fatalf("read-only write status = %d, want 404", w.Code)
		}
	})
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

func TestInstallProtectedReceiveHooksRoutesHeadsAndTagsThroughProcReceive(t *testing.T) {
	fake := newFakeGit(t)
	svc, err := NewService(Config{StorageRoot: t.TempDir(), gitBinary: fake.path})
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.InitBareRepository(context.Background(), testRepositoryID); err != nil {
		t.Fatal(err)
	}
	if err := svc.InstallProtectedReceiveHooks(context.Background(), testRepositoryID); err != nil {
		t.Fatal(err)
	}
	log := fake.readLog(t)
	for _, invocation := range []string{
		"ARG:config\nARG:--replace-all\nARG:receive.procReceiveRefs\nARG:refs/heads/",
		"ARG:config\nARG:--add\nARG:receive.procReceiveRefs\nARG:refs/tags/",
	} {
		if !strings.Contains(log, invocation) {
			t.Fatalf("Git invocation log does not contain %q:\n%s", invocation, log)
		}
	}
}

func TestModConfigExposesRuntimeDependencies(t *testing.T) {
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
		t.Fatalf("exported Git config fields = %#v, want StorageRoot", exported)
	}
	for _, field := range exported {
		want := "storageRoot"
		if got := field.Tag.Get("yaml"); got != want {
			t.Fatalf("Config.%s yaml tag = %q, want %q", field.Name, got, want)
		}
		if got := field.Tag.Get("mapstructure"); got != want {
			t.Fatalf("Config.%s mapstructure tag = %q, want %q", field.Name, got, want)
		}
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
	gitPATAuthenticator := auth.GitPATAuthenticator(fakeGitAuthenticator{})
	authorizer := auth.Authorizer(fakeGitAuthorizer{})
	receiveCoordinator := gitservice.ReceiveCoordinator(&recordingReceiveCoordinator{coordination: &recordingReceiveCoordination{}})
	hub.Map(&engine, &resolver, &distributionResolver, &gitPATAuthenticator, &authorizer, &receiveCoordinator)
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
	gitPATAuthenticator := auth.GitPATAuthenticator(fakeGitAuthenticator{})
	authorizer := auth.Authorizer(fakeGitAuthorizer{})
	receiveCoordinator := gitservice.ReceiveCoordinator(&recordingReceiveCoordinator{coordination: &recordingReceiveCoordination{}})
	hub.Map(&engine, &resolver, &distributionResolver, &gitPATAuthenticator, &authorizer, &receiveCoordinator)

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
		{name: "receive-pack advertisement requires authentication", method: http.MethodGet, path: "/git/team-a/plugin-one.git/info/refs?service=git-receive-pack", want: http.StatusUnauthorized},
		{name: "receive-pack rpc requires authentication", method: http.MethodPost, path: "/git/team-a/plugin-one.git/git-receive-pack", want: http.StatusUnauthorized},
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

func TestRealGitSmartHTTPAuthorizedPushInteroperability(t *testing.T) {
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

	engine := newSmartHTTPTestEngineWithResolver(t, Config{StorageRoot: root, gitBinary: gitBinary}, &fakeRepositoryResolver{repositories: map[string]gitservice.Repository{
		"team-a/plugin-one": {ID: testRepositoryID, NamespaceID: "namespace-a", OwnerUserID: "user-alice", Slug: "plugin-one", Visibility: "private", Status: gitservice.StatusReady},
	}})
	server := httptest.NewServer(engine)
	defer server.Close()

	worktree := t.TempDir()
	runGitCommand(t, gitBinary, worktree, "init")
	runGitCommand(t, gitBinary, worktree, "config", "user.name", "Marketplace Test")
	runGitCommand(t, gitBinary, worktree, "config", "user.email", "marketplace@example.invalid")
	runGitCommand(t, gitBinary, worktree, "config", "commit.gpgSign", "false")
	runGitCommand(t, gitBinary, worktree, "config", "tag.gpgSign", "false")
	if err := os.WriteFile(filepath.Join(worktree, "README"), []byte("private push\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifestDirectory := filepath.Join(worktree, ".claude-plugin")
	if err := os.MkdirAll(manifestDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifestDirectory, "plugin.json"), []byte(`{"name":"plugin-one","description":"receive quarantine fixture"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitCommand(t, gitBinary, worktree, "add", "README", ".claude-plugin/plugin.json")
	runGitCommand(t, gitBinary, worktree, "commit", "-m", "initial")
	runGitCommand(t, gitBinary, worktree, "tag", "v1.0.0")
	runGitCommand(t, gitBinary, worktree, "remote", "add", "origin", strings.Replace(server.URL, "http://", "http://alice:git-write-pat@", 1)+"/git/team-a/plugin-one.git")
	runGitCommand(t, gitBinary, worktree, "push", "origin", "HEAD:refs/heads/main", "refs/tags/v1.0.0")

	cloneDir := filepath.Join(t.TempDir(), "clone")
	runGitCommand(t, gitBinary, "", "clone", strings.Replace(server.URL, "http://", "http://alice:git-write-pat@", 1)+"/git/team-a/plugin-one.git", cloneDir)
	content, err := os.ReadFile(filepath.Join(cloneDir, "README"))
	if err != nil || string(content) != "private push\n" {
		t.Fatalf("authorized clone content = %q, err=%v", content, err)
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
	runGitCommand(t, gitBinary, worktree, "config", "commit.gpgSign", "false")
	runGitCommand(t, gitBinary, worktree, "config", "tag.gpgSign", "false")
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

func basicAuthorization(username, plaintext string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+plaintext))
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
	return newSmartHTTPTestEngineWithDependencies(t, config, repositoryResolver, fakeGitAuthenticator{})
}

func newSmartHTTPTestEngineWithDependencies(t *testing.T, config Config, repositoryResolver gitservice.RepositoryResolver, gitPATAuthenticator auth.GitPATAuthenticator) *jinengine.Engine {
	t.Helper()
	engine := jinengine.New()
	hub := kernel.Hub{Injector: inject.New()}
	resolver := repositoryResolver
	distributionResolver := distributionservice.Resolver(fakeDistributionResolver{})
	authenticator := gitPATAuthenticator
	authorizer := auth.Authorizer(fakeGitAuthorizer{})
	receiveCoordinator := gitservice.ReceiveCoordinator(&recordingReceiveCoordinator{coordination: &recordingReceiveCoordination{}})
	hub.Map(&engine, &resolver, &distributionResolver, &authenticator, &authorizer, &receiveCoordinator)
	mod := &Mod{config: config}
	if err := mod.Init(&hub); err != nil {
		t.Fatalf("Mod.Init() error = %v", err)
	}
	if err := mod.Load(&hub); err != nil {
		t.Fatalf("Mod.Load() error = %v", err)
	}
	return engine
}

type typedNilAuthenticator struct{}

func (*typedNilAuthenticator) AuthenticateGitPAT(context.Context, string, string, auth.GitOperation) (auth.Principal, error) {
	return auth.Principal{}, errors.New("not called")
}

type typedNilAuthorizer struct{}

func (*typedNilAuthorizer) Authorize(context.Context, auth.Principal, auth.Action, auth.ResourceRef) error {
	return errors.New("not called")
}

type typedNilDistributionResolver struct{}

func (*typedNilDistributionResolver) ResolvePlugin(context.Context, uuid.UUID) (distributionservice.PluginGrant, error) {
	return distributionservice.PluginGrant{}, distributionservice.ErrNotFound
}

func (*typedNilDistributionResolver) ResolveMarketplace(context.Context, distributionservice.MarketplacePublicKey) (distributionservice.MarketplaceGrant, error) {
	return distributionservice.MarketplaceGrant{}, distributionservice.ErrNotFound
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

type recordingGitPATAuthenticator struct {
	principal  auth.Principal
	err        error
	operations []auth.GitOperation
}

func (authenticator *recordingGitPATAuthenticator) AuthenticateGitPAT(_ context.Context, _, _ string, operation auth.GitOperation) (auth.Principal, error) {
	authenticator.operations = append(authenticator.operations, operation)
	return authenticator.principal, authenticator.err
}

func gitPATPrincipal(t *testing.T, actions ...auth.Action) auth.Principal {
	t.Helper()
	principal, err := auth.NewUserPrincipal("user-alice", "alice", auth.CredentialPAT, auth.RestrictedScopes(actions...))
	if err != nil {
		t.Fatal(err)
	}
	return principal
}

type fakeGitAuthenticator struct{}

func (fakeGitAuthenticator) AuthenticateGitPAT(_ context.Context, username, plaintext string, operation auth.GitOperation) (auth.Principal, error) {
	if username != "alice" {
		return auth.Principal{}, errors.New("invalid credentials")
	}
	switch plaintext {
	case "git-write-pat":
		return auth.NewUserPrincipal("user-alice", username, auth.CredentialPAT, auth.RestrictedScopes(auth.ActionPluginRead, auth.ActionPluginWrite))
	case "git-clone-pat":
		if operation != auth.GitOperationRead {
			return auth.Principal{}, errors.New("invalid credentials")
		}
		return auth.NewUserPrincipal("user-alice", username, auth.CredentialPAT, auth.RestrictedScopes(auth.ActionPluginRead))
	case "sub-read-pat", "account-password", "jwt-token", "wrong", "revoked-pat", "expired-pat", "disabled-user-pat":
		return auth.Principal{}, errors.New("invalid credentials")
	default:
		return auth.Principal{}, errors.New("invalid credentials")
	}
}

type fakeGitAuthorizer struct{}

func (fakeGitAuthorizer) Authorize(_ context.Context, principal auth.Principal, action auth.Action, resource auth.ResourceRef) error {
	if resource.Type != auth.ResourcePlugin || resource.ID != testRepositoryID {
		return errors.New("unknown plugin")
	}
	if action == auth.ActionPluginRead && principal.IsAnonymous() {
		return nil
	}
	if principal.IsUser() && principal.Allows(action) {
		return nil
	}
	return errors.New("denied")
}

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
  --git-dir=*)
    if [ "${2-}" = "symbolic-ref" ] && [ "${3-}" = "HEAD" ] && [ "${4-}" = "refs/heads/main" ]; then
      exit 0
    fi
    if [ "${2-}" = "config" ]; then
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
