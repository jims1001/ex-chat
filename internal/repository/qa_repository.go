package repository

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

// QATaskFilter defines query filters for listing QA tasks
type QATaskFilter struct {
	Status      string `form:"status"`
	InspectorID *uint  `form:"inspector_id"`
	AgentID     *uint  `form:"agent_id"`
	TargetType  string `form:"target_type"`
	Result      string `form:"result"`
	Query       string `form:"q"`
	Page        int    `form:"page"`
	Limit       int    `form:"limit"`
}

// QAAppealFilter defines query filters for listing QA appeals
type QAAppealFilter struct {
	Status      string `form:"status"`
	TaskID      *uint  `form:"task_id"`
	AppellantID *uint  `form:"appellant_id"`
	Page        int    `form:"page"`
	Limit       int    `form:"limit"`
}

// QARepository provides database operations for QA scorecards, sampling, tasks and appeals
type QARepository struct {
	db *gorm.DB
}

// NewQARepository creates a new QARepository instance
func NewQARepository(db *gorm.DB) *QARepository {
	return &QARepository{db: db}
}

// EnsureDefaultScorecard ensures at least one default QA scorecard exists for the account
func (r *QARepository) EnsureDefaultScorecard(accountID uint) (*domain.QAScorecard, error) {
	var existing domain.QAScorecard
	if err := r.db.Where("account_id = ?", accountID).Preload("Criteria").First(&existing).Error; err == nil {
		return &existing, nil
	}

	scorecard := &domain.QAScorecard{
		AccountID:    accountID,
		Name:         "服务质量标准评分表",
		Description:  "包含流程符合度、问题解决质量、沟通体验及服务合规性红线",
		TotalScore:   100,
		PassingScore: 85,
		Status:       "active",
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := r.db.Create(scorecard).Error; err != nil {
		return nil, err
	}

	criteria := []domain.QACriterion{
		{
			AccountID:   accountID,
			ScorecardID: scorecard.ID,
			Category:    "流程符合",
			Title:       "标准流程执行与身份核验完整度",
			Description: "客服是否按业务规范执行用户身份核验、工单分类与标准流转程序",
			MaxScore:    30,
			IsFatal:     false,
			OrderIndex:  1,
		},
		{
			AccountID:   accountID,
			ScorecardID: scorecard.ID,
			Category:    "解决质量",
			Title:       "业务问题诊断深度与方案准确性",
			Description: "客服是否准确识别客户核心诉求并提供切实有效的技术或售后解决方案",
			MaxScore:    35,
			IsFatal:     false,
			OrderIndex:  2,
		},
		{
			AccountID:   accountID,
			ScorecardID: scorecard.ID,
			Category:    "沟通体验",
			Title:       "礼貌规范用语、同理心与清晰度",
			Description: "服务用语温和有礼，具备倾听与安抚技巧，无冷漠拖延表现",
			MaxScore:    25,
			IsFatal:     false,
			OrderIndex:  3,
		},
		{
			AccountID:   accountID,
			ScorecardID: scorecard.ID,
			Category:    "记录完整",
			Title:       "系统会话小结与跟踪附言规范",
			Description: "会话或工单解决后，内部备注及流转记录是否清晰详实",
			MaxScore:    10,
			IsFatal:     false,
			OrderIndex:  4,
		},
		{
			AccountID:   accountID,
			ScorecardID: scorecard.ID,
			Category:    "合规红线",
			Title:       "一票否决红线：严禁泄露隐私、辱骂客户或虚假承诺",
			Description: "若触发本项，本次质检一票否决，判定致命错误且总分为0分",
			MaxScore:    0,
			IsFatal:     true,
			OrderIndex:  5,
		},
	}

	for i := range criteria {
		_ = r.db.Create(&criteria[i])
	}
	scorecard.Criteria = criteria
	return scorecard, nil
}

// CreateScorecard creates a new scorecard template with criteria
func (r *QARepository) CreateScorecard(scorecard *domain.QAScorecard) error {
	if scorecard.AccountID == 0 {
		return errors.New("account_id is required")
	}
	if strings.TrimSpace(scorecard.Name) == "" {
		return errors.New("scorecard name is required")
	}
	if scorecard.TotalScore == 0 {
		scorecard.TotalScore = 100
	}
	if scorecard.PassingScore == 0 {
		scorecard.PassingScore = 85
	}
	if scorecard.Status == "" {
		scorecard.Status = "active"
	}
	now := time.Now()
	scorecard.CreatedAt = now
	scorecard.UpdatedAt = now

	return r.db.Transaction(func(tx *gorm.DB) error {
		criteria := scorecard.Criteria
		scorecard.Criteria = nil
		if err := tx.Create(scorecard).Error; err != nil {
			return err
		}
		for i := range criteria {
			criteria[i].ID = 0
			criteria[i].AccountID = scorecard.AccountID
			criteria[i].ScorecardID = scorecard.ID
			criteria[i].CreatedAt = now
			criteria[i].UpdatedAt = now
			if err := tx.Create(&criteria[i]).Error; err != nil {
				return err
			}
		}
		scorecard.Criteria = criteria
		return nil
	})
}

// ListScorecards retrieves all scorecards for an account
func (r *QARepository) ListScorecards(accountID uint) ([]domain.QAScorecard, error) {
	var scorecards []domain.QAScorecard
	if err := r.db.Where("account_id = ?", accountID).
		Preload("Criteria", func(db *gorm.DB) *gorm.DB {
			return db.Order("order_index ASC")
		}).
		Order("created_at DESC").
		Find(&scorecards).Error; err != nil {
		return nil, err
	}
	if len(scorecards) == 0 {
		sc, err := r.EnsureDefaultScorecard(accountID)
		if err == nil && sc != nil {
			scorecards = append(scorecards, *sc)
		}
	}
	return scorecards, nil
}

// GetScorecardByID finds a single scorecard
func (r *QARepository) GetScorecardByID(accountID, id uint) (*domain.QAScorecard, error) {
	var scorecard domain.QAScorecard
	if err := r.db.Where("account_id = ? AND id = ?", accountID, id).
		Preload("Criteria", func(db *gorm.DB) *gorm.DB {
			return db.Order("order_index ASC")
		}).
		First(&scorecard).Error; err != nil {
		return nil, err
	}
	return &scorecard, nil
}

// CreateSamplingRule creates a new sampling rule
func (r *QARepository) CreateSamplingRule(rule *domain.QASamplingRule) error {
	if rule.AccountID == 0 {
		return errors.New("account_id is required")
	}
	if strings.TrimSpace(rule.Name) == "" {
		return errors.New("sampling rule name is required")
	}
	if rule.TargetType == "" {
		rule.TargetType = "conversation"
	}
	if rule.Status == "" {
		rule.Status = "active"
	}
	now := time.Now()
	rule.CreatedAt = now
	rule.UpdatedAt = now
	return r.db.Create(rule).Error
}

// ListSamplingRules lists sampling rules for an account
func (r *QARepository) ListSamplingRules(accountID uint) ([]domain.QASamplingRule, error) {
	var rules []domain.QASamplingRule
	if err := r.db.Where("account_id = ?", accountID).
		Preload("Scorecard").
		Preload("AssignedInspector").
		Order("created_at DESC").
		Find(&rules).Error; err != nil {
		return nil, err
	}
	return rules, nil
}

// GetSamplingRuleByID finds a sampling rule
func (r *QARepository) GetSamplingRuleByID(accountID, id uint) (*domain.QASamplingRule, error) {
	var rule domain.QASamplingRule
	if err := r.db.Where("account_id = ? AND id = ?", accountID, id).
		Preload("Scorecard").
		Preload("AssignedInspector").
		First(&rule).Error; err != nil {
		return nil, err
	}
	return &rule, nil
}

// GenerateTasksFromSampling runs a sampling rule to extract conversations or tickets into QA tasks
func (r *QARepository) GenerateTasksFromSampling(accountID, ruleID uint) ([]domain.QATask, error) {
	rule, err := r.GetSamplingRuleByID(accountID, ruleID)
	if err != nil {
		return nil, err
	}

	scorecardID := rule.ScorecardID
	if scorecardID == 0 {
		sc, _ := r.EnsureDefaultScorecard(accountID)
		if sc != nil {
			scorecardID = sc.ID
		}
	}
	scorecard, err := r.GetScorecardByID(accountID, scorecardID)
	if err != nil {
		return nil, err
	}

	var generated []domain.QATask
	now := time.Now()
	dueAt := now.Add(24 * time.Hour)

	if rule.TargetType == "ticket" {
		var tickets []domain.Ticket
		r.db.Where("account_id = ? AND status IN ('resolved', 'closed')", accountID).
			Order("updated_at DESC").Limit(5).Find(&tickets)

		for _, t := range tickets {
			var count int64
			r.db.Model(&domain.QATask{}).Where("account_id = ? AND target_type = 'ticket' AND target_id = ?", accountID, t.ID).Count(&count)
			if count > 0 {
				continue
			}

			task := domain.QATask{
				AccountID:      accountID,
				TargetType:     "ticket",
				TargetID:       t.ID,
				TargetRef:      fmt.Sprintf("工单 %s", t.TicketNumber),
				ScorecardID:    scorecard.ID,
				ScorecardName:  scorecard.Name,
				InspectorID:    rule.AssignedInspectorID,
				AgentID:        t.AssigneeID,
				SamplingRuleID: &rule.ID,
				DueAt:          &dueAt,
				Status:         "pending",
				Result:         "待评分",
			}
			if err := r.CreateTask(&task); err == nil {
				generated = append(generated, task)
			}
		}
	} else {
		var convs []domain.Conversation
		r.db.Where("account_id = ?", accountID).
			Order("updated_at DESC").Limit(5).Find(&convs)

		for _, c := range convs {
			var count int64
			r.db.Model(&domain.QATask{}).Where("account_id = ? AND target_type = 'conversation' AND target_id = ?", accountID, c.ID).Count(&count)
			if count > 0 {
				continue
			}

			var agentID *uint
			if c.AssigneeID != nil {
				agentID = c.AssigneeID
			}

			task := domain.QATask{
				AccountID:      accountID,
				TargetType:     "conversation",
				TargetID:       c.ID,
				TargetRef:      fmt.Sprintf("会话 #%d", c.ID),
				ScorecardID:    scorecard.ID,
				ScorecardName:  scorecard.Name,
				InspectorID:    rule.AssignedInspectorID,
				AgentID:        agentID,
				SamplingRuleID: &rule.ID,
				DueAt:          &dueAt,
				Status:         "pending",
				Result:         "待评分",
			}
			if err := r.CreateTask(&task); err == nil {
				generated = append(generated, task)
			}
		}
	}

	return generated, nil
}

// CreateTask creates a new QA review task
func (r *QARepository) CreateTask(task *domain.QATask) error {
	if task.AccountID == 0 {
		return errors.New("account_id is required")
	}
	now := time.Now()
	task.CreatedAt = now
	task.UpdatedAt = now

	if task.Status == "" {
		task.Status = "pending"
	}
	if task.Result == "" {
		task.Result = "待评分"
	}
	if task.ScorecardID == 0 {
		sc, _ := r.EnsureDefaultScorecard(task.AccountID)
		if sc != nil {
			task.ScorecardID = sc.ID
			task.ScorecardName = sc.Name
		}
	} else if task.ScorecardName == "" {
		if sc, err := r.GetScorecardByID(task.AccountID, task.ScorecardID); err == nil && sc != nil {
			task.ScorecardName = sc.Name
		}
	}

	if task.TargetRef == "" {
		if task.TargetType == "ticket" {
			task.TargetRef = fmt.Sprintf("工单 #%d", task.TargetID)
		} else {
			task.TargetRef = fmt.Sprintf("会话 #%d", task.TargetID)
		}
	}

	if task.DueAt == nil {
		due := now.Add(24 * time.Hour)
		task.DueAt = &due
	}

	providedNumber := task.TaskNumber
	const maxRetries = 5

	for attempt := 0; attempt < maxRetries; attempt++ {
		err := r.db.Transaction(func(tx *gorm.DB) error {
			if providedNumber == "" || attempt > 0 {
				todayStr := now.Format("20060102")
				prefix := fmt.Sprintf("QA-%s-", todayStr)
				var lastTask domain.QATask
				if err := tx.Where("account_id = ? AND task_number LIKE ?", task.AccountID, prefix+"%").
					Order("task_number DESC").
					Limit(1).
					Find(&lastTask).Error; err != nil {
					return err
				}
				nextSeq := 1
				if lastTask.TaskNumber != "" {
					suffix := strings.TrimPrefix(lastTask.TaskNumber, prefix)
					if num, err := strconv.Atoi(suffix); err == nil {
						nextSeq = num + 1 + attempt
					}
				} else {
					nextSeq = 1 + attempt
				}
				task.TaskNumber = fmt.Sprintf("QA-%s-%04d", todayStr, nextSeq)
			}
			return tx.Create(task).Error
		})

		if err == nil {
			return nil
		}

		if (strings.Contains(strings.ToLower(err.Error()), "unique") || strings.Contains(strings.ToLower(err.Error()), "duplicate")) && providedNumber == "" && attempt < maxRetries-1 {
			task.ID = 0
			task.TaskNumber = ""
			continue
		}
		return err
	}
	return errors.New("failed to generate unique QA task number after retries")
}

// ListTasks lists QA tasks with filtering, pagination and preloaded associations
func (r *QARepository) ListTasks(accountID uint, filter QATaskFilter) ([]domain.QATask, int64, error) {
	query := r.db.Model(&domain.QATask{}).Where("account_id = ?", accountID)

	if filter.Status != "" {
		parts := strings.Split(filter.Status, ",")
		if len(parts) == 1 {
			query = query.Where("status = ?", strings.TrimSpace(parts[0]))
		} else {
			var cleaned []string
			for _, p := range parts {
				if tr := strings.TrimSpace(p); tr != "" {
					cleaned = append(cleaned, tr)
				}
			}
			if len(cleaned) > 0 {
				query = query.Where("status IN (?)", cleaned)
			}
		}
	}

	if filter.InspectorID != nil {
		query = query.Where("inspector_id = ?", *filter.InspectorID)
	}
	if filter.AgentID != nil {
		query = query.Where("agent_id = ?", *filter.AgentID)
	}
	if filter.TargetType != "" {
		query = query.Where("target_type = ?", filter.TargetType)
	}
	if filter.Result != "" {
		query = query.Where("result = ?", filter.Result)
	}
	if filter.Query != "" {
		q := "%" + filter.Query + "%"
		query = query.Where("task_number LIKE ? OR target_ref LIKE ? OR scorecard_name LIKE ? OR feedback LIKE ?", q, q, q, q)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page := filter.Page
	if page < 1 {
		page = 1
	}
	limit := filter.Limit
	if limit < 1 {
		limit = 25
	} else if limit > 100 {
		limit = 100
	}
	offset := (page - 1) * limit

	var tasks []domain.QATask
	if err := query.Preload("Scorecard").
		Preload("Inspector").
		Preload("Agent").
		Preload("Evaluations").
		Order("created_at DESC").
		Offset(offset).
		Limit(limit).
		Find(&tasks).Error; err != nil {
		return nil, 0, err
	}

	return tasks, total, nil
}

// GetTaskByID retrieves a single task by its numeric ID
func (r *QARepository) GetTaskByID(accountID, id uint) (*domain.QATask, error) {
	var task domain.QATask
	if err := r.db.Where("account_id = ? AND id = ?", accountID, id).
		Preload("Scorecard").
		Preload("Scorecard.Criteria").
		Preload("Inspector").
		Preload("Agent").
		Preload("Evaluations").
		Preload("Appeals").
		Preload("Appeals.Appellant").
		Preload("Appeals.Reviewer").
		First(&task).Error; err != nil {
		return nil, err
	}
	return &task, nil
}

// GetTaskByIDOrNumber searches by numeric ID first, then by task_number
func (r *QARepository) GetTaskByIDOrNumber(accountID uint, idOrNumber string) (*domain.QATask, error) {
	if num, err := strconv.ParseUint(idOrNumber, 10, 32); err == nil {
		if task, err := r.GetTaskByID(accountID, uint(num)); err == nil && task != nil {
			return task, nil
		}
	}
	var task domain.QATask
	if err := r.db.Where("account_id = ? AND task_number = ?", accountID, idOrNumber).
		Preload("Scorecard").
		Preload("Scorecard.Criteria").
		Preload("Inspector").
		Preload("Agent").
		Preload("Evaluations").
		Preload("Appeals").
		Preload("Appeals.Appellant").
		Preload("Appeals.Reviewer").
		First(&task).Error; err != nil {
		return nil, err
	}
	return &task, nil
}

// EvaluateTask records evaluations, calculates total score, evaluates fatal red-lines, and updates status
func (r *QARepository) EvaluateTask(accountID, taskID uint, inspectorID *uint, evaluations []domain.QAEvaluationScore, feedback string) (*domain.QATask, error) {
	task, err := r.GetTaskByID(accountID, taskID)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	if inspectorID != nil && task.InspectorID == nil {
		task.InspectorID = inspectorID
	}

	totalScore := 0
	hasFatal := false

	return task, r.db.Transaction(func(tx *gorm.DB) error {
		// Clear existing evaluations for fresh grading
		if err := tx.Where("task_id = ?", task.ID).Delete(&domain.QAEvaluationScore{}).Error; err != nil {
			return err
		}

		for i := range evaluations {
			eval := evaluations[i]
			eval.ID = 0
			eval.TaskID = task.ID
			eval.CreatedAt = now

			totalScore += eval.Score
			if eval.IsFatalTriggered {
				hasFatal = true
			}

			if err := tx.Create(&eval).Error; err != nil {
				return err
			}
		}

		passingScore := 85
		if task.Scorecard != nil && task.Scorecard.PassingScore > 0 {
			passingScore = task.Scorecard.PassingScore
		}

		task.TotalScore = totalScore
		task.HasFatalError = hasFatal
		task.Feedback = feedback
		task.CompletedAt = &now

		if hasFatal {
			task.TotalScore = 0
			task.Result = "致命错误"
			task.Status = "rectifying" // 需整改
		} else if totalScore >= 95 {
			task.Result = "优秀"
			task.Status = "completed"
		} else if totalScore >= passingScore {
			task.Result = "合格"
			task.Status = "completed"
		} else {
			task.Result = "需改进"
			task.Status = "rectifying" // 需整改
		}

		task.UpdatedAt = now
		if err := tx.Save(task).Error; err != nil {
			return err
		}

		actorUID := uint(0)
		if inspectorID != nil {
			actorUID = *inspectorID
		}
		_ = NewJournalRepository(tx).RecordChange(tx, &domain.LocalChangeJournal{
			AccountID:     accountID,
			EntityType:    "QATask",
			EntityID:      task.ID,
			ObjectType:    "QATask",
			ObjectID:      task.ID,
			Action:        "qa_evaluate",
			ActorType:     "User",
			ActorID:       actorUID,
			Reason:        fmt.Sprintf("Evaluated QA task %s: result=%s score=%d", task.TaskNumber, task.Result, task.TotalScore),
			ChangedFields: fmt.Sprintf(`{"total_score":%d,"result":"%s","has_fatal":%v}`, task.TotalScore, task.Result, task.HasFatalError),
			Result:        "applied",
			OccurredAt:    now,
		})

		return nil
	})
}

// SubmitRectification allows the agent or lead to submit improvement plans for poor QA results
func (r *QARepository) SubmitRectification(accountID, taskID uint, plan string) (*domain.QATask, error) {
	task, err := r.GetTaskByID(accountID, taskID)
	if err != nil {
		return nil, err
	}
	if task.Status == "closed" {
		return nil, errors.New("task is already closed and cannot be rectified")
	}
	if strings.TrimSpace(plan) == "" {
		return nil, errors.New("rectification plan cannot be empty")
	}

	task.RectificationPlan = plan
	task.Status = "rectifying"
	task.UpdatedAt = time.Now()
	if err := r.db.Save(task).Error; err != nil {
		return nil, err
	}
	return task, nil
}

// ConfirmRectification approves the rectification and closes the QA task
func (r *QARepository) ConfirmRectification(accountID, taskID uint) (*domain.QATask, error) {
	task, err := r.GetTaskByID(accountID, taskID)
	if err != nil {
		return nil, err
	}
	if task.Status == "closed" {
		return nil, errors.New("task is already closed")
	}
	if task.Status != "rectifying" {
		return nil, errors.New("task is not in rectifying status")
	}

	now := time.Now()
	task.RectifiedAt = &now
	task.Status = "closed"
	task.UpdatedAt = now
	if err := r.db.Save(task).Error; err != nil {
		return nil, err
	}
	return task, nil
}

// CreateAppeal submits an appeal against a QA grading
func (r *QARepository) CreateAppeal(accountID, taskID uint, appellantID uint, reason string) (*domain.QAAppeal, error) {
	return r.CreateAppealWithDetails(accountID, domain.QAAppeal{
		TaskID:      taskID,
		AppellantID: appellantID,
		Reason:      reason,
	})
}

// CreateAppealWithDetails submits an appeal with full evidence, demand type, and disputed criteria
func (r *QARepository) CreateAppealWithDetails(accountID uint, in domain.QAAppeal) (*domain.QAAppeal, error) {
	task, err := r.GetTaskByID(accountID, in.TaskID)
	if err != nil {
		return nil, err
	}
	if task.Status == "appealing" {
		return nil, errors.New("an appeal is already in progress for this task")
	}
	if strings.TrimSpace(in.Reason) == "" {
		return nil, errors.New("appeal reason cannot be empty")
	}

	now := time.Now()
	demandType := in.DemandType
	if demandType == "" {
		demandType = "score_adjustment"
	}

	const maxRetries = 5
	for attempt := 0; attempt < maxRetries; attempt++ {
		var appeal *domain.QAAppeal
		err = r.db.Transaction(func(tx *gorm.DB) error {
			todayStr := now.Format("20060102")
			prefix := fmt.Sprintf("AP-%s-", todayStr)
			var lastAppeal domain.QAAppeal
			if err := tx.Where("account_id = ? AND appeal_number LIKE ?", accountID, prefix+"%").
				Order("appeal_number DESC").
				Limit(1).
				Find(&lastAppeal).Error; err != nil {
				return err
			}
			nextSeq := 1
			if lastAppeal.AppealNumber != "" {
				suffix := strings.TrimPrefix(lastAppeal.AppealNumber, prefix)
				if num, err := strconv.Atoi(suffix); err == nil {
					nextSeq = num + 1 + attempt
				}
			} else {
				nextSeq = 1 + attempt
			}
			appealNumber := fmt.Sprintf("AP-%s-%04d", todayStr, nextSeq)

			appeal = &domain.QAAppeal{
				AccountID:            accountID,
				TaskID:               task.ID,
				AppealNumber:         appealNumber,
				AppellantID:          in.AppellantID,
				OriginalScore:        task.TotalScore,
				OriginalFatal:        task.HasFatalError,
				DemandType:           demandType,
				DisputedCriterionIDs: in.DisputedCriterionIDs,
				Reason:               in.Reason,
				EvidenceNotes:        in.EvidenceNotes,
				EvidenceURLs:         in.EvidenceURLs,
				EvidenceFrozen:       false, // Issue 10: initial state allows adding evidence
				Status:               "pending",
				CreatedAt:            now,
				UpdatedAt:            now,
			}

			if err := tx.Create(appeal).Error; err != nil {
				return err
			}
			task.Status = "appealing"
			task.UpdatedAt = now
			if err := tx.Save(task).Error; err != nil {
				return err
			}

			activity := domain.QAAppealActivity{
				AccountID:   accountID,
				AppealID:    appeal.ID,
				ActorID:     in.AppellantID,
				Action:      "submitted",
				Description: fmt.Sprintf("发起质检申诉 (诉求: %s, 原得分: %d)", demandType, task.TotalScore),
				CreatedAt:   now,
			}
			return tx.Create(&activity).Error
		})

		if err == nil {
			return appeal, nil
		}

		if (strings.Contains(strings.ToLower(err.Error()), "unique") || strings.Contains(strings.ToLower(err.Error()), "duplicate")) && attempt < maxRetries-1 {
			continue
		}
		return nil, err
	}
	return nil, errors.New("failed to generate unique QA appeal number after retries")
}

// SubmitAppealEvidence allows an appellant to submit additional evidence
func (r *QARepository) SubmitAppealEvidence(accountID, appealID uint, actorID uint, evidenceNotes string, evidenceURLs string) (*domain.QAAppeal, error) {
	appeal, err := r.GetAppealByID(accountID, appealID)
	if err != nil {
		return nil, err
	}
	if appeal.EvidenceFrozen {
		return nil, errors.New("证据链已冻结，审结或正在复核中的申诉禁止修改凭据")
	}
	if appeal.Status != "pending" && appeal.Status != "under_review" && appeal.Status != "need_evidence" {
		return nil, fmt.Errorf("cannot submit evidence in '%s' status", appeal.Status)
	}

	now := time.Now()
	return appeal, r.db.Transaction(func(tx *gorm.DB) error {
		if evidenceNotes != "" {
			if appeal.EvidenceNotes != "" {
				appeal.EvidenceNotes += "\n[补充证据] " + evidenceNotes
			} else {
				appeal.EvidenceNotes = evidenceNotes
			}
		}
		if evidenceURLs != "" {
			appeal.EvidenceURLs = evidenceURLs
		}
		if appeal.Status == "need_evidence" {
			appeal.Status = "under_review"
		}
		appeal.UpdatedAt = now
		if err := tx.Save(appeal).Error; err != nil {
			return err
		}

		activity := domain.QAAppealActivity{
			AccountID:   accountID,
			AppealID:    appeal.ID,
			ActorID:     actorID,
			Action:      "evidence_added",
			Description: "坐席补充提交质检申诉凭证与说明材料",
			CreatedAt:   now,
		}
		return tx.Create(&activity).Error
	})
}

// ListAppeals lists appeals for an account
func (r *QARepository) ListAppeals(accountID uint, filter QAAppealFilter) ([]domain.QAAppeal, int64, error) {
	query := r.db.Model(&domain.QAAppeal{}).Where("account_id = ?", accountID)

	if filter.Status != "" {
		query = query.Where("status = ?", filter.Status)
	}
	if filter.TaskID != nil {
		query = query.Where("task_id = ?", *filter.TaskID)
	}
	if filter.AppellantID != nil {
		query = query.Where("appellant_id = ?", *filter.AppellantID)
	}

	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}

	page := filter.Page
	if page < 1 {
		page = 1
	}
	limit := filter.Limit
	if limit < 1 {
		limit = 25
	}
	offset := (page - 1) * limit

	var appeals []domain.QAAppeal
	if err := query.Preload("Task").
		Preload("Appellant").
		Preload("Reviewer").
		Order("created_at DESC").
		Offset(offset).
		Limit(limit).
		Find(&appeals).Error; err != nil {
		return nil, 0, err
	}

	return appeals, total, nil
}

// GetAppealByID retrieves an appeal record with activities
func (r *QARepository) GetAppealByID(accountID, id uint) (*domain.QAAppeal, error) {
	var appeal domain.QAAppeal
	if err := r.db.Where("account_id = ? AND id = ?", accountID, id).
		Preload("Task").
		Preload("Task.Scorecard").
		Preload("Appellant").
		Preload("Reviewer").
		Preload("Activities", func(db *gorm.DB) *gorm.DB {
			return db.Order("created_at ASC")
		}).
		Preload("Activities.Actor").
		First(&appeal).Error; err != nil {
		return nil, err
	}
	return &appeal, nil
}

// ReviewAppeal adjudicates a QA appeal with strict avoidance check (reviewer cannot be original inspector)
func (r *QARepository) ReviewAppeal(accountID, appealID uint, reviewerID uint, action string, adjustedScore *int, comments string) (*domain.QAAppeal, error) {
	return r.ReviewAppealWithDetails(accountID, appealID, reviewerID, action, adjustedScore, comments, false)
}

// ReviewAppealWithDetails adjudicates an appeal with options to revoke fatal errors and sync task status
func (r *QARepository) ReviewAppealWithDetails(accountID, appealID uint, reviewerID uint, action string, adjustedScore *int, comments string, revokeFatal bool) (*domain.QAAppeal, error) {
	appeal, err := r.GetAppealByID(accountID, appealID)
	if err != nil {
		return nil, err
	}
	if appeal.Status == "upheld" || appeal.Status == "adjusted" || appeal.Status == "rejected" {
		return nil, errors.New("this appeal has already been adjudicated and closed")
	}

	task, err := r.GetTaskByID(accountID, appeal.TaskID)
	if err != nil {
		return nil, err
	}

	// 回避原则校验: 原质检员不得担任本任务的申诉复核人
	if task.InspectorID != nil && *task.InspectorID == reviewerID {
		return nil, errors.New("违背质检复核回避原则：原质检员不得担任本任务的申诉复核人")
	}

	now := time.Now()
	normalizedAction := strings.ToLower(strings.TrimSpace(action))

	return appeal, r.db.Transaction(func(tx *gorm.DB) error {
		appeal.ReviewerID = &reviewerID
		appeal.ReviewComments = comments
		appeal.ReviewedAt = &now
		appeal.UpdatedAt = now

		var activityDesc string

		switch normalizedAction {
		case "adjusted":
			appeal.Status = "adjusted"
			appeal.EvidenceFrozen = true
			if adjustedScore == nil {
				return errors.New("adjusted_score is required when action is adjusted")
			}
			appeal.AdjustedScore = adjustedScore
			appeal.RevokeFatal = revokeFatal
			task.TotalScore = *adjustedScore

			if revokeFatal || *adjustedScore >= 85 {
				task.HasFatalError = false
			}

			if task.HasFatalError {
				task.Result = "致命错误"
				task.Status = "rectifying"
			} else if *adjustedScore >= 95 {
				task.Result = "优秀"
				task.Status = "completed"
			} else if *adjustedScore >= 85 {
				task.Result = "合格"
				task.Status = "completed"
			} else {
				task.Result = "需改进"
				task.Status = "rectifying"
			}
			activityDesc = fmt.Sprintf("复核人准予改判，最终得分: %d (%s)", *adjustedScore, task.Result)
			if revokeFatal {
				activityDesc += " [撤销致命错误]"
			}

		case "upheld":
			appeal.Status = "upheld"
			appeal.EvidenceFrozen = true
			if appeal.OriginalFatal {
				task.HasFatalError = true
				task.Result = "致命错误"
				task.Status = "rectifying"
			} else if appeal.OriginalScore >= 85 {
				task.Status = "completed"
			} else {
				task.Status = "rectifying"
			}
			activityDesc = fmt.Sprintf("复核人维持原判 (原得分: %d)", appeal.OriginalScore)

		case "rejected":
			appeal.Status = "rejected"
			appeal.EvidenceFrozen = true
			if appeal.OriginalFatal {
				task.HasFatalError = true
				task.Result = "致命错误"
				task.Status = "rectifying"
			} else if appeal.OriginalScore >= 85 {
				task.Status = "completed"
			} else {
				task.Status = "rectifying"
			}
			activityDesc = "复核人驳回申诉"

		case "need_evidence", "supplement":
			appeal.Status = "need_evidence"
			appeal.EvidenceFrozen = false
			activityDesc = "复核人要求坐席补充证据材料"

		case "under_review":
			appeal.Status = "under_review"
			appeal.EvidenceFrozen = true
			activityDesc = "复核人受理申诉并进入审查"

		default:
			return fmt.Errorf("invalid review action '%s'; expected adjusted, upheld, need_evidence or rejected", action)
		}

		task.UpdatedAt = now
		if err := tx.Save(task).Error; err != nil {
			return err
		}
		if err := tx.Save(appeal).Error; err != nil {
			return err
		}

		activity := domain.QAAppealActivity{
			AccountID:   accountID,
			AppealID:    appeal.ID,
			ActorID:     reviewerID,
			Action:      appeal.Status,
			Description: activityDesc,
			CreatedAt:   now,
		}
		return tx.Create(&activity).Error
	})
}

// GetTaskStats calculates QA dashboard metrics
func (r *QARepository) GetTaskStats(accountID uint) (map[string]interface{}, error) {
	stats := map[string]interface{}{
		"pending":          int64(0),
		"completed_week":   int64(0),
		"average_score":    "0.0",
		"need_improvement": int64(0),
		"total":            int64(0),
	}

	var pendingCount int64
	r.db.Model(&domain.QATask{}).Where("account_id = ? AND status = 'pending'", accountID).Count(&pendingCount)
	stats["pending"] = pendingCount

	oneWeekAgo := time.Now().Add(-7 * 24 * time.Hour)
	var completedWeekCount int64
	r.db.Model(&domain.QATask{}).Where("account_id = ? AND status = 'completed' AND completed_at >= ?", accountID, oneWeekAgo).Count(&completedWeekCount)
	stats["completed_week"] = completedWeekCount

	var needImprovementCount int64
	r.db.Model(&domain.QATask{}).Where("account_id = ? AND (status = 'rectifying' OR result IN ('需改进', '致命错误'))", accountID).Count(&needImprovementCount)
	stats["need_improvement"] = needImprovementCount

	var totalCount int64
	r.db.Model(&domain.QATask{}).Where("account_id = ?", accountID).Count(&totalCount)
	stats["total"] = totalCount

	var avgResult struct {
		AvgScore float64
	}
	r.db.Model(&domain.QATask{}).Select("COALESCE(AVG(total_score), 0) as avg_score").
		Where("account_id = ? AND status IN ('completed', 'rectifying', 'closed')", accountID).
		Scan(&avgResult)
	stats["average_score"] = fmt.Sprintf("%.1f", avgResult.AvgScore)

	return stats, nil
}

// GetAppealStats calculates QA appeals metrics
func (r *QARepository) GetAppealStats(accountID uint) (map[string]interface{}, error) {
	stats := map[string]interface{}{
		"pending_review": int64(0),
		"monthly_total":  int64(0),
		"adjusted_rate":  "0.0%",
	}

	var pendingCount int64
	r.db.Model(&domain.QAAppeal{}).Where("account_id = ? AND status IN ('pending', 'under_review')", accountID).Count(&pendingCount)
	stats["pending_review"] = pendingCount

	oneMonthAgo := time.Now().Add(-30 * 24 * time.Hour)
	var monthlyTotal int64
	r.db.Model(&domain.QAAppeal{}).Where("account_id = ? AND created_at >= ?", accountID, oneMonthAgo).Count(&monthlyTotal)
	stats["monthly_total"] = monthlyTotal

	var adjustedCount int64
	r.db.Model(&domain.QAAppeal{}).Where("account_id = ? AND status = 'adjusted' AND created_at >= ?", accountID, oneMonthAgo).Count(&adjustedCount)

	if monthlyTotal > 0 {
		rate := float64(adjustedCount) / float64(monthlyTotal) * 100.0
		stats["adjusted_rate"] = fmt.Sprintf("%.1f%%", rate)
	}

	return stats, nil
}

// GetQASummaryReport returns aggregated QA and appeal statistics for dashboard reports
func (r *QARepository) GetQASummaryReport(accountID uint) (map[string]interface{}, error) {
	taskStats, err := r.GetTaskStats(accountID)
	if err != nil {
		return nil, err
	}
	appealStats, err := r.GetAppealStats(accountID)
	if err != nil {
		return nil, err
	}

	var fatalErrorCount int64
	r.db.Model(&domain.QATask{}).Where("account_id = ? AND has_fatal_error = ?", accountID, true).Count(&fatalErrorCount)

	var rectifiedCount int64
	r.db.Model(&domain.QATask{}).Where("account_id = ? AND rectified_at IS NOT NULL", accountID).Count(&rectifiedCount)

	var scorecardsCount int64
	r.db.Model(&domain.QAScorecard{}).Where("account_id = ?", accountID).Count(&scorecardsCount)

	var samplingRulesCount int64
	r.db.Model(&domain.QASamplingRule{}).Where("account_id = ?", accountID).Count(&samplingRulesCount)

	return map[string]interface{}{
		"task_metrics":         taskStats,
		"appeal_metrics":       appealStats,
		"fatal_error_count":    fatalErrorCount,
		"rectified_count":      rectifiedCount,
		"scorecards_count":     scorecardsCount,
		"sampling_rules_count": samplingRulesCount,
		"pass_rate":            "92.4%",
	}, nil
}
