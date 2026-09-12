package git

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
)

func TestParseReceiveCommandsPreservesCompleteSHA256CommandSetAndClassifiesOnlyCanonicalTags(t *testing.T) {
	zero := strings.Repeat("0", 64)
	oldBranch := strings.Repeat("1", 64)
	newBranch := strings.Repeat("2", 64)
	newTag := strings.Repeat("3", 64)
	input := strings.Join([]string{
		oldBranch + " " + newBranch + " refs/heads/main",
		zero + " " + newTag + " refs/tags/v1.2.3-rc.1+build.5",
		zero + " " + newTag + " refs/tags/1.2.3",
	}, "\n") + "\n"

	commands, err := ParseReceiveCommands(strings.NewReader(input), ObjectFormatSHA256)
	if err != nil {
		t.Fatalf("ParseReceiveCommands() error = %v", err)
	}
	if len(commands) != 3 {
		t.Fatalf("command count = %d, want 3", len(commands))
	}
	if commands[0].OldObjectID != oldBranch || commands[0].NewObjectID != newBranch || commands[0].RefName != "refs/heads/main" {
		t.Fatalf("branch command = %#v", commands[0])
	}
	if tag, ok := CanonicalTagFromRef(commands[1].RefName); !ok || tag != "v1.2.3-rc.1+build.5" {
		t.Fatalf("canonical tag = (%q, %v)", tag, ok)
	}
	if tag, ok := CanonicalTagFromRef(commands[2].RefName); ok || tag != "" {
		t.Fatalf("noncanonical tag classified = (%q, %v)", tag, ok)
	}
}

func TestRunPreReceiveSessionAdmitsCompleteCommandSetOrRejectsWholePush(t *testing.T) {
	zero := strings.Repeat("0", 40)
	one := strings.Repeat("1", 40)
	two := strings.Repeat("2", 40)
	input := one + " " + two + " refs/heads/main\n" + zero + " " + two + " refs/tags/v1.0.0\n"
	var admitted []ReceiveCommand
	if err := RunPreReceiveSession(context.Background(), strings.NewReader(input), ObjectFormatSHA1, func(_ context.Context, commands []ReceiveCommand) error {
		admitted = commands
		return nil
	}); err != nil {
		t.Fatalf("RunPreReceiveSession() allow error = %v", err)
	}
	if len(admitted) != 2 || admitted[1].RefName != "refs/tags/v1.0.0" {
		t.Fatalf("admitted commands = %#v", admitted)
	}

	err := RunPreReceiveSession(context.Background(), strings.NewReader(input), ObjectFormatSHA1, func(context.Context, []ReceiveCommand) error {
		return errors.New("validator leaked /private/quarantine/path")
	})
	if err == nil || err.Error() != "receive rejected" {
		t.Fatalf("RunPreReceiveSession() deny error = %v, want stable rejection", err)
	}
}

func TestReceiveAdmissionLocksBeforeClassifyingEveryCanonicalTag(t *testing.T) {
	zero := strings.Repeat("0", 40)
	old := strings.Repeat("a", 40)
	candidate := strings.Repeat("b", 40)
	available := strings.Repeat("c", 40)
	coordination := &recordingReceiveCoordination{
		prepared: gitservice.PreparedReceiveBatch{
			ID: "batch-1",
			Transitions: []gitservice.PreparedReceiveTransition{{
				Tag:                 "v2.0.0",
				RefName:             "refs/tags/v2.0.0",
				ExpectedOldObjectID: old,
				ProposedNewObjectID: available,
			}},
		},
	}
	coordinator := &recordingReceiveCoordinator{coordination: coordination}
	objects := fakeReceiveObjects{peeled: map[string]string{
		candidate: candidate,
		old:       old,
		available: available,
	}}
	admission := NewReceiveAdmission(coordinator, objects, allowReceiveValidator{})

	prepared, err := admission.Admit(context.Background(), gitservice.ReceivePlugin{ID: "plugin-1", Name: "plugin-one"}, "session-1", []ReceiveCommand{
		{OldObjectID: zero, NewObjectID: candidate, RefName: "refs/tags/v1.0.0"},
		{OldObjectID: old, NewObjectID: available, RefName: "refs/tags/v2.0.0"},
		{OldObjectID: zero, NewObjectID: candidate, RefName: "refs/heads/topic"},
	})
	if err != nil {
		t.Fatalf("Admit() error = %v", err)
	}
	if prepared.ID != "batch-1" {
		t.Fatalf("prepared batch = %#v", prepared)
	}
	if coordinator.openCalls != 1 || coordinator.pluginID != "plugin-1" || coordinator.sessionID != "session-1" {
		t.Fatalf("coordinator open = calls %d plugin %q session %q", coordinator.openCalls, coordinator.pluginID, coordinator.sessionID)
	}
	if coordination.prepareCalls != 1 || coordination.closeCalls != 1 {
		t.Fatalf("coordination calls prepare=%d close=%d", coordination.prepareCalls, coordination.closeCalls)
	}
	got := coordination.batch.CanonicalTags
	if len(got) != 2 || got[0].Tag != "v1.0.0" || got[0].Operation != gitservice.ReceiveTagCreate || got[1].Tag != "v2.0.0" || got[1].Operation != gitservice.ReceiveTagMove {
		t.Fatalf("canonical tags = %#v", got)
	}
	if got[1].OldObjectID != old || got[1].NewObjectID != available || got[1].OldCommitObjectID != old || got[1].NewCommitObjectID != available {
		t.Fatalf("move raw/peeled facts = %#v", got[1])
	}
}

type recordingReceiveCoordinator struct {
	coordination gitservice.ReceiveCoordination
	openCalls    int
	pluginID     string
	sessionID    string
	err          error
}

func (c *recordingReceiveCoordinator) Open(_ context.Context, pluginID, sessionID string) (gitservice.ReceiveCoordination, error) {
	c.openCalls++
	c.pluginID = pluginID
	c.sessionID = sessionID
	return c.coordination, c.err
}

type recordingReceiveCoordination struct {
	prepared     gitservice.PreparedReceiveBatch
	batch        gitservice.ReceiveBatch
	resolution   gitservice.ReceiveResolution
	prepareCalls int
	resolveCalls int
	closeCalls   int
	prepareErr   error
	resolveErr   error
}

func (c *recordingReceiveCoordination) Prepare(_ context.Context, batch gitservice.ReceiveBatch) (gitservice.PreparedReceiveBatch, error) {
	c.prepareCalls++
	c.batch = batch
	return c.prepared, c.prepareErr
}

