package distribution

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	identitydomain "github.com/Esonhugh/MarketplaceServer/mod/backend/domain/identity"
	"gorm.io/gorm"
)

// GORMUserMarketplaceRepository is a read-only query adapter. It intentionally
// does not embed GORMRepository because that repository also exposes mutation
// and immutable-projection operations unrelated to this endpoint.
type GORMUserMarketplaceRepository struct {
	db *gorm.DB
}

func NewGORMUserMarketplaceRepository(db *gorm.DB) (*GORMUserMarketplaceRepository, error) {
	if db == nil {
		return nil, errors.New("user marketplace repository requires database")
	}
	return &GORMUserMarketplaceRepository{db: db}, nil
}

func (repository *GORMUserMarketplaceRepository) FindUserMarketplace(ctx context.Context, username string) (UserMarketplace, error) {
	var result UserMarketplace
	err := repository.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user identitydomain.User
		if err := tx.
			Select("id", "username", "display_name", "status").
			Where("username = ?", username).
			Take(&user).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrUserMarketplaceNotFound
			}
			return fmt.Errorf("find marketplace user: %w", err)
		}
		var namespace identitydomain.Namespace
		if err := tx.
			Select("id", "slug").
			Where("kind = ? AND owner_user_id = ?", identitydomain.NamespaceKindUser, user.ID).
			Take(&namespace).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrUserMarketplaceNotFound
			}
			return fmt.Errorf("find user marketplace namespace: %w", err)
		}

		var candidates []UserMarketplaceCandidate
		if err := tx.
			Table("plugins AS p").
			Select(`p.namespace_id, n.slug AS namespace_slug,
				p.id AS plugin_id, p.slug AS plugin_slug, p.description AS plugin_description, p.status AS plugin_status,
				r.id AS repository_id, r.slug AS repository_slug, r.status AS repository_status,
				v.id AS version_id, v.version, v.status AS version_status, v.tag_name, v.commit_sha`).
			Joins("JOIN namespaces AS n ON n.id = p.namespace_id").
			Joins("JOIN repositories AS r ON r.id = p.repository_id AND r.namespace_id = p.namespace_id").
			Joins("JOIN plugin_versions AS v ON v.plugin_id = p.id").
			Find(&candidates).Error; err != nil {
			return fmt.Errorf("find user marketplace plugins: %w", err)
		}
		result = UserMarketplace{UserID: user.ID, Username: user.Username, DisplayName: user.DisplayName, Status: user.Status, NamespaceID: namespace.ID, NamespaceSlug: namespace.Slug, Candidates: candidates}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return UserMarketplace{}, err
	}
	return result, nil
}

var _ UserMarketplaceRepository = (*GORMUserMarketplaceRepository)(nil)
