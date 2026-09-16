package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"github.com/OracleBetX-Projects/ex-chat/pkg/logger"
	"gorm.io/gorm"
)

var ErrSMTPNotConfigured = errors.New("smtp service is not configured")

type EmailService struct {
	db         *gorm.DB
	sender     EmailSender
	fromEmail  string
	configured bool
	allowMock  bool
}

type InboxSMTPConfig struct {
	Address  string `json:"smtp_address"`
	Host     string `json:"address"`
	Port     string `json:"smtp_port"`
	PortNum  int    `json:"port"`
	Username string `json:"smtp_username"`
	Login    string `json:"smtp_login"`
	Password string `json:"smtp_password"`
	Enabled  *bool  `json:"smtp_enabled"`
}

func NewEmailService(db *gorm.DB) *EmailService {
	fromEmail := os.Getenv("MAILER_SENDER_EMAIL")
	if fromEmail == "" {
		fromEmail = os.Getenv("SMTP_FROM_EMAIL")
	}
	if fromEmail == "" {
		fromEmail = "support@ex-chat.local"
	}

	allowMock := os.Getenv("ENV") == "test" || os.Getenv("APP_ENV") == "test" || os.Getenv("ALLOW_MOCK_EMAIL") == "true"
	configured := false
	var sender EmailSender

	smtpHost := os.Getenv("SMTP_ADDRESS")
	if smtpHost != "" {
		smtpPort := os.Getenv("SMTP_PORT")
		if smtpPort == "" {
			smtpPort = "587"
		}
		sender = NewSMTPEmailSender(
			smtpHost,
			smtpPort,
			os.Getenv("SMTP_USERNAME"),
			os.Getenv("SMTP_PASSWORD"),
		)
		configured = true
	} else if allowMock {
		sender = NewMockEmailSender()
	}

	return &EmailService{
		db:         db,
		sender:     sender,
		fromEmail:  fromEmail,
		configured: configured,
		allowMock:  allowMock,
	}
}

func (s *EmailService) SetAllowMock(allow bool) {
	s.allowMock = allow
	if allow && s.sender == nil {
		s.sender = NewMockEmailSender()
	}
}

func (s *EmailService) IsAllowMock() bool {
	return s.allowMock
}

func (s *EmailService) IsConfigured() bool {
	if s.configured && s.sender != nil {
		return true
	}
	if s.allowMock && s.sender != nil {
		return true
	}
	return false
}

func (s *EmailService) SetSender(sender EmailSender) {
	if sender != nil {
		s.sender = sender
		if _, isMock := sender.(*MockEmailSender); isMock {
			s.allowMock = true
		} else {
			s.configured = true
		}
	} else {
		s.sender = nil
		s.configured = false
		s.allowMock = false
	}
}

func (s *EmailService) GetSender() EmailSender {
	return s.sender
}

func (s *EmailService) SetFromEmail(from string) {
	if from != "" {
		s.fromEmail = from
	}
}

func (s *EmailService) GetFromEmail() string {
	return s.fromEmail
}

func (s *EmailService) getSenderForConversation(conv *domain.Conversation) (EmailSender, string) {
	// If inbox has channel-level SMTP configuration in ProviderConfig, prioritize that
	if conv != nil && conv.Inbox != nil && conv.Inbox.ProviderConfig != "" {
		var cfg InboxSMTPConfig
		if err := json.Unmarshal([]byte(conv.Inbox.ProviderConfig), &cfg); err == nil {
			host := cfg.Address
			if host == "" {
				host = cfg.Host
			}
			if host != "" && (cfg.Enabled == nil || *cfg.Enabled) {
				port := cfg.Port
				if port == "" && cfg.PortNum > 0 {
					port = strconv.Itoa(cfg.PortNum)
				}
				if port == "" {
					port = "587"
				}
				user := cfg.Username
				if user == "" {
					user = cfg.Login
				}
				from := s.fromEmail
				if user != "" && strings.Contains(user, "@") {
					from = user
				}
				return NewSMTPEmailSender(host, port, user, cfg.Password), from
			}
		}
	}

	if s.sender != nil {
		return s.sender, s.fromEmail
	}
	return nil, s.fromEmail
}

