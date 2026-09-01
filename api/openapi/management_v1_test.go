package openapi_test

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const (
	loginPath             = "/api/v1/auth/login"
	healthPath            = "/api/v1/health"
	tokensPath            = "/api/v1/me/tokens"
	tokenPath             = "/api/v1/me/tokens/{tokenId}"
	revealPath            = "/api/v1/me/tokens/{tokenId}/reveal"
	pluginsPath           = "/api/v1/namespaces/{namespace}/plugins"
	pluginPath            = "/api/v1/namespaces/{namespace}/plugins/{plugin}"
	pluginVersionsPath    = "/api/v1/namespaces/{namespace}/plugins/{plugin}/versions"
	publishVersionPath    = "/api/v1/namespaces/{namespace}/plugins/{plugin}/versions:publish"
	pluginVersionPath     = "/api/v1/namespaces/{namespace}/plugins/{plugin}/versions/{tag}"
	repositoryRefsPath    = "/api/v1/namespaces/{namespace}/plugins/{plugin}/repository/refs"
	repositoryTreePath    = "/api/v1/namespaces/{namespace}/plugins/{plugin}/repository/tree"
	repositoryBlobPath    = "/api/v1/namespaces/{namespace}/plugins/{plugin}/repository/blob"
	repositoryCommitsPath = "/api/v1/namespaces/{namespace}/plugins/{plugin}/repository/commits"
	archivePluginPath     = "/api/v1/namespaces/{namespace}/plugins/{plugin}:archive"
	restorePluginPath     = "/api/v1/namespaces/{namespace}/plugins/{plugin}:restore"
	visibilityPath        = "/api/v1/namespaces/{namespace}/plugins/{plugin}:set-visibility"
	defaultVersionPath    = "/api/v1/namespaces/{namespace}/plugins/{plugin}/versions/{tag}:set-default"
	clearDefaultPath      = "/api/v1/namespaces/{namespace}/plugins/{plugin}/default-version"
	capabilitiesPath      = "/api/v1/auth/capabilities"
	registerPath          = "/api/v1/auth/register"
	mePath                = "/api/v1/me"
	adminUsersPath        = "/api/v1/admin/users"
	adminUserPath         = "/api/v1/admin/users/{userId}"
	disableUserPath       = "/api/v1/admin/users/{userId}:disable"
	enableUserPath        = "/api/v1/admin/users/{userId}:enable"
	systemAdminPath       = "/api/v1/admin/users/{userId}/system-admin"
	teamsPath             = "/api/v1/teams"
	teamPath              = "/api/v1/teams/{team}"
	teamMembersPath       = "/api/v1/teams/{team}/members"
	teamMemberPath        = "/api/v1/teams/{team}/members/{userId}"
	teamInvitesPath       = "/api/v1/teams/{team}/invitations"
	teamInvitePath        = "/api/v1/teams/{team}/invitations/{invitationId}"
	reissueInvitePath     = "/api/v1/teams/{team}/invitations/{invitationId}:reissue"
	inboxPath             = "/api/v1/me/team-invitations"
	acceptInvitePath      = "/api/v1/me/team-invitations/{invitationId}:accept"
	rejectInvitePath      = "/api/v1/me/team-invitations/{invitationId}:reject"
)

func TestManagementV1ParsesAndResolvesLocalReferences(t *testing.T) {
	document := loadDocument(t, "management-v1.yaml")
	if got := stringValue(t, document, "openapi"); got != "3.1.0" {
		t.Fatalf("openapi = %q, want 3.1.0", got)
	}
	walkReferences(t, document, document, "#")
}

func TestManagementV1PublishesOnlyImplementedSlices(t *testing.T) {
	document := loadDocument(t, "management-v1.yaml")
	paths := mapValue(t, document, "paths")
	want := []string{
		capabilitiesPath, loginPath, registerPath, healthPath, mePath,
		adminUsersPath, adminUserPath, disableUserPath, enableUserPath, systemAdminPath,
		teamsPath, teamPath, teamMembersPath, teamMemberPath, teamInvitesPath,
		teamInvitePath, reissueInvitePath, inboxPath, acceptInvitePath, rejectInvitePath,
		tokensPath, tokenPath, revealPath, pluginsPath, pluginPath, archivePluginPath,
		restorePluginPath, visibilityPath, pluginVersionsPath, publishVersionPath,
		pluginVersionPath, defaultVersionPath, clearDefaultPath,
		repositoryRefsPath, repositoryTreePath, repositoryBlobPath, repositoryCommitsPath,
	}
	sort.Strings(want)
	got := sortedKeys(paths)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("published paths = %v, want %v", got, want)
	}

	assertSecurity(t, operation(t, document, loginPath, "post"), []any{})
	assertSecurity(t, operation(t, document, healthPath, "get"), []any{})
	for _, target := range []struct {
		path   string
		method string
	}{
		{tokensPath, "get"},
		{tokensPath, "post"},
		{tokenPath, "delete"},
		{revealPath, "post"},
	} {
		assertBearerOnly(t, operation(t, document, target.path, target.method))
	}

	schemes := mapValue(t, mapValue(t, document, "components"), "securitySchemes")
	if got := sortedKeys(schemes); !reflect.DeepEqual(got, []string{"BearerJWT"}) {
		t.Fatalf("security schemes = %v, want BearerJWT only", got)
	}
}

