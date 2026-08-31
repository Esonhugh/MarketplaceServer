package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
)

const pluginSourceRepositoryID = "01J9ZQ2M8K4V7T6P5N3R1X0ABC"

func TestPluginSourceInspectorAllowsCanonicalLightweightAndAnnotatedTags(t *testing.T) {
	for _, tagType := range []struct {
		name      string
		annotated bool
	}{
		{name: "lightweight"},
		{name: "annotated", annotated: true},
	} {
		t.Run(tagType.name, func(t *testing.T) {
			fixture := newPluginSourceFixture(t, pluginSourceFixtureOptions{
				annotated: tagType.annotated,
				manifest: `{
  "name": "plugin-one",
  "description": "fixture"
}
`,
			})
			validator := newPluginSourceValidator(t, "plugin validation passed", 0)
			inspector, err := NewPluginSourceInspector(fixture.service, validator.path)
			if err != nil {
				t.Fatalf("NewPluginSourceInspector() error = %v", err)
			}

			inspection, err := inspector.InspectPluginSource(context.Background(), pluginSourceRepositoryID, "v1.2.3", "plugin-one")
			if err != nil {
				t.Fatalf("InspectPluginSource() error = %v", err)
			}
			if inspection.RawTagObjectID != fixture.rawTagObjectID {
				t.Fatalf("raw ref object ID = %q, want %q", inspection.RawTagObjectID, fixture.rawTagObjectID)
			}
			if inspection.CommitObjectID != fixture.commitObjectID {
				t.Fatalf("peeled commit object ID = %q, want %q", inspection.CommitObjectID, fixture.commitObjectID)
			}
			if tagType.annotated && inspection.RawTagObjectID == inspection.CommitObjectID {
				t.Fatal("annotated tag raw object ID was not preserved separately from its peeled commit")
			}
			if got, want := string(inspection.ManifestSnapshot), fixture.manifest; got != want {
				t.Fatalf("manifest snapshot = %q, want %q", got, want)
			}
			sum := sha256.Sum256([]byte(fixture.manifest))
			if got, want := inspection.ManifestDigest, hex.EncodeToString(sum[:]); got != want {
				t.Fatalf("manifest digest = %q, want %q", got, want)
			}

			invocation := validator.invocation(t)
			if got, want := strings.Join(invocation.args, "\x00"), "plugin\x00validate\x00"+invocation.directory+"\x00--strict"; got != want {
				t.Fatalf("validator arguments = %#v, want claude plugin validate <dir> --strict", invocation.args)
			}
			if !strings.HasPrefix(filepath.Base(filepath.Dir(invocation.directory)), "marketplace-plugin-source-") {
				t.Fatalf("validator directory = %q, want isolated materialized directory", invocation.directory)
			}
			if _, err := os.Stat(invocation.directory); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("validator materialization directory remains after inspection: %v", err)
			}
			if invocation.gitConfigNoSystem != "1" || invocation.gitTerminalPrompt != "0" {
				t.Fatalf("validator environment = %#v, want minimal Git environment", invocation)
			}
		})
	}
}

func TestPluginSourceInspectorSupportsSHA256Repositories(t *testing.T) {
	fixture := newPluginSourceFixture(t, pluginSourceFixtureOptions{
		objectFormat: "sha256",
		annotated:    true,
		manifest:     `{"name":"plugin-one"}`,
	})
	if len(fixture.rawTagObjectID) != 64 || len(fixture.commitObjectID) != 64 {
		t.Fatalf("SHA-256 fixture object IDs = %q / %q, want 64 hex characters", fixture.rawTagObjectID, fixture.commitObjectID)
	}
	validator := newPluginSourceValidator(t, "plugin validation passed", 0)
	inspector, err := NewPluginSourceInspector(fixture.service, validator.path)
	if err != nil {
		t.Fatal(err)
	}

	inspection, err := inspector.InspectPluginSource(context.Background(), pluginSourceRepositoryID, "v1.2.3", "plugin-one")
	if err != nil {
		t.Fatalf("InspectPluginSource() error = %v", err)
	}
	if inspection.RawTagObjectID != fixture.rawTagObjectID || inspection.CommitObjectID != fixture.commitObjectID {
		t.Fatalf("SHA-256 inspection = %#v, want raw/commit %q/%q", inspection, fixture.rawTagObjectID, fixture.commitObjectID)
	}
}