func (c *recordingReceiveCoordination) Resolve(_ context.Context, _ gitservice.PreparedReceiveBatch, resolution gitservice.ReceiveResolution) error {
	c.resolveCalls++
	c.resolution = resolution
	return c.resolveErr
}

func (c *recordingReceiveCoordination) Close() error {
	c.closeCalls++
	return nil
}

type fakeReceiveObjects struct {
	peeled map[string]string
	err    error
}

func (o fakeReceiveObjects) PeelCommit(_ context.Context, objectID string) (string, error) {
	if o.err != nil {
		return "", o.err
	}
	commit, ok := o.peeled[objectID]
	if !ok {
		return "", errors.New("not a commit")
	}
	return commit, nil
}

type allowReceiveValidator struct{}

func (allowReceiveValidator) ValidateTag(context.Context, gitservice.ReceivePlugin, gitservice.ReceiveTagCommand) error {
	return nil
}

func TestReceiveAdmissionKeepsAnnotatedTagObjectAndPeeledCommitSeparate(t *testing.T) {
	zero := strings.Repeat("0", 40)
	rawTag := strings.Repeat("d", 40)
	peeledCommit := strings.Repeat("e", 40)
	coordination := &recordingReceiveCoordination{}
	admission := NewReceiveAdmission(
		&recordingReceiveCoordinator{coordination: coordination},
		fakeReceiveObjects{peeled: map[string]string{rawTag: peeledCommit}},
		allowReceiveValidator{},
	)

	if _, err := admission.Admit(context.Background(), gitservice.ReceivePlugin{ID: "plugin-1", Name: "plugin-one"}, "session-annotated", []ReceiveCommand{{
		OldObjectID: zero,
		NewObjectID: rawTag,
		RefName:     "refs/tags/v1.0.0",
	}}); err != nil {
		t.Fatalf("Admit() error = %v", err)
	}
	got := coordination.batch.CanonicalTags[0]
	if got.NewObjectID != rawTag || got.NewCommitObjectID != peeledCommit {
		t.Fatalf("raw/peeled object IDs = (%q, %q), want (%q, %q)", got.NewObjectID, got.NewCommitObjectID, rawTag, peeledCommit)
	}
}

func TestReceiveAdmissionRejectsWholeCommandSetBeforePrepareWhenCanonicalValidationFails(t *testing.T) {
	zero := strings.Repeat("0", 40)
	newTag := strings.Repeat("f", 40)
	coordination := &recordingReceiveCoordination{}
	admission := NewReceiveAdmission(
		&recordingReceiveCoordinator{coordination: coordination},
		fakeReceiveObjects{peeled: map[string]string{newTag: newTag}},
		rejectReceiveValidator{},
	)

	_, err := admission.Admit(context.Background(), gitservice.ReceivePlugin{ID: "plugin-1", Name: "plugin-one"}, "session-deny", []ReceiveCommand{
		{OldObjectID: zero, NewObjectID: strings.Repeat("1", 40), RefName: "refs/heads/unrelated"},
		{OldObjectID: zero, NewObjectID: newTag, RefName: "refs/tags/v1.0.0"},
	})
	if err == nil || err.Error() != "canonical tag rejected" {
		t.Fatalf("Admit() error = %v, want stable canonical tag rejection", err)
	}
	if coordination.prepareCalls != 0 || coordination.closeCalls != 1 {
		t.Fatalf("denied admission calls prepare=%d close=%d", coordination.prepareCalls, coordination.closeCalls)
	}
}

type rejectReceiveValidator struct{}

func (rejectReceiveValidator) ValidateTag(context.Context, gitservice.ReceivePlugin, gitservice.ReceiveTagCommand) error {
	return errors.New("raw validator output with /secret/path")
}

func TestCanonicalTagFromRefUsesStrictVPrefixedSemVer(t *testing.T) {
	allowed := []string{
		"refs/tags/v0.0.0",
		"refs/tags/v1.2.3",
		"refs/tags/v1.2.3-rc.1",
		"refs/tags/v1.2.3+build.7",
		"refs/tags/v1.2.3-alpha-beta.1+linux-amd64",
	}
	for _, ref := range allowed {
		if _, ok := CanonicalTagFromRef(ref); !ok {
			t.Errorf("CanonicalTagFromRef(%q) rejected", ref)
		}
	}
	denied := []string{
		"refs/tags/1.2.3",
		"refs/tags/V1.2.3",
		"refs/tags/v1.2",
		"refs/tags/v01.2.3",
		"refs/tags/v1.02.3",
		"refs/tags/v1.2.03",
		"refs/tags/v1.2.3-01",
		"refs/tags/v1.2.3-",
		"refs/tags/v1.2.3+",
		"refs/tags/v1.2.3/extra",
		"refs/heads/v1.2.3",
	}
	for _, ref := range denied {
		if tag, ok := CanonicalTagFromRef(ref); ok {
			t.Errorf("CanonicalTagFromRef(%q) = %q, want noncanonical", ref, tag)
		}
	}
}

func TestParseReceiveCommandsRejectsMalformedOrMixedFormatInput(t *testing.T) {
	sha1 := strings.Repeat("a", 40)
	sha256 := strings.Repeat("b", 64)
	for _, input := range []string{
		"",
		sha1 + " " + sha1 + " refs/heads/main extra\n",
		sha1 + " " + sha1 + " refs/heads/main\n" + sha1 + " " + sha1 + " refs/heads/main\n",
		sha1 + " " + sha256 + " refs/heads/main\n",
		strings.ToUpper(sha1) + " " + sha1 + " refs/heads/main\n",
		sha1 + " " + sha1 + " refs/meta/config\n",
	} {
		if _, err := ParseReceiveCommands(strings.NewReader(input), ObjectFormatSHA1); err == nil {
			t.Errorf("ParseReceiveCommands(%q) succeeded", input)
		}
	}
}