func TestManagementV1IdentityResponsesAndSchemas(t *testing.T) {
	document := loadDocument(t, "management-v1.yaml")
	assertStatuses(t, operation(t, document, capabilitiesPath, "get"), "200")
	assertStatuses(t, operation(t, document, registerPath, "post"), "201", "400", "404", "409", "422", "500")
	assertBearerOnly(t, operation(t, document, mePath, "get"))
	assertStatuses(t, operation(t, document, mePath, "get"), "200", "401", "403", "404", "500")
	assertStatuses(t, operation(t, document, loginPath, "post"), "200", "400", "401", "500")
	assertStatuses(t, operation(t, document, healthPath, "get"), "200")
	assertStatuses(t, operation(t, document, tokensPath, "get"), "200", "401", "403", "422", "500")
	assertStatuses(t, operation(t, document, tokensPath, "post"), "201", "400", "401", "403", "422", "500")
	assertStatuses(t, operation(t, document, tokenPath, "delete"), "204", "401", "403", "404", "500")
	assertStatuses(t, operation(t, document, revealPath, "post"), "200", "400", "401", "403", "404", "500")
	assertBearerOnly(t, operation(t, document, adminUsersPath, "get"))
	assertBearerOnly(t, operation(t, document, adminUsersPath, "post"))
	assertStatuses(t, operation(t, document, adminUsersPath, "get"), "200", "401", "403", "422", "500")
	assertStatuses(t, operation(t, document, adminUsersPath, "post"), "201", "400", "401", "403", "409", "422", "500")
	for _, target := range []struct{ path, method string }{{adminUserPath, "get"}, {adminUserPath, "patch"}, {disableUserPath, "post"}, {enableUserPath, "post"}, {systemAdminPath, "put"}, {systemAdminPath, "delete"}, {teamsPath, "get"}, {teamsPath, "post"}, {teamPath, "get"}, {teamPath, "patch"}, {teamMembersPath, "get"}, {teamMemberPath, "put"}, {teamMemberPath, "delete"}, {teamInvitesPath, "get"}, {teamInvitesPath, "post"}, {teamInvitePath, "delete"}, {reissueInvitePath, "post"}, {inboxPath, "get"}, {acceptInvitePath, "post"}, {rejectInvitePath, "post"}} {
		assertBearerOnly(t, operation(t, document, target.path, target.method))
	}

	components := mapValue(t, document, "components")
	schemas := mapValue(t, components, "schemas")
	assertRequired(t, schema(t, schemas, "LoginSuccess"), "data")
	assertRequired(t, schema(t, schemas, "HealthSuccess"), "data")
	assertRequired(t, schema(t, schemas, "TokenPageData"), "items", "page", "size", "total")
	assertRequired(t, schema(t, schemas, "TokenMetadata"), "id", "name", "preset", "status", "expiresAt", "lastUsedAt", "revokedAt", "createdAt")
	assertRequired(t, schema(t, schemas, "CreateTokenRequest"), "name", "preset")
	assertRequired(t, schema(t, schemas, "RevealTokenRequest"), "password")
	assertRequired(t, schema(t, schemas, "Error"), "code", "message", "requestId")

	preset := schema(t, schemas, "TokenPreset")
	if got := stringSlice(t, preset["enum"]); !reflect.DeepEqual(got, []string{"sub-read", "git-clone", "git-write"}) {
		t.Fatalf("TokenPreset enum = %v", got)
	}
	status := schema(t, schemas, "TokenStatus")
	if got := stringSlice(t, status["enum"]); !reflect.DeepEqual(got, []string{"active", "expired", "revoked"}) {
		t.Fatalf("TokenStatus enum = %v", got)
	}

	parameters := mapValue(t, components, "parameters")
	if got := sortedKeys(parameters); !reflect.DeepEqual(got, []string{"InvitationId", "Namespace", "Page", "PluginSlug", "RepositoryPath", "RepositoryRef", "RequiredRepositoryPath", "Size", "TagName", "TeamScope", "TeamSlug", "TokenId", "UserId", "UserStatus"}) {
		t.Fatalf("parameters = %v, want deployed management parameters", got)
	}
	pageSchema := mapValue(t, mapValue(t, parameters, "Page"), "schema")
	sizeSchema := mapValue(t, mapValue(t, parameters, "Size"), "schema")
	if pageSchema["default"] != 1 || sizeSchema["default"] != 20 || sizeSchema["maximum"] != 100 {
		t.Fatalf("pagination defaults/bounds do not match runtime: page=%v size=%v", pageSchema, sizeSchema)
	}
}

