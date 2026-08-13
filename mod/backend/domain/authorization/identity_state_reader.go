package authorization

import (
	"context"
	"errors"
	"fmt"

	distributiondomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/distribution"
	identitydao "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/dao"
	identitymodel "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"gorm.io/gorm"
)

type GORMIdentityStateReader struct {
	db         *gorm.DB
	identities *identitydao.Repository
}

func NewGORMIdentityStateReader(db *gorm.DB, identities *identitydao.Repository) (*GORMIdentityStateReader, error) {
	if db == nil || identities == nil {
		return nil, errors.New("authorization identity state reader requires database and identity repository")
	}
	return &GORMIdentityStateReader{db: db, identities: identities}, nil
}

func (reader *GORMIdentityStateReader) ReadAuthorizationState(ctx context.Context, principal auth.Principal, resource auth.ResourceRef) (IdentityState, error) {
	state := IdentityState{}
	ownerNamespaceID, ownerUserID, public, err := reader.resourceState(ctx, resource)
	if err != nil {
		return IdentityState{}, err
	}
	state.Public = public
	if principal.IsAnonymous() {
		return state, nil
	}
	if !principal.IsUser() || principal.UserID() == "" {
		return IdentityState{}, ErrIdentityUnknown
	}
	user, err := reader.identities.FindUserByID(ctx, principal.UserID())
	if errors.Is(err, identitydao.ErrIdentityNotFound) {
		return IdentityState{}, ErrIdentityUnknown
	}
	if err != nil {
		return IdentityState{}, err
	}
	state.Active = user.Status == identitymodel.UserStatusActive
	if !state.Active {
		return state, nil
	}
	state.SystemAdmin, err = reader.identities.IsSystemAdmin(ctx, user.ID)
	if err != nil {
		return IdentityState{}, err
	}
	if ownerNamespaceID != "" {
		var namespace identitymodel.Namespace
		err = reader.db.WithContext(ctx).Where("id = ?", ownerNamespaceID).Take(&namespace).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return IdentityState{}, ErrIdentityUnknown
		}
		if err != nil {
			return IdentityState{}, fmt.Errorf("resolve authorization namespace: %w", err)
		}
		state.OwnsPersonalNamespace = namespace.Kind == identitymodel.NamespaceKindUser && namespace.OwnerUserID != nil && *namespace.OwnerUserID == user.ID
	}
	state.OwnsResource = ownerUserID == user.ID
	return state, nil
}

func (reader *GORMIdentityStateReader) resourceState(ctx context.Context, resource auth.ResourceRef) (string, string, bool, error) {
	switch resource.Type {
	case "user":
		namespaceID, err := reader.personalNamespaceID(ctx, resource.ID)
		return namespaceID, resource.ID, false, err
	case "namespace":
		return resource.ID, "", false, nil
	case "repository":
		var record distributiondomain.Repository
		if err := reader.db.WithContext(ctx).Select("namespace_id", "visibility", "status").Where("id = ?", resource.ID).Take(&record).Error; err != nil {
			return "", "", false, mapResourceError(err)
		}
		return record.NamespaceID, "", record.Visibility == "public" && (record.Status == distributiondomain.RepositoryStatusReady || record.Status == distributiondomain.RepositoryStatusReadOnly), nil
	case "plugin":
		var record distributiondomain.Plugin
		if err := reader.db.WithContext(ctx).Select("namespace_id", "visibility", "status").Where("id = ?", resource.ID).Take(&record).Error; err != nil {
			return "", "", false, mapResourceError(err)
		}
		return record.NamespaceID, "", record.Visibility == "public" && record.Status == distributiondomain.StatusActive, nil
	case "marketplace":
		var record distributiondomain.MarketplaceTemplate
		if err := reader.db.WithContext(ctx).Select("namespace_id", "visibility", "status").Where("id = ?", resource.ID).Take(&record).Error; err != nil {
			return "", "", false, mapResourceError(err)
		}
		return record.NamespaceID, "", record.Visibility == "public" && record.Status == distributiondomain.StatusActive, nil
	case "token_collection":
		namespaceID, err := reader.personalNamespaceID(ctx, resource.ID)
		return namespaceID, resource.ID, false, err
	case "token":
		var token identitymodel.PersonalAccessToken
		if err := reader.db.WithContext(ctx).Select("user_id").Where("id = ?", resource.ID).Take(&token).Error; err != nil {
			return "", "", false, mapResourceError(err)
		}
		namespaceID, err := reader.personalNamespaceID(ctx, token.UserID)
		return namespaceID, token.UserID, false, err
	default:
		return "", "", false, ErrIdentityUnknown
	}
}

func (reader *GORMIdentityStateReader) personalNamespaceID(ctx context.Context, userID string) (string, error) {
	var namespace identitymodel.Namespace
	if err := reader.db.WithContext(ctx).
		Select("id").
		Where("kind = ? AND owner_user_id = ?", identitymodel.NamespaceKindUser, userID).
		Take(&namespace).Error; err != nil {
		return "", mapResourceError(err)
	}
	return namespace.ID, nil
}

func mapResourceError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrIdentityUnknown
	}
	return fmt.Errorf("resolve authorization resource: %w", err)
}

var _ IdentityStateReader = (*GORMIdentityStateReader)(nil)
