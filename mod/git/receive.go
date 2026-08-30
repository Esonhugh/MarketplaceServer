package git

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/google/uuid"
)

type ObjectFormat string

const (
	ObjectFormatSHA1   ObjectFormat = "sha1"
	ObjectFormatSHA256 ObjectFormat = "sha256"
)

var canonicalTagPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-((?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

type ReceiveCommand struct {
	OldObjectID string
	NewObjectID string
	RefName     string
}

func ParseReceiveCommands(input io.Reader, objectFormat ObjectFormat) ([]ReceiveCommand, error) {
	oidLength, err := objectIDLength(objectFormat)
	if err != nil {
		return nil, err
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	var commands []ReceiveCommand
	seen := make(map[string]struct{})
	for scanner.Scan() {
		fields := strings.Split(scanner.Text(), " ")
		if len(fields) != 3 || fields[0] == "" || fields[1] == "" || fields[2] == "" {
			return nil, errors.New("invalid receive command")
		}
		if !validObjectID(fields[0], oidLength) || !validObjectID(fields[1], oidLength) {
			return nil, errors.New("invalid receive object id")
		}
		if !validReceiveRefName(fields[2]) {
			return nil, errors.New("invalid receive ref")
		}
		if _, duplicate := seen[fields[2]]; duplicate {
			return nil, errors.New("duplicate receive ref")
		}
		seen[fields[2]] = struct{}{}
		commands = append(commands, ReceiveCommand{OldObjectID: fields[0], NewObjectID: fields[1], RefName: fields[2]})
	}
	if err := scanner.Err(); err != nil {
		return nil, errors.New("read receive commands")
	}
	if len(commands) == 0 {
		return nil, errors.New("empty receive command set")
	}
	return commands, nil
}

func CanonicalTagFromRef(refName string) (string, bool) {
	const prefix = "refs/tags/"
	if !strings.HasPrefix(refName, prefix) {
		return "", false
	}
	tag := strings.TrimPrefix(refName, prefix)
	if !canonicalTagPattern.MatchString(tag) {
		return "", false
	}
	return tag, true
}

type ReceiveObjectInspector interface {
	PeelCommit(ctx context.Context, objectID string) (string, error)
}

type ReceiveTagValidator interface {
	ValidateTag(ctx context.Context, plugin gitservice.ReceivePlugin, command gitservice.ReceiveTagCommand) error
}

type ReceiveAdmission struct {
	coordinator gitservice.ReceiveCoordinator
	objects     ReceiveObjectInspector
	validator   ReceiveTagValidator
}

func NewReceiveAdmission(coordinator gitservice.ReceiveCoordinator, objects ReceiveObjectInspector, validator ReceiveTagValidator) *ReceiveAdmission {
	return &ReceiveAdmission{coordinator: coordinator, objects: objects, validator: validator}
}

func (a *ReceiveAdmission) Admit(ctx context.Context, plugin gitservice.ReceivePlugin, sessionID string, commands []ReceiveCommand) (gitservice.PreparedReceiveBatch, error) {
	if a == nil || a.coordinator == nil || a.objects == nil || a.validator == nil {
		return gitservice.PreparedReceiveBatch{}, errors.New("receive admission is not configured")
	}
	if plugin.ID == "" || plugin.Name == "" || sessionID == "" || len(commands) == 0 {
		return gitservice.PreparedReceiveBatch{}, errors.New("invalid receive admission request")
	}
	coordination, err := a.coordinator.Open(ctx, plugin.ID, sessionID)
	if err != nil {
		return gitservice.PreparedReceiveBatch{}, errors.New("receive coordination unavailable")
	}
	if coordination == nil {
		return gitservice.PreparedReceiveBatch{}, errors.New("receive coordination unavailable")
	}
	defer coordination.Close()

	batch := gitservice.ReceiveBatch{SessionID: sessionID, Plugin: plugin, Commands: make([]gitservice.ReceiveRefCommand, 0, len(commands))}
	for _, command := range commands {
		batch.Commands = append(batch.Commands, gitservice.ReceiveRefCommand(command))
		tag, canonical := CanonicalTagFromRef(command.RefName)
		if !canonical {
			continue
		}
		tagCommand, err := a.classifyTag(ctx, tag, command)
		if err != nil {
			return gitservice.PreparedReceiveBatch{}, errors.New("canonical tag rejected")
		}
		if tagCommand.Operation != gitservice.ReceiveTagDelete {
			if err := a.validator.ValidateTag(ctx, plugin, tagCommand); err != nil {
				return gitservice.PreparedReceiveBatch{}, errors.New("canonical tag rejected")
			}
		}
		batch.CanonicalTags = append(batch.CanonicalTags, tagCommand)
	}
	prepared, err := coordination.Prepare(ctx, batch)
	if err != nil {
		return gitservice.PreparedReceiveBatch{}, errors.New("receive batch rejected")
	}
	return prepared, nil
}

func (a *ReceiveAdmission) classifyTag(ctx context.Context, tag string, command ReceiveCommand) (gitservice.ReceiveTagCommand, error) {
	zeroOld := allZeroObjectID(command.OldObjectID)
	zeroNew := allZeroObjectID(command.NewObjectID)
	if zeroOld && zeroNew {
		return gitservice.ReceiveTagCommand{}, errors.New("invalid no-op command")
	}
	tagCommand := gitservice.ReceiveTagCommand{
		Tag:         tag,
		RefName:     command.RefName,
		OldObjectID: command.OldObjectID,
		NewObjectID: command.NewObjectID,
	}
	switch {
	case zeroOld:
		tagCommand.Operation = gitservice.ReceiveTagCreate
	case zeroNew:
		tagCommand.Operation = gitservice.ReceiveTagDelete
	default:
		tagCommand.Operation = gitservice.ReceiveTagMove
	}
	var err error
	if !zeroOld {
		tagCommand.OldCommitObjectID, err = a.objects.PeelCommit(ctx, command.OldObjectID)
		if err != nil {
			return gitservice.ReceiveTagCommand{}, err
		}
	}
	if !zeroNew {
		tagCommand.NewCommitObjectID, err = a.objects.PeelCommit(ctx, command.NewObjectID)
		if err != nil {
			return gitservice.ReceiveTagCommand{}, err
		}
	}
	return tagCommand, nil
}

func allZeroObjectID(oid string) bool {
	return oid != "" && strings.Trim(oid, "0") == ""
}

func RunPreReceiveSession(ctx context.Context, input io.Reader, objectFormat ObjectFormat, admit func(context.Context, []ReceiveCommand) error) error {
	if input == nil || admit == nil {
		return errors.New("invalid pre-receive session")
	}
	commands, err := ParseReceiveCommands(input, objectFormat)
	if err != nil {
		return err
	}
	if err := admit(ctx, commands); err != nil {
		return errors.New("receive rejected")
	}
	return nil
}

type receiveHookSession struct {
	listener       net.Listener
	done           chan error
	service        *Service
	inspector      *pluginSourceInspector
	context        receiveContext
	repositoryPath string
	sessionID      string
	cancel         context.CancelFunc
}

func startReceiveHookSession(ctx context.Context, service *Service, inspector *pluginSourceInspector, receive receiveContext) (*receiveHookSession, error) {
	if service == nil || inspector == nil || receive.coordinator == nil || receive.repositoryID == "" || receive.pluginName == "" {
		return nil, errors.New("protected receive is not configured")
	}
	repositoryPath, err := service.existingRepositoryPath(receive.repositoryID)
	if err != nil {
		return nil, err
	}
	socketDirectory, err := os.MkdirTemp("", "marketplace-receive-")
	if err != nil {
		return nil, errors.New("create protected receive session")
	}
	socketPath := filepath.Join(socketDirectory, "hook.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		_ = os.RemoveAll(socketDirectory)
		return nil, errors.New("create protected receive session")
	}
	sessionContext, cancel := context.WithCancel(ctx)
	session := &receiveHookSession{
		listener: listener, done: make(chan error, 1), service: service, inspector: inspector,
		context: receive, repositoryPath: repositoryPath, sessionID: uuid.NewString(), cancel: cancel,
	}
	go session.serve(sessionContext)
	return session, nil
}

func (s *receiveHookSession) SocketPath() string { return s.listener.Addr().String() }

func (s *receiveHookSession) Wait(_ error) error {
	s.cancel()
	_ = s.listener.Close()
	err := <-s.done
	_ = os.RemoveAll(filepath.Dir(s.listener.Addr().String()))
	return err
}

func (s *receiveHookSession) Abort() {
	s.cancel()
	_ = s.listener.Close()
	<-s.done
	_ = os.RemoveAll(filepath.Dir(s.listener.Addr().String()))
}

func (s *receiveHookSession) serve(ctx context.Context) {
	for {
		connection, err := s.listener.Accept()
		if err != nil {
			s.done <- errors.New("protected receive hook unavailable")
			return
		}
		reader := bufio.NewReader(connection)
		mode, err := reader.ReadString('\n')
		if err != nil {
			_ = connection.Close()
			s.done <- errors.New("protected receive hook request failed")
			return
		}
		switch strings.TrimSpace(mode) {
		case "pre-receive":
			_, err = io.Copy(io.Discard, reader)
			_ = connection.Close()
			if err != nil {
				s.done <- errors.New("protected receive hook request failed")
				return
			}
		case "proc-receive":
			plugin := gitservice.ReceivePlugin{ID: s.context.repositoryID, Name: s.context.pluginName}
			lifecycle := &protectedReceiveLifecycle{
				coordinator: s.context.coordinator, service: s.service, inspector: s.inspector,
				repositoryID: s.context.repositoryID, repositoryPath: s.repositoryPath,
				plugin: plugin, sessionID: s.sessionID,
			}
			err = RunCoordinatedProcReceiveSession(ctx, s.service.gitBinary, s.repositoryPath, reader, connection, lifecycle)
			_ = connection.Close()
			s.done <- err
			return
		default:
			_ = connection.Close()
			s.done <- errors.New("unsupported protected receive hook")
			return
		}
	}
}

func runReceiveHookClient(ctx context.Context, mode, socketPath string, input io.Reader, output io.Writer) error {
	if !protectedReceiveHookModes[mode] || socketPath == "" {
		return errors.New("protected receive hook is not configured")
	}
	connection, err := (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
	if err != nil {
		return errors.New("protected receive coordinator unavailable")
	}
	defer connection.Close()
	if _, err := io.WriteString(connection, mode+"\n"); err != nil {
		return errors.New("protected receive request failed")
	}
	if _, err := io.Copy(connection, input); err != nil {
		return errors.New("protected receive request failed")
	}
	if unix, ok := connection.(*net.UnixConn); ok {
		_ = unix.CloseWrite()
	}
	if _, err := io.Copy(output, connection); err != nil {
		return errors.New("protected receive response failed")
	}
	return nil
}

type protectedReceiveLifecycle struct {
	coordinator    gitservice.ReceiveCoordinator
	service        *Service
	inspector      *pluginSourceInspector
	repositoryID   string
	repositoryPath string
	plugin         gitservice.ReceivePlugin
	sessionID      string
	coordination   gitservice.ReceiveCoordination
	prepared       gitservice.PreparedReceiveBatch
}

func (l *protectedReceiveLifecycle) Prepare(ctx context.Context, commands []ReceiveCommand) error {
	if l == nil || l.coordinator == nil || l.service == nil || l.inspector == nil || l.repositoryID == "" || l.repositoryPath == "" || l.plugin.ID == "" || l.plugin.Name == "" || l.sessionID == "" {
		return errors.New("protected receive is not configured")
	}
	coordination, err := l.coordinator.Open(ctx, l.plugin.ID, l.sessionID)
	if err != nil || coordination == nil {
		return errors.New("receive coordination unavailable")
	}
	l.coordination = coordination
	batch := gitservice.ReceiveBatch{SessionID: l.sessionID, Plugin: l.plugin, Commands: make([]gitservice.ReceiveRefCommand, 0, len(commands))}
	for _, command := range commands {
		batch.Commands = append(batch.Commands, gitservice.ReceiveRefCommand(command))
		tag, canonical := CanonicalTagFromRef(command.RefName)
		if !canonical {
			continue
		}
		tagCommand, err := l.classifyTag(ctx, tag, command)
		if err != nil {
			l.close()
			return errors.New("canonical tag rejected")
		}
		if tagCommand.Operation != gitservice.ReceiveTagDelete {
			inspection, err := l.inspector.inspectCommit(ctx, l.repositoryPath, tagCommand.NewObjectID, tagCommand.NewCommitObjectID, l.plugin.Name)
			if err != nil {
				l.close()
				return errors.New("canonical tag rejected")
			}
			tagCommand.NewManifestDigest = inspection.ManifestDigest
			tagCommand.NewManifestSnapshot = inspection.ManifestSnapshot
		}
		batch.CanonicalTags = append(batch.CanonicalTags, tagCommand)
	}
	prepared, err := coordination.Prepare(ctx, batch)
	if err != nil {
		l.close()
		return errors.New("receive batch rejected")
	}
	l.prepared = prepared
	return nil
}

func (l *protectedReceiveLifecycle) classifyTag(ctx context.Context, tag string, command ReceiveCommand) (gitservice.ReceiveTagCommand, error) {
	objects := repositoryReceiveObjects{gitBinary: l.service.gitBinary, repositoryPath: l.repositoryPath}
	admission := ReceiveAdmission{objects: objects}
	return admission.classifyTag(ctx, tag, command)
}

func (l *protectedReceiveLifecycle) Finalize(ctx context.Context, _ []ReceiveCommand, _ bool) error {
	defer l.close()
	if l.coordination == nil {
		return errors.New("receive coordination unavailable")
	}
	if l.prepared.ID == "" && len(l.prepared.Transitions) == 0 {
		return nil
	}
	observed := make([]gitservice.ObservedReceiveRef, 0, len(l.prepared.Transitions))
	for _, transition := range l.prepared.Transitions {
		ref, err := l.service.ReadPluginRef(ctx, l.repositoryID, transition.RefName)
		if err != nil {
			return errors.New("receive ref reread failed")
		}
		observed = append(observed, ref)
	}
	return l.coordination.Resolve(ctx, l.prepared, ClassifyReceiveResolution(l.prepared, observed))
}

func (l *protectedReceiveLifecycle) close() {
	if l.coordination != nil {
		_ = l.coordination.Close()
		l.coordination = nil
	}
}

type repositoryReceiveObjects struct {
	gitBinary      string
	repositoryPath string
}

func (o repositoryReceiveObjects) PeelCommit(ctx context.Context, objectID string) (string, error) {
	command := exec.CommandContext(ctx, o.gitBinary, "--git-dir="+o.repositoryPath, "rev-parse", "--verify", objectID+"^{commit}")
	command.Env = minimalGitEnvironment()
	command.Stderr = io.Discard
	output, err := command.Output()
	if err != nil {
		return "", errors.New("receive object unavailable")
	}
	commit := strings.TrimSpace(string(output))
	if !validGitObjectID(commit) {
		return "", errors.New("receive object unavailable")
	}
	return commit, nil
}

type ProcReceiveLifecycle interface {
	Prepare(context.Context, []ReceiveCommand) error
	Finalize(context.Context, []ReceiveCommand, bool) error
}

type procReceiveLifecycleFunc struct {
	admit func(context.Context, []ReceiveCommand) error
}

func (l procReceiveLifecycleFunc) Prepare(ctx context.Context, commands []ReceiveCommand) error {
	if l.admit == nil {
		return nil
	}
	return l.admit(ctx, commands)
}

func (procReceiveLifecycleFunc) Finalize(context.Context, []ReceiveCommand, bool) error { return nil }

func RunProcReceiveSession(ctx context.Context, gitBinary, repositoryPath string, input io.Reader, output io.Writer, admit func(context.Context, []ReceiveCommand) error) error {
	return RunCoordinatedProcReceiveSession(ctx, gitBinary, repositoryPath, input, output, procReceiveLifecycleFunc{admit: admit})
}

func RunCoordinatedProcReceiveSession(ctx context.Context, gitBinary, repositoryPath string, input io.Reader, output io.Writer, lifecycle ProcReceiveLifecycle) error {
	if gitBinary == "" || repositoryPath == "" || input == nil || output == nil {
		return errors.New("invalid proc-receive session")
	}
	reader := bufio.NewReader(input)
	negotiation, flush, err := readPktLine(reader)
	if err != nil || flush {
		return errors.New("invalid proc-receive negotiation")
	}
	version, features, err := parseProcReceiveNegotiation(negotiation)
	if err != nil {
		return err
	}
	if _, flush, err := readPktLine(reader); err != nil || !flush {
		return errors.New("invalid proc-receive negotiation flush")
	}
	responseFeatures := make([]string, 0, 2)
	if features["push-options"] {
		responseFeatures = append(responseFeatures, "push-options")
	}
	if features["atomic"] {
		responseFeatures = append(responseFeatures, "atomic")
	}
	response := "version=" + version
	if len(responseFeatures) > 0 {
		response += "\x00" + strings.Join(responseFeatures, " ")
	}
	if _, err := io.WriteString(output, formatPktLine(response)); err != nil {
		return errors.New("write proc-receive negotiation")
	}
	if _, err := io.WriteString(output, "0000"); err != nil {
		return errors.New("write proc-receive negotiation flush")
	}

	var commandText strings.Builder
	for {
		payload, flush, err := readPktLine(reader)
		if err != nil {
			return errors.New("read proc-receive command")
		}
		if flush {
			break
		}
		if payload == "" || strings.ContainsAny(payload, "\x00\r\n") {
			return errors.New("invalid proc-receive command")
		}
		commandText.WriteString(payload)
		commandText.WriteByte('\n')
	}
	format, err := detectObjectFormat(commandText.String())
	if err != nil {
		return err
	}
	commands, err := ParseReceiveCommands(strings.NewReader(commandText.String()), format)
	if err != nil {
		return err
	}
	if features["push-options"] {
		if err := consumeProcReceivePushOptions(reader); err != nil {
			return err
		}
	}
	if lifecycle == nil {
		lifecycle = procReceiveLifecycleFunc{}
	}
	if err := lifecycle.Prepare(ctx, commands); err != nil {
		if resultErr := writeProcReceiveResults(output, commands, false); resultErr != nil {
			return resultErr
		}
		return errors.New("receive rejected")
	}
	transaction, err := BuildUpdateRefTransaction(commands)
	if err != nil {
		if resultErr := writeProcReceiveResults(output, commands, false); resultErr != nil {
			return resultErr
		}
		return err
	}
	cmd := exec.CommandContext(ctx, gitBinary, "--git-dir="+repositoryPath, "update-ref", "--stdin")
	cmd.Env = minimalGitEnvironment()
	cmd.Stdin = bytesReader(transaction)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		if finalizeErr := lifecycle.Finalize(ctx, commands, false); finalizeErr != nil {
			return errors.New("receive resolution failed")
		}
		if resultErr := writeProcReceiveResults(output, commands, false); resultErr != nil {
			return resultErr
		}
		return errors.New("receive ref transaction failed")
	}
	if err := lifecycle.Finalize(ctx, commands, true); err != nil {
		return errors.New("receive resolution failed")
	}
	if err := writeProcReceiveResults(output, commands, true); err != nil {
		return err
	}
	return nil
}

func parseProcReceiveNegotiation(payload string) (string, map[string]bool, error) {
	payload = strings.TrimSuffix(payload, "\n")
	if strings.ContainsAny(payload, "\r\n") {
		return "", nil, errors.New("invalid proc-receive negotiation")
	}
	parts := strings.SplitN(payload, "\x00", 2)
	if parts[0] != "version=1" {
		return "", nil, errors.New("unsupported proc-receive protocol")
	}
	features := make(map[string]bool)
	if len(parts) == 1 || parts[1] == "" {
		return "1", features, nil
	}
	if strings.ContainsRune(parts[1], '\x00') {
		return "", nil, errors.New("invalid proc-receive negotiation")
	}
	seen := make(map[string]struct{})
	for _, feature := range strings.Split(parts[1], " ") {
		if feature == "" || containsProtocolControl(feature) {
			return "", nil, errors.New("invalid proc-receive negotiation")
		}
		if _, duplicate := seen[feature]; duplicate {
			return "", nil, errors.New("invalid proc-receive negotiation")
		}
		seen[feature] = struct{}{}
		switch feature {
		case "atomic", "push-options":
			features[feature] = true
		}
	}
	return "1", features, nil
}

func consumeProcReceivePushOptions(reader *bufio.Reader) error {
	for {
		option, flush, err := readPktLine(reader)
		if err != nil {
			return errors.New("read proc-receive push options")
		}
		if flush {
			return nil
		}
		if strings.ContainsAny(option, "\x00\r\n") {
			return errors.New("invalid proc-receive push option")
		}
	}
}

func containsProtocolControl(value string) bool {
	for i := 0; i < len(value); i++ {
		if value[i] <= ' ' || value[i] == 0x7f {
			return true
		}
	}
	return false
}

func detectObjectFormat(commands string) (ObjectFormat, error) {
	line, _, _ := strings.Cut(commands, "\n")
	fields := strings.Split(line, " ")
	if len(fields) != 3 {
		return "", errors.New("invalid proc-receive command")
	}
	switch len(fields[0]) {
	case 40:
		return ObjectFormatSHA1, nil
	case 64:
		return ObjectFormatSHA256, nil
	default:
		return "", errors.New("unsupported receive object format")
	}
}

func RunReceiveHook(ctx context.Context, mode, gitBinary, repositoryPath string, objectFormat ObjectFormat, input io.Reader, output io.Writer, admit func(context.Context, []ReceiveCommand) error) error {
	switch mode {
	case "pre-receive":
		return RunPreReceiveSession(ctx, input, objectFormat, admit)
	case "proc-receive":
		return RunProcReceiveSession(ctx, gitBinary, repositoryPath, input, output, admit)
	default:
		return errors.New("unsupported receive hook mode")
	}
}

func readPktLine(reader *bufio.Reader) (string, bool, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return "", false, err
	}
	length, err := strconv.ParseUint(string(header), 16, 16)
	if err != nil {
		return "", false, errors.New("invalid pkt-line length")
	}
	if length == 0 {
		return "", true, nil
	}
	if length < 4 || length > 65520 {
		return "", false, errors.New("invalid pkt-line length")
	}
	payload := make([]byte, int(length)-4)
	if _, err := io.ReadFull(reader, payload); err != nil {
		return "", false, err
	}
	return strings.TrimSuffix(string(payload), "\n"), false, nil
}

func formatPktLine(payload string) string {
	return fmt.Sprintf("%04x%s\n", len(payload)+5, payload)
}

func writeProcReceiveResults(output io.Writer, commands []ReceiveCommand, accepted bool) error {
	status := "ok"
	reason := ""
	if !accepted {
		status = "ng"
		reason = " receive rejected"
	}
	for _, command := range commands {
		if _, err := io.WriteString(output, formatPktLine(status+" "+command.RefName+reason)); err != nil {
			return errors.New("write proc-receive result")
		}
	}
	if _, err := io.WriteString(output, "0000"); err != nil {
		return errors.New("write proc-receive result flush")
	}
	return nil
}

func minimalGitEnvironment() []string {
	environment := []string{"GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0", "LANG=C"}
	for _, name := range []string{"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_QUARANTINE_PATH"} {
		if value, ok := os.LookupEnv(name); ok {
			environment = append(environment, name+"="+value)
		}
	}
	return environment
}

func bytesReader(data []byte) io.Reader {
	return strings.NewReader(string(data))
}

func BuildUpdateRefTransaction(commands []ReceiveCommand) ([]byte, error) {
	if len(commands) == 0 {
		return nil, errors.New("empty update-ref transaction")
	}
	var transaction strings.Builder
	transaction.WriteString("start\n")
	seen := make(map[string]struct{}, len(commands))
	for _, command := range commands {
		if !validReceiveRefName(command.RefName) || command.OldObjectID == "" || command.NewObjectID == "" || len(command.OldObjectID) != len(command.NewObjectID) {
			return nil, errors.New("invalid update-ref command")
		}
		if _, duplicate := seen[command.RefName]; duplicate {
			return nil, errors.New("duplicate update-ref command")
		}
		seen[command.RefName] = struct{}{}
		if allZeroObjectID(command.NewObjectID) {
			if allZeroObjectID(command.OldObjectID) {
				return nil, errors.New("invalid update-ref no-op")
			}
			fmt.Fprintf(&transaction, "delete %s %s\n", command.RefName, command.OldObjectID)
			continue
		}
		fmt.Fprintf(&transaction, "update %s %s %s\n", command.RefName, command.NewObjectID, command.OldObjectID)
	}
	transaction.WriteString("prepare\ncommit\n")
	return []byte(transaction.String()), nil
}

type ReceiveRefReader interface {
	ReadRef(ctx context.Context, refName string) (gitservice.ObservedReceiveRef, error)
}

func ClassifyReceiveResolution(prepared gitservice.PreparedReceiveBatch, observed []gitservice.ObservedReceiveRef) gitservice.ReceiveResolution {
	resolution := gitservice.ReceiveResolution{Observed: append([]gitservice.ObservedReceiveRef(nil), observed...)}
	if prepared.ID == "" || len(prepared.Transitions) == 0 || len(observed) != len(prepared.Transitions) {
		resolution.Disposition = gitservice.ReceiveManualRequired
		return resolution
	}
	observedByRef := make(map[string]gitservice.ObservedReceiveRef, len(observed))
	for _, ref := range observed {
		if ref.RefName == "" {
			resolution.Disposition = gitservice.ReceiveManualRequired
			return resolution
		}
		if _, duplicate := observedByRef[ref.RefName]; duplicate {
			resolution.Disposition = gitservice.ReceiveManualRequired
			return resolution
		}
		observedByRef[ref.RefName] = ref
	}
	allProposed := true
	allOld := true
	for _, transition := range prepared.Transitions {
		ref, ok := observedByRef[transition.RefName]
		if !ok {
			resolution.Disposition = gitservice.ReceiveManualRequired
			return resolution
		}
		proposedMatches := observedObjectIDMatches(ref, transition.ProposedNewObjectID)
		oldMatches := observedObjectIDMatches(ref, transition.ExpectedOldObjectID)
		allProposed = allProposed && proposedMatches
		allOld = allOld && oldMatches
		if !proposedMatches && !oldMatches {
			resolution.Disposition = gitservice.ReceiveManualRequired
			return resolution
		}
	}
	switch {
	case allProposed:
		resolution.Disposition = gitservice.ReceiveCompleted
	case allOld:
		resolution.Disposition = gitservice.ReceiveAborted
	default:
		resolution.Disposition = gitservice.ReceiveManualRequired
	}
	return resolution
}

func observedObjectIDMatches(observed gitservice.ObservedReceiveRef, expected string) bool {
	if allZeroObjectID(expected) {
		return !observed.Exists
	}
	return observed.Exists && observed.ObjectID == expected
}

type ReceiveFinalizer struct {
	coordinator gitservice.ReceiveCoordinator
	refs        ReceiveRefReader
}

func NewReceiveFinalizer(coordinator gitservice.ReceiveCoordinator, refs ReceiveRefReader) *ReceiveFinalizer {
	return &ReceiveFinalizer{coordinator: coordinator, refs: refs}
}

func (f *ReceiveFinalizer) Finalize(ctx context.Context, pluginID, sessionID string, prepared gitservice.PreparedReceiveBatch) error {
	if f == nil || f.coordinator == nil || f.refs == nil || pluginID == "" || sessionID == "" || prepared.ID == "" || len(prepared.Transitions) == 0 {
		return errors.New("invalid receive finalization request")
	}
	coordination, err := f.coordinator.Open(ctx, pluginID, sessionID)
	if err != nil || coordination == nil {
		return errors.New("receive coordination unavailable")
	}
	defer coordination.Close()
	observed := make([]gitservice.ObservedReceiveRef, 0, len(prepared.Transitions))
	for _, transition := range prepared.Transitions {
		ref, err := f.refs.ReadRef(ctx, transition.RefName)
		if err != nil {
			return errors.New("receive ref reread failed")
		}
		observed = append(observed, ref)
	}
	resolution := ClassifyReceiveResolution(prepared, observed)
	if err := coordination.Resolve(ctx, prepared, resolution); err != nil {
		return errors.New("receive resolution failed")
	}
	return nil
}

func objectIDLength(format ObjectFormat) (int, error) {
	switch format {
	case ObjectFormatSHA1:
		return 40, nil
	case ObjectFormatSHA256:
		return 64, nil
	default:
		return 0, fmt.Errorf("unsupported object format %q", format)
	}
}

func validObjectID(oid string, length int) bool {
	if len(oid) != length {
		return false
	}
	for _, c := range oid {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

func validReceiveRefName(refName string) bool {
	if !strings.HasPrefix(refName, "refs/") || strings.ContainsAny(refName, " \t\r\n\x00") {
		return false
	}
	if strings.HasPrefix(refName, "refs/heads/") || strings.HasPrefix(refName, "refs/tags/") {
		return len(strings.Split(refName, "/")) >= 3
	}
	return false
}
