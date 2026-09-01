package dao

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity/model"
	"github.com/google/uuid"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func openTeamSQLite(t *testing.T) (*gorm.DB, *Repository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:team-%s?mode=memory&cache=shared&_foreign_keys=1", uuid.NewString())), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	repository, err := NewRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	return db, repository
}
func teamUser(id, username string) model.User {
	return model.User{ID: id, Username: username, DisplayName: username, Status: model.UserStatusActive, PasswordHash: "hash", CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
}
func TestTeamMigrationConstraints(t *testing.T) {
	db, _ := openTeamSQLite(t)
	u := teamUser(uuid.NewString(), "alice")
	if err := db.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	owner := u.ID
	personal := model.Namespace{ID: uuid.NewString(), Kind: model.NamespaceKindUser, Slug: "alice", DisplayName: "Alice", OwnerUserID: &owner}
	if err := db.Create(&personal).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO team_memberships(namespace_id,user_id,role,created_at,updated_at) VALUES(?,?,?,?,?)", personal.ID, u.ID, model.TeamRoleOwner, time.Now(), time.Now()).Error; err == nil {
		t.Fatal("accepted membership on personal namespace")
	}
	team := model.Namespace{ID: uuid.NewString(), Kind: model.NamespaceKindTeam, Slug: "security", DisplayName: "Security"}
	if err := db.Create(&team).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&model.TeamMembership{NamespaceID: team.ID, UserID: u.ID, Role: "invalid"}).Error; err == nil {
		t.Fatal("accepted invalid role")
	}
	now := time.Now().UTC()
	first := model.TeamInvitation{ID: uuid.NewString(), NamespaceID: team.ID, UserID: u.ID, Role: model.TeamRoleViewer, InvitedByUserID: u.ID, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(time.Hour)}
	if err := db.Create(&first).Error; err != nil {
		t.Fatal(err)
	}
	duplicate := first
	duplicate.ID = uuid.NewString()
	if err := db.Create(&duplicate).Error; err == nil {
		t.Fatal("accepted duplicate pending invitation")
	}
	if err := db.Model(&model.TeamInvitation{}).Where("id = ?", first.ID).Update("namespace_id", personal.ID).Error; err == nil {
		t.Fatal("accepted invitation identity update")
	}
}
func TestTeamRepositoryLifecycleAndLastOwner(t *testing.T) {
	db, r := openTeamSQLite(t)
	owner := teamUser(uuid.NewString(), "owner")
	member := teamUser(uuid.NewString(), "member")
	db.Create(&owner)
	db.Create(&member)
	now := time.Now().UTC()
	team := model.Namespace{ID: uuid.NewString(), Kind: model.NamespaceKindTeam, Slug: "ops", DisplayName: "Ops", CreatedAt: now, UpdatedAt: now}
	if err := r.CreateTeam(context.Background(), team, model.TeamMembership{NamespaceID: team.ID, UserID: owner.ID, Role: model.TeamRoleOwner, CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := r.RemoveTeamMember(context.Background(), team.ID, owner.ID); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("last owner error=%v", err)
	}
	inv := model.TeamInvitation{ID: uuid.NewString(), NamespaceID: team.ID, UserID: member.ID, Role: model.TeamRoleDeveloper, InvitedByUserID: owner.ID, CreatedAt: now, UpdatedAt: now, ExpiresAt: now.Add(7 * 24 * time.Hour)}
	if err := r.CreateTeamInvitation(context.Background(), inv); err != nil {
		t.Fatal(err)
	}
	namespaceID, err := r.RespondInvitation(context.Background(), inv.ID, member.ID, true, now.Add(time.Minute))
	if err != nil || namespaceID != team.ID {
		t.Fatalf("accept=%q,%v", namespaceID, err)
	}
	role, err := r.TeamRole(context.Background(), team.ID, member.ID)
	if err != nil || role != model.TeamRoleDeveloper {
		t.Fatalf("role=%q,%v", role, err)
	}
	if _, err := r.RespondInvitation(context.Background(), inv.ID, member.ID, true, now.Add(time.Minute)); !errors.Is(err, ErrIdentityConflict) {
		t.Fatalf("second accept=%v", err)
	}
}
