package authorization

import (
	"testing"

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