func TestManagementV1PlaintextAppearsOnlyInCreateAndRevealSuccess(t *testing.T) {
	document := loadDocument(t, "management-v1.yaml")
	schemas := mapValue(t, mapValue(t, document, "components"), "schemas")

	plaintextPATSchema := schema(t, schemas, "RevealedToken")
	if !containsProperty(plaintextPATSchema, "token") {
		t.Fatal("RevealedToken must contain plaintext token")
	}
	for _, name := range []string{"TokenMetadata", "TokenPageData", "TokenPageSuccess"} {
		if containsProperty(schema(t, schemas, name), "token") {
			t.Fatalf("%s must not contain plaintext token", name)
		}
	}

	assertResponseSchema(t, operation(t, document, tokensPath, "post"), "201", "#/components/schemas/CreatedTokenSuccess")
	assertResponseSchema(t, operation(t, document, revealPath, "post"), "200", "#/components/schemas/RevealedTokenSuccess")
	assertResponseSchema(t, operation(t, document, tokensPath, "get"), "200", "#/components/schemas/TokenPageSuccess")
	assertResponseSchema(t, operation(t, document, loginPath, "post"), "200", "#/components/schemas/LoginSuccess")
	assertResponseSchema(t, operation(t, document, healthPath, "get"), "200", "#/components/schemas/HealthSuccess")
	assertNoResponseContent(t, operation(t, document, tokenPath, "delete"), "204")

	for _, target := range []struct {
		operation map[string]any
		status    string
	}{
		{operation(t, document, tokensPath, "post"), "201"},
		{operation(t, document, revealPath, "post"), "200"},
	} {
		response := response(t, target.operation, target.status)
		headers := mapValue(t, response, "headers")
		if _, ok := headers["Cache-Control"]; !ok {
			t.Fatalf("secret response %s lacks Cache-Control", target.status)
		}
		if _, ok := headers["Pragma"]; !ok {
			t.Fatalf("secret response %s lacks Pragma", target.status)
		}
	}
}

func TestManagementV1RevealUnauthenticatedDeclaresOptionalBearerChallenge(t *testing.T) {
	document := loadDocument(t, "management-v1.yaml")
	responses := mapValue(t, mapValue(t, document, "components"), "responses")
	revealUnauthenticated := mapValue(t, responses, "RevealUnauthenticated")
	headers := mapValue(t, revealUnauthenticated, "headers")
	challenge := mapValue(t, headers, "WWW-Authenticate")
	if got := stringValue(t, challenge, "$ref"); got != "#/components/headers/BearerChallenge" {
		t.Fatalf("WWW-Authenticate $ref = %q", got)
	}
	if required, ok := challenge["required"].(bool); ok && required {
		t.Fatal("WWW-Authenticate must remain optional for wrong-password responses")
	}
}

func TestManagementV1DesignParsesAndResolvesLocalReferences(t *testing.T) {
	document := loadDocument(t, "management-v1-design.yaml")
	if got := stringValue(t, document, "openapi"); got != "3.1.0" {
		t.Fatalf("openapi = %q, want 3.1.0", got)
	}
	walkReferences(t, document, document, "#")
}

func TestManagementV1DesignCreatePATIncludesBadRequest(t *testing.T) {
	document := loadDocument(t, "management-v1-design.yaml")
	assertStatuses(t, operation(t, document, tokensPath, "post"), "201", "400", "401", "403", "422", "500")
}

func TestManagementV1DeployedPluginAndVersionSemantics(t *testing.T) {
	assertPluginAndVersionSemantics(t, loadDocument(t, "management-v1.yaml"))
}

func TestManagementV1DesignPluginAndVersionSemantics(t *testing.T) {
	assertPluginAndVersionSemantics(t, loadDocument(t, "management-v1-design.yaml"))
}