func TestPluginSourceInspectorRejectsInvalidInputsBeforeRunningValidator(t *testing.T) {
	fixture := newPluginSourceFixture(t, pluginSourceFixtureOptions{manifest: `{"name":"plugin-one"}`})
	validator := newPluginSourceValidator(t, "plugin validation passed", 0)
	inspector, err := NewPluginSourceInspector(fixture.service, validator.path)
	if err != nil {
		t.Fatal(err)
	}

	for _, request := range []struct {
		name         string
		repositoryID string
		tag          string
		slug         string
	}{
		{name: "repository traversal", repositoryID: "../../outside", tag: "v1.2.3", slug: "plugin-one"},
		{name: "repository suffix", repositoryID: pluginSourceRepositoryID + ".git", tag: "v1.2.3", slug: "plugin-one"},
		{name: "noncanonical tag", repositoryID: pluginSourceRepositoryID, tag: "release-1.2.3", slug: "plugin-one"},
		{name: "tag traversal", repositoryID: pluginSourceRepositoryID, tag: "v1.2.3/../../main", slug: "plugin-one"},
		{name: "invalid slug", repositoryID: pluginSourceRepositoryID, tag: "v1.2.3", slug: "Plugin-One"},
	} {
		t.Run(request.name, func(t *testing.T) {
			if _, err := inspector.InspectPluginSource(context.Background(), request.repositoryID, request.tag, request.slug); err == nil {
				t.Fatal("InspectPluginSource() succeeded; want error")
			}
		})
	}
	if invocation := validator.tryInvocation(t); invocation != nil {
		t.Fatalf("invalid source request ran validator: %#v", invocation)
	}
}