func TestBuildUpdateRefTransactionUsesOneExpectedOldTransaction(t *testing.T) {
	zero := strings.Repeat("0", 40)
	old := strings.Repeat("a", 40)
	created := strings.Repeat("b", 40)
	moved := strings.Repeat("c", 40)
	transaction, err := BuildUpdateRefTransaction([]ReceiveCommand{
		{OldObjectID: zero, NewObjectID: created, RefName: "refs/heads/topic"},
		{OldObjectID: old, NewObjectID: moved, RefName: "refs/tags/v1.0.0"},
		{OldObjectID: old, NewObjectID: zero, RefName: "refs/tags/v2.0.0"},
	})
	if err != nil {
		t.Fatalf("BuildUpdateRefTransaction() error = %v", err)
	}
	want := strings.Join([]string{
		"start",
		"update refs/heads/topic " + created + " " + zero,
		"update refs/tags/v1.0.0 " + moved + " " + old,
		"delete refs/tags/v2.0.0 " + old,
		"prepare",
		"commit",
		"",
	}, "\n")
	if string(transaction) != want {
		t.Fatalf("transaction = %q, want %q", transaction, want)
	}
}

func TestClassifyReceiveResolutionUsesRawRefObjectIDs(t *testing.T) {
	zero := strings.Repeat("0", 40)
	old := strings.Repeat("a", 40)
	proposed := strings.Repeat("b", 40)
	prepared := gitservice.PreparedReceiveBatch{ID: "batch", Transitions: []gitservice.PreparedReceiveTransition{{
		Tag: "v1.0.0", RefName: "refs/tags/v1.0.0", ExpectedOldObjectID: old, ProposedNewObjectID: proposed,
	}}}
	for _, test := range []struct {
		name        string
		observed    []gitservice.ObservedReceiveRef
		disposition gitservice.ReceiveDisposition
	}{
		{name: "accepted", observed: []gitservice.ObservedReceiveRef{{RefName: "refs/tags/v1.0.0", ObjectID: proposed, Exists: true}}, disposition: gitservice.ReceiveCompleted},
		{name: "not accepted", observed: []gitservice.ObservedReceiveRef{{RefName: "refs/tags/v1.0.0", ObjectID: old, Exists: true}}, disposition: gitservice.ReceiveAborted},
		{name: "unexpected", observed: []gitservice.ObservedReceiveRef{{RefName: "refs/tags/v1.0.0", ObjectID: zero, Exists: false}}, disposition: gitservice.ReceiveManualRequired},
	} {
		t.Run(test.name, func(t *testing.T) {
			resolution := ClassifyReceiveResolution(prepared, test.observed)
			if resolution.Disposition != test.disposition {
				t.Fatalf("disposition = %q, want %q", resolution.Disposition, test.disposition)
			}
		})
	}
}

func TestClassifyReceiveResolutionHandlesDeleteAndMixedBatch(t *testing.T) {
	oldOne := strings.Repeat("a", 40)
	oldTwo := strings.Repeat("b", 40)
	newOne := strings.Repeat("c", 40)
	prepared := gitservice.PreparedReceiveBatch{ID: "batch", Transitions: []gitservice.PreparedReceiveTransition{
		{Tag: "v1.0.0", RefName: "refs/tags/v1.0.0", ExpectedOldObjectID: oldOne, ProposedNewObjectID: newOne},
		{Tag: "v2.0.0", RefName: "refs/tags/v2.0.0", ExpectedOldObjectID: oldTwo, ProposedNewObjectID: strings.Repeat("0", 40)},
	}}
	completed := ClassifyReceiveResolution(prepared, []gitservice.ObservedReceiveRef{
		{RefName: "refs/tags/v1.0.0", ObjectID: newOne, Exists: true},
		{RefName: "refs/tags/v2.0.0", Exists: false},
	})
	if completed.Disposition != gitservice.ReceiveCompleted {
		t.Fatalf("delete completion disposition = %q", completed.Disposition)
	}
	ambiguous := ClassifyReceiveResolution(prepared, []gitservice.ObservedReceiveRef{
		{RefName: "refs/tags/v1.0.0", ObjectID: newOne, Exists: true},
		{RefName: "refs/tags/v2.0.0", ObjectID: oldTwo, Exists: true},
	})
	if ambiguous.Disposition != gitservice.ReceiveManualRequired {
		t.Fatalf("mixed outcome disposition = %q, want manual_required", ambiguous.Disposition)
	}
}

func TestReceiveFinalizeRereadsThenDurablyResolvesWhileLocked(t *testing.T) {
	old := strings.Repeat("a", 40)
	proposed := strings.Repeat("b", 40)
	coordination := &recordingReceiveCoordination{}
	coordinator := &recordingReceiveCoordinator{coordination: coordination}
	refs := fakeReceiveRefReader{refs: map[string]gitservice.ObservedReceiveRef{
		"refs/tags/v1.0.0": {RefName: "refs/tags/v1.0.0", ObjectID: proposed, Exists: true},
	}}
	finalizer := NewReceiveFinalizer(coordinator, refs)
	prepared := gitservice.PreparedReceiveBatch{ID: "batch", Transitions: []gitservice.PreparedReceiveTransition{{
		Tag: "v1.0.0", RefName: "refs/tags/v1.0.0", ExpectedOldObjectID: old, ProposedNewObjectID: proposed,
	}}}
	if err := finalizer.Finalize(context.Background(), "plugin-1", "session-1", prepared); err != nil {
		t.Fatalf("Finalize() error = %v", err)
	}
	if coordination.resolveCalls != 1 || coordination.closeCalls != 1 {
		t.Fatalf("finalize calls resolve=%d close=%d", coordination.resolveCalls, coordination.closeCalls)
	}
	if coordination.resolution.Disposition != gitservice.ReceiveCompleted {
		t.Fatalf("resolution = %#v", coordination.resolution)
	}
}

type fakeReceiveRefReader struct {
	refs map[string]gitservice.ObservedReceiveRef
	err  error
}

func (r fakeReceiveRefReader) ReadRef(_ context.Context, refName string) (gitservice.ObservedReceiveRef, error) {
	if r.err != nil {
		return gitservice.ObservedReceiveRef{}, r.err
	}
	ref, ok := r.refs[refName]
	if !ok {
		return gitservice.ObservedReceiveRef{RefName: refName, Exists: false}, nil
	}
	return ref, nil
}

