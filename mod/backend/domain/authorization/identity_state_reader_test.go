package authorization

import (
	"context"
	"fmt"
	"testing"

	identitydao "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/dao"
	identitymodel "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestNewGORMIdentityStateReaderRequiresDependencies(t *testing.T) {
	db := &gorm.DB{}
	if _, err := NewGORMIdentityStateReader(nil, nil); err == nil {
		t.Fatal("NewGORMIdentityStateReader(nil, nil) succeeded")
	}
	if _, err := NewGORMIdentityStateReader(db, nil); err == nil {
		t.Fatal("NewGORMIdentityStateReader(db, nil) succeeded")
	}
}

func TestGORMIdentityStateReaderResolvesLiveTeamState(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:authorization-%s?mode=memory&cache=shared&_foreign_keys=1", uuid.NewString())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := identitydao.Migrate(db); err != nil {
		t.Fatal(err)
	}
	user := identitymodel.User{ID: uuid.NewString(), Username: "alice", DisplayName: "Alice", Status: identitymodel.UserStatusActive, PasswordHash: "unused"}
	team := identitymodel.Namespace{ID: uuid.NewString(), Kind: identitymodel.NamespaceKindTeam, Slug: "security", DisplayName: "Security"}
	invitation := identitymodel.TeamInvitation{ID: uuid.NewString(), NamespaceID: team.ID, UserID: user.ID, Role: identitymodel.TeamRoleDeveloper, InvitedByUserID: user.ID}
	invitation.CreatedAt = invitation.CreatedAt.UTC()
	invitation.ExpiresAt = invitation.CreatedAt.AddDate(0, 0, 7)
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&team).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&identitymodel.TeamMembership{NamespaceID: team.ID, UserID: user.ID, Role: identitymodel.TeamRoleMaintainer}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&invitation).Error; err != nil {
		t.Fatal(err)
	}
	repository, err := identitydao.NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := NewGORMIdentityStateReader(db, repository)
	if err != nil {
		t.Fatal(err)
	}
	principal, err := auth.NewUserPrincipal(user.ID, user.Username, auth.CredentialJWT, auth.UnrestrictedScopes())
	if err != nil {
		t.Fatal(err)
	}
	state, err := reader.ReadAuthorizationState(context.Background(), principal, auth.ResourceRef{Type: auth.ResourceTeamInvitation, ID: invitation.ID, NamespaceID: team.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !state.Active || state.TeamRole != identitymodel.TeamRoleMaintainer || !state.InvitationTarget {
		t.Fatalf("state = %+v", state)
	}
	if err := db.Model(&user).Update("status", identitymodel.UserStatusDisabled).Error; err != nil {
		t.Fatal(err)
	}
	state, err = reader.ReadAuthorizationState(context.Background(), principal, auth.ResourceRef{Type: auth.ResourceNamespace, ID: team.ID})
	if err != nil {
		t.Fatal(err)
	}
	if state.Active || state.TeamRole != "" {
		t.Fatalf("disabled state = %+v", state)
	}
}
