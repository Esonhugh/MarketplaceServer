package plugin

import (
	"context"
	"errors"

	plugindomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/plugin"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
)

// DomainLifecycleAdapter preserves the handler's API-only contract while the
// Plugin domain evolves independently. It must not expose persistence records
// or storage details.
type DomainLifecycleAdapter struct {
	service domainLifecycle
}

type domainLifecycle interface {
	Create(context.Context, auth.Principal, plugindomain.CreateInput) (plugindomain.PluginView, error)
	List(context.Context, auth.Principal, string, int, int) (plugindomain.PluginPage, error)
	Get(context.Context, auth.Principal, string, string) (plugindomain.PluginView, error)
	Archive(context.Context, auth.Principal, string, string) error
	Restore(context.Context, auth.Principal, string, string) error
	SetVisibility(context.Context, auth.Principal, string, string, string) error
	ListVersions(context.Context, auth.Principal, string, string, int, int) (plugindomain.VersionPage, error)
	GetVersion(context.Context, auth.Principal, string, string, string) (plugindomain.VersionView, error)
	Publish(context.Context, auth.Principal, string, string, plugindomain.PublishInput) (plugindomain.VersionView, error)
	SetDefaultVersion(context.Context, auth.Principal, string, string, string) error
	ClearDefaultVersion(context.Context, auth.Principal, string, string) error
}

func NewDomainLifecycleAdapter(service any) *DomainLifecycleAdapter {
	lifecycle, ok := service.(domainLifecycle)
	if !ok || lifecycle == nil {
		return nil
	}
	return &DomainLifecycleAdapter{service: lifecycle}
}

func (adapter *DomainLifecycleAdapter) Create(ctx context.Context, principal auth.Principal, input CreateInput) (Plugin, error) {
	result, err := adapter.service.Create(ctx, principal, plugindomain.CreateInput{
		Namespace: input.Namespace, Name: input.Name, Visibility: input.Visibility,
	})
	return pluginFromDomain(result), mapDomainError(err)
}

func (adapter *DomainLifecycleAdapter) List(ctx context.Context, principal auth.Principal, namespace string, page, size int) (PluginPage, error) {
	result, err := adapter.service.List(ctx, principal, namespace, page, size)
	return pluginPageFromDomain(result), mapDomainError(err)
}

func (adapter *DomainLifecycleAdapter) Get(ctx context.Context, principal auth.Principal, namespace, plugin string) (Plugin, error) {
	result, err := adapter.service.Get(ctx, principal, namespace, plugin)
	return pluginFromDomain(result), mapDomainError(err)
}

func (adapter *DomainLifecycleAdapter) Archive(ctx context.Context, principal auth.Principal, namespace, plugin string) error {
	return mapDomainError(adapter.service.Archive(ctx, principal, namespace, plugin))
}

func (adapter *DomainLifecycleAdapter) Restore(ctx context.Context, principal auth.Principal, namespace, plugin string) error {
	return mapDomainError(adapter.service.Restore(ctx, principal, namespace, plugin))
}

func (adapter *DomainLifecycleAdapter) SetVisibility(ctx context.Context, principal auth.Principal, namespace, plugin, visibility string) error {
	return mapDomainError(adapter.service.SetVisibility(ctx, principal, namespace, plugin, visibility))
}

func (adapter *DomainLifecycleAdapter) ListVersions(ctx context.Context, principal auth.Principal, namespace, plugin string, page, size int) (VersionPage, error) {
	result, err := adapter.service.ListVersions(ctx, principal, namespace, plugin, page, size)
	return versionPageFromDomain(result), mapDomainError(err)
}

func (adapter *DomainLifecycleAdapter) GetVersion(ctx context.Context, principal auth.Principal, namespace, plugin, tag string) (Version, error) {
	result, err := adapter.service.GetVersion(ctx, principal, namespace, plugin, tag)
	return versionFromDomain(result), mapDomainError(err)
}

func (adapter *DomainLifecycleAdapter) Publish(ctx context.Context, principal auth.Principal, namespace, plugin string, input PublishInput) (Version, error) {
	result, err := adapter.service.Publish(ctx, principal, namespace, plugin, plugindomain.PublishInput{Tag: input.Tag, MakeDefault: input.MakeDefault})
	return versionFromDomain(result), mapDomainError(err)
}

func (adapter *DomainLifecycleAdapter) SetDefaultVersion(ctx context.Context, principal auth.Principal, namespace, plugin, tag string) error {
	return mapDomainError(adapter.service.SetDefaultVersion(ctx, principal, namespace, plugin, tag))
}

func (adapter *DomainLifecycleAdapter) ClearDefaultVersion(ctx context.Context, principal auth.Principal, namespace, plugin string) error {
	return mapDomainError(adapter.service.ClearDefaultVersion(ctx, principal, namespace, plugin))
}

func pluginFromDomain(value plugindomain.PluginView) Plugin {
	return Plugin{
		Namespace: value.Namespace, Name: value.Name, Status: value.Status, Visibility: value.Visibility,
		RepositoryStatus: value.RepositoryStatus, CloneURL: value.CloneURL, DefaultVersion: value.DefaultVersion,
		CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func pluginPageFromDomain(value plugindomain.PluginPage) PluginPage {
	items := make([]Plugin, len(value.Items))
	for index, item := range value.Items {
		items[index] = pluginFromDomain(item)
	}
	return PluginPage{Items: items, Page: value.Page, Size: value.Size, Total: value.Total}
}

func versionFromDomain(value plugindomain.VersionView) Version {
	return Version{
		Tag: value.Tag, Status: value.Status, CommitSHA: value.CommitSHA,
		PublishedAt: value.PublishedAt, UpdatedAt: value.UpdatedAt,
	}
}

func versionPageFromDomain(value plugindomain.VersionPage) VersionPage {
	items := make([]Version, len(value.Items))
	for index, item := range value.Items {
		items[index] = versionFromDomain(item)
	}
	return VersionPage{Items: items, Page: value.Page, Size: value.Size, Total: value.Total}
}

func mapDomainError(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, plugindomain.ErrForbidden):
		return ErrForbidden
	case errors.Is(err, plugindomain.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, plugindomain.ErrAlreadyExists), errors.Is(err, plugindomain.ErrConflict):
		return ErrConflict
	case errors.Is(err, plugindomain.ErrGone):
		return ErrGone
	case errors.Is(err, plugindomain.ErrInvalidInput):
		return ErrInvalidInput
	default:
		return err
	}
}

var _ Lifecycle = (*DomainLifecycleAdapter)(nil)
