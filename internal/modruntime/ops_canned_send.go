package modruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/internal/foundation"
	"gorm.io/gorm"
)

const cannedResponsePrepareSend = "ops.canned_response.prepare_send.v1"
const cannedResponseSend = "ops.canned_response.send.v1"

// The prepare receipt commits this immutable snapshot before a send can invoke
// another owner. It is a preparation-time snapshot, not a live-template permit.
type cannedSendIntent struct {
	IntentID         string `json:"intent_id"`
	AccountID        string `json:"account_id"`
	ActorID          string `json:"actor_id"`
	ConversationID   string `json:"conversation_id"`
	CannedResponseID string `json:"canned_response_id"`
	ObjectVersion    string `json:"object_version"`
	ResolvedContent  string `json:"resolved_content"`
}

func cannedSendInvalid() error {
	return &ContractFailure{Type: "validation_failed", Message: "canned send command shape is unsupported"}
}
func cannedSendUnknown() error {
	return &ContractFailure{Type: "result_unknown", Message: "message outcome is unknown; retry the same prepared intent"}
}
func cannedSendText(raw string) bool {
	return len(raw) <= 100000 && utf8.ValidString(raw) && strings.TrimSpace(raw) != ""
}
func cannedSendVersion(raw string) bool {
	n, err := strconv.ParseInt(raw, 10, 64)
	return err == nil && n >= 0 && strconv.FormatInt(n, 10) == raw
}
func cannedSendIntentID(raw string) bool {
	if len(raw) != 64 {
		return false
	}
	_, err := hex.DecodeString(raw)
	return err == nil && strings.ToLower(raw) == raw
}
func parseCannedSend(ctx context.Context, c foundation.CommandEnvelope) error {
	_, actorOK := cannedResponseID(c.Actor.ID)
	if c.Actor.Type != "User" || c.Actor.AuthSource != "module" || !actorOK {
		return &ContractFailure{Type: "permission_denied", Message: "canned send requires a trusted module User actor"}
	}
	_, targetOK := cannedResponseID(c.Target.ID)
	if c.Validate() != nil || uint64(c.AccountID) > math.MaxInt64 || !targetOK || c.Target.Type != "conversation" || c.Target.ExpectedVersion != 0 || strings.TrimSpace(c.IdempotencyKey) != c.IdempotencyKey {
		return cannedSendInvalid()
	}
	switch c.CommandType {
	case cannedResponsePrepareSend:
		source, ok := c.Input["canned_response_id"].(string)
		_, idOK := cannedResponseID(source)
		version, vOK := c.Input["object_version"].(string)
		if len(c.Input) != 3 || !ok || !idOK || !vOK || !cannedSendVersion(version) {
			return cannedSendInvalid()
		}
		if _, err := cannedPreviewVariables(ctx, c.Input["variables"]); err != nil {
			return err
		}
	case cannedResponseSend:
		intent, ok := c.Input["intent_id"].(string)
		content, textOK := c.Input["resolved_content"].(string)
		if len(c.Input) != 2 || !ok || !cannedSendIntentID(intent) || !textOK || !cannedSendText(content) {
			return cannedSendInvalid()
		}
	default:
		return cannedSendInvalid()
	}
	return ctx.Err()
}
func (r *Runtime) authorizeCannedSend(ctx context.Context, c foundation.CommandEnvelope) error {
	if err := parseCannedSend(ctx, c); err != nil {
		return err
	}
	userID, _ := cannedResponseID(c.Actor.ID)
	dep, ok := r.Config.Dependencies["TEN"]
	if !ok {
		return &ContractFailure{Type: "dependency_unavailable", Message: "TEN membership dependency is unavailable"}
	}
	return requireCannedResponseMember(ctx, newModuleClient("OPS", dep, r.Config.ContractVersion, r.Config.MaxTimeout), c.AccountID, c.Actor, userID)
}
func (r *Runtime) registerCannedSend() error {
	for _, capability := range []string{cannedResponsePrepareSend, cannedResponseSend} {
		r.Contracts.authorizers[capability] = r.authorizeCannedSend
	}
	if err := r.Contracts.RegisterCommand(cannedResponsePrepareSend, r.prepareCannedSend); err != nil {
		return err
	}
	return r.Contracts.RegisterCommand(cannedResponseSend, r.sendCannedIntent)
}
func (r *Runtime) prepareCannedSend(ctx context.Context, c foundation.CommandEnvelope) (any, error) {
	if err := r.authorizeCannedSend(ctx, c); err != nil {
		return nil, err
	}
	if tx, ok := ctx.Value(transactionKey{}).(*gorm.DB); !ok || tx == nil {
		return nil, &ContractFailure{Type: "dependency_unavailable", Message: "canned intent transaction is unavailable"}
	}
	preview := r.Contracts.queries[cannedResponsePreview]
	if preview == nil {
		return nil, &ContractFailure{Type: "dependency_unavailable", Message: "canned preview is unavailable"}
	}
	q := foundation.QueryEnvelope{QueryType: cannedResponsePreview, AccountID: c.AccountID, Actor: c.Actor, Filter: map[string]any{"id": json.Number(c.Input["canned_response_id"].(string)), "variables": c.Input["variables"]}, Sort: []string{"id"}, Page: "1", Fields: append([]string(nil), cannedPreviewFields[:]...), Consistency: "strong", RequestedAt: time.Now().UTC()}
	result, err := preview(ctx, q)
	if err != nil {
		return nil, err
	}
	view, ok := result.(cannedPreviewView)
	if !ok || view.AccountID != c.AccountID || strconv.FormatUint(uint64(view.ID), 10) != c.Input["canned_response_id"] {
		return nil, &ContractFailure{Type: "dependency_unavailable", Message: "canned preview returned incompatible data"}
	}
	if strconv.FormatInt(view.ObjectVersion, 10) != c.Input["object_version"] {
		return nil, &ContractFailure{Type: "state_conflict", Message: "canned response version changed before preparation"}
	}
	if !cannedSendText(view.ResolvedContent) {
		return nil, cannedSendInvalid()
	}
	hash := sha256.Sum256([]byte(c.IdempotencyKey))
	intent := cannedSendIntent{IntentID: hex.EncodeToString(hash[:]), AccountID: strconv.FormatUint(uint64(c.AccountID), 10), ActorID: c.Actor.ID, ConversationID: c.Target.ID, CannedResponseID: c.Input["canned_response_id"].(string), ObjectVersion: c.Input["object_version"].(string), ResolvedContent: view.ResolvedContent}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return intent, nil
}

