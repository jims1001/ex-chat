package service_test

import (
	"testing"

	"github.com/OracleBetX-Projects/ex-chat/internal/config"
	"github.com/OracleBetX-Projects/ex-chat/internal/database"
	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/repository"
	"github.com/OracleBetX-Projects/ex-chat/internal/service"
	"github.com/OracleBetX-Projects/ex-chat/internal/ws"
)

func TestRoutingService_AutoAssign(t *testing.T) {
	cfg := &config.Config{
		DBDriver: "sqlite",
		DBPath:   ":memory:",
	}
	db, _ := database.InitDB(cfg)

	userRepo := repository.NewUserRepository(db)
	accountRepo := repository.NewAccountRepository(db)
	inboxRepo := repository.NewInboxRepository(db)
	convRepo := repository.NewConversationRepository(db)
	hub := ws.NewHub()
	go hub.Run()

	routingService := service.NewRoutingService(db, convRepo, hub)

	account := domain.Account{Name: "Routing Corp"}
	_ = accountRepo.Create(&account)

	// Agent 1: Online
	agent1 := domain.User{
		Name:         "Agent 1",
		Email:        "a1@test.com",
		Role:         domain.RoleAgent,
		Availability: domain.AvailabilityOnline,
	}
	_ = userRepo.Create(&agent1)
	_ = accountRepo.AddMember(account.ID, agent1.ID, domain.RoleAgent)

	// Agent 2: Offline
	agent2 := domain.User{
		Name:         "Agent 2",
		Email:        "a2@test.com",
		Role:         domain.RoleAgent,
		Availability: domain.AvailabilityOffline,
	}
	_ = userRepo.Create(&agent2)
	_ = accountRepo.AddMember(account.ID, agent2.ID, domain.RoleAgent)

	inbox := domain.Inbox{
		AccountID:    account.ID,
		Name:         "Support Channel",
		WebsiteToken: "token_route_1",
	}
	_ = inboxRepo.Create(&inbox)
	_ = inboxRepo.AddMember(inbox.ID, agent1.ID)
	_ = inboxRepo.AddMember(inbox.ID, agent2.ID)

	// Create new unassigned conversation
	conv := domain.Conversation{
		AccountID: account.ID,
		InboxID:   inbox.ID,
		ContactID: 1,
		Status:    domain.ConversationStatusOpen,
	}
	_ = convRepo.Create(&conv)

	assigned, err := routingService.AutoAssign(&conv)
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if assigned == nil {
		t.Fatalf("expected assigned agent, got nil")
	}

	// Only Agent 1 is online, so Agent 1 must be chosen
	if assigned.ID != agent1.ID {
		t.Fatalf("expected assigned to agent 1 (%d), got %d", agent1.ID, assigned.ID)
	}

	// Now set Agent 2 to online
	_ = userRepo.UpdateAvailability(agent2.ID, domain.AvailabilityOnline)

	// Create second conversation -> Agent 1 already has 1 open conversation, Agent 2 has 0
	// So Agent 2 must be chosen (workload balancing)
	conv2 := domain.Conversation{
		AccountID: account.ID,
		InboxID:   inbox.ID,
		ContactID: 2,
		Status:    domain.ConversationStatusOpen,
	}
	_ = convRepo.Create(&conv2)

	assigned2, err := routingService.AutoAssign(&conv2)
	if err != nil {
		t.Fatalf("expected nil error on conv2, got %v", err)
	}
	if assigned2 == nil || assigned2.ID != agent2.ID {
		t.Fatalf("expected load balancing to pick agent 2 (%d), got %+v", agent2.ID, assigned2)
	}
}
