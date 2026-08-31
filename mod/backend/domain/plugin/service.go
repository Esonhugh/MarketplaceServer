package plugin

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	identitymodel "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/Esonhugh/MarketplaceServer/pkg/gitservice"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

var (
	ErrForbidden    = errors.New("plugin operation forbidden")
	ErrConflict     = errors.New("plugin operation conflicts with current state")
	ErrGone         = errors.New("plugin version is deleted")
	ErrInvalidInput = errors.New("invalid plugin input")
	ErrUnavailable  = errors.New("plugin service unavailable")

	slugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

type CreateInput struct {
	Namespace  string
	Name       string
	Visibility string
}

type PublishInput struct {
	Tag         string
	MakeDefault bool
}

// PluginView deliberately contains only management-safe fields.
type PluginView struct {
	Namespace        string
	Name             string
	Status           string
	Visibility       string
	RepositoryStatus string
	CloneURL         string
	DefaultVersion   *string
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// VersionView omits internal IDs, manifest bytes, raw ref IDs, and tombstone facts.
type VersionView struct {
	Tag         string
	Status      string
	CommitSHA   string
	PublishedAt time.Time
	UpdatedAt   time.Time
}

type PluginPage struct {
	Items []PluginView
	Page  int
	Size  int
	Total int64
}

type VersionPage struct {
	Items []VersionView
	Page  int
	Size  int
	Total int64
}

type Service struct {
	db           *gorm.DB
	repository   *RepositoryStore
	authorizer   auth.Authorizer
	provisioner  gitservice.RepositoryProvisioner
	inspector    gitservice.PluginSourceInspector
	locks        *keyedLocks
	effectLocker EffectLocker
	now          func() time.Time
	newID        func() string
}

func NewService(db *gorm.DB, authorizer auth.Authorizer, provisioner gitservice.RepositoryProvisioner, inspector gitservice.PluginSourceInspector, effectLocker EffectLocker) (*Service, error) {
	if db == nil || authorizer == nil || provisioner == nil || effectLocker == nil {
		return nil, errors.New("plugin service requires database, authorizer, repository provisioner, and effect lock")
	}
	repository, err := NewRepository(db)
	if err != nil {
		return nil, err
	}
	return &Service{
		db: db, repository: repository, authorizer: authorizer, provisioner: provisioner, inspector: inspector,
		locks: newKeyedLocks(), effectLocker: effectLocker, now: func() time.Time { return time.Now().UTC() }, newID: uuid.NewString,
	}, nil
}

func (service *Service) Create(ctx context.Context, principal auth.Principal, input CreateInput) (PluginView, error) {
	input.Namespace = strings.TrimSpace(input.Namespace)
	input.Name = strings.TrimSpace(input.Name)
	input.Visibility = strings.TrimSpace(input.Visibility)
	if input.Visibility == "" {
		input.Visibility = VisibilityPublic
	}
	if !validSlug(input.Namespace) || !validSlug(input.Name) || !validVisibility(input.Visibility) {
		return PluginView{}, ErrInvalidInput
	}

	namespace, err := service.findNamespaceBySlug(ctx, input.Namespace)
	if err != nil {
		return PluginView{}, err
	}
	if err := service.authorize(ctx, principal, auth.ActionPluginCreate, auth.ResourceRef{Type: auth.ResourceNamespace, ID: namespace.ID}); err != nil {
		return PluginView{}, err
	}
	unlock := service.locks.lock("create:" + namespace.ID + ":" + input.Name)
	defer unlock()
	if _, err := service.repository.FindBySlug(ctx, namespace.ID, input.Name); err == nil {
		return PluginView{}, ErrConflict
	} else if !errors.Is(err, ErrNotFound) {
		return PluginView{}, ErrUnavailable
	}

	pluginID := service.newID()
	identity := gitservice.RepositoryIdentity{ID: pluginID, StorageKey: pluginID}
	provisioned, err := service.provisioner.ProvisionRepository(ctx, identity)
	if err != nil {
		return PluginView{}, ErrUnavailable
	}
	now := service.now()
	aggregate := Aggregate{
		Plugin:     Plugin{ID: pluginID, NamespaceID: namespace.ID, Slug: input.Name, Visibility: input.Visibility, Status: PluginStatusDraft, CreatedAt: now, UpdatedAt: now},
		Repository: Repository{ID: pluginID, StorageKey: provisioned.StorageKey, Status: RepositoryStatusReady, CreatedAt: now, UpdatedAt: now},
	}
	if err := service.repository.Create(ctx, aggregate); err != nil {
		if provisioned.Created() {
			if cleanupErr := service.provisioner.RemoveProvisionedRepository(ctx, provisioned); cleanupErr != nil {
				_ = service.recordOrphanCleanup(ctx, aggregate)
			}
		}
		if errors.Is(err, ErrAlreadyExists) {
			return PluginView{}, ErrConflict
		}
		return PluginView{}, ErrUnavailable
	}
	return pluginView(namespace.Slug, aggregate), nil
}

func (service *Service) List(ctx context.Context, principal auth.Principal, namespaceSlug string, page, size int) (PluginPage, error) {
	if !validSlug(namespaceSlug) || !validPage(page, size) {
		return PluginPage{}, ErrInvalidInput
	}
	namespace, err := service.findNamespaceBySlug(ctx, namespaceSlug)
	if err != nil {
		return PluginPage{}, err
	}
	if err := service.authorize(ctx, principal, auth.ActionPluginList, auth.ResourceRef{Type: auth.ResourceNamespace, ID: namespace.ID}); err != nil {
		return PluginPage{}, err
	}
	aggregates, total, err := service.repository.List(ctx, namespace.ID, page, size)
	if err != nil {
		return PluginPage{}, wrapUnavailable("list Plugins", err)
	}
	items := make([]PluginView, 0, len(aggregates))
	for _, aggregate := range aggregates {
		items = append(items, pluginView(namespace.Slug, aggregate))
	}
	return PluginPage{Items: items, Page: page, Size: size, Total: total}, nil
}

func (service *Service) Get(ctx context.Context, principal auth.Principal, namespaceSlug, pluginSlug string) (PluginView, error) {
	namespace, aggregate, err := service.exactAggregate(ctx, namespaceSlug, pluginSlug)
	if err != nil {
		return PluginView{}, err
	}
	if err := service.authorizeExactRead(ctx, principal, aggregate); err != nil {
		return PluginView{}, err
	}
	return pluginView(namespace.Slug, aggregate), nil
}

func (service *Service) Archive(ctx context.Context, principal auth.Principal, namespaceSlug, pluginSlug string) error {
	namespace, aggregate, err := service.exactAggregate(ctx, namespaceSlug, pluginSlug)
	if err != nil {
		return err
	}
	if err := service.authorizeMutation(ctx, principal, auth.ActionPluginArchive, aggregate); err != nil {
		return err
	}
	unlock := service.locks.lock("plugin:" + aggregate.Plugin.ID)
	defer unlock()
	effectUnlock, err := service.effectLocker.LockPlugin(ctx, aggregate.Plugin.ID)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = effectUnlock() }()
	aggregate, err = service.repository.FindBySlug(ctx, namespace.ID, pluginSlug)
	if err != nil {
		return service.mapMutationLookup(err)
	}
	if aggregate.Plugin.Status == PluginStatusArchived {
		return nil
	}
	if aggregate.Plugin.Status != PluginStatusDraft && aggregate.Plugin.Status != PluginStatusActive {
		return ErrConflict
	}
	now := service.now()
	result := service.db.WithContext(ctx).Model(&Plugin{}).
		Where("namespace_id = ? AND id = ? AND status = ?", namespace.ID, aggregate.Plugin.ID, aggregate.Plugin.Status).
		Updates(map[string]any{"status": PluginStatusArchived, "archived_from": aggregate.Plugin.Status, "updated_at": now})
	if result.Error != nil {
		return wrapUnavailable("archive Plugin", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrConflict
	}
	return nil
}

func (service *Service) Restore(ctx context.Context, principal auth.Principal, namespaceSlug, pluginSlug string) error {
	namespace, aggregate, err := service.exactAggregate(ctx, namespaceSlug, pluginSlug)
	if err != nil {
		return err
	}
	if err := service.authorizeMutation(ctx, principal, auth.ActionPluginArchive, aggregate); err != nil {
		return err
	}
	unlock := service.locks.lock("plugin:" + aggregate.Plugin.ID)
	defer unlock()
	effectUnlock, err := service.effectLocker.LockPlugin(ctx, aggregate.Plugin.ID)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = effectUnlock() }()
	aggregate, err = service.repository.FindBySlug(ctx, namespace.ID, pluginSlug)
	if err != nil {
		return service.mapMutationLookup(err)
	}
	if aggregate.Plugin.Status != PluginStatusArchived || aggregate.Plugin.ArchivedFrom == nil ||
		(*aggregate.Plugin.ArchivedFrom != PluginStatusDraft && *aggregate.Plugin.ArchivedFrom != PluginStatusActive) {
		return ErrConflict
	}
	now := service.now()
	result := service.db.WithContext(ctx).Model(&Plugin{}).
		Where("namespace_id = ? AND id = ? AND status = ? AND archived_from = ?", namespace.ID, aggregate.Plugin.ID, PluginStatusArchived, *aggregate.Plugin.ArchivedFrom).
		Updates(map[string]any{"status": *aggregate.Plugin.ArchivedFrom, "archived_from": nil, "updated_at": now})
	if result.Error != nil {
		return wrapUnavailable("restore Plugin", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrConflict
	}
	return nil
}

func (service *Service) SetVisibility(ctx context.Context, principal auth.Principal, namespaceSlug, pluginSlug, visibility string) error {
	if !validVisibility(visibility) {
		return ErrInvalidInput
	}
	namespace, aggregate, err := service.exactAggregate(ctx, namespaceSlug, pluginSlug)
	if err != nil {
		return err
	}
	if err := service.authorizeMutation(ctx, principal, auth.ActionPluginArchive, aggregate); err != nil {
		return err
	}
	unlock := service.locks.lock("plugin:" + aggregate.Plugin.ID)
	defer unlock()
	effectUnlock, err := service.effectLocker.LockPlugin(ctx, aggregate.Plugin.ID)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = effectUnlock() }()
	aggregate, err = service.repository.FindBySlug(ctx, namespace.ID, pluginSlug)
	if err != nil {
		return service.mapMutationLookup(err)
	}
	if aggregate.Plugin.Status == PluginStatusArchived {
		return ErrConflict
	}
	if aggregate.Plugin.Visibility == visibility {
		return nil
	}
	result := service.db.WithContext(ctx).Model(&Plugin{}).
		Where("namespace_id = ? AND id = ? AND status <> ?", namespace.ID, aggregate.Plugin.ID, PluginStatusArchived).
		Updates(map[string]any{"visibility": visibility, "updated_at": service.now()})
	if result.Error != nil {
		return wrapUnavailable("set Plugin visibility", result.Error)
	}
	if result.RowsAffected != 1 {
		return ErrConflict
	}
	return nil
}

func (service *Service) ListVersions(ctx context.Context, principal auth.Principal, namespaceSlug, pluginSlug string, page, size int) (VersionPage, error) {
	if !validPage(page, size) {
		return VersionPage{}, ErrInvalidInput
	}
	_, aggregate, err := service.exactAggregate(ctx, namespaceSlug, pluginSlug)
	if err != nil {
		return VersionPage{}, err
	}
	if err := service.authorizeExactRead(ctx, principal, aggregate); err != nil {
		return VersionPage{}, err
	}
	versions, total, err := service.repository.ListVersions(ctx, aggregate.Plugin.ID, page, size)
	if err != nil {
		return VersionPage{}, wrapUnavailable("list Plugin Versions", err)
	}
	items := make([]VersionView, 0, len(versions))
	for _, version := range versions {
		items = append(items, versionView(version))
	}
	return VersionPage{Items: items, Page: page, Size: size, Total: total}, nil
}

func (service *Service) GetVersion(ctx context.Context, principal auth.Principal, namespaceSlug, pluginSlug, tag string) (VersionView, error) {
	if !validCanonicalTag(tag) {
		return VersionView{}, ErrNotFound
	}
	_, aggregate, err := service.exactAggregate(ctx, namespaceSlug, pluginSlug)
	if err != nil {
		return VersionView{}, err
	}
	if err := service.authorizeExactRead(ctx, principal, aggregate); err != nil {
		return VersionView{}, err
	}
	version, err := service.repository.FindVersion(ctx, aggregate.Plugin.ID, tag)
	if errors.Is(err, ErrNotFound) {
		return VersionView{}, ErrNotFound
	}
	if err != nil {
		return VersionView{}, wrapUnavailable("get Plugin Version", err)
	}
	if version.Status == VersionStatusDeleted {
		return VersionView{}, ErrGone
	}
	if version.Status != VersionStatusAvailable || version.CommitSHA == nil {
		return VersionView{}, ErrUnavailable
	}
	return versionView(version), nil
}

func (service *Service) Publish(ctx context.Context, principal auth.Principal, namespaceSlug, pluginSlug string, input PublishInput) (VersionView, error) {
	if !validCanonicalTag(input.Tag) {
		return VersionView{}, ErrInvalidInput
	}
	if service.inspector == nil {
		return VersionView{}, ErrUnavailable
	}
	namespace, aggregate, err := service.exactAggregate(ctx, namespaceSlug, pluginSlug)
	if err != nil {
		return VersionView{}, err
	}
	if err := service.authorizeExactRead(ctx, principal, aggregate); err != nil {
		return VersionView{}, err
	}
	if aggregate.Plugin.Status == PluginStatusArchived || aggregate.Repository.Status != RepositoryStatusReady {
		return VersionView{}, ErrConflict
	}
	if err := service.authorize(ctx, principal, auth.ActionPluginPublish, pluginResource(aggregate)); err != nil {
		return VersionView{}, err
	}

	unlock := service.locks.lock("plugin:" + aggregate.Plugin.ID)
	defer unlock()
	effectUnlock, err := service.effectLocker.LockPlugin(ctx, aggregate.Plugin.ID)
	if err != nil {
		return VersionView{}, ErrUnavailable
	}
	defer func() { _ = effectUnlock() }()
	aggregate, err = service.repository.FindBySlug(ctx, namespace.ID, pluginSlug)
	if err != nil {
		return VersionView{}, service.mapMutationLookup(err)
	}
	if aggregate.Plugin.Status == PluginStatusArchived || aggregate.Repository.Status != RepositoryStatusReady {
		return VersionView{}, ErrConflict
	}

	inspection, err := service.inspector.InspectPluginSource(ctx, aggregate.Repository.ID, input.Tag, aggregate.Plugin.Slug)
	if err != nil {
		return VersionView{}, ErrConflict
	}
	if !validInspection(inspection) {
		return VersionView{}, ErrUnavailable
	}
	confirmed, err := service.inspector.InspectPluginSource(ctx, aggregate.Repository.ID, input.Tag, aggregate.Plugin.Slug)
	if err != nil {
		return VersionView{}, ErrConflict
	}
	if !validInspection(confirmed) {
		return VersionView{}, ErrUnavailable
	}
	if inspection.RawTagObjectID != confirmed.RawTagObjectID || inspection.CommitObjectID != confirmed.CommitObjectID ||
		inspection.ManifestDigest != confirmed.ManifestDigest || !bytes.Equal(inspection.ManifestSnapshot, confirmed.ManifestSnapshot) {
		return VersionView{}, ErrConflict
	}
	inspection = confirmed
	now := service.now()
	var published PluginVersion
	err = service.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var currentPlugin Plugin
		if err := tx.Select("id", "status", "default_version_tag").
			Where("namespace_id = ? AND id = ?", namespace.ID, aggregate.Plugin.ID).
			Take(&currentPlugin).Error; err != nil {
			return err
		}
		var currentRepository Repository
		if err := tx.Select("id", "status").Where("id = ?", aggregate.Repository.ID).Take(&currentRepository).Error; err != nil {
			return err
		}
		if currentPlugin.Status == PluginStatusArchived || currentRepository.Status != RepositoryStatusReady {
			return ErrConflict
		}
		if unresolvedReceiveExists(tx, aggregate.Plugin.ID) {
			return ErrConflict
		}

		var version PluginVersion
		err := tx.Where("plugin_id = ? AND tag = ?", aggregate.Plugin.ID, input.Tag).Take(&version).Error
		historyOperation := ""
		switch {
		case errors.Is(err, gorm.ErrRecordNotFound):
			rawTag, commit := inspection.RawTagObjectID, inspection.CommitObjectID
			digest := inspection.ManifestDigest
			version = PluginVersion{
				ID: service.newID(), PluginID: aggregate.Plugin.ID, Tag: input.Tag, Status: VersionStatusAvailable,
				RawTagObjectID: &rawTag, CommitSHA: &commit, ManifestDigest: &digest, ManifestSnapshot: append([]byte(nil), inspection.ManifestSnapshot...),
				PublishedAt: now, CreatedAt: now, UpdatedAt: now,
			}
			if err := tx.Create(&version).Error; err != nil {
				if isUniqueConstraintError(err) {
					return ErrConflict
				}
				return err
			}
			historyOperation = VersionHistoryOperationPublish
		case err != nil:
			return err
		case version.Status == VersionStatusDeleted:
			rawTag, commit := inspection.RawTagObjectID, inspection.CommitObjectID
			digest := inspection.ManifestDigest
			result := tx.Exec(`UPDATE plugin_versions
				SET status = ?, raw_tag_object_id = ?, commit_sha = ?, manifest_digest = ?, manifest_snapshot = ?, deleted_at = NULL, published_at = ?, updated_at = ?
				WHERE id = ? AND plugin_id = ? AND tag = ? AND status = ?`,
				VersionStatusAvailable, rawTag, commit, digest, append([]byte(nil), inspection.ManifestSnapshot...), now, now,
				version.ID, aggregate.Plugin.ID, input.Tag, VersionStatusDeleted)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrConflict
			}
			version.Status, version.RawTagObjectID, version.CommitSHA, version.ManifestDigest = VersionStatusAvailable, &rawTag, &commit, &digest
			version.ManifestSnapshot, version.DeletedAt, version.PublishedAt, version.UpdatedAt = append([]byte(nil), inspection.ManifestSnapshot...), nil, now, now
			historyOperation = VersionHistoryOperationRestore
		case version.Status == VersionStatusAvailable:
			if version.RawTagObjectID == nil || *version.RawTagObjectID != inspection.RawTagObjectID ||
				version.CommitSHA == nil || *version.CommitSHA != inspection.CommitObjectID ||
				version.ManifestDigest == nil || *version.ManifestDigest != inspection.ManifestDigest ||
				!bytes.Equal(version.ManifestSnapshot, inspection.ManifestSnapshot) {
				return ErrConflict
			}
		default:
			return ErrConflict
		}
		if historyOperation != "" {
			newCommit := inspection.CommitObjectID
			if err := tx.Create(&PluginVersionHistory{
				ID: service.newID(), PluginID: aggregate.Plugin.ID, Tag: input.Tag, Operation: historyOperation,
				NewCommitSHA: &newCommit, CorrelationID: service.newID(), CreatedAt: now,
			}).Error; err != nil {
				return err
			}
		}

		updates := map[string]any{}
		if currentPlugin.Status == PluginStatusDraft {
			updates["status"] = PluginStatusActive
		}
		if input.MakeDefault && (currentPlugin.DefaultVersionTag == nil || *currentPlugin.DefaultVersionTag != input.Tag) {
			updates["default_version_tag"] = input.Tag
		}
		if len(updates) > 0 {
			updates["updated_at"] = now
			result := tx.Model(&Plugin{}).
				Where("namespace_id = ? AND id = ? AND status = ?", namespace.ID, aggregate.Plugin.ID, currentPlugin.Status).
				Updates(updates)
			if result.Error != nil {
				return result.Error
			}
			if result.RowsAffected != 1 {
				return ErrConflict
			}
		}
		published = version
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrConflict) {
			return VersionView{}, ErrConflict
		}
		return VersionView{}, wrapUnavailable("publish Plugin Version", err)
	}
	return versionView(published), nil
}