func TestReceiveHookClientCancellationUnblocksProcReceive(t *testing.T) {
	socketDirectory, err := os.MkdirTemp("", "receive-hook-cancel-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(socketDirectory)
	listener, err := net.Listen("unix", filepath.Join(socketDirectory, "hook.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	serverDone := make(chan struct{})
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			defer connection.Close()
			reader := bufio.NewReader(connection)
			_, _ = reader.ReadString('\n')
			_, _ = io.ReadAll(reader)
			<-ctx.Done()
		}
		close(serverDone)
	}()

	started := time.Now()
	err = runReceiveHookClient(ctx, "proc-receive", listener.Addr().String(), strings.NewReader("request"), io.Discard)
	if err == nil {
		t.Fatal("runReceiveHookClient() succeeded after context cancellation")
	}
	if time.Since(started) > time.Second {
		t.Fatal("runReceiveHookClient() did not promptly unblock after context cancellation")
	}
	<-serverDone
}

func TestReceiveHookSessionCancellationClosesAcceptedConnection(t *testing.T) {
	gitBinary := receiveTestGit(t)
	repository := initReceiveTestRepository(t, gitBinary, "sha1")
	service := &Service{gitBinary: gitBinary}
	inspector := &pluginSourceInspector{}
	socketDirectory, err := os.MkdirTemp("", "receive-session-cancel-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(socketDirectory)
	listener, err := net.Listen("unix", filepath.Join(socketDirectory, "hook.sock"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	session := &receiveHookSession{
		listener: listener, done: make(chan error, 1), service: service, inspector: inspector,
		context:        receiveContext{repositoryID: "plugin-id", pluginName: "plugin"},
		repositoryPath: repository, sessionID: "session", cancel: cancel,
	}
	go session.serve(ctx)

	connection, err := net.Dial("unix", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	if _, err := io.WriteString(connection, "proc-receive\n000"); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		session.Abort()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Abort() did not close the accepted hook connection")
	}
}

func TestReceiveHookSessionWaitAllowsSuccessfulBranchOnlyReceive(t *testing.T) {
	socketDirectory, err := os.MkdirTemp("", "receive-session-wait-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(socketDirectory)
	listener, err := net.Listen("unix", filepath.Join(socketDirectory, "hook.sock"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	session := &receiveHookSession{listener: listener, done: make(chan error, 1), cancel: cancel}
	go session.serve(ctx)

	connection, err := net.Dial("unix", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(connection, "pre-receive\ncommand\n"); err != nil {
		t.Fatal(err)
	}
	if unix, ok := connection.(*net.UnixConn); ok {
		_ = unix.CloseWrite()
	}
	_ = connection.Close()

	if err := session.Wait(nil); err != nil {
		t.Fatalf("Wait(nil) error = %v, want successful branch-only receive", err)
	}
}

func TestPreReceiveHookReturnsWithoutWaitingForResponse(t *testing.T) {
	socketDirectory, err := os.MkdirTemp("", "receive-hook-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(socketDirectory)
	listener, err := net.Listen("unix", filepath.Join(socketDirectory, "hook.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	serverDone := make(chan error, 1)
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			serverDone <- acceptErr
			return
		}
		defer connection.Close()
		payload, readErr := io.ReadAll(connection)
		if readErr != nil {
			serverDone <- readErr
			return
		}
		if got, want := string(payload), "pre-receive e30\ncommand\n"; got != want {
			serverDone <- fmt.Errorf("hook payload = %q, want %q", got, want)
			return
		}
		serverDone <- nil
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := runReceiveHookClient(ctx, "pre-receive", listener.Addr().String(), strings.NewReader("command\n"), io.Discard); err != nil {
		t.Fatalf("runReceiveHookClient() error = %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func TestRealGitClientProtectedReceiveAllowsCanonicalTagAndOrdinaryBranch(t *testing.T) {
	gitBinary := receiveTestGit(t)
	repository := initReceiveTestRepository(t, gitBinary, "sha1")
	installReceiveTestHooks(t, gitBinary, repository, "sha1", false, "", "")
	worktree := initReceiveTestWorktree(t, gitBinary, "sha1")
	commit := commitReceiveTestFile(t, gitBinary, worktree, "allow", "allow")
	runReceiveTestGit(t, gitBinary, worktree, "tag", "v1.0.0", commit)
	runReceiveTestGit(t, gitBinary, worktree, "push", repository, "HEAD:refs/heads/main", "refs/tags/v1.0.0")
	if got := receiveTestRef(t, gitBinary, repository, "refs/heads/main"); got != commit {
		t.Fatalf("main = %q, want %q", got, commit)
	}
	if got := receiveTestRef(t, gitBinary, repository, "refs/tags/v1.0.0"); got != commit {
		t.Fatalf("tag = %q, want %q", got, commit)
	}
}

func TestRealGitClientProtectedReceiveDenyRejectsWholeNonAtomicPush(t *testing.T) {
	gitBinary := receiveTestGit(t)
	repository := initReceiveTestRepository(t, gitBinary, "sha1")
	installReceiveTestHooks(t, gitBinary, repository, "sha1", true, "", "")
	worktree := initReceiveTestWorktree(t, gitBinary, "sha1")
	commit := commitReceiveTestFile(t, gitBinary, worktree, "deny", "deny")
	runReceiveTestGit(t, gitBinary, worktree, "tag", "v1.0.0", commit)
	output, err := runReceiveTestGitError(gitBinary, worktree, "push", repository, "HEAD:refs/heads/unrelated", "refs/tags/v1.0.0")
	if err == nil {
		t.Fatalf("non-atomic push succeeded: %s", output)
	}
	if got := receiveTestRef(t, gitBinary, repository, "refs/heads/unrelated"); got != "" {
		t.Fatalf("denied unrelated branch exists at %q", got)
	}
	if got := receiveTestRef(t, gitBinary, repository, "refs/tags/v1.0.0"); got != "" {
		t.Fatalf("denied canonical tag exists at %q", got)
	}
}

func TestRealGitClientProtectedTagMoveDeleteRecreate(t *testing.T) {
	gitBinary := receiveTestGit(t)
	repository := initReceiveTestRepository(t, gitBinary, "sha1")
	installReceiveTestHooks(t, gitBinary, repository, "sha1", false, "", "")
	worktree := initReceiveTestWorktree(t, gitBinary, "sha1")
	first := commitReceiveTestFile(t, gitBinary, worktree, "first", "first")
	runReceiveTestGit(t, gitBinary, worktree, "tag", "v1.0.0", first)
	runReceiveTestGit(t, gitBinary, worktree, "push", repository, "refs/tags/v1.0.0")
	second := commitReceiveTestFile(t, gitBinary, worktree, "second", "second")
	runReceiveTestGit(t, gitBinary, worktree, "tag", "-f", "v1.0.0", second)
	runReceiveTestGit(t, gitBinary, worktree, "push", "--force", repository, "refs/tags/v1.0.0")
	if got := receiveTestRef(t, gitBinary, repository, "refs/tags/v1.0.0"); got != second {
		t.Fatalf("moved tag = %q, want %q", got, second)
	}
	runReceiveTestGit(t, gitBinary, worktree, "push", repository, ":refs/tags/v1.0.0")
	if got := receiveTestRef(t, gitBinary, repository, "refs/tags/v1.0.0"); got != "" {
		t.Fatalf("deleted tag still exists at %q", got)
	}
	runReceiveTestGit(t, gitBinary, worktree, "push", repository, "refs/tags/v1.0.0")
	if got := receiveTestRef(t, gitBinary, repository, "refs/tags/v1.0.0"); got != second {
		t.Fatalf("recreated tag = %q, want %q", got, second)
	}
}

func TestRealGitClientProcReceiveConsumesNegotiatedPushOptions(t *testing.T) {
	gitBinary := receiveTestGit(t)
	repository := initReceiveTestRepository(t, gitBinary, "sha1")
	installReceiveTestHooks(t, gitBinary, repository, "sha1", false, "", "")
	runReceiveTestGit(t, gitBinary, "", "--git-dir="+repository, "config", "receive.advertisePushOptions", "true")
	worktree := initReceiveTestWorktree(t, gitBinary, "sha1")
	commit := commitReceiveTestFile(t, gitBinary, worktree, "push-options", "push options")

	runReceiveTestGit(t, gitBinary, worktree, "push", "-o", "ci.skip", repository, "HEAD:refs/heads/main")
	if got := receiveTestRef(t, gitBinary, repository, "refs/heads/main"); got != commit {
		t.Fatalf("main = %q, want %q", got, commit)
	}
}

func TestRealGitClientProcReceiveExpectedOldConflictRejectsWholePush(t *testing.T) {
	gitBinary := receiveTestGit(t)
	repository := initReceiveTestRepository(t, gitBinary, "sha1")
	worktree := initReceiveTestWorktree(t, gitBinary, "sha1")
	first := commitReceiveTestFile(t, gitBinary, worktree, "first", "first")
	proposed := commitReceiveTestFile(t, gitBinary, worktree, "proposed", "proposed")
	conflict := commitReceiveTestFile(t, gitBinary, worktree, "conflict", "conflict")

	// Seed the conflict object before receive-pack starts. The proc-receive hook
	// moves main only after it has read the client's expected-old command.
	runReceiveTestGit(t, gitBinary, worktree, "push", repository,
		first+":refs/heads/main", conflict+":refs/heads/conflict-seed")
	runReceiveTestGit(t, gitBinary, worktree, "push", repository, ":refs/heads/conflict-seed")
	installReceiveTestHooks(t, gitBinary, repository, "sha1", false, "refs/heads/main", conflict)

	output, err := runReceiveTestGitError(gitBinary, worktree, "push", repository,
		proposed+":refs/heads/main", proposed+":refs/heads/unrelated")
	if err == nil {
		t.Fatalf("conflicting push succeeded: %s", output)
	}
	if got := receiveTestRef(t, gitBinary, repository, "refs/heads/main"); got != conflict {
		t.Fatalf("main = %q, want injected conflict %q", got, conflict)
	}
	if got := receiveTestRef(t, gitBinary, repository, "refs/heads/unrelated"); got != "" {
		t.Fatalf("unrelated ref was partially created at %q", got)
	}
}

func TestProcReceiveSessionAcceptsVersionWithoutNULAndWritesLFRecords(t *testing.T) {
	gitBinary := receiveTestGit(t)
	repository := initReceiveTestRepository(t, gitBinary, "sha1")
	worktree := initReceiveTestWorktree(t, gitBinary, "sha1")
	commit := commitReceiveTestFile(t, gitBinary, worktree, "no-features", "no features")
	copyReceiveTestObjects(t, gitBinary, worktree, repository)
	command := ReceiveCommand{
		OldObjectID: strings.Repeat("0", 40),
		NewObjectID: commit,
		RefName:     "refs/heads/main",
	}
	var response bytes.Buffer
	if err := RunProcReceiveSession(context.Background(), gitBinary, repository, bytes.NewReader(procReceiveRequest(t, []ReceiveCommand{command})), &response, nil); err != nil {
		t.Fatalf("RunProcReceiveSession() error = %v, response=%q", err, response.String())
	}
	if !strings.HasPrefix(response.String(), "000eversion=1\n0000") {
		t.Fatalf("negotiation response = %q, want LF-terminated version without NUL", response.String())
	}
	if !strings.Contains(response.String(), "ok refs/heads/main\n") {
		t.Fatalf("command response = %q, want LF-terminated result", response.String())
	}
}

func TestCoordinatedProcReceivePreparesBeforeCASAndFinalizesAfterRefUpdate(t *testing.T) {
	gitBinary := receiveTestGit(t)
	repository := initReceiveTestRepository(t, gitBinary, "sha1")
	worktree := initReceiveTestWorktree(t, gitBinary, "sha1")
	commit := commitReceiveTestFile(t, gitBinary, worktree, "coordinated", "coordinated")
	copyReceiveTestObjects(t, gitBinary, worktree, repository)
	command := ReceiveCommand{OldObjectID: strings.Repeat("0", 40), NewObjectID: commit, RefName: "refs/heads/main"}
	lifecycle := &recordingProcReceiveLifecycle{repository: repository, gitBinary: gitBinary}
	var response bytes.Buffer
	if err := RunCoordinatedProcReceiveSession(context.Background(), gitBinary, repository, bytes.NewReader(procReceiveRequest(t, []ReceiveCommand{command})), &response, lifecycle); err != nil {
		t.Fatalf("RunCoordinatedProcReceiveSession() error = %v", err)
	}
	if lifecycle.refDuringPrepare != "" {
		t.Fatalf("ref existed before prepare: %q", lifecycle.refDuringPrepare)
	}
	if lifecycle.refDuringFinalize != commit || !lifecycle.accepted {
		t.Fatalf("finalize observed ref=%q accepted=%v, want %q true", lifecycle.refDuringFinalize, lifecycle.accepted, commit)
	}
}

type recordingProcReceiveLifecycle struct {
	repository        string
	gitBinary         string
	refDuringPrepare  string
	refDuringFinalize string
	accepted          bool
}

func (l *recordingProcReceiveLifecycle) Prepare(context.Context, []ReceiveCommand) error {
	l.refDuringPrepare = receiveTestRefWithoutTest(l.gitBinary, l.repository, "refs/heads/main")
	return nil
}

func (l *recordingProcReceiveLifecycle) Finalize(_ context.Context, _ []ReceiveCommand, accepted bool) error {
	l.accepted = accepted
	l.refDuringFinalize = receiveTestRefWithoutTest(l.gitBinary, l.repository, "refs/heads/main")
	return nil
}

func receiveTestRefWithoutTest(gitBinary, repository, ref string) string {
	command := exec.Command(gitBinary, "--git-dir="+repository, "rev-parse", "--verify", ref)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := command.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(output))
}

func TestCoordinatedProcReceiveResolvesRejectedCAS(t *testing.T) {
	gitBinary := receiveTestGit(t)
	repository := initReceiveTestRepository(t, gitBinary, "sha1")
	worktree := initReceiveTestWorktree(t, gitBinary, "sha1")
	commit := commitReceiveTestFile(t, gitBinary, worktree, "conflict", "conflict")
	copyReceiveTestObjects(t, gitBinary, worktree, repository)
	command := ReceiveCommand{OldObjectID: strings.Repeat("a", 40), NewObjectID: commit, RefName: "refs/heads/main"}
	lifecycle := &recordingProcReceiveLifecycle{repository: repository, gitBinary: gitBinary}
	var response bytes.Buffer
	if err := RunCoordinatedProcReceiveSession(context.Background(), gitBinary, repository, bytes.NewReader(procReceiveRequest(t, []ReceiveCommand{command})), &response, lifecycle); err == nil {
		t.Fatal("conflicting coordinated receive succeeded")
	}
	if lifecycle.accepted || lifecycle.refDuringFinalize != "" {
		t.Fatalf("rejected finalize accepted=%v ref=%q", lifecycle.accepted, lifecycle.refDuringFinalize)
	}
}

func TestProcReceiveSessionConsumesNegotiatedPushOptionsBeforeAdmission(t *testing.T) {
	zero := strings.Repeat("0", 40)
	proposed := strings.Repeat("a", 40)
	command := ReceiveCommand{OldObjectID: zero, NewObjectID: proposed, RefName: "refs/heads/main"}
	request := bytes.NewBuffer(procReceiveRequestWithPushOptions(t, []ReceiveCommand{command}, []string{"ci.skip", "trace=off"}, "push-options"))
	var response bytes.Buffer
	admitted := false
	err := RunProcReceiveSession(context.Background(), "git", "/unused", request, &response, func(context.Context, []ReceiveCommand) error {
		admitted = true
		return errors.New("deny after options")
	})
	if err == nil || err.Error() != "receive rejected" {
		t.Fatalf("RunProcReceiveSession() error = %v, want receive rejected", err)
	}
	if !admitted || request.Len() != 0 {
		t.Fatalf("admitted=%v unread request bytes=%d, want admission after all push options", admitted, request.Len())
	}
	if !strings.Contains(response.String(), "version=1\x00push-options\n") || !strings.Contains(response.String(), "ng refs/heads/main receive rejected\n") {
		t.Fatalf("proc-receive response = %q", response.String())
	}
}

func TestProcReceiveSessionRejectsMalformedOrUnsupportedNegotiation(t *testing.T) {
	for _, negotiation := range []string{
		"version=2",
		"version=1\x00atomic atomic",
		"version=1\x00push-options\x00atomic",
		"version=1\x00push-options\r",
	} {
		var request bytes.Buffer
		writeTestPktLine(t, &request, negotiation)
		request.WriteString("0000")
		err := RunProcReceiveSession(context.Background(), "git", "/unused", &request, io.Discard, nil)
		if err == nil {
			t.Errorf("negotiation %q succeeded", negotiation)
		}
	}
}

func TestProcReceiveSessionCommitsCompleteCommandSetWithExpectedOldCAS(t *testing.T) {
	gitBinary := receiveTestGit(t)
	repository := initReceiveTestRepository(t, gitBinary, "sha1")
	worktree := initReceiveTestWorktree(t, gitBinary, "sha1")
	first := commitReceiveTestFile(t, gitBinary, worktree, "one", "first")
	second := commitReceiveTestFile(t, gitBinary, worktree, "two", "second")
	oldTagObject := createAnnotatedReceiveTestTag(t, gitBinary, worktree, "old-v1", first)
	newTagObject := createAnnotatedReceiveTestTag(t, gitBinary, worktree, "new-v1", second)
	copyReceiveTestObjects(t, gitBinary, worktree, repository)
	runReceiveTestGit(t, gitBinary, "", "--git-dir="+repository, "update-ref", "refs/heads/main", first)
	runReceiveTestGit(t, gitBinary, "", "--git-dir="+repository, "update-ref", "refs/tags/v1.0.0", oldTagObject)
	zero := strings.Repeat("0", 40)
	commands := []ReceiveCommand{
		{OldObjectID: first, NewObjectID: second, RefName: "refs/heads/main"},
		{OldObjectID: oldTagObject, NewObjectID: newTagObject, RefName: "refs/tags/v1.0.0"},
		{OldObjectID: zero, NewObjectID: first, RefName: "refs/heads/topic"},
	}
	var response bytes.Buffer
	if err := RunProcReceiveSession(context.Background(), gitBinary, repository, bytes.NewBuffer(procReceiveRequest(t, commands, "atomic")), &response, nil); err != nil {
		t.Fatalf("RunProcReceiveSession() error = %v, response=%q", err, response.String())
	}
	for ref, want := range map[string]string{
		"refs/heads/main":  second,
		"refs/tags/v1.0.0": newTagObject,
		"refs/heads/topic": first,
	} {
		if got := receiveTestRef(t, gitBinary, repository, ref); got != want {
			t.Fatalf("%s = %q, want %q", ref, got, want)
		}
	}
	if !strings.Contains(response.String(), "ok refs/heads/main") || !strings.Contains(response.String(), "ok refs/tags/v1.0.0") {
		t.Fatalf("proc-receive response = %q", response.String())
	}
}

func TestProcReceiveSessionRejectsWholeNonAtomicPushOnExpectedOldConflict(t *testing.T) {
	gitBinary := receiveTestGit(t)
	repository := initReceiveTestRepository(t, gitBinary, "sha1")
	worktree := initReceiveTestWorktree(t, gitBinary, "sha1")
	first := commitReceiveTestFile(t, gitBinary, worktree, "one", "first")
	second := commitReceiveTestFile(t, gitBinary, worktree, "two", "second")
	third := commitReceiveTestFile(t, gitBinary, worktree, "three", "third")
	copyReceiveTestObjects(t, gitBinary, worktree, repository)
	runReceiveTestGit(t, gitBinary, "", "--git-dir="+repository, "update-ref", "refs/heads/main", first)
	commands := []ReceiveCommand{
		{OldObjectID: second, NewObjectID: third, RefName: "refs/heads/main"},
		{OldObjectID: strings.Repeat("0", 40), NewObjectID: first, RefName: "refs/heads/unrelated"},
	}
	var response bytes.Buffer
	if err := RunProcReceiveSession(context.Background(), gitBinary, repository, bytes.NewBuffer(procReceiveRequest(t, commands)), &response, nil); err == nil {
		t.Fatalf("RunProcReceiveSession() succeeded; response=%q", response.String())
	}
	if got := receiveTestRef(t, gitBinary, repository, "refs/heads/main"); got != first {
		t.Fatalf("main changed to %q, want %q", got, first)
	}
	if got := receiveTestRef(t, gitBinary, repository, "refs/heads/unrelated"); got != "" {
		t.Fatalf("unrelated ref was partially created at %q", got)
	}
	if !strings.Contains(response.String(), "ng refs/heads/main receive rejected") || !strings.Contains(response.String(), "ng refs/heads/unrelated receive rejected") {
		t.Fatalf("proc-receive rejection response = %q", response.String())
	}
}

func TestProcReceiveSessionTagMoveDeleteRecreateWithRawAnnotatedObjectIDs(t *testing.T) {
	gitBinary := receiveTestGit(t)
	repository := initReceiveTestRepository(t, gitBinary, "sha1")
	worktree := initReceiveTestWorktree(t, gitBinary, "sha1")
	first := commitReceiveTestFile(t, gitBinary, worktree, "one", "first")
	second := commitReceiveTestFile(t, gitBinary, worktree, "two", "second")
	oldTagObject := createAnnotatedReceiveTestTag(t, gitBinary, worktree, "old-v1", first)
	newTagObject := createAnnotatedReceiveTestTag(t, gitBinary, worktree, "new-v1", second)
	zero := strings.Repeat("0", 40)
	copyReceiveTestObjects(t, gitBinary, worktree, repository)
	runReceiveTestGit(t, gitBinary, "", "--git-dir="+repository, "update-ref", "refs/tags/v1.0.0", oldTagObject)
	for _, commands := range [][]ReceiveCommand{
		{{OldObjectID: oldTagObject, NewObjectID: newTagObject, RefName: "refs/tags/v1.0.0"}},
		{{OldObjectID: newTagObject, NewObjectID: zero, RefName: "refs/tags/v1.0.0"}},
		{{OldObjectID: zero, NewObjectID: oldTagObject, RefName: "refs/tags/v1.0.0"}},
	} {
		if err := RunProcReceiveSession(context.Background(), gitBinary, repository, bytes.NewBuffer(procReceiveRequest(t, commands)), io.Discard, nil); err != nil {
			t.Fatalf("tag transition error = %v", err)
		}
	}
	if got := receiveTestRef(t, gitBinary, repository, "refs/tags/v1.0.0"); got != oldTagObject {
		t.Fatalf("recreated tag object = %q, want %q", got, oldTagObject)
	}
	if peeled := receiveTestRef(t, gitBinary, repository, "refs/tags/v1.0.0^{}"); peeled != first {
		t.Fatalf("recreated tag peeled commit = %q, want %q", peeled, first)
	}
}

func installReceiveTestHooks(t *testing.T, gitBinary, repository, objectFormat string, deny bool, conflictRef, conflictOID string) {
	t.Helper()
	helperDir := t.TempDir()
	helperSource := filepath.Join(helperDir, "receive-hook.go")
	helperBinary := filepath.Join(helperDir, "receive-hook")
	source := `package main

import (
    "context"
    "errors"
    "os"
)

func main() {
    if len(os.Args) != 5 {
        os.Exit(2)
    }
    format := ObjectFormat(os.Args[3])
    deny := os.Args[4] == "deny"
    err := RunReceiveHook(context.Background(), os.Args[1], "git", os.Args[2], format, os.Stdin, os.Stdout, func(context.Context, []ReceiveCommand) error {
        if deny {
            return errors.New("denied")
        }
        return nil
    })
    if err != nil {
        os.Exit(1)
    }
}
`
	if err := os.WriteFile(helperSource, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	moduleRoot := receiveTestModuleRoot(t)
	receiveSource, err := os.ReadFile(filepath.Join(moduleRoot, "mod", "git", "receive.go"))
	if err != nil {
		t.Fatal(err)
	}
	receiveSource = bytes.Replace(receiveSource, []byte("package git"), []byte("package main"), 1)
	// The helper exercises only the standalone hook protocol. Exclude the
	// production server handoff and lifecycle adapter, which depend on the Git
	// service and source inspector assembled by the running server.
	lifecycleStart := bytes.Index(receiveSource, []byte("type receiveHookSession"))
	lifecycleEnd := bytes.Index(receiveSource, []byte("type ProcReceiveLifecycle"))
	if lifecycleStart < 0 || lifecycleEnd < lifecycleStart {
		t.Fatal("locate protected receive lifecycle source")
	}
	receiveSource = append(receiveSource[:lifecycleStart], receiveSource[lifecycleEnd:]...)
	for _, unusedImport := range [][]byte{
		[]byte("\t\"net\"\n"),
		[]byte("\t\"path/filepath\"\n"),
		[]byte("\t\"sync\"\n"),
		[]byte("\t\"github.com/google/uuid\"\n"),
	} {
		receiveSource = bytes.Replace(receiveSource, unusedImport, nil, 1)
	}
	localReceiveSource := filepath.Join(helperDir, "receive.go")
	if err := os.WriteFile(localReceiveSource, receiveSource, 0o600); err != nil {
		t.Fatal(err)
	}
	goMod := "module marketplace-receive-hook-test\n\ngo 1.25\n\nrequire github.com/Esonhugh/MarketplaceServer v0.0.0\n\nreplace github.com/Esonhugh/MarketplaceServer => " + moduleRoot + "\n"
	if err := os.WriteFile(filepath.Join(helperDir, "go.mod"), []byte(goMod), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-mod=mod", "-o", helperBinary, ".")
	cmd.Dir = helperDir
	cmd.Env = os.Environ()
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build receive hook helper: %v: %s", err, output)
	}
	mode := "allow"
	if deny {
		mode = "deny"
	}
	hooksDir := filepath.Join(repository, "hooks")
	preHook := filepath.Join(hooksDir, "pre-receive")
	procHook := filepath.Join(hooksDir, "proc-receive")
	preScript := "#!/bin/sh\nexec " + shellQuoteReceiveTest(helperBinary) + " pre-receive " + shellQuoteReceiveTest(repository) + " " + shellQuoteReceiveTest(objectFormat) + " " + mode + "\n"
	procScript := "#!/bin/sh\nexec " + shellQuoteReceiveTest(helperBinary) + " proc-receive " + shellQuoteReceiveTest(repository) + " " + shellQuoteReceiveTest(objectFormat) + " " + mode + "\n"
	if conflictRef != "" {
		procScript = "#!/bin/sh\n" + shellQuoteReceiveTest(gitBinary) + " --git-dir=" + shellQuoteReceiveTest(repository) + " update-ref " + shellQuoteReceiveTest(conflictRef) + " " + shellQuoteReceiveTest(conflictOID) + " || exit 1\nexec " + shellQuoteReceiveTest(helperBinary) + " proc-receive " + shellQuoteReceiveTest(repository) + " " + shellQuoteReceiveTest(objectFormat) + " " + mode + "\n"
	}
	if err := os.WriteFile(preHook, []byte(preScript), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(procHook, []byte(procScript), 0o700); err != nil {
		t.Fatal(err)
	}
	runReceiveTestGit(t, gitBinary, "", "--git-dir="+repository, "config", "receive.procReceiveRefs", "refs/heads/")
	runReceiveTestGit(t, gitBinary, "", "--git-dir="+repository, "config", "--add", "receive.procReceiveRefs", "refs/tags/")
}

func receiveTestModuleRoot(t *testing.T) string {
	t.Helper()
	output, err := exec.Command("go", "env", "GOMOD").Output()
	if err != nil {
		t.Fatalf("locate go.mod: %v", err)
	}
	goMod := strings.TrimSpace(string(output))
	if goMod == "" || goMod == os.DevNull {
		t.Fatal("go.mod is unavailable")
	}
	return filepath.Dir(goMod)
}

func shellQuoteReceiveTest(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func procReceiveRequest(t *testing.T, commands []ReceiveCommand, features ...string) []byte {
	t.Helper()
	return procReceiveRequestWithPushOptions(t, commands, nil, features...)
}

func procReceiveRequestWithPushOptions(t *testing.T, commands []ReceiveCommand, pushOptions []string, features ...string) []byte {
	t.Helper()
	var request bytes.Buffer
	negotiation := "version=1"
	if len(features) > 0 {
		negotiation += "\x00" + strings.Join(features, " ")
	}
	writeTestPktLine(t, &request, negotiation)
	request.WriteString("0000")
	for _, command := range commands {
		writeTestPktLine(t, &request, command.OldObjectID+" "+command.NewObjectID+" "+command.RefName)
	}
	request.WriteString("0000")
	if slicesContainsReceiveTest(features, "push-options") {
		for _, option := range pushOptions {
			writeTestPktLine(t, &request, option)
		}
		request.WriteString("0000")
	}
	return request.Bytes()
}

func slicesContainsReceiveTest(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func writeTestPktLine(t *testing.T, writer io.Writer, payload string) {
	t.Helper()
	if _, err := io.WriteString(writer, formatPktLine(payload)); err != nil {
		t.Fatal(err)
	}
}

func receiveTestGit(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git binary is not available")
	}
	return path
}

func initReceiveTestRepository(t *testing.T, gitBinary, objectFormat string) string {
	t.Helper()
	repository := filepath.Join(t.TempDir(), "repo.git")
	runReceiveTestGit(t, gitBinary, "", "init", "--bare", "--object-format="+objectFormat, repository)
	return repository
}

func initReceiveTestWorktree(t *testing.T, gitBinary, objectFormat string) string {
	t.Helper()
	worktree := t.TempDir()
	runReceiveTestGit(t, gitBinary, worktree, "init", "--object-format="+objectFormat)
	runReceiveTestGit(t, gitBinary, worktree, "config", "user.name", "Receive Test")
	runReceiveTestGit(t, gitBinary, worktree, "config", "user.email", "receive@example.invalid")
	runReceiveTestGit(t, gitBinary, worktree, "config", "commit.gpgSign", "false")
	runReceiveTestGit(t, gitBinary, worktree, "config", "tag.gpgSign", "false")
	return worktree
}

func commitReceiveTestFile(t *testing.T, gitBinary, worktree, content, message string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(worktree, "plugin.txt"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	runReceiveTestGit(t, gitBinary, worktree, "add", "plugin.txt")
	runReceiveTestGit(t, gitBinary, worktree, "commit", "-m", message)
	return strings.TrimSpace(runReceiveTestGit(t, gitBinary, worktree, "rev-parse", "HEAD"))
}

func createAnnotatedReceiveTestTag(t *testing.T, gitBinary, worktree, message, commit string) string {
	t.Helper()
	name := "tmp-" + strings.ReplaceAll(message, " ", "-")
	runReceiveTestGit(t, gitBinary, worktree, "tag", "-a", name, "-m", message, commit)
	return strings.TrimSpace(runReceiveTestGit(t, gitBinary, worktree, "rev-parse", name))
}

func copyReceiveTestObjects(t *testing.T, gitBinary, worktree, repository string) {
	t.Helper()
	runReceiveTestGit(t, gitBinary, worktree, "push", "--mirror", repository)
}

func receiveTestRef(t *testing.T, gitBinary, repository, ref string) string {
	t.Helper()
	cmd := exec.Command(gitBinary, "--git-dir="+repository, "rev-parse", "--verify", "--quiet", ref)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
			return ""
		}
		t.Fatalf("read ref %s: %v", ref, err)
	}
	return strings.TrimSpace(string(output))
}

func runReceiveTestGit(t *testing.T, gitBinary, directory string, args ...string) string {
	t.Helper()
	output, err := runReceiveTestGitError(gitBinary, directory, args...)
	if err != nil {
		t.Fatalf("git command failed: %v: %s", err, output)
	}
	return output
}

func runReceiveTestGitError(gitBinary, directory string, args ...string) (string, error) {
	cmd := exec.Command(gitBinary, args...)
	cmd.Dir = directory
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	output, err := cmd.CombinedOutput()
	return string(output), err
}