func readCannedSendIntent(ctx context.Context, tx *gorm.DB, c foundation.CommandEnvelope) (cannedSendIntent, error) {
	var intent cannedSendIntent
	missing := func() (cannedSendIntent, error) {
		return cannedSendIntent{}, &ContractFailure{Type: "resource_not_found", Message: "prepared canned intent was not found"}
	}
	corrupt := func() (cannedSendIntent, error) {
		return cannedSendIntent{}, &ContractFailure{Type: "dependency_unavailable", Message: "prepared canned intent is unavailable"}
	}
	id := c.Input["intent_id"].(string)
	var row domain.LocalChangeJournal
	err := tx.WithContext(ctx).Where("change_id = ?", "cmd-"+id[:48]).First(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return missing()
	}
	if err != nil {
		return corrupt()
	}
	actorID, _ := cannedResponseID(c.Actor.ID)
	if row.SourceModule != "OPS" || row.ObjectType != cannedResponsePrepareSend || row.Action != "command_receipt" || row.Result != "applied" || row.Status != "delivered" || row.IdempotencyKeyHash != id || row.AccountID != c.AccountID || row.ActorType != "User" || row.ActorID != actorID {
		return missing()
	}
	decoder := json.NewDecoder(strings.NewReader(row.AfterState))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&intent) != nil {
		return corrupt()
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return corrupt()
	}
	_, sourceOK := cannedResponseID(intent.CannedResponseID)
	if intent.IntentID != id || intent.AccountID != strconv.FormatUint(uint64(row.AccountID), 10) || intent.ActorID != strconv.FormatUint(uint64(row.ActorID), 10) || !sourceOK || !cannedSendVersion(intent.ObjectVersion) || !cannedSendText(intent.ResolvedContent) {
		return corrupt()
	}
	if _, ok := cannedResponseID(intent.ConversationID); !ok {
		return corrupt()
	}
	if intent.ConversationID != c.Target.ID {
		return missing()
	}
	if intent.ResolvedContent != c.Input["resolved_content"] {
		return cannedSendIntent{}, &ContractFailure{Type: "state_conflict", Message: "confirmation does not match prepared canned intent"}
	}
	return intent, ctx.Err()
}
func (r *Runtime) sendCannedIntent(ctx context.Context, c foundation.CommandEnvelope) (any, error) {
	if err := r.authorizeCannedSend(ctx, c); err != nil {
		return nil, err
	}
	tx, ok := ctx.Value(transactionKey{}).(*gorm.DB)
	if !ok || tx == nil {
		return nil, &ContractFailure{Type: "dependency_unavailable", Message: "canned send transaction is unavailable"}
	}
	intent, err := readCannedSendIntent(ctx, tx, c)
	if err != nil {
		return nil, err
	}
	dep, ok := r.Config.Dependencies["MSG"]
	if !ok {
		return nil, &ContractFailure{Type: "dependency_unavailable", Message: "MSG dependency is unavailable"}
	}
	downstream := c
	downstream.CommandType = messageFromCannedPreview
	downstream.CommandID = "ops-canned-send-" + intent.IntentID[:48]
	downstream.CausationID = c.CommandID
	downstream.IdempotencyKey = cannedResponseSend + ":" + intent.IntentID
	downstream.Input = map[string]any{"canned_response_id": intent.CannedResponseID, "object_version": intent.ObjectVersion, "resolved_content": intent.ResolvedContent}
	downstream.TimeoutPolicy.StatusQuery = "msg.message.get.v1"
	var result messageView
	if err := newModuleClient("OPS", dep, r.Config.ContractVersion, r.Config.MaxTimeout).command(ctx, downstream, &result); err != nil {
		var failure *ContractFailure
		if errors.As(err, &failure) && failure.Type != "dependency_unavailable" && failure.Type != "permanent_external_failure" {
			return nil, err
		}
		return nil, cannedSendUnknown()
	}
	actorID, _ := cannedResponseID(intent.ActorID)
	conversationID, _ := cannedResponseID(intent.ConversationID)
	if result.ID == 0 || uint64(result.ID) > math.MaxInt64 || result.AccountID != c.AccountID || result.ConversationID != conversationID || result.SenderType != domain.SenderTypeUser || result.SenderID != actorID || result.MessageType != domain.MessageTypeOutgoing || result.ContentType != domain.ContentTypeText || result.Content != strings.TrimSpace(intent.ResolvedContent) || result.Private || result.Status != domain.MessageStatusQueued || result.Deleted || result.DeletedAt != nil || result.EditedAt != nil || result.DeliveryFailed {
		return nil, cannedSendUnknown()
	}
	if ctx.Err() != nil {
		return nil, cannedSendUnknown()
	}
	return result, nil
}
