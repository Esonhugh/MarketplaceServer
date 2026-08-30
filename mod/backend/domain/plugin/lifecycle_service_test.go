package plugin

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

func TestServiceVersionReadsAreScopedAuthorizedAndHideTombstones(t *testing.T) {
	db := newServiceTestDatabase(t)
	alpha := createServiceTestNamespace(t, db, "alpha")
	bravo := createServiceTestNamespace(t, db, "bravo")
	now := time.Now().UTC()
	alphaPlugin := createServiceTestAggregate(t, db, alpha.ID, "scanner", PluginStatusActive, VisibilityPublic, RepositoryStatusReady, now)
	bravoPlugin := createServiceTestAggregate(t, db, bravo.ID, "scanner", PluginStatusActive, VisibilityPublic, RepositoryStatusReady, now)
	available := createServiceTestVersion(t, db, alphaPlugin.Plugin.ID, "v1.0.0", VersionStatusAvailable, strings.Repeat("a", 40), now.Add(time.Minute))
	createServiceTestVersion(t, db, alphaPlugin.Plugin.ID, "v0.9.0", VersionStatusAvailable, strings.Repeat("b", 40), now)
	deleted := createServiceTestVersion(t, db, alphaPlugin.Plugin.ID, "v2.0.0", VersionStatusDeleted, "", now.Add(2*time.Minute))
	createServiceTestVersion(t, db, bravoPlugin.Plugin.ID, "v3.0.0", VersionStatusAvailable, strings.Repeat("c", 40), now.Add(3*time.Minute))
	authorizer := &serviceTestAuthorizer{}
	service := newServiceForTest(t, db, authorizer, &serviceTestProvisioner{}, nil)
	principal := serviceTestPrincipal(t)

	page, err := service.ListVersions(t.Context(), principal, alpha.Slug, alphaPlugin.Plugin.Slug, 1, 10)
	if err != nil {
		t.Fatalf("ListVersions() error = %v", err)
	}
	if page.Total != 2 || len(page.Items) != 2 || page.Items[0].Tag != available.Tag || page.Items[1].Tag != "v0.9.0" {
		t.Fatalf("ListVersions() = %#v", page)
	}
	if got := authorizer.last(); got.action != auth.ActionPluginRead || got.resource != (auth.ResourceRef{Type: auth.ResourcePlugin, ID: alphaPlugin.Plugin.ID, NamespaceID: alpha.ID}) {
		t.Fatalf("ListVersions() authorization = %#v", got)
	}

	got, err := service.GetVersion(t.Context(), principal, alpha.Slug, alphaPlugin.Plugin.Slug, available.Tag)
	if err != nil {
		t.Fatalf("GetVersion() error = %v", err)
	}
	if got.Tag != available.Tag || got.Status != VersionStatusAvailable || got.CommitSHA != *available.CommitSHA {
		t.Fatalf("GetVersion() = %#v", got)
	}
	if _, err := service.GetVersion(t.Context(), principal, alpha.Slug, alphaPlugin.Plugin.Slug, deleted.Tag); !errors.Is(err, ErrGone) {
		t.Fatalf("deleted GetVersion() error = %v, want ErrGone", err)
	}
	if _, err := service.GetVersion(t.Context(), principal, alpha.Slug, alphaPlugin.Plugin.Slug, "v3.0.0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant GetVersion() error = %v, want ErrNotFound", err)
	}

	denied := newServiceForTest(t, db, &serviceTestAuthorizer{deny: true}, &serviceTestProvisioner{}, nil)
	if _, err := denied.GetVersion(t.Context(), principal, alpha.Slug, alphaPlugin.Plugin.Slug, deleted.Tag); !errors.Is(err, ErrNotFound) {
		t.Fatalf("denied tombstone GetVersion() error = %v, want nondisclosing ErrNotFound", err)
	}
	if _, err := denied.ListVersions(t.Context(), principal, alpha.Slug, alphaPlugin.Plugin.Slug, 1, 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("denied ListVersions() error = %v, want nondisclosing ErrNotFound", err)
	}
	if _, err := service.ListVersions(t.Context(), principal, alpha.Slug, alphaPlugin.Plugin.Slug, 1, 101); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid ListVersions() error = %v, want ErrInvalidInput", err)
	}
}

