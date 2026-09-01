package authorization

import (
	"context"
	"errors"
	"fmt"

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
	resourceState, err := reader.resourceState(ctx, resource)
	if err != nil {
		return IdentityState{}, err
	}
	state := IdentityState{Plugin: resourceState.plugin}
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
	if resourceState.ownerNamespaceID != "" {
		var namespace identitymodel.Namespace
		err = reader.db.WithContext(ctx).Where("id = ?", resourceState.ownerNamespaceID).Take(&namespace).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return IdentityState{}, ErrIdentityUnknown
		}
		if err != nil {
			return IdentityState{}, fmt.Errorf("resolve authorization namespace: %w", err)
		}
		state.OwnsPersonalNamespace = namespace.Kind == identitymodel.NamespaceKindUser && namespace.OwnerUserID != nil && *namespace.OwnerUserID == user.ID
	}
	state.OwnsResource = resourceState.ownerUserID == user.ID
	if resourceState.teamNamespaceID != "" {
		var membership struct{ Role string }
		err = reader.db.WithContext(ctx).Table("team_memberships").Select("role").
			Where("namespace_id = ? AND user_id = ?", resourceState.teamNamespaceID, user.ID).Take(&membership).Error
		if err == nil {
			state.TeamRole = membership.Role
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return IdentityState{}, fmt.Errorf("resolve Team membership: %w", err)
		}
	}
	state.InvitationTarget = resourceState.invitedUserID == user.ID
	return state, nil
}

type resourceAuthorizationState struct {
	ownerNamespaceID string
	ownerUserID      string
	teamNamespaceID  string
	invitedUserID    string
	plugin           auth.PluginAuthorizationFacts
}

func (reader *GORMIdentityStateReader) resourceState(ctx context.Context, resource auth.ResourceRef) (resourceAuthorizationState, error) {
	switch resource.Type {
	case auth.ResourceUser:
		// User administration is authorized solely by the actor's current global
		// administrator membership. Do not resolve the target here: service commands
		// map an absent target to the documented not-found result.
		return resourceAuthorizationState{ownerUserID: resource.ID}, nil
	case auth.ResourceUserCollection:
		return resourceAuthorizationState{}, nil
	case auth.ResourceNamespace:
		return resourceAuthorizationState{ownerNamespaceID: resource.ID, teamNamespaceID: resource.ID}, nil
	case auth.ResourceTeamMembership:
		return resourceAuthorizationState{ownerNamespaceID: resource.NamespaceID, teamNamespaceID: resource.NamespaceID}, nil
	case auth.ResourceTeamInvitation:
		var invitation struct {
			NamespaceID string
			UserID      string
		}
		if err := reader.db.WithContext(ctx).Table("team_invitations").Select("namespace_id, user_id").Where("id = ? AND namespace_id = ?", resource.ID, resource.NamespaceID).Take(&invitation).Error; err != nil {
			return resourceAuthorizationState{}, mapResourceError(err)
		}
		return resourceAuthorizationState{ownerNamespaceID: invitation.NamespaceID, teamNamespaceID: invitation.NamespaceID, invitedUserID: invitation.UserID}, nil
	case auth.ResourcePlugin:
		return reader.pluginState(ctx, resource)
	case auth.ResourceMarketplace:
		var record struct {
			NamespaceID string
		}
		if err := reader.db.WithContext(ctx).Table("marketplace_templates").Select("namespace_id").Where("id = ?", resource.ID).Take(&record).Error; err != nil {
			return resourceAuthorizationState{}, mapResourceError(err)
		}
		return resourceAuthorizationState{ownerNamespaceID: record.NamespaceID}, nil
	case auth.ResourceTokenCollection:
		namespaceID, err := reader.personalNamespaceID(ctx, resource.ID)
		return resourceAuthorizationState{ownerNamespaceID: namespaceID, ownerUserID: resource.ID}, err
	case auth.ResourceToken:
		var token struct {
			UserID string
		}
		if err := reader.db.WithContext(ctx).Table("personal_access_tokens").Select("user_id").Where("id = ?", resource.ID).Take(&token).Error; err != nil {
			return resourceAuthorizationState{}, mapResourceError(err)
		}
		namespaceID, err := reader.personalNamespaceID(ctx, token.UserID)
		return resourceAuthorizationState{ownerNamespaceID: namespaceID, ownerUserID: token.UserID}, err
	default:
		return resourceAuthorizationState{}, ErrIdentityUnknown
	}
}

func (reader *GORMIdentityStateReader) pluginState(ctx context.Context, resource auth.ResourceRef) (resourceAuthorizationState, error) {
	if resource.ID == "" || resource.NamespaceID == "" {
		return resourceAuthorizationState{}, ErrIdentityUnknown
	}
	var record struct {
		NamespaceID      string
		Visibility       string
		Status           string
		RepositoryStatus string
	}
	// Plugin is the aggregate and policy root. The hidden repository is joined
	// only to obtain its operational fact; it is never addressed as a resource.
	err := reader.db.WithContext(ctx).
		Table("plugins AS plugin").
		Select("plugin.namespace_id, plugin.visibility, plugin.status, repository.status AS repository_status").
		Joins("JOIN repositories AS repository ON repository.id = plugin.id").
		Where("plugin.id = ? AND plugin.namespace_id = ?", resource.ID, resource.NamespaceID).
		Take(&record).Error
	if err != nil {
		return resourceAuthorizationState{}, mapResourceError(err)
	}
	return resourceAuthorizationState{
		ownerNamespaceID: record.NamespaceID,
		teamNamespaceID:  record.NamespaceID,
		plugin: auth.PluginAuthorizationFacts{
			Visibility:       auth.PluginVisibility(record.Visibility),
			Status:           auth.PluginStatus(record.Status),
			RepositoryStatus: auth.RepositoryOperationalStatus(record.RepositoryStatus),
		},
	}, nil
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