func (service *Service) SetDefaultVersion(ctx context.Context, principal auth.Principal, namespaceSlug, pluginSlug, tag string) error {
	if !validCanonicalTag(tag) {
		return ErrNotFound
	}
	namespace, aggregate, err := service.exactAggregate(ctx, namespaceSlug, pluginSlug)
	if err != nil {
		return err
	}
	if err := service.authorizeMutation(ctx, principal, auth.ActionPluginPublish, aggregate); err != nil {
		return err
	}
	if aggregate.Plugin.Status == PluginStatusArchived || aggregate.Repository.Status != RepositoryStatusReady {
		return ErrConflict
	}
	unlock := service.locks.lock("plugin:" + aggregate.Plugin.ID)
	defer unlock()
	effectUnlock, err := service.effectLocker.LockPlugin(ctx, aggregate.Plugin.ID)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = effectUnlock() }()
	if unresolvedReceiveExists(service.db.WithContext(ctx), aggregate.Plugin.ID) {
		return ErrConflict
	}
	if err := SetDefaultVersion(ctx, service.db, namespace.ID, aggregate.Plugin.ID, tag); err != nil {
		switch {
		case errors.Is(err, ErrVersionNotAvailable):
			return ErrConflict
		case errors.Is(err, ErrNotFound):
			return ErrNotFound
		default:
			return wrapUnavailable("set default Plugin Version", err)
		}
	}
	return nil
}