func TestServicePublishStrictlyInspectsActivatesAndSetsDefault(t *testing.T) {
	db := newServiceTestDatabase(t)
	namespace := createServiceTestNamespace(t, db, "security")
	aggregate := createServiceTestAggregate(t, db, namespace.ID, "scanner", PluginStatusDraft, VisibilityPrivate, RepositoryStatusReady, time.Now().UTC())
	inspection := serviceTestInspection("a", "b", "c", `{"name":"scanner"}`)
	inspector := &serviceTestInspector{inspections: []gitservice.PluginSourceInspection{inspection, inspection}}
	authorizer := &serviceTestAuthorizer{}
	service := newServiceForTest(t, db, authorizer, &serviceTestProvisioner{}, inspector)
	publishedAt := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return publishedAt }
	principal := serviceTestPrincipal(t)

	published, err := service.Publish(t.Context(), principal, namespace.Slug, aggregate.Plugin.Slug, PublishInput{Tag: "v1.2.3", MakeDefault: true})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if published.Tag != "v1.2.3" || published.Status != VersionStatusAvailable || published.CommitSHA != inspection.CommitObjectID || !published.PublishedAt.Equal(publishedAt) {
		t.Fatalf("Publish() = %#v", published)
	}
	if len(inspector.calls) != 2 || inspector.calls[0].repositoryID != aggregate.Repository.ID || inspector.calls[0].tag != "v1.2.3" || inspector.calls[0].pluginName != aggregate.Plugin.Slug {
		t.Fatalf("inspection calls = %#v", inspector.calls)
	}
	if got := authorizer.last(); got.action != auth.ActionPluginPublish || got.resource != (auth.ResourceRef{Type: auth.ResourcePlugin, ID: aggregate.Plugin.ID, NamespaceID: namespace.ID}) {
		t.Fatalf("Publish() authorization = %#v", got)
	}
	if len(authorizer.calls) != 2 || authorizer.calls[0].action != auth.ActionPluginRead {
		t.Fatalf("Publish() authorization order = %#v", authorizer.calls)
	}
	stored := loadServiceTestAggregate(t, service, namespace.ID, aggregate.Plugin.Slug)
	if stored.Plugin.Status != PluginStatusActive || stored.Plugin.DefaultVersionTag == nil || *stored.Plugin.DefaultVersionTag != "v1.2.3" {
		t.Fatalf("activated Plugin = %#v", stored.Plugin)
	}
	version, err := service.repository.FindVersion(t.Context(), aggregate.Plugin.ID, "v1.2.3")
	if err != nil {
		t.Fatal(err)
	}
	if version.CommitSHA == nil || *version.CommitSHA != inspection.CommitObjectID || version.ManifestDigest == nil || *version.ManifestDigest != inspection.ManifestDigest || string(version.ManifestSnapshot) != string(inspection.ManifestSnapshot) {
		t.Fatalf("stored Version = %#v", version)
	}
}

