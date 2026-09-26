package test

import (
	"errors"
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"gorm.io/gorm"
)

func testRoutingServiceOwnerBoundary(t *testing.T, openStore func(string) *gorm.DB) {
	rteDB, chnDB, tenDB := openStore("RTE"), openStore("CHN"), openStore("TEN")
	iamDB, conDB, msgDB := openStore("IAM"), openStore("CON"), openStore("MSG")

	agentA := domain.User{Name: "Agent A", Email: "agent-a@example.test", Availability: domain.AvailabilityOnline}
	agentB := domain.User{Name: "Agent B", Email: "agent-b@example.test", Availability: domain.AvailabilityOnline}
	fallback := domain.User{Name: "Fallback", Email: "fallback-routing@example.test", Availability: domain.AvailabilityOnline}
	foreign := domain.User{Name: "Foreign", Email: "foreign-routing@example.test", Availability: domain.AvailabilityOnline}
	for _, user := range []*domain.User{&agentA, &agentB, &fallback, &foreign} {
		if err := iamDB.Create(user).Error; err != nil {
			t.Fatal(err)
		}
	}

	capacity := domain.AgentCapacityPolicy{AccountID: 1, Name: "One active conversation"}
	if err := rteDB.Create(&capacity).Error; err != nil {
		t.Fatal(err)
	}
	for _, userID := range []uint{agentA.ID, agentB.ID} {
		if err := tenDB.Create(&domain.AccountUser{AccountID: 1, UserID: userID, Role: domain.RoleAgent, AgentCapacityPolicyID: &capacity.ID}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := tenDB.Create(&domain.AccountUser{AccountID: 1, UserID: fallback.ID, Role: domain.RoleAgent}).Error; err != nil {
		t.Fatal(err)
	}
	if err := tenDB.Create(&domain.AccountUser{AccountID: 2, UserID: foreign.ID, Role: domain.RoleAgent}).Error; err != nil {
		t.Fatal(err)
	}
	ownedTeam := domain.Team{AccountID: 1, Name: "Routing fallback"}
	foreignTeam := domain.Team{AccountID: 2, Name: "Foreign routing"}
	if err := tenDB.Create(&ownedTeam).Error; err != nil {
		t.Fatal(err)
	}
	if err := tenDB.Create(&foreignTeam).Error; err != nil {
		t.Fatal(err)
	}

	policyRepo := repository.NewAssignmentPolicyRepository(rteDB)
	policy := domain.AssignmentPolicy{AccountID: 1, Name: "Routing", StrategyType: domain.StrategyRoundRobin, Enabled: true, FallbackAssigneeID: &fallback.ID}
	if err := policyRepo.Create(&policy); err != nil {
		t.Fatal(err)
	}
	inbox := domain.Inbox{AccountID: 1, Name: "Owner inbox", AssignmentPolicyID: &policy.ID}
	if err := chnDB.Create(&inbox).Error; err != nil {
		t.Fatal(err)
	}
	for _, userID := range []uint{agentA.ID, agentB.ID} {
		if err := chnDB.Create(&domain.InboxMember{InboxID: inbox.ID, UserID: userID}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := rteDB.Create(&domain.InboxCapacityLimit{AgentCapacityPolicyID: capacity.ID, InboxID: inbox.ID, ConversationLimit: 1}).Error; err != nil {
		t.Fatal(err)
	}
	for _, userID := range []uint{agentA.ID, agentB.ID} {
		conversation := domain.Conversation{AccountID: 1, InboxID: inbox.ID, AssigneeID: &userID, Status: domain.ConversationStatusOpen}
		if err := conDB.Create(&conversation).Error; err != nil {
			t.Fatal(err)
		}
	}
	foreignLoad := domain.Conversation{AccountID: 2, InboxID: inbox.ID, AssigneeID: &agentA.ID, Status: domain.ConversationStatusOpen}
	if err := conDB.Create(&foreignLoad).Error; err != nil {
		t.Fatal(err)
	}
	target := domain.Conversation{AccountID: 1, InboxID: inbox.ID, Status: domain.ConversationStatusOpen}
	if err := conDB.Create(&target).Error; err != nil {
		t.Fatal(err)
	}

	userRepo := repository.NewUserRepository(iamDB)
	accountRepo := repository.NewAccountRepositoryWithUserPort(tenDB, userRepo)
	teamRepo := repository.NewTeamRepositoryWithUserPort(tenDB, userRepo)
	inboxRepo := repository.NewInboxRepositoryWithAccountPort(chnDB, accountRepo)
	convRepo := repository.NewConversationRepositoryWithReadPorts(conDB, nil, nil, inboxRepo, nil, userRepo, teamRepo, nil)
	msgRepo := repository.NewMessageRepositoryWithConversationPort(msgDB, convRepo)
	capacityRepo := repository.NewAgentCapacityPolicyRepository(rteDB)
	routing := service.NewRoutingService(rteDB, convRepo, nil)
	routing.SetOwnerRepositories(inboxRepo, accountRepo, teamRepo, policyRepo, capacityRepo, msgRepo)

	if hasCapacity, load, err := routing.CheckAgentCapacity(1, inbox.ID, agentA.ID, nil, target.ID); err != nil || hasCapacity || load != 1 {
		t.Fatalf("owner count hasCapacity=%v load=%d err=%v", hasCapacity, load, err)
	}
	assigned, err := routing.AutoAssign(&target)
	if err != nil || assigned == nil || assigned.ID != fallback.ID || target.AssigneeID == nil || *target.AssigneeID != fallback.ID {
		t.Fatalf("fallback assignment=%+v target=%+v err=%v", assigned, target, err)
	}

	policy.FallbackAssigneeID = nil
	policy.FallbackTeamID = &ownedTeam.ID
	if err := policyRepo.Update(&policy); err != nil {
		t.Fatal(err)
	}
	teamTarget := domain.Conversation{AccountID: 1, InboxID: inbox.ID, Status: domain.ConversationStatusOpen}
	if err := conDB.Create(&teamTarget).Error; err != nil {
		t.Fatal(err)
	}
	if assigned, err := routing.AutoAssign(&teamTarget); err != nil || assigned != nil || teamTarget.TeamID == nil || *teamTarget.TeamID != ownedTeam.ID {
		t.Fatalf("fallback team assignment=%+v target=%+v err=%v", assigned, teamTarget, err)
	}

	policy.FallbackAssigneeID = &foreign.ID
	policy.FallbackTeamID = nil
	if err := policyRepo.Update(&policy); err != nil {
		t.Fatal(err)
	}
	foreignTarget := domain.Conversation{AccountID: 1, InboxID: inbox.ID, Status: domain.ConversationStatusOpen}
	if err := conDB.Create(&foreignTarget).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := routing.AutoAssign(&foreignTarget); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-account fallback was not rejected: %v", err)
	}

	policy.FallbackAssigneeID = nil
	policy.FallbackTeamID = &foreignTeam.ID
	if err := policyRepo.Update(&policy); err != nil {
		t.Fatal(err)
	}
	if _, err := routing.AutoAssign(&foreignTarget); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("cross-account team was not rejected: %v", err)
	}

	if rteDB.Migrator().HasTable(&domain.User{}) || rteDB.Migrator().HasTable(&domain.Inbox{}) || rteDB.Migrator().HasTable(&domain.Conversation{}) || rteDB.Migrator().HasTable(&domain.AccountUser{}) {
		t.Fatal("RTE created a foreign authority table")
	}
	if chnDB.Migrator().HasTable(&domain.User{}) || chnDB.Migrator().HasTable(&domain.Conversation{}) {
		t.Fatal("CHN created an IAM or CON authority table")
	}

	chnSQL, err := chnDB.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := chnSQL.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := routing.CheckAgentCapacity(1, inbox.ID, agentA.ID, nil, target.ID); err == nil {
		t.Fatal("CHN outage was hidden by the capacity query")
	}
}