func (service *Service) ClearDefaultVersion(ctx context.Context, principal auth.Principal, namespaceSlug, pluginSlug string) error {
	namespace, aggregate, err := service.exactAggregate(ctx, namespaceSlug, pluginSlug)
	if err != nil {
		return err
	}
	if err := service.authorizeMutation(ctx, principal, auth.ActionPluginPublish, aggregate); err != nil {
		return err
	}
	if aggregate.Plugin.Status == PluginStatusArchived || aggregate.Repository.Status != RepositoryStatusReady {
		return ErrConflict
	}
	unlock := service.locks.lock("plugin:" + aggregate.Plugin.ID)
	defer unlock()
	effectUnlock, err := service.effectLocker.LockPlugin(ctx, aggregate.Plugin.ID)
	if err != nil {
		return ErrUnavailable
	}
	defer func() { _ = effectUnlock() }()
	if unresolvedReceiveExists(service.db.WithContext(ctx), aggregate.Plugin.ID) {
		return ErrConflict
	}
	if err := service.repository.ClearDefaultVersion(ctx, namespace.ID, aggregate.Plugin.ID, service.now()); err != nil {
		switch {
		case errors.Is(err, ErrVersionNotAvailable):
			return ErrConflict
		case errors.Is(err, ErrNotFound):
			return ErrNotFound
		default:
			return wrapUnavailable("clear default Plugin Version", err)
		}
	}
	return nil
}