func TestPluginSourceInspectorRejectsMissingAndNonCommitTags(t *testing.T) {
	t.Run("missing tag", func(t *testing.T) {
		fixture := newPluginSourceFixture(t, pluginSourceFixtureOptions{manifest: `{"name":"plugin-one"}`})
		validator := newPluginSourceValidator(t, "plugin validation passed", 0)
		inspector, err := NewPluginSourceInspector(fixture.service, validator.path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inspector.InspectPluginSource(context.Background(), pluginSourceRepositoryID, "v2.0.0", "plugin-one"); err == nil {
			t.Fatal("InspectPluginSource() succeeded for a missing tag; want error")
		}
		if invocation := validator.tryInvocation(t); invocation != nil {
			t.Fatalf("missing tag ran validator: %#v", invocation)
		}
	})

	t.Run("tag does not peel to a commit", func(t *testing.T) {
		fixture := newPluginSourceFixture(t, pluginSourceFixtureOptions{manifest: `{"name":"plugin-one"}`})
		blobID := strings.TrimSpace(pluginSourceGitOutput(t, fixture.gitBinary, fixture.worktree, "hash-object", "-w", "--stdin"))
		if blobID == "" {
			t.Fatal("hash-object returned an empty blob ID")
		}
		pluginSourceGit(t, fixture.gitBinary, fixture.worktree, "tag", "v2.0.0", blobID)
		pluginSourceGit(t, fixture.gitBinary, fixture.worktree, "push", "origin", "refs/tags/v2.0.0")

		validator := newPluginSourceValidator(t, "plugin validation passed", 0)
		inspector, err := NewPluginSourceInspector(fixture.service, validator.path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inspector.InspectPluginSource(context.Background(), pluginSourceRepositoryID, "v2.0.0", "plugin-one"); err == nil {
			t.Fatal("InspectPluginSource() succeeded for a tag pointing to a blob; want error")
		}
		if invocation := validator.tryInvocation(t); invocation != nil {
			t.Fatalf("non-commit tag ran validator: %#v", invocation)
		}
	})
}

func TestPluginSourceInspectorRejectsValidatorWarningsErrorsAndNonzeroWithoutLeakingOutput(t *testing.T) {
	for _, outcome := range []struct {
		name   string
		output string
		exit   int
	}{
		{name: "warning", output: "warning: source /private/validator-output", exit: 0},
		{name: "error", output: "error: source /private/validator-output", exit: 0},
		{name: "nonzero", output: "untrusted validator output /private/validator-output", exit: 17},
	} {
		t.Run(outcome.name, func(t *testing.T) {
			fixture := newPluginSourceFixture(t, pluginSourceFixtureOptions{manifest: `{"name":"plugin-one"}`})
			validator := newPluginSourceValidator(t, outcome.output, outcome.exit)
			inspector, err := NewPluginSourceInspector(fixture.service, validator.path)
			if err != nil {
				t.Fatal(err)
			}

			_, err = inspector.InspectPluginSource(context.Background(), pluginSourceRepositoryID, "v1.2.3", "plugin-one")
			if err == nil {
				t.Fatal("InspectPluginSource() succeeded; want validator rejection")
			}
			if got := err.Error(); strings.Contains(got, "/private/validator-output") || strings.Contains(got, "warning:") || strings.Contains(got, "error:") {
				t.Fatalf("validator output leaked through error: %q", got)
			}
			invocation := validator.invocation(t)
			if _, statErr := os.Stat(invocation.directory); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatalf("validator materialization directory remains after rejection: %v", statErr)
			}
		})
	}
}

func TestPluginSourceInspectorRejectsInvalidAndMismatchedManifest(t *testing.T) {
	for _, manifest := range []struct {
		name     string
		contents *string
	}{
		{name: "missing"},
		{name: "malformed", contents: pluginSourceString(`{"name":`)},
		{name: "missing name", contents: pluginSourceString(`{"description":"fixture"}`)},
		{name: "wrong case", contents: pluginSourceString(`{"name":"Plugin-One"}`)},
	} {
		t.Run(manifest.name, func(t *testing.T) {
			fixture := newPluginSourceFixture(t, pluginSourceFixtureOptions{manifestPointer: manifest.contents})
			validator := newPluginSourceValidator(t, "plugin validation passed", 0)
			inspector, err := NewPluginSourceInspector(fixture.service, validator.path)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := inspector.InspectPluginSource(context.Background(), pluginSourceRepositoryID, "v1.2.3", "plugin-one"); err == nil {
				t.Fatal("InspectPluginSource() succeeded; want manifest rejection")
			}
		})
	}
}

type pluginSourceFixtureOptions struct {
	annotated       bool
	objectFormat    string
	manifest        string
	manifestPointer *string
}

type pluginSourceFixture struct {
	service        *Service
	gitBinary      string
	worktree       string
	manifest       string
	rawTagObjectID string
	commitObjectID string
}

func newPluginSourceFixture(t *testing.T, options pluginSourceFixtureOptions) pluginSourceFixture {
	t.Helper()
	gitBinary := requirePluginSourceGit(t)
	root := t.TempDir()
	service, err := NewService(Config{StorageRoot: root, gitBinary: gitBinary})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	repositoryPath, err := service.repositoryPath(pluginSourceRepositoryID)
	if err != nil {
		t.Fatal(err)
	}
	if options.objectFormat == "sha256" {
		if err := os.MkdirAll(filepath.Dir(repositoryPath), 0o700); err != nil {
			t.Fatal(err)
		}
		command := exec.Command(gitBinary, "init", "--bare", "--initial-branch=main", "--object-format=sha256", repositoryPath)
		command.Env = gitEnv()
		if _, err := command.CombinedOutput(); err != nil {
			t.Skipf("Git does not support SHA-256 repositories: %v", err)
		}
	} else if err := service.InitBareRepository(context.Background(), pluginSourceRepositoryID); err != nil {
		t.Fatalf("InitBareRepository() error = %v", err)
	}

	worktree := filepath.Join(t.TempDir(), "work")
	pluginSourceGit(t, gitBinary, "", "clone", repositoryPath, worktree)
	pluginSourceGit(t, gitBinary, worktree, "config", "user.name", "Plugin Source Test")
	pluginSourceGit(t, gitBinary, worktree, "config", "user.email", "plugin-source@example.invalid")
	pluginSourceGit(t, gitBinary, worktree, "config", "commit.gpgSign", "false")
	pluginSourceGit(t, gitBinary, worktree, "config", "tag.gpgSign", "false")
	if err := os.WriteFile(filepath.Join(worktree, "README.md"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := options.manifest
	if options.manifestPointer != nil {
		manifest = *options.manifestPointer
	}
	if options.manifestPointer != nil || options.manifest != "" {
		manifestPath := filepath.Join(worktree, ".claude-plugin", "plugin.json")
		if err := os.MkdirAll(filepath.Dir(manifestPath), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pluginSourceGit(t, gitBinary, worktree, "add", ".")
	pluginSourceGit(t, gitBinary, worktree, "commit", "-m", "fixture")
	commitObjectID := strings.TrimSpace(pluginSourceGitOutput(t, gitBinary, worktree, "rev-parse", "HEAD"))
	if options.annotated {
		pluginSourceGit(t, gitBinary, worktree, "tag", "-a", "v1.2.3", "-m", "release")
	} else {
		pluginSourceGit(t, gitBinary, worktree, "tag", "v1.2.3")
	}
	pluginSourceGit(t, gitBinary, worktree, "push", "origin", "HEAD:refs/heads/main", "refs/tags/v1.2.3")
	rawTagObjectID := strings.TrimSpace(pluginSourceGitOutput(t, gitBinary, "", "--git-dir="+repositoryPath, "show-ref", "--verify", "--hash", "refs/tags/v1.2.3"))

	return pluginSourceFixture{
		service:        service,
		gitBinary:      gitBinary,
		worktree:       worktree,
		manifest:       manifest,
		rawTagObjectID: rawTagObjectID,
		commitObjectID: commitObjectID,
	}
}

type pluginSourceValidator struct {
	path string
	log  string
}

type pluginSourceValidatorInvocation struct {
	args              []string
	directory         string
	gitConfigNoSystem string
	gitTerminalPrompt string
}

func newPluginSourceValidator(t *testing.T, output string, exitCode int) pluginSourceValidator {
	t.Helper()
	dir := t.TempDir()
	logPath := filepath.Join(dir, "validator.log")
	path := filepath.Join(dir, "fake-claude")
	script := "#!/bin/sh\n" +
		"set -eu\n" +
		"log=" + pluginSourceShellQuote(logPath) + "\n" +
		"printf 'ARG:%s\\n' \"$@\" > \"$log\"\n" +
		"printf 'GIT_CONFIG_NOSYSTEM:%s\\n' \"${GIT_CONFIG_NOSYSTEM-}\" >> \"$log\"\n" +
		"printf 'GIT_TERMINAL_PROMPT:%s\\n' \"${GIT_TERMINAL_PROMPT-}\" >> \"$log\"\n" +
		"if [ \"$#\" -ne 4 ] || [ \"$1\" != plugin ] || [ \"$2\" != validate ] || [ \"$4\" != --strict ]; then exit 41; fi\n" +
		"if [ ! -f \"$3/.claude-plugin/plugin.json\" ]; then exit 42; fi\n" +
		"printf 'DIR:%s\\n' \"$3\" >> \"$log\"\n" +
		"printf '%s\\n' " + pluginSourceShellQuote(output) + "\n" +
		"exit " + strconv.Itoa(exitCode) + "\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return pluginSourceValidator{path: path, log: logPath}
}

func (v pluginSourceValidator) invocation(t *testing.T) pluginSourceValidatorInvocation {
	t.Helper()
	invocation := v.tryInvocation(t)
	if invocation == nil {
		t.Fatal("validator was not invoked")
	}
	return *invocation
}

func (v pluginSourceValidator) tryInvocation(t *testing.T) *pluginSourceValidatorInvocation {
	t.Helper()
	contents, err := os.ReadFile(v.log)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	invocation := &pluginSourceValidatorInvocation{}
	for _, line := range strings.Split(strings.TrimSpace(string(contents)), "\n") {
		switch {
		case strings.HasPrefix(line, "ARG:"):
			invocation.args = append(invocation.args, strings.TrimPrefix(line, "ARG:"))
		case strings.HasPrefix(line, "DIR:"):
			invocation.directory = strings.TrimPrefix(line, "DIR:")
		case strings.HasPrefix(line, "GIT_CONFIG_NOSYSTEM:"):
			invocation.gitConfigNoSystem = strings.TrimPrefix(line, "GIT_CONFIG_NOSYSTEM:")
		case strings.HasPrefix(line, "GIT_TERMINAL_PROMPT:"):
			invocation.gitTerminalPrompt = strings.TrimPrefix(line, "GIT_TERMINAL_PROMPT:")
		}
	}
	return invocation
}

func requirePluginSourceGit(t *testing.T) string {
	t.Helper()
	gitBinary, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git binary is not available")
	}
	return gitBinary
}

func pluginSourceGit(t *testing.T, gitBinary, dir string, args ...string) {
	t.Helper()
	command := exec.Command(gitBinary, args...)
	command.Dir = dir
	command.Env = gitEnv()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, output)
	}
}

func pluginSourceGitOutput(t *testing.T, gitBinary, dir string, args ...string) string {
	t.Helper()
	command := exec.Command(gitBinary, args...)
	command.Dir = dir
	command.Env = gitEnv()
	output, err := command.Output()
	if err != nil {
		t.Fatalf("git %s failed: %v", strings.Join(args, " "), err)
	}
	return string(output)
}

func pluginSourceShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func pluginSourceString(value string) *string {
	return &value
}

var _ gitservice.PluginSourceInspector = (*pluginSourceInspector)(nil)
