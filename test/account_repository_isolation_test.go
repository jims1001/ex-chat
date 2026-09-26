package test

import (
	"errors"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"gorm.io/gorm"
)

func testAccountRepositoryIAMBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	tenDB, iamDB := openStore("TEN"), openStore("IAM")
	owned := domain.User{Name: "TEN IAM member", Email: "ten-iam@example.test"}
	if err := iamDB.Create(&owned).Error; err != nil {
		t.Fatal(err)
	}
	repo := repository.NewAccountRepositoryWithUserPort(tenDB, repository.NewUserRepository(iamDB))
	if err := repo.AddMember(1, owned.ID, domain.RoleAgent); err != nil {
		t.Fatal(err)
	}
	if err := repo.AddMember(1, owned.ID+999, domain.RoleAgent); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("missing IAM user was accepted: %v", err)
	}

	members, err := repo.ListMembers(1)
	if err != nil || len(members) != 1 || members[0].User == nil || members[0].User.ID != owned.ID {
		t.Fatalf("IAM member composition failed: members=%+v err=%v", members, err)
	}
	foreign, err := repo.ListMembers(2)
	if err != nil || len(foreign) != 0 {
		t.Fatalf("cross-account members leaked: members=%+v err=%v", foreign, err)
	}
	if tenDB.Migrator().HasTable(&domain.User{}) {
		t.Fatal("TEN created IAM authority table")
	}

	outageIAM := openStore("IAM")
	outageSQL, err := outageIAM.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := outageSQL.Close(); err != nil {
		t.Fatal(err)
	}
	outageRepo := repository.NewAccountRepositoryWithUserPort(tenDB, repository.NewUserRepository(outageIAM))
	if err := outageRepo.AddMember(1, owned.ID+1, domain.RoleAgent); err == nil {
		t.Fatal("IAM outage was hidden during member creation")
	}
	if _, err := outageRepo.ListMembers(1); err == nil {
		t.Fatal("IAM outage was hidden during member hydration")
	}

	var count int64
	if err := tenDB.Model(&domain.AccountUser{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("failed IAM writes changed TEN: count=%d err=%v", count, err)
	}
}
