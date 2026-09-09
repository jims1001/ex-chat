package service

import (
	"context"
	"errors"
	"fmt"
	"html"
	"os"
	"strings"
	"time"

	"github.com/OracleBetX-Projects/ex-chat/internal/domain"
	"gorm.io/gorm"
)

type EmailService struct {
	db        *gorm.DB
	sender    EmailSender
	fromEmail string
}

func NewEmailService(db *gorm.DB) *EmailService {
	fromEmail := os.Getenv("MAILER_SENDER_EMAIL")
	if fromEmail == "" {
		fromEmail = os.Getenv("SMTP_FROM_EMAIL")
	}
	if fromEmail == "" {
		fromEmail = "support@ex-chat.local"
	}

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
	} else {
		sender = NewMockEmailSender()
	}

	return &EmailService{
		db:        db,
		sender:    sender,
		fromEmail: fromEmail,
	}
}

func (s *EmailService) SetSender(sender EmailSender) {
	if sender != nil {
		s.sender = sender
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

// SendTranscript formats and sends conversation transcript email and records EmailLog audit
func (s *EmailService) SendTranscript(ctx context.Context, accountID uint, conv *domain.Conversation, messages []domain.Message, toEmail string) error {
	if toEmail == "" {
		return errors.New("recipient email is required")
	}
	if conv == nil {
		return errors.New("conversation is required")
	}

	displayID := conv.ID
	if conv.DisplayID > 0 {
		displayID = uint(conv.DisplayID)
	}

	subject := fmt.Sprintf("[#%d] 会话记录", displayID)
	htmlContent := s.RenderTranscriptHTML(conv, messages)
	textContent := s.RenderTranscriptText(conv, messages)

	var sendErr error
	if s.sender != nil {
		sendErr = s.sender.Send(ctx, s.fromEmail, toEmail, subject, htmlContent, textContent)
	}

	// Persist email audit log
	convID := conv.ID
	emailLog := domain.EmailLog{
		AccountID:      accountID,
		ConversationID: &convID,
		ToEmail:        toEmail,
		FromEmail:      s.fromEmail,
		Subject:        subject,
		ContentHTML:    htmlContent,
		ContentText:    textContent,
		Status:         "sent",
		CreatedAt:      time.Now(),
	}

	if sendErr != nil {
		emailLog.Status = "failed"
		emailLog.Error = sendErr.Error()
	}

	if s.db != nil {
		_ = s.db.Create(&emailLog).Error
	}

	return sendErr
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