func unresolvedReceiveExists(db *gorm.DB, pluginID string) bool {
	var count int64
	return db.Model(&ReceiveIntent{}).
		Where("plugin_id = ? AND state IN ?", pluginID, []string{ReceiveBatchStatePrepared, ReceiveBatchStateFinalizing, ReceiveBatchStateManualRequired}).
		Limit(1).
		Count(&count).Error != nil || count != 0
}

func (service *Service) exactAggregate(ctx context.Context, namespaceSlug, pluginSlug string) (identitymodel.Namespace, Aggregate, error) {
	if !validSlug(namespaceSlug) || !validSlug(pluginSlug) {
		return identitymodel.Namespace{}, Aggregate{}, ErrNotFound
	}
	var namespace identitymodel.Namespace
	if err := service.db.WithContext(ctx).Where("slug = ?", namespaceSlug).Take(&namespace).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return identitymodel.Namespace{}, Aggregate{}, ErrNotFound
		}
		return identitymodel.Namespace{}, Aggregate{}, ErrUnavailable
	}
	aggregate, err := service.repository.FindBySlug(ctx, namespace.ID, pluginSlug)
	if errors.Is(err, ErrNotFound) {
		return identitymodel.Namespace{}, Aggregate{}, ErrNotFound
	}
	if err != nil {
		return identitymodel.Namespace{}, Aggregate{}, wrapUnavailable("find Plugin", err)
	}
	return namespace, aggregate, nil
}

