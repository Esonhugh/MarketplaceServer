package identity

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/Esonhugh/MarketplaceServer/pkg/auth"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	BootstrapAdminUsername = "admin"
	bootstrapLockID        = 0x4d505349
)

var (
	ErrBootstrapAdminPasswordRequired = errors.New("identity: bootstrap administrator password is required when no users exist")
	ErrBootstrapAdminPasswordReserved = errors.New("identity: bootstrap administrator password uses reserved API-key prefix")
)

type PasswordHasher func(password string, params auth.Argon2idParams) (string, error)

type Bootstrapper struct {
	db     *gorm.DB
	env    Environment
	hash   PasswordHasher
	params auth.Argon2idParams
	newID  func() string
}

func NewBootstrapper(db *gorm.DB, environment Environment) (*Bootstrapper, error) {
	if db == nil {
		return nil, errors.New("identity bootstrap requires database")
	}
	if environment == nil {
		environment = OSEnvironment{}
	}
	return &Bootstrapper{
		db:     db,
		env:    environment,
		hash:   auth.HashPassword,
		params: auth.DefaultArgon2idParams(),
		newID:  uuid.NewString,
	}, nil
}

func (bootstrapper *Bootstrapper) Bootstrap(ctx context.Context) error {
	if err := validateAPIKeyPepper(bootstrapper.env); err != nil {
		return err
	}
	runTransaction := func(db *gorm.DB) error {
		return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
			if err := lockBootstrapTransaction(tx); err != nil {
				return fmt.Errorf("serialize identity bootstrap: %w", err)
			}
			if err := ensureSystemGroups(tx); err != nil {
				return err
			}

			var count int64
			if err := tx.Model(&User{}).Count(&count).Error; err != nil {
				return fmt.Errorf("count bootstrap users: %w", err)
			}
			if count > 0 {
				return nil
			}
			password, ok := bootstrapper.env.LookupEnv(BootstrapAdminPasswordEnvironment)
			if !ok || password == "" {
				return ErrBootstrapAdminPasswordRequired
			}
			if auth.HasAPIKeyPrefix(password) {
				return ErrBootstrapAdminPasswordReserved
			}
			passwordHash, err := bootstrapper.hash(password, bootstrapper.params)
			if err != nil {
				return fmt.Errorf("hash bootstrap administrator password: %w", err)
			}
			userID := bootstrapper.newID()
			user := User{
				ID: userID, Username: BootstrapAdminUsername, DisplayName: "Administrator",
				Status: UserStatusActive, PasswordHash: passwordHash,
			}
			if err := tx.Create(&user).Error; err != nil {
				return fmt.Errorf("create bootstrap administrator: %w", err)
			}
			namespace := Namespace{
				ID: bootstrapper.newID(), Kind: NamespaceKindUser, Slug: BootstrapAdminUsername,
				DisplayName: user.DisplayName, OwnerUserID: &userID,
			}
			if err := tx.Create(&namespace).Error; err != nil {
				return fmt.Errorf("create bootstrap administrator namespace: %w", err)
			}
			membership := UserGroupMembership{UserID: userID, GroupID: AdminSystemGroupID}
			if err := tx.Create(&membership).Error; err != nil {
				return fmt.Errorf("create bootstrap administrator membership: %w", err)
			}
			return nil
		})
	}
	if !strings.EqualFold(bootstrapper.db.Dialector.Name(), "mysql") {
		return runTransaction(bootstrapper.db)
	}
	connection := bootstrapper.db.Connection(func(db *gorm.DB) error {
		if err := acquireMySQLBootstrapLock(db); err != nil {
			return err
		}
		defer func() { _ = db.Exec("SELECT RELEASE_LOCK(?)", "marketplace_identity_bootstrap").Error }()
		return runTransaction(db)
	})
	if connection != nil {
		return fmt.Errorf("serialize identity bootstrap: %w", connection)
	}
	return nil
}

func ensureSystemGroups(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("identity: ensure system groups requires database")
	}
	groups := []SystemGroup{
		{ID: AdminSystemGroupID, Name: AdminSystemGroupName, Description: "Global system administrators"},
		{ID: DefaultSystemGroupID, Name: DefaultSystemGroupName, Description: "All active users"},
	}
	for i := range groups {
		if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&groups[i]).Error; err != nil {
			return fmt.Errorf("ensure system group %s: %w", groups[i].Name, err)
		}
	}
	var persisted []SystemGroup
	if err := tx.Where("id IN ?", []string{AdminSystemGroupID, DefaultSystemGroupID}).Find(&persisted).Error; err != nil {
		return fmt.Errorf("verify system groups: %w", err)
	}
	byID := make(map[string]SystemGroup, len(persisted))
	for _, group := range persisted {
		byID[group.ID] = group
	}
	for _, expected := range groups {
		actual, ok := byID[expected.ID]
		if !ok || actual.Name != expected.Name || actual.Description != expected.Description {
			return fmt.Errorf("identity: fixed system group %s does not match source-controlled definition", expected.Name)
		}
	}
	return nil
}

func lockBootstrapTransaction(tx *gorm.DB) error {
	if tx == nil {
		return errors.New("identity: bootstrap lock requires database transaction")
	}
	switch strings.ToLower(tx.Dialector.Name()) {
	case "postgres":
		return tx.Exec("SELECT pg_advisory_xact_lock(?)", bootstrapLockID).Error
	case "mysql":
		return nil
	default:
		return fmt.Errorf("unsupported bootstrap database driver %q", tx.Dialector.Name())
	}
}

func acquireMySQLBootstrapLock(db *gorm.DB) error {
	var acquired int
	if err := db.Raw("SELECT GET_LOCK(?, ?)", "marketplace_identity_bootstrap", 30).Scan(&acquired).Error; err != nil {
		return err
	}
	if acquired != 1 {
		return errors.New("database bootstrap lock was not acquired")
	}
	return nil
}