func assertPluginAndVersionSemantics(t *testing.T, document map[string]any) {
	t.Helper()

	assertBearerOnly(t, operation(t, document, pluginsPath, "get"))
	assertBearerOnly(t, operation(t, document, pluginsPath, "post"))
	assertStatuses(t, operation(t, document, pluginsPath, "get"), "200", "401", "403", "422", "500")
	assertStatuses(t, operation(t, document, pluginsPath, "post"), "201", "400", "401", "403", "409", "422", "500")

	assertSecurity(t, operation(t, document, pluginPath, "get"), []any{map[string]any{}, map[string]any{"BearerJWT": []any{}}})
	assertStatuses(t, operation(t, document, pluginPath, "get"), "200", "401", "403", "404", "500")
	for _, path := range []string{archivePluginPath, restorePluginPath} {
		assertBearerOnly(t, operation(t, document, path, "post"))
		assertStatuses(t, operation(t, document, path, "post"), "204", "401", "403", "404", "409", "500")
		assertNoResponseContent(t, operation(t, document, path, "post"), "204")
	}
	assertBearerOnly(t, operation(t, document, visibilityPath, "post"))
	assertStatuses(t, operation(t, document, visibilityPath, "post"), "204", "400", "401", "403", "404", "409", "422", "500")
	assertNoResponseContent(t, operation(t, document, visibilityPath, "post"), "204")

	assertBearerOnly(t, operation(t, document, pluginVersionsPath, "get"))
	assertStatuses(t, operation(t, document, pluginVersionsPath, "get"), "200", "401", "403", "404", "422", "500")
	assertBearerOnly(t, operation(t, document, publishVersionPath, "post"))
	assertStatuses(t, operation(t, document, publishVersionPath, "post"), "201", "400", "401", "403", "404", "409", "422", "500")
	assertSecurity(t, operation(t, document, pluginVersionPath, "get"), []any{map[string]any{}, map[string]any{"BearerJWT": []any{}}})
	assertStatuses(t, operation(t, document, pluginVersionPath, "get"), "200", "401", "403", "404", "410", "500")
	assertBearerOnly(t, operation(t, document, defaultVersionPath, "post"))
	assertStatuses(t, operation(t, document, defaultVersionPath, "post"), "204", "401", "403", "404", "409", "500")
	assertNoResponseContent(t, operation(t, document, defaultVersionPath, "post"), "204")
	assertBearerOnly(t, operation(t, document, clearDefaultPath, "delete"))
	assertStatuses(t, operation(t, document, clearDefaultPath, "delete"), "204", "401", "403", "404", "409", "500")
	assertNoResponseContent(t, operation(t, document, clearDefaultPath, "delete"), "204")

	schemas := mapValue(t, mapValue(t, document, "components"), "schemas")
	pluginStatus := schema(t, schemas, "PluginStatus")
	if got := stringSlice(t, pluginStatus["enum"]); !reflect.DeepEqual(got, []string{"draft", "active", "archived"}) {
		t.Fatalf("PluginStatus enum = %v", got)
	}
	plugin := schema(t, schemas, "Plugin")
	assertRequired(t, plugin, "namespace", "name", "status", "visibility", "repositoryStatus", "cloneUrl", "defaultVersion", "createdAt", "updatedAt")
	pluginProperties := mapValue(t, plugin, "properties")
	for _, forbidden := range []string{"id", "pluginId", "repositoryId", "storagePath", "storageKey"} {
		if _, ok := pluginProperties[forbidden]; ok {
			t.Fatalf("Plugin must not expose %s", forbidden)
		}
	}
	if got := stringSlice(t, schema(t, schemas, "RepositoryStatus")["enum"]); !reflect.DeepEqual(got, []string{"ready", "readOnly", "error"}) {
		t.Fatalf("RepositoryStatus enum = %v", got)
	}

	versionStatus := schema(t, schemas, "PluginVersionStatus")
	if got := stringValue(t, versionStatus, "const"); got != "available" {
		t.Fatalf("PluginVersionStatus const = %q", got)
	}
	version := schema(t, schemas, "PluginVersion")
	assertRequired(t, version, "tag", "status", "commitSha", "publishedAt", "updatedAt")
	versionProperties := mapValue(t, version, "properties")
	for _, forbidden := range []string{"id", "pluginId", "version"} {
		if _, ok := versionProperties[forbidden]; ok {
			t.Fatalf("PluginVersion must not expose %s", forbidden)
		}
	}
}