// SendTranscript formats and sends conversation transcript email and records EmailLog audit
func (s *EmailService) SendTranscript(ctx context.Context, accountID uint, conv *domain.Conversation, messages []domain.Message, toEmail string) error {
	log := logger.WithComponent("email")
	if toEmail == "" {
		log.Warn("transcript email skipped: recipient email is required", "account_id", accountID)
		return errors.New("recipient email is required")
	}
	if conv == nil {
		log.Warn("transcript email skipped: conversation is required", "account_id", accountID)
		return errors.New("conversation is required")
	}

	displayID := conv.ID
	if conv.DisplayID > 0 {
		displayID = uint(conv.DisplayID)
	}

	subject := fmt.Sprintf("[#%d] 会话记录", displayID)
	htmlContent := s.RenderTranscriptHTML(conv, messages)
	textContent := s.RenderTranscriptText(conv, messages)

	sender, fromEmail := s.getSenderForConversation(conv)
	isConfigured := s.IsConfigured() || (sender != nil && sender != s.sender)

	// Persist email audit log helper
	logEmailAudit := func(status, errMsg string) {
		convID := conv.ID
		now := time.Now()
		deliveryStatus := domain.EmailDeliveryStatusSent
		var nextRetry *time.Time
		if status == "failed" {
			deliveryStatus = domain.EmailDeliveryStatusFailed
			retryTime := now.Add(5 * time.Minute)
			nextRetry = &retryTime
		}
		emailLog := domain.EmailLog{
			AccountID:      accountID,
			ConversationID: &convID,
			EmailType:      "transcript",
			ToEmail:        toEmail,
			FromEmail:      fromEmail,
			Subject:        subject,
			ContentHTML:    htmlContent,
			ContentText:    textContent,
			Status:         status,
			DeliveryStatus: deliveryStatus,
			RetryCount:     0,
			MaxRetries:     3,
			NextRetryAt:    nextRetry,
			LastAttemptAt:  &now,
			Error:          errMsg,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if s.db != nil {
			_ = s.db.Create(&emailLog).Error
		}
	}

	if sender == nil || !isConfigured {
		log.Warn("transcript email rejected: smtp service is not configured",
			"account_id", accountID,
			"conversation_id", conv.ID,
			"to_email", toEmail,
		)
		logEmailAudit("failed", "smtp service is not configured")
		return ErrSMTPNotConfigured
	}

	log.Info("sending conversation transcript email",
		"account_id", accountID,
		"conversation_id", conv.ID,
		"to_email", toEmail,
		"message_count", len(messages),
	)

	sendErr := sender.Send(ctx, fromEmail, toEmail, subject, htmlContent, textContent)
	if sendErr != nil {
		log.Error("failed to deliver transcript email",
			"account_id", accountID,
			"conversation_id", conv.ID,
			"to_email", toEmail,
			"error", sendErr.Error(),
		)
		logEmailAudit("failed", sendErr.Error())
		return sendErr
	}

	log.Info("transcript email delivered successfully",
		"account_id", accountID,
		"conversation_id", conv.ID,
		"to_email", toEmail,
	)
	logEmailAudit("sent", "")
	return nil
}

// RenderTranscriptHTML creates a clean, responsive HTML email representation of conversation
func (s *EmailService) RenderTranscriptHTML(conv *domain.Conversation, messages []domain.Message) string {
	displayID := conv.ID
	if conv.DisplayID > 0 {
		displayID = uint(conv.DisplayID)
	}

	contactName := "访客"
	if conv.Contact != nil {
		if conv.Contact.Name != "" {
			contactName = conv.Contact.Name
		} else if conv.Contact.Email != "" {
			contactName = conv.Contact.Email
		}
	}

	inboxName := "在线渠道"
	if conv.Inbox != nil && conv.Inbox.Name != "" {
		inboxName = conv.Inbox.Name
	}

	// Check branded layout header color if available
	headerColor := "#1f93ff"
	logoHTML := ""
	if s.db != nil && conv.AccountID > 0 {
		var branded domain.BrandedEmailLayout
		if err := s.db.Where("account_id = ?", conv.AccountID).First(&branded).Error; err == nil {
			if branded.HeaderColor != "" {
				headerColor = branded.HeaderColor
			}
			if branded.LogoURL != "" {
				logoHTML = fmt.Sprintf(`<img src="%s" alt="Logo" style="max-height: 36px; margin-bottom: 8px; display: block;" />`, html.EscapeString(branded.LogoURL))
			}
		}
	}

	var sb strings.Builder
	sb.WriteString("<!DOCTYPE html><html><head><meta charset=\"UTF-8\"/><style>")
	sb.WriteString("body { font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Helvetica, Arial, sans-serif; margin: 0; padding: 20px; background-color: #f7f9fa; color: #1f2d3d; }")
	sb.WriteString(".container { max-width: 640px; margin: 0 auto; background: #ffffff; border-radius: 8px; overflow: hidden; box-shadow: 0 1px 3px rgba(0,0,0,0.1); border: 1px solid #e1e4e8; }")
	sb.WriteString(fmt.Sprintf(".header { background: %s; color: #ffffff; padding: 20px; }", headerColor))
	sb.WriteString(".header h2 { margin: 0 0 6px 0; font-size: 18px; font-weight: 600; }")
	sb.WriteString(".header .meta { font-size: 13px; opacity: 0.9; }")
	sb.WriteString(".content { padding: 20px; }")
	sb.WriteString(".message { margin-bottom: 16px; display: flex; flex-direction: column; }")
	sb.WriteString(".sender-info { font-size: 12px; color: #64748b; margin-bottom: 4px; font-weight: 600; }")
	sb.WriteString(".bubble { display: inline-block; padding: 10px 14px; border-radius: 8px; font-size: 14px; line-height: 1.5; word-break: break-word; }")
	sb.WriteString(".incoming .bubble { background-color: #f1f5f9; color: #0f172a; align-self: flex-start; }")
	sb.WriteString(".outgoing .bubble { background-color: #e0f2fe; color: #0369a1; align-self: flex-start; }")
	sb.WriteString(".activity .bubble { background-color: #f8fafc; color: #64748b; font-style: italic; font-size: 12px; border-left: 3px solid #cbd5e1; }")
	sb.WriteString(".time { font-size: 11px; color: #94a3b8; margin-top: 4px; }")
	sb.WriteString(".footer { padding: 16px 20px; font-size: 12px; color: #94a3b8; border-top: 1px solid #f1f5f9; text-align: center; }")
	sb.WriteString("</style></head><body>")

	sb.WriteString("<div class=\"container\">")
	sb.WriteString("<div class=\"header\">")
	if logoHTML != "" {
		sb.WriteString(logoHTML)
	}
	sb.WriteString(fmt.Sprintf("<h2>会话记录 #%d</h2>", displayID))
	sb.WriteString(fmt.Sprintf("<div class=\"meta\">渠道: %s | 客户: %s | 时间: %s</div>",
		html.EscapeString(inboxName),
		html.EscapeString(contactName),
		conv.CreatedAt.Format("2006-01-02 15:04:05"),
	))
	sb.WriteString("</div>")

	sb.WriteString("<div class=\"content\">")
	if len(messages) == 0 {
		sb.WriteString("<div style=\"color: #94a3b8; text-align: center; padding: 20px;\">暂无消息记录</div>")
	}

	for _, msg := range messages {
		msgClass := "outgoing"
		if msg.MessageType == domain.MessageTypeIncoming {
			msgClass = "incoming"
		} else if msg.MessageType == domain.MessageTypeActivity {
			msgClass = "activity"
		}

		senderName := getMessageSenderName(&msg, contactName)
		if msg.Private {
			senderName += " (内部备注)"
		}

		timeStr := msg.CreatedAt.Format("2006-01-02 15:04:05")
		escapedContent := html.EscapeString(msg.Content)
		formattedContent := strings.ReplaceAll(escapedContent, "\n", "<br/>")

		sb.WriteString(fmt.Sprintf("<div class=\"message %s\">", msgClass))
		sb.WriteString(fmt.Sprintf("<div class=\"sender-info\">%s</div>", html.EscapeString(senderName)))
		sb.WriteString(fmt.Sprintf("<div class=\"bubble\">%s</div>", formattedContent))
		sb.WriteString(fmt.Sprintf("<div class=\"time\">%s</div>", timeStr))
		sb.WriteString("</div>")
	}
	sb.WriteString("</div>")

	sb.WriteString("<div class=\"footer\">")
	sb.WriteString(fmt.Sprintf("此邮件为 ex-chat 客户服务系统自动生成的会话备份，发送时间: %s", time.Now().Format("2006-01-02 15:04:05")))
	sb.WriteString("</div>")
	sb.WriteString("</div></body></html>")

	return sb.String()
}

// RenderTranscriptText generates clean plain-text format of conversation transcript
func (s *EmailService) RenderTranscriptText(conv *domain.Conversation, messages []domain.Message) string {
	displayID := conv.ID
	if conv.DisplayID > 0 {
		displayID = uint(conv.DisplayID)
	}

	contactName := "访客"
	if conv.Contact != nil {
		if conv.Contact.Name != "" {
			contactName = conv.Contact.Name
		} else if conv.Contact.Email != "" {
			contactName = conv.Contact.Email
		}
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("=== 会话记录 #%d ===\n", displayID))
	sb.WriteString(fmt.Sprintf("客户: %s\n", contactName))
	sb.WriteString(fmt.Sprintf("创建时间: %s\n", conv.CreatedAt.Format("2006-01-02 15:04:05")))
	sb.WriteString("========================\n\n")

	for _, msg := range messages {
		senderName := getMessageSenderName(&msg, contactName)
		if msg.Private {
			senderName += " (内部备注)"
		}

		timeStr := msg.CreatedAt.Format("2006-01-02 15:04:05")
		sb.WriteString(fmt.Sprintf("[%s] %s:\n%s\n\n", timeStr, senderName, msg.Content))
	}

	sb.WriteString("------------------------\n")
	sb.WriteString("此邮件由 ex-chat 客户服务系统自动发送\n")

	return sb.String()
}

func getMessageSenderName(msg *domain.Message, defaultContactName string) string {
	if msg.MessageType == domain.MessageTypeIncoming {
		return defaultContactName
	}
	if msg.MessageType == domain.MessageTypeActivity {
		return "系统提示"
	}
	if msg.Sender != nil {
		switch s := msg.Sender.(type) {
		case domain.User:
			if s.Name != "" {
				return s.Name
			}
		case *domain.User:
			if s != nil && s.Name != "" {
				return s.Name
			}
		case domain.Contact:
			if s.Name != "" {
				return s.Name
			}
		case *domain.Contact:
			if s != nil && s.Name != "" {
				return s.Name
			}
		}
	}
	if msg.SenderType == domain.SenderTypeUser {
		return "客服坐席"
	}
	return "客服"
}

// SendConfirmationEmail formats and sends user account confirmation email and tracks audit status
func (s *EmailService) SendConfirmationEmail(ctx context.Context, user *domain.User, confirmationToken string) (*domain.EmailLog, error) {
	log := logger.WithComponent("email")
	if user == nil || user.Email == "" {
		return nil, errors.New("user and email are required")
	}

	appURL := os.Getenv("FRONTEND_URL")
	if appURL == "" {
		appURL = os.Getenv("APP_URL")
	}
	if appURL == "" {
		appURL = "http://localhost:3000"
	}
	confirmLink := fmt.Sprintf("%s/auth/confirmation?confirmation_token=%s", strings.TrimRight(appURL, "/"), confirmationToken)

	subject := "欢迎加入 ExChat - 请验证您的电子邮箱"
	htmlBody := fmt.Sprintf(`<!DOCTYPE html><html><head><meta charset="UTF-8"/></head><body style="font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif; background-color: #f7f9fa; padding: 24px; color: #1f2d3d;">
<div style="max-width: 560px; margin: 0 auto; background: #ffffff; border-radius: 8px; border: 1px solid #e2e8f0; padding: 32px;">
<h2 style="margin-top: 0; color: #0f172a;">欢迎加入 ExChat 客服工作台</h2>
<p style="font-size: 15px; line-height: 1.6; color: #334155;">尊敬的 <strong>%s</strong>：</p>
<p style="font-size: 15px; line-height: 1.6; color: #334155;">感谢您注册 ExChat。请点击下方按钮完成邮箱验证并激活您的客服管理账户：</p>
<div style="margin: 28px 0; text-align: center;">
<a href="%s" style="background-color: #1f93ff; color: #ffffff; padding: 12px 28px; text-decoration: none; border-radius: 6px; font-weight: 600; display: inline-block;">立即验证我的邮箱</a>
</div>
<p style="font-size: 13px; color: #64748b; line-height: 1.5;">若上方按钮无法点击，请复制以下链接至浏览器地址栏访问：<br/><a href="%s" style="color: #1f93ff; word-break: break-all;">%s</a></p>
<hr style="border: none; border-top: 1px solid #e2e8f0; margin: 24px 0;"/>
<p style="font-size: 12px; color: #94a3b8; margin-bottom: 0;">此邮件由 ExChat 系统自动发出，如果您未曾注册，请忽略本邮件。</p>
</div></body></html>`, html.EscapeString(user.Name), confirmLink, confirmLink, confirmLink)

	textBody := fmt.Sprintf("欢迎加入 ExChat！\n\n尊敬的 %s：\n请访问以下链接完成电子邮箱验证以激活账户：\n%s\n\n如果这不是您的操作，请忽略此邮件。", user.Name, confirmLink)

	now := time.Now()
	emailLog := domain.EmailLog{
		AccountID:      0, // User signup may not have primary accountID yet
		UserID:         &user.ID,
		EmailType:      "confirmation",
		ToEmail:        user.Email,
		FromEmail:      s.fromEmail,
		Subject:        subject,
		ContentHTML:    htmlBody,
		ContentText:    textBody,
		Status:         "pending",
		DeliveryStatus: domain.EmailDeliveryStatusPending,
		RetryCount:     0,
		MaxRetries:     3,
		LastAttemptAt:  &now,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	if s.db != nil {
		_ = s.db.Create(&emailLog).Error
	}

	sender := s.sender
	isConfigured := s.IsConfigured()

	if sender == nil || !isConfigured {
		errMsg := "smtp service is not configured"
		log.Warn("confirmation email rejected: smtp not configured", "user_id", user.ID, "email", user.Email)
		emailLog.Status = "failed"
		emailLog.DeliveryStatus = domain.EmailDeliveryStatusFailed
		emailLog.Error = errMsg
		retryTime := now.Add(5 * time.Minute)
		emailLog.NextRetryAt = &retryTime
		if s.db != nil && emailLog.ID > 0 {
			s.db.Save(&emailLog)
		}
		return &emailLog, ErrSMTPNotConfigured
	}

	sendErr := sender.Send(ctx, s.fromEmail, user.Email, subject, htmlBody, textBody)
	if sendErr != nil {
		log.Error("failed to deliver confirmation email", "user_id", user.ID, "email", user.Email, "error", sendErr.Error())
		emailLog.Status = "failed"
		emailLog.DeliveryStatus = domain.EmailDeliveryStatusFailed
		emailLog.Error = sendErr.Error()
		retryTime := now.Add(5 * time.Minute)
		emailLog.NextRetryAt = &retryTime
		if s.db != nil && emailLog.ID > 0 {
			s.db.Save(&emailLog)
		}
		return &emailLog, sendErr
	}

	emailLog.Status = "sent"
	emailLog.DeliveryStatus = domain.EmailDeliveryStatusSent
	emailLog.Error = ""
	emailLog.NextRetryAt = nil
	if s.db != nil && emailLog.ID > 0 {
		s.db.Save(&emailLog)
	}

	log.Info("confirmation email sent successfully", "user_id", user.ID, "email", user.Email)
	return &emailLog, nil
}

// RecordBounce registers an email bounce event (hard/soft bounce, spam complaint) and updates status
func (s *EmailService) RecordBounce(emailLogID uint, bounceReason string) (*domain.EmailLog, error) {
	if s.db == nil {
		return nil, errors.New("database not available")
	}

	var emailLog domain.EmailLog
	if err := s.db.First(&emailLog, emailLogID).Error; err != nil {
		return nil, fmt.Errorf("email log %d not found: %w", emailLogID, err)
	}

	now := time.Now()
	emailLog.Status = "bounced"
	emailLog.DeliveryStatus = domain.EmailDeliveryStatusBounced
	emailLog.BounceReason = bounceReason
	emailLog.NextRetryAt = nil // Do not retry bounced emails
	emailLog.UpdatedAt = now

	if err := s.db.Save(&emailLog).Error; err != nil {
		return nil, fmt.Errorf("failed to update bounced email log: %w", err)
	}

	logger.WithComponent("email").Warn("recorded email bounce",
		"email_log_id", emailLogID,
		"to_email", emailLog.ToEmail,
		"bounce_reason", bounceReason,
	)

	return &emailLog, nil
}

// RetryFailedEmails finds pending/failed emails eligible for retry and reattempts dispatch
func (s *EmailService) RetryFailedEmails(ctx context.Context, limit int) (int, error) {
	if s.db == nil {
		return 0, nil
	}
	if !s.IsConfigured() {
		return 0, ErrSMTPNotConfigured
	}

	if limit <= 0 || limit > 100 {
		limit = 50
	}

	now := time.Now()
	var pendingLogs []domain.EmailLog
	err := s.db.Where("status = ? AND retry_count < max_retries AND (next_retry_at IS NULL OR next_retry_at <= ?)",
		domain.EmailDeliveryStatusFailed, now).
		Order("id ASC").
		Limit(limit).
		Find(&pendingLogs).Error

	if err != nil {
		return 0, fmt.Errorf("failed to fetch retryable emails: %w", err)
	}

	if len(pendingLogs) == 0 {
		return 0, nil
	}

	retriedCount := 0
	for _, l := range pendingLogs {
		logItem := l
		logItem.RetryCount++
		logItem.LastAttemptAt = &now
		logItem.UpdatedAt = now

		sender := s.sender
		fromEmail := logItem.FromEmail
		if fromEmail == "" {
			fromEmail = s.fromEmail
		}

		sendErr := sender.Send(ctx, fromEmail, logItem.ToEmail, logItem.Subject, logItem.ContentHTML, logItem.ContentText)
		if sendErr != nil {
			logItem.Error = sendErr.Error()
			if logItem.RetryCount >= logItem.MaxRetries {
				logItem.Status = "failed"
				logItem.DeliveryStatus = domain.EmailDeliveryStatusFailed
				logItem.NextRetryAt = nil // Exhausted all retries
			} else {
				// Exponential backoff: 5m, 15m, 45m
				backoffMinutes := 5 * (1 << (logItem.RetryCount - 1))
				nextTime := now.Add(time.Duration(backoffMinutes) * time.Minute)
				logItem.NextRetryAt = &nextTime
			}
			logger.WithComponent("email").Error("retry failed email delivery failed",
				"email_log_id", logItem.ID,
				"retry_count", logItem.RetryCount,
				"error", sendErr.Error(),
			)
		} else {
			logItem.Status = "sent"
			logItem.DeliveryStatus = domain.EmailDeliveryStatusSent
			logItem.Error = ""
			logItem.NextRetryAt = nil
			retriedCount++
			logger.WithComponent("email").Info("retry failed email delivered successfully",
				"email_log_id", logItem.ID,
				"retry_count", logItem.RetryCount,
				"to_email", logItem.ToEmail,
			)
		}

		_ = s.db.Save(&logItem).Error
	}

	return retriedCount, nil
}

// StartEmailRetryWorker launches an asynchronous background job to periodically retry failed emails
func (s *EmailService) StartEmailRetryWorker(ctx context.Context, interval time.Duration) {
	if interval < 10*time.Second {
		interval = 1 * time.Minute
	}
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if s.IsConfigured() {
					_, _ = s.RetryFailedEmails(ctx, 20)
				}
			}
		}
	}()
}