func (service *Service) authorizeExactRead(ctx context.Context, principal auth.Principal, aggregate Aggregate) error {
	resource := pluginResource(aggregate)
	if err := service.authorizer.Authorize(ctx, principal, auth.ActionPluginRead, resource); err != nil {
		return ErrNotFound
	}
	return nil
}

func (service *Service) authorizeMutation(ctx context.Context, principal auth.Principal, action auth.Action, aggregate Aggregate) error {
	if err := service.authorizer.Authorize(ctx, principal, action, pluginResource(aggregate)); err == nil {
		return nil
	}
	if principal.IsAnonymous() {
		return ErrNotFound
	}
	if err := service.authorizer.Authorize(ctx, principal, auth.ActionPluginRead, pluginResource(aggregate)); err != nil {
		return ErrNotFound
	}
	return ErrForbidden
}

func (service *Service) mapMutationLookup(err error) error {
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	return wrapUnavailable("reload Plugin", err)
}

func pluginResource(aggregate Aggregate) auth.ResourceRef {
	return auth.ResourceRef{Type: auth.ResourcePlugin, ID: aggregate.Plugin.ID, NamespaceID: aggregate.Plugin.NamespaceID}
}

func versionView(version PluginVersion) VersionView {
	commit := ""
	if version.CommitSHA != nil {
		commit = *version.CommitSHA
	}
	return VersionView{Tag: version.Tag, Status: version.Status, CommitSHA: commit, PublishedAt: version.PublishedAt.UTC(), UpdatedAt: version.UpdatedAt.UTC()}
}