func loadDocument(t *testing.T, name string) map[string]any {
	t.Helper()
	_, current, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	contents, err := os.ReadFile(filepath.Join(filepath.Dir(current), name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	var document map[string]any
	if err := yaml.Unmarshal(contents, &document); err != nil {
		t.Fatalf("parse %s: %v", name, err)
	}
	return document
}

func operation(t *testing.T, document map[string]any, path, method string) map[string]any {
	t.Helper()
	return mapValue(t, mapValue(t, mapValue(t, document, "paths"), path), method)
}

func response(t *testing.T, operation map[string]any, status string) map[string]any {
	t.Helper()
	return mapValue(t, mapValue(t, operation, "responses"), status)
}

func schema(t *testing.T, schemas map[string]any, name string) map[string]any {
	t.Helper()
	return mapValue(t, schemas, name)
}

func assertBearerOnly(t *testing.T, operation map[string]any) {
	t.Helper()
	assertSecurity(t, operation, []any{map[string]any{"BearerJWT": []any{}}})
}

func assertSecurity(t *testing.T, operation map[string]any, want []any) {
	t.Helper()
	got, ok := operation["security"].([]any)
	if !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("security = %#v, want %#v", operation["security"], want)
	}
}

func assertStatuses(t *testing.T, operation map[string]any, want ...string) {
	t.Helper()
	got := sortedKeys(mapValue(t, operation, "responses"))
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("response statuses = %v, want %v", got, want)
	}
}

func assertRequired(t *testing.T, schema map[string]any, want ...string) {
	t.Helper()
	got := stringSlice(t, schema["required"])
	sort.Strings(got)
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("required = %v, want %v", got, want)
	}
}

func assertResponseSchema(t *testing.T, operation map[string]any, status, want string) {
	t.Helper()
	applicationJSON := mapValue(t, mapValue(t, response(t, operation, status), "content"), "application/json")
	responseSchema := mapValue(t, applicationJSON, "schema")
	if got := stringValue(t, responseSchema, "$ref"); got != want {
		t.Fatalf("response %s schema = %q, want %q", status, got, want)
	}
}

func assertNoResponseContent(t *testing.T, operation map[string]any, status string) {
	t.Helper()
	if _, ok := response(t, operation, status)["content"]; ok {
		t.Fatalf("response %s unexpectedly declares content", status)
	}
}

func walkReferences(t *testing.T, root map[string]any, value any, location string) {
	t.Helper()
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if key == "$ref" {
				reference, ok := child.(string)
				if !ok || !strings.HasPrefix(reference, "#/") {
					t.Fatalf("%s.$ref = %#v, want local reference", location, child)
				}
				if _, err := resolveReference(root, reference); err != nil {
					t.Fatalf("%s.$ref %q: %v", location, reference, err)
				}
			}
			walkReferences(t, root, child, location+"/"+key)
		}
	case []any:
		for index, child := range typed {
			walkReferences(t, root, child, fmt.Sprintf("%s/%d", location, index))
		}
	}
}

func resolveReference(root map[string]any, reference string) (any, error) {
	var current any = root
	for _, segment := range strings.Split(strings.TrimPrefix(reference, "#/"), "/") {
		segment = strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
		mapping, ok := current.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%q does not resolve through an object", segment)
		}
		next, ok := mapping[segment]
		if !ok {
			return nil, fmt.Errorf("missing segment %q", segment)
		}
		current = next
	}
	return current, nil
}

func containsProperty(value any, property string) bool {
	switch typed := value.(type) {
	case map[string]any:
		if properties, ok := typed["properties"].(map[string]any); ok {
			if _, ok := properties[property]; ok {
				return true
			}
		}
		for _, child := range typed {
			if containsProperty(child, property) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsProperty(child, property) {
				return true
			}
		}
	}
	return false
}

func mapValue(t *testing.T, object map[string]any, key string) map[string]any {
	t.Helper()
	value, ok := object[key].(map[string]any)
	if !ok {
		t.Fatalf("%q = %#v, want object", key, object[key])
	}
	return value
}

func stringValue(t *testing.T, object map[string]any, key string) string {
	t.Helper()
	value, ok := object[key].(string)
	if !ok {
		t.Fatalf("%q = %#v, want string", key, object[key])
	}
	return value
}

func stringSlice(t *testing.T, value any) []string {
	t.Helper()
	values, ok := value.([]any)
	if !ok {
		t.Fatalf("value = %#v, want array", value)
	}
	result := make([]string, len(values))
	for index, value := range values {
		text, ok := value.(string)
		if !ok {
			t.Fatalf("value[%d] = %#v, want string", index, value)
		}
		result[index] = text
	}
	return result
}

func sortedKeys(object map[string]any) []string {
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