func TestServicePublishIsIdempotentAndRejectsCASOrLifecycleConflicts(t *testing.T) {
	db := newServiceTestDatabase(t)
	namespace := createServiceTestNamespace(t, db, "security")
	now := time.Now().UTC()
	aggregate := createServiceTestAggregate(t, db, namespace.ID, "scanner", PluginStatusActive, VisibilityPublic, RepositoryStatusReady, now)
	inspection := serviceTestInspection("a", "b", "c", `{"name":"scanner"}`)
	inspector := &serviceTestInspector{inspections: []gitservice.PluginSourceInspection{inspection, inspection, inspection, inspection}}
	service := newServiceForTest(t, db, &serviceTestAuthorizer{}, &serviceTestProvisioner{}, inspector)
	principal := serviceTestPrincipal(t)

	first, err := service.Publish(t.Context(), principal, namespace.Slug, aggregate.Plugin.Slug, PublishInput{Tag: "v1.0.0"})
	if err != nil {
		t.Fatalf("first Publish() error = %v", err)
	}
	second, err := service.Publish(t.Context(), principal, namespace.Slug, aggregate.Plugin.Slug, PublishInput{Tag: "v1.0.0"})
	if err != nil {
		t.Fatalf("idempotent Publish() error = %v", err)
	}
	if !second.PublishedAt.Equal(first.PublishedAt) {
		t.Fatalf("idempotent Publish() changed publishedAt: %v != %v", second.PublishedAt, first.PublishedAt)
	}
	var versions int64
	if err := db.Model(&PluginVersion{}).Where("plugin_id = ? AND tag = ?", aggregate.Plugin.ID, "v1.0.0").Count(&versions).Error; err != nil {
		t.Fatal(err)
	}
	if versions != 1 {
		t.Fatalf("Version rows = %d, want 1", versions)
	}

	changedRef := inspection
	changedRef.RawTagObjectID = strings.Repeat("d", 40)
	casInspector := &serviceTestInspector{inspections: []gitservice.PluginSourceInspection{inspection, changedRef}}
	casService := newServiceForTest(t, db, &serviceTestAuthorizer{}, &serviceTestProvisioner{}, casInspector)
	if _, err := casService.Publish(t.Context(), principal, namespace.Slug, aggregate.Plugin.Slug, PublishInput{Tag: "v2.0.0"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("moving-ref Publish() error = %v, want ErrConflict", err)
	}
	if _, err := service.repository.FindVersion(t.Context(), aggregate.Plugin.ID, "v2.0.0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("CAS failure persisted Version: %v", err)
	}

	archived := createServiceTestAggregate(t, db, namespace.ID, "archived", PluginStatusDraft, VisibilityPublic, RepositoryStatusReady, now)
	from := PluginStatusDraft
	if err := db.Model(&Plugin{}).Where("id = ?", archived.Plugin.ID).Updates(map[string]any{"status": PluginStatusArchived, "archived_from": from}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := service.Publish(t.Context(), principal, namespace.Slug, "archived", PublishInput{Tag: "v1.0.0"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("archived Publish() error = %v, want ErrConflict", err)
	}
	readOnly := createServiceTestAggregate(t, db, namespace.ID, "read-only", PluginStatusActive, VisibilityPublic, RepositoryStatusReadOnly, now)
	if _, err := service.Publish(t.Context(), principal, namespace.Slug, readOnly.Plugin.Slug, PublishInput{Tag: "v1.0.0"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("readOnly Publish() error = %v, want ErrConflict", err)
	}
	if _, err := service.Publish(t.Context(), principal, namespace.Slug, aggregate.Plugin.Slug, PublishInput{Tag: "1.0.0"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("noncanonical Publish() error = %v, want ErrInvalidInput", err)
	}

	publishDenied := newServiceForTest(t, db, &serviceTestAuthorizer{denied: map[auth.Action]bool{auth.ActionPluginPublish: true}}, &serviceTestProvisioner{}, inspector)
	if _, err := publishDenied.Publish(t.Context(), principal, namespace.Slug, readOnly.Plugin.Slug, PublishInput{Tag: "v1.0.0"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("disclosed readOnly denied Publish() error = %v, want lifecycle ErrConflict", err)
	}
	undisclosed := newServiceForTest(t, db, &serviceTestAuthorizer{deny: true}, &serviceTestProvisioner{}, inspector)
	if _, err := undisclosed.Publish(t.Context(), principal, namespace.Slug, aggregate.Plugin.Slug, PublishInput{Tag: "v4.0.0"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("undisclosed Publish() error = %v, want ErrNotFound", err)
	}
}

func TestServicePublishRestoresTombstoneWithoutRestoringDefault(t *testing.T) {
	db := newServiceTestDatabase(t)
	namespace := createServiceTestNamespace(t, db, "security")
	aggregate := createServiceTestAggregate(t, db, namespace.ID, "scanner", PluginStatusActive, VisibilityPublic, RepositoryStatusReady, time.Now().UTC())
	createServiceTestVersion(t, db, aggregate.Plugin.ID, "v1.0.0", VersionStatusDeleted, "", time.Now().UTC())
	inspection := serviceTestInspection("a", "b", "c", `{"name":"scanner"}`)
	inspector := &serviceTestInspector{inspections: []gitservice.PluginSourceInspection{inspection, inspection}}
	service := newServiceForTest(t, db, &serviceTestAuthorizer{}, &serviceTestProvisioner{}, inspector)

	if _, err := service.Publish(t.Context(), serviceTestPrincipal(t), namespace.Slug, aggregate.Plugin.Slug, PublishInput{Tag: "v1.0.0"}); err != nil {
		t.Fatalf("restore Publish() error = %v", err)
	}
	stored := loadServiceTestAggregate(t, service, namespace.ID, aggregate.Plugin.Slug)
	if stored.Plugin.DefaultVersionTag != nil {
		t.Fatalf("restored Version unexpectedly restored default = %q", *stored.Plugin.DefaultVersionTag)
	}
	version, err := service.repository.FindVersion(t.Context(), aggregate.Plugin.ID, "v1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if version.Status != VersionStatusAvailable || version.DeletedAt != nil || version.CommitSHA == nil || *version.CommitSHA != inspection.CommitObjectID {
		t.Fatalf("restored Version = %#v", version)
	}
}

func TestServiceDefaultCommandsAreScopedIdempotentAndDeniedSafely(t *testing.T) {
	db := newServiceTestDatabase(t)
	alpha := createServiceTestNamespace(t, db, "alpha")
	bravo := createServiceTestNamespace(t, db, "bravo")
	now := time.Now().UTC()
	alphaPlugin := createServiceTestAggregate(t, db, alpha.ID, "scanner", PluginStatusActive, VisibilityPrivate, RepositoryStatusReady, now)
	bravoPlugin := createServiceTestAggregate(t, db, bravo.ID, "scanner", PluginStatusActive, VisibilityPrivate, RepositoryStatusReady, now)
	createServiceTestVersion(t, db, alphaPlugin.Plugin.ID, "v1.0.0", VersionStatusAvailable, strings.Repeat("a", 40), now)
	createServiceTestVersion(t, db, alphaPlugin.Plugin.ID, "v2.0.0", VersionStatusDeleted, "", now)
	createServiceTestVersion(t, db, bravoPlugin.Plugin.ID, "v3.0.0", VersionStatusAvailable, strings.Repeat("b", 40), now)
	service := newServiceForTest(t, db, &serviceTestAuthorizer{}, &serviceTestProvisioner{}, nil)
	principal := serviceTestPrincipal(t)

	if err := service.SetDefaultVersion(t.Context(), principal, alpha.Slug, alphaPlugin.Plugin.Slug, "v1.0.0"); err != nil {
		t.Fatalf("SetDefaultVersion() error = %v", err)
	}
	if err := service.SetDefaultVersion(t.Context(), principal, alpha.Slug, alphaPlugin.Plugin.Slug, "v1.0.0"); err != nil {
		t.Fatalf("idempotent SetDefaultVersion() error = %v", err)
	}
	if err := service.SetDefaultVersion(t.Context(), principal, alpha.Slug, alphaPlugin.Plugin.Slug, "v2.0.0"); !errors.Is(err, ErrConflict) {
		t.Fatalf("deleted SetDefaultVersion() error = %v, want ErrConflict", err)
	}
	if err := service.SetDefaultVersion(t.Context(), principal, alpha.Slug, alphaPlugin.Plugin.Slug, "v3.0.0"); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-tenant SetDefaultVersion() error = %v, want ErrConflict", err)
	}
	if err := service.ClearDefaultVersion(t.Context(), principal, alpha.Slug, alphaPlugin.Plugin.Slug); err != nil {
		t.Fatalf("ClearDefaultVersion() error = %v", err)
	}
	if err := service.ClearDefaultVersion(t.Context(), principal, alpha.Slug, alphaPlugin.Plugin.Slug); err != nil {
		t.Fatalf("idempotent ClearDefaultVersion() error = %v", err)
	}
	stored := loadServiceTestAggregate(t, service, alpha.ID, alphaPlugin.Plugin.Slug)
	if stored.Plugin.DefaultVersionTag != nil {
		t.Fatalf("default = %q, want nil", *stored.Plugin.DefaultVersionTag)
	}

	deniedAuthorizer := &serviceTestAuthorizer{denied: map[auth.Action]bool{auth.ActionPluginPublish: true}}
	denied := newServiceForTest(t, db, deniedAuthorizer, &serviceTestProvisioner{}, nil)
	if err := denied.SetDefaultVersion(t.Context(), principal, alpha.Slug, alphaPlugin.Plugin.Slug, "v1.0.0"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("disclosed denied SetDefaultVersion() error = %v, want ErrForbidden", err)
	}
	fullyDenied := newServiceForTest(t, db, &serviceTestAuthorizer{deny: true}, &serviceTestProvisioner{}, nil)
	if err := fullyDenied.ClearDefaultVersion(t.Context(), principal, alpha.Slug, alphaPlugin.Plugin.Slug); !errors.Is(err, ErrNotFound) {
		t.Fatalf("undisclosed denied ClearDefaultVersion() error = %v, want ErrNotFound", err)
	}
}

func TestServiceLifecycleMutationsDenyWithoutChangingState(t *testing.T) {
	db := newServiceTestDatabase(t)
	namespace := createServiceTestNamespace(t, db, "security")
	aggregate := createServiceTestAggregate(t, db, namespace.ID, "scanner", PluginStatusActive, VisibilityPublic, RepositoryStatusReady, time.Now().UTC())
	principal := serviceTestPrincipal(t)
	disclosedDenied := newServiceForTest(t, db, &serviceTestAuthorizer{denied: map[auth.Action]bool{auth.ActionPluginArchive: true}}, &serviceTestProvisioner{}, nil)

	if err := disclosedDenied.Archive(t.Context(), principal, namespace.Slug, aggregate.Plugin.Slug); !errors.Is(err, ErrForbidden) {
		t.Fatalf("denied Archive() error = %v, want ErrForbidden", err)
	}
	if err := disclosedDenied.SetVisibility(t.Context(), principal, namespace.Slug, aggregate.Plugin.Slug, VisibilityPrivate); !errors.Is(err, ErrForbidden) {
		t.Fatalf("denied SetVisibility() error = %v, want ErrForbidden", err)
	}
	stored := loadServiceTestAggregate(t, disclosedDenied, namespace.ID, aggregate.Plugin.Slug)
	if stored.Plugin.Status != PluginStatusActive || stored.Plugin.Visibility != VisibilityPublic {
		t.Fatalf("denied mutations changed Plugin = %#v", stored.Plugin)
	}

	undisclosed := newServiceForTest(t, db, &serviceTestAuthorizer{deny: true}, &serviceTestProvisioner{}, nil)
	if err := undisclosed.Archive(t.Context(), principal, namespace.Slug, aggregate.Plugin.Slug); !errors.Is(err, ErrNotFound) {
		t.Fatalf("undisclosed Archive() error = %v, want ErrNotFound", err)
	}
	if err := disclosedDenied.SetVisibility(t.Context(), principal, namespace.Slug, aggregate.Plugin.Slug, "internal"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid SetVisibility() error = %v, want ErrInvalidInput", err)
	}
}

type serviceTestInspectionCall struct {
	repositoryID string
	tag          string
	pluginName   string
}

type serviceTestInspector struct {
	inspections []gitservice.PluginSourceInspection
	err         error
	calls       []serviceTestInspectionCall
}

func (inspector *serviceTestInspector) InspectPluginSource(_ context.Context, repositoryID, tag, pluginName string) (gitservice.PluginSourceInspection, error) {
	inspector.calls = append(inspector.calls, serviceTestInspectionCall{repositoryID: repositoryID, tag: tag, pluginName: pluginName})
	if inspector.err != nil {
		return gitservice.PluginSourceInspection{}, inspector.err
	}
	if len(inspector.inspections) == 0 {
		return gitservice.PluginSourceInspection{}, errors.New("fixture inspection unavailable")
	}
	inspection := inspector.inspections[0]
	inspector.inspections = inspector.inspections[1:]
	return inspection, nil
}

func serviceTestInspection(raw, commit, digestCharacter, manifest string) gitservice.PluginSourceInspection {
	return gitservice.PluginSourceInspection{
		RawTagObjectID: rawObjectID(raw), CommitObjectID: rawObjectID(commit),
		ManifestDigest: strings.Repeat(digestCharacter, 64), ManifestSnapshot: []byte(manifest),
	}
}

func rawObjectID(character string) string {
	return strings.Repeat(character, 40)
}

func createServiceTestVersion(t *testing.T, db *gorm.DB, pluginID, tag, status, commit string, publishedAt time.Time) PluginVersion {
	t.Helper()
	version := PluginVersion{ID: uuid.NewString(), PluginID: pluginID, Tag: tag, Status: status, PublishedAt: publishedAt, CreatedAt: publishedAt, UpdatedAt: publishedAt}
	if status == VersionStatusAvailable {
		digest := strings.Repeat("d", 64)
		version.CommitSHA = &commit
		version.ManifestDigest = &digest
		version.ManifestSnapshot = []byte(`{"name":"scanner"}`)
	} else {
		deletedAt := publishedAt.Add(time.Second)
		version.DeletedAt = &deletedAt
	}
	if err := db.Create(&version).Error; err != nil {
		t.Fatal(err)
	}
	return version
}
