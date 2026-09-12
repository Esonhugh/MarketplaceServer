package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
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
			inspector, err := NewPluginSourceInspector(fixture.service)
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

		})
	}
}

func TestPluginSourceInspectorReadsObjectsFromReceiveQuarantineOnly(t *testing.T) {
	fixture := newPluginSourceFixture(t, pluginSourceFixtureOptions{
		manifest: `{"name":"plugin-one","description":"fixture"}`,
	})
	if err := os.WriteFile(filepath.Join(fixture.worktree, "quarantine-only.txt"), []byte("new object\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	pluginSourceGit(t, fixture.gitBinary, fixture.worktree, "add", ".")
	pluginSourceGit(t, fixture.gitBinary, fixture.worktree, "commit", "-m", "quarantine candidate")
	commitObjectID := strings.TrimSpace(pluginSourceGitOutput(t, fixture.gitBinary, fixture.worktree, "rev-parse", "HEAD"))

	repositoryPath, err := fixture.service.existingRepositoryPath(pluginSourceRepositoryID)
	if err != nil {
		t.Fatal(err)
	}
	objects := repositoryReceiveObjects{gitBinary: fixture.gitBinary, repositoryPath: repositoryPath}
	if _, err := objects.PeelCommit(context.Background(), commitObjectID); err == nil {
		t.Fatal("PeelCommit() found an unreceived object without the receive object environment")
	}
	inspector := &pluginSourceInspector{service: fixture.service}
	if _, err := inspector.inspectCommit(context.Background(), repositoryPath, commitObjectID, commitObjectID, "plugin-one", pluginSourceGitEnv()); err == nil {
		t.Fatal("inspectCommit() found an unreceived object without the receive object environment")
	}

	environment := receiveObjectEnvironment{
		ObjectDirectory:            filepath.Join(fixture.worktree, ".git", "objects"),
		AlternateObjectDirectories: filepath.Join(repositoryPath, "objects"),
		QuarantinePath:             filepath.Join(fixture.worktree, ".git", "objects"),
	}
	objects.environment = environment
	peeled, err := objects.PeelCommit(context.Background(), commitObjectID)
	if err != nil {
		t.Fatalf("PeelCommit() in quarantine error = %v", err)
	}
	if peeled != commitObjectID {
		t.Fatalf("PeelCommit() = %q, want %q", peeled, commitObjectID)
	}
	inspection, err := inspector.inspectCommit(context.Background(), repositoryPath, commitObjectID, peeled, "plugin-one", environment.gitEnvironment())
	if err != nil {
		t.Fatalf("inspectCommit() in quarantine error = %v", err)
	}
	if inspection.CommitObjectID != commitObjectID {
		t.Fatalf("inspectCommit() commit = %q, want %q", inspection.CommitObjectID, commitObjectID)
	}
}

func TestPluginSourceGitEnvironmentDoesNotInheritReceiveObjectVariables(t *testing.T) {
	for _, name := range []string{"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_QUARANTINE_PATH"} {
		t.Setenv(name, "/untrusted/global/value")
	}
	for _, entry := range pluginSourceGitEnv() {
		if strings.HasPrefix(entry, "GIT_OBJECT_DIRECTORY=") || strings.HasPrefix(entry, "GIT_ALTERNATE_OBJECT_DIRECTORIES=") || strings.HasPrefix(entry, "GIT_QUARANTINE_PATH=") {
			t.Fatalf("clean plugin environment inherited receive variable %q", entry)
		}
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
	inspector, err := NewPluginSourceInspector(fixture.service)
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

func TestPluginSourceInspectorRejectsInvalidInputs(t *testing.T) {
	fixture := newPluginSourceFixture(t, pluginSourceFixtureOptions{manifest: `{"name":"plugin-one"}`})
	inspector, err := NewPluginSourceInspector(fixture.service)
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
}

func TestPluginSourceInspectorRejectsMissingAndNonCommitTags(t *testing.T) {
	t.Run("missing tag", func(t *testing.T) {
		fixture := newPluginSourceFixture(t, pluginSourceFixtureOptions{manifest: `{"name":"plugin-one"}`})
		inspector, err := NewPluginSourceInspector(fixture.service)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inspector.InspectPluginSource(context.Background(), pluginSourceRepositoryID, "v2.0.0", "plugin-one"); err == nil {
			t.Fatal("InspectPluginSource() succeeded for a missing tag; want error")
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
		inspector, err := NewPluginSourceInspector(fixture.service)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inspector.InspectPluginSource(context.Background(), pluginSourceRepositoryID, "v2.0.0", "plugin-one"); err == nil {
			t.Fatal("InspectPluginSource() succeeded for a tag pointing to a blob; want error")
		}
	})
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
		{name: "unknown field", contents: pluginSourceString(`{"name":"plugin-one","unexpected":true}`)},
	} {
		t.Run(manifest.name, func(t *testing.T) {
			fixture := newPluginSourceFixture(t, pluginSourceFixtureOptions{manifestPointer: manifest.contents})
			inspector, err := NewPluginSourceInspector(fixture.service)
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
	files           map[string]string
	symlinks        map[string]string
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
	for name, contents := range options.files {
		path := filepath.Join(worktree, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, target := range options.symlinks {
		path := filepath.Join(worktree, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, path); err != nil {
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

func TestPluginProfileRejectsMalformedSkill(t *testing.T) {
	fixture := newPluginSourceFixture(t, pluginSourceFixtureOptions{manifest: `{"name":"plugin-one"}`, files: map[string]string{"skills/example/SKILL.md": "not frontmatter"}})
	inspector, err := NewPluginSourceInspector(fixture.service)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inspector.InspectPluginSource(context.Background(), pluginSourceRepositoryID, "v1.2.3", "plugin-one"); err == nil {
		t.Fatal("malformed skill accepted")
	}
}

func TestPluginSourceTreeOutputLimit(t *testing.T) {
	writer := &pluginSourceTreeWriter{remaining: 4}
	if n, err := writer.Write([]byte("1234")); n != 4 || err != nil {
		t.Fatalf("at limit: %d %v", n, err)
	}
	if _, err := writer.Write([]byte("5")); err == nil || writer.Len() != 4 {
		t.Fatal("tree output exceeded its memory budget")
	}
}

func TestPluginProfileV1Fixtures(t *testing.T) {
	const skill = "---\ndescription: Useful skill\n---\nInstructions.\n"
	cases := []struct {
		name, manifest  string
		files, symlinks map[string]string
		valid           bool
	}{
		{name: "minimal", valid: true},
		{name: "json depth limit", manifest: `{"name":"plugin-one","metadata":{"x":` + strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65) + `}}`},
		{name: "frontmatter size limit", files: map[string]string{"skills/one/SKILL.md": "---\ndescription: " + strings.Repeat("x", 64<<10) + "\n---\n"}},
		{name: "multiple skills", files: map[string]string{"skills/one/SKILL.md": skill, "skills/two/SKILL.md": "---\nname: second\ndescription: |\n  Second skill\nallowed-tools: [Read, Bash]\ndisable-model-invocation: true\nuser-invocable: false\ncontext: fork\nagent: Explore\nmodel: inherit\nargument-hint: '[file]'\nmetadata: {owner: team}\n---\nBody"}, valid: true},
		{name: "custom skills", manifest: `{"name":"plugin-one","skills":["./extra"]}`, files: map[string]string{"extra/one/SKILL.md": skill}, valid: true},
		{name: "string skills", manifest: `{"name":"plugin-one","skills":"./extra"}`, files: map[string]string{"extra/one/SKILL.md": skill}, valid: true},
		{name: "all manifest fields", manifest: `{"name":"plugin-one","displayName":"Example","version":"1.2.3-beta.1+build.2","description":"Demo","author":{"name":"A","email":"a@example.invalid","url":"https://example.invalid"},"homepage":"https://example.invalid","repository":"https://example.invalid/repo","license":"MIT","keywords":["test"],"metadata":{"x":true},"defaultEnabled":true}`, valid: true},
		{name: "wrong type", manifest: `{"name":"plugin-one","description":2}`},
		{name: "null", manifest: `{"name":"plugin-one","description":null}`},
		{name: "version", manifest: `{"name":"plugin-one","version":"1.2"}`},
		{name: "version leading zero", manifest: `{"name":"plugin-one","version":"1.2.3-01"}`},
		{name: "name mismatch", manifest: `{"name":"other"}`},
		{name: "duplicate json", manifest: `{"name":"plugin-one","name":"plugin-one"}`},
		{name: "nested duplicate json", manifest: `{"name":"plugin-one","metadata":{"items":[{"x":1,"x":2}]}}`},
		{name: "case folded json", manifest: `{"Name":"plugin-one"}`},
		{name: "json trailing", manifest: `{"name":"plugin-one"} {}`},
		{name: "unknown author", manifest: `{"name":"plugin-one","author":{"secret":true}}`},
		{name: "keywords type", manifest: `{"name":"plugin-one","keywords":[null]}`},
		{name: "metadata type", manifest: `{"name":"plugin-one","metadata":[]}`},
		{name: "manifest non utf8", manifest: "{\"name\":\"plugin-one\",\"description\":\"\xff\"}"},
		{name: "skills type", manifest: `{"name":"plugin-one","skills":true}`},
		{name: "skills null", manifest: `{"name":"plugin-one","skills":null}`},
		{name: "path escape", manifest: `{"name":"plugin-one","skills":"./../outside"}`},
		{name: "absolute path", manifest: `{"name":"plugin-one","skills":"/private/outside"}`},
		{name: "missing directory", manifest: `{"name":"plugin-one","skills":"./missing"}`},
		{name: "missing skill file", files: map[string]string{"skills/one/readme.txt": "text"}},
		{name: "root skill", files: map[string]string{"SKILL.md": skill}},
		{name: "single skills root", files: map[string]string{"skills/SKILL.md": skill}},
		{name: "yaml malformed", files: map[string]string{"skills/one/SKILL.md": "---\ndescription: [\n---\n"}},
		{name: "yaml sequence", files: map[string]string{"skills/one/SKILL.md": "---\n- description\n---\n"}},
		{name: "yaml duplicate", files: map[string]string{"skills/one/SKILL.md": "---\ndescription: one\ndescription: two\n---\n"}},
		{name: "yaml unknown", files: map[string]string{"skills/one/SKILL.md": "---\ndescription: useful\nunknown: true\n---\n"}},
		{name: "yaml type", files: map[string]string{"skills/one/SKILL.md": "---\ndescription: 42\n---\n"}},
		{name: "yaml hook unsupported", files: map[string]string{"skills/one/SKILL.md": "---\ndescription: useful\nhooks: {}\n---\n"}},
		{name: "yaml alias", files: map[string]string{"skills/one/SKILL.md": "---\ndescription: &d useful\nname: *d\n---\n"}},
		{name: "yaml merge", files: map[string]string{"skills/one/SKILL.md": "---\ndescription: useful\n<<: {name: one}\n---\n"}},
		{name: "yaml metadata duplicate", files: map[string]string{"skills/one/SKILL.md": "---\ndescription: useful\nmetadata: {owner: one, owner: two}\n---\n"}},
		{name: "yaml tools alias", files: map[string]string{"skills/one/SKILL.md": "---\ndescription: &d useful\nallowed-tools: [*d]\n---\n"}},
		{name: "yaml document boundary", files: map[string]string{"skills/one/SKILL.md": "---\ndescription: useful\n...\nname: one\n---\n"}},
		{name: "crlf", files: map[string]string{"skills/one/SKILL.md": strings.ReplaceAll(skill, "\n", "\r\n")}, valid: true},
		{name: "nul body", files: map[string]string{"skills/one/SKILL.md": skill + "\x00"}},
		{name: "bom boundary", files: map[string]string{"skills/one/SKILL.md": "\ufeff" + skill}},
		{name: "delimiter whitespace", files: map[string]string{"skills/one/SKILL.md": "--- \ndescription: useful\n---\n"}},
		{name: "missing description", files: map[string]string{"skills/one/SKILL.md": "---\nname: one\n---\n"}},
		{name: "empty description", files: map[string]string{"skills/one/SKILL.md": "---\ndescription: ''\n---\n"}},
		{name: "missing closing delimiter", files: map[string]string{"skills/one/SKILL.md": "---\ndescription: useful\n"}},
		{name: "not first line", files: map[string]string{"skills/one/SKILL.md": "\n" + skill}},
		{name: "non utf8 skill", files: map[string]string{"skills/one/SKILL.md": skill + "\xff"}},
		{name: "invalid fallback", files: map[string]string{"skills/Bad_Name/SKILL.md": skill}},
		{name: "duplicate name", files: map[string]string{"skills/one/SKILL.md": skill, "skills/two/SKILL.md": "---\nname: one\ndescription: useful\n---\n"}},
		{name: "escaping symlink", symlinks: map[string]string{"escape": "../outside"}},
		{name: "chained symlink escape", symlinks: map[string]string{"alias": ".", "escape": "alias/../outside"}},
		{name: "safe auxiliary symlink", files: map[string]string{"resource.txt": "data"}, symlinks: map[string]string{"alias": "resource.txt"}, valid: true},
		{name: "skill symlink", files: map[string]string{"source.md": skill}, symlinks: map[string]string{"skills/one/SKILL.md": "../../source.md"}},
		{name: "directory symlink", files: map[string]string{"extra/one/SKILL.md": skill}, symlinks: map[string]string{"skills": "extra"}},
	}
	for _, component := range []string{"commands", "agents", "workflows", "hooks", "output-styles", "themes", "monitors", "bin", "settings"} {
		cases = append(cases, struct {
			name, manifest  string
			files, symlinks map[string]string
			valid           bool
		}{name: "unsupported " + component, files: map[string]string{component + "/entry": "text"}})
	}
	for _, component := range []string{".mcp.json", ".lsp.json", "settings.json"} {
		cases = append(cases, struct {
			name, manifest  string
			files, symlinks map[string]string
			valid           bool
		}{name: "unsupported " + component, files: map[string]string{component: "{}"}})
	}
	for _, component := range []string{"commands", "agents", "workflows", "hooks", "mcpServers", "lspServers", "outputStyles", "themes", "monitors", "bin", "settings"} {
		cases = append(cases, struct {
			name, manifest  string
			files, symlinks map[string]string
			valid           bool
		}{name: "manifest unsupported " + component, manifest: `{"name":"plugin-one","` + component + `":{}}`})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			manifest := tc.manifest
			if manifest == "" {
				manifest = `{"name":"plugin-one"}`
			}
			fixture := newPluginSourceFixture(t, pluginSourceFixtureOptions{manifest: manifest, files: tc.files, symlinks: tc.symlinks})
			inspector, err := NewPluginSourceInspector(fixture.service)
			if err != nil {
				t.Fatal(err)
			}
			_, err = inspector.InspectPluginSource(context.Background(), pluginSourceRepositoryID, "v1.2.3", "plugin-one")
			if tc.valid {
				if err != nil {
					t.Fatal(err)
				}
			} else if err == nil || err.Error() != ErrPluginSourceInvalid.Error() {
				t.Fatalf("want stable redacted rejection, got %v", err)
			}
		})
	}
}