func validPage(page, size int) bool {
	return page >= 1 && size >= 1 && size <= 100
}

var canonicalTagPattern = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-((?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(?:\.(?:0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*))?(?:\+([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$`)

func validCanonicalTag(tag string) bool {
	return len(tag) <= 255 && canonicalTagPattern.MatchString(tag)
}

func validInspection(inspection gitservice.PluginSourceInspection) bool {
	return validObjectID(inspection.RawTagObjectID) && validObjectID(inspection.CommitObjectID) &&
		len(inspection.ManifestDigest) == 64 && len(inspection.ManifestSnapshot) > 0
}

func validObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' && character < 'a' || character > 'f' {
			return false
		}
	}
	return true
}

func (service *Service) findNamespaceBySlug(ctx context.Context, slug string) (identitymodel.Namespace, error) {
	var namespace identitymodel.Namespace
	if err := service.db.WithContext(ctx).Where("slug = ?", slug).Take(&namespace).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			// Namespace create/list does not disclose whether the namespace exists.
			return identitymodel.Namespace{}, ErrForbidden
		}
		return identitymodel.Namespace{}, wrapUnavailable("find namespace", err)
	}
	return namespace, nil
}

func (service *Service) recordOrphanCleanup(ctx context.Context, aggregate Aggregate) error {
	now := service.now()
	return service.db.WithContext(ctx).Create(&RepositoryOrphanCleanup{
		ID: service.newID(), PluginID: aggregate.Plugin.ID, StorageKey: aggregate.Repository.StorageKey,
		State: OrphanCleanupStatePending, CreatedAt: now, UpdatedAt: now,
	}).Error
}

func (service *Service) authorize(ctx context.Context, principal auth.Principal, action auth.Action, resource auth.ResourceRef) error {
	if err := service.authorizer.Authorize(ctx, principal, action, resource); err != nil {
		return ErrForbidden
	}
	return nil
}

func pluginView(namespaceSlug string, aggregate Aggregate) PluginView {
	return PluginView{
		Namespace: namespaceSlug, Name: aggregate.Plugin.Slug, Status: aggregate.Plugin.Status,
		Visibility: aggregate.Plugin.Visibility, RepositoryStatus: aggregate.Repository.Status,
		CloneURL:       "/git/" + namespaceSlug + "/" + aggregate.Plugin.Slug + ".git",
		DefaultVersion: cloneString(aggregate.Plugin.DefaultVersionTag),
		CreatedAt:      aggregate.Plugin.CreatedAt.UTC(), UpdatedAt: aggregate.Plugin.UpdatedAt.UTC(),
	}
}

func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func validSlug(value string) bool {
	return len(value) >= 1 && len(value) <= 63 && slugPattern.MatchString(value)
}

func validVisibility(value string) bool {
	return value == VisibilityPublic || value == VisibilityPrivate
}

type keyedLocks struct {
	mu    sync.Mutex
	locks map[string]*keyedLock
}

type keyedLock struct {
	mu   sync.Mutex
	refs int
}

func newKeyedLocks() *keyedLocks {
	return &keyedLocks{locks: make(map[string]*keyedLock)}
}

func (locks *keyedLocks) lock(key string) func() {
	locks.mu.Lock()
	entry := locks.locks[key]
	if entry == nil {
		entry = &keyedLock{}
		locks.locks[key] = entry
	}
	entry.refs++
	locks.mu.Unlock()
	entry.mu.Lock()
	return func() {
		entry.mu.Unlock()
		locks.mu.Lock()
		entry.refs--
		if entry.refs == 0 {
			delete(locks.locks, key)
		}
		locks.mu.Unlock()
	}
}

func wrapUnavailable(operation string, err error) error {
	if err == nil {
		return ErrUnavailable
	}
	return fmt.Errorf("%s: %w", operation, ErrUnavailable)
}
