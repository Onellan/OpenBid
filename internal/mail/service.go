package mail

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"net"
	netmail "net/mail"
	"net/smtp"
	"net/textproto"
	"sort"
	"strconv"
	"strings"
	"time"

	"openbid/internal/models"
)

const (
	SecurityTLS      = "tls"
	SecuritySTARTTLS = "starttls"
	SecurityPlain    = "plain"
)

var ErrEmailConfig = errors.New("email is not ready to send")

type SettingsLoader interface {
	GetEmailSettings(context.Context) (models.EmailSettings, error)
}

type Sender interface {
	Send(context.Context, models.EmailMessage) (models.EmailSendResult, error)
}

type Transport interface {
	Send(context.Context, models.EmailSettings, models.EmailMessage) (models.EmailSendResult, error)
}

type Service struct {
	loader    SettingsLoader
	transport Transport
}

type Readiness struct {
	Ready         bool
	Enabled       bool
	Status        string
	StatusLabel   string
	StatusTone    string
	Summary       string
	MissingFields []string
	InvalidFields []string
}

func NewService(loader SettingsLoader, transport Transport) *Service {
	if transport == nil {
		transport = SMTPTransport{}
	}
	return &Service{loader: loader, transport: transport}
}

func NormalizeSettings(settings models.EmailSettings) models.EmailSettings {
	settings.ID = "global"
	settings.SMTPHost = strings.TrimSpace(settings.SMTPHost)
	settings.SMTPSecurityMode = strings.ToLower(strings.TrimSpace(settings.SMTPSecurityMode))
	if settings.SMTPSecurityMode == "" {
		settings.SMTPSecurityMode = SecuritySTARTTLS
	}
	settings.SMTPUsername = strings.TrimSpace(settings.SMTPUsername)
	settings.SMTPFromEmail = strings.TrimSpace(settings.SMTPFromEmail)
	settings.SMTPFromName = strings.Join(strings.Fields(strings.TrimSpace(settings.SMTPFromName)), " ")
	settings.SMTPReplyTo = strings.TrimSpace(settings.SMTPReplyTo)
	settings.TestRecipient = strings.TrimSpace(settings.TestRecipient)
	if settings.SMTPPort <= 0 {
		settings.SMTPPort = 587
	}
	if settings.TimeoutSeconds <= 0 {
		settings.TimeoutSeconds = 10
	}
	if settings.TimeoutSeconds > 120 {
		settings.TimeoutSeconds = 120
	}
	return settings
}

func SettingsReadiness(settings models.EmailSettings) Readiness {
	settings = NormalizeSettings(settings)
	readiness := Readiness{
		Enabled:     settings.Enabled,
		Status:      "not_configured",
		StatusLabel: "Email not configured",
		StatusTone:  "warning",
		Summary:     "Outbound email is optional and is not ready to send yet.",
	}
	addMissing := func(label string) {
		readiness.MissingFields = append(readiness.MissingFields, label)
	}
	addInvalid := func(label string) {
		readiness.InvalidFields = append(readiness.InvalidFields, label)
	}
	if settings.SMTPHost == "" {
		addMissing("SMTP host")
	}
	if settings.SMTPPort <= 0 {
		addMissing("SMTP port")
	} else if settings.SMTPPort > 65535 {
		addInvalid("SMTP port must be between 1 and 65535")
	}
	switch settings.SMTPSecurityMode {
	case SecurityTLS, SecuritySTARTTLS, SecurityPlain:
	default:
		addInvalid("SMTP security mode must be TLS, STARTTLS, or plain")
	}
	if settings.SMTPFromEmail == "" {
		addMissing("Sender/from email address")
	} else if !validEmailAddress(settings.SMTPFromEmail) {
		addInvalid("Sender/from email address is invalid")
	}
	if settings.SMTPAuthRequired {
		if settings.SMTPUsername == "" {
			addMissing("SMTP username")
		}
		if strings.TrimSpace(settings.SMTPPassword) == "" {
			addMissing("SMTP password or app password")
		}
	}
	if settings.SMTPReplyTo != "" && !validEmailAddress(settings.SMTPReplyTo) {
		addInvalid("Reply-to address is invalid")
	}
	if settings.TestRecipient != "" && !validEmailAddress(settings.TestRecipient) {
		addInvalid("Default test recipient is invalid")
	}
	sort.Strings(readiness.MissingFields)
	sort.Strings(readiness.InvalidFields)
	hasRequired := len(readiness.MissingFields) == 0 && len(readiness.InvalidFields) == 0
	switch {
	case !settings.Enabled && !hasRequired:
		return readiness
	case !settings.Enabled:
		readiness.Status = "disabled"
		readiness.StatusLabel = "Email disabled"
		readiness.StatusTone = "info"
		readiness.Summary = "SMTP settings are complete, but global outbound email is turned off."
		return readiness
	case !hasRequired:
		readiness.Status = "partial"
		readiness.StatusLabel = "Email partially configured"
		readiness.StatusTone = "warning"
		readiness.Summary = "Complete the missing or invalid required fields before sending email."
		return readiness
	default:
		readiness.Ready = true
		readiness.Status = "ready"
		readiness.StatusLabel = "Email ready"
		readiness.StatusTone = "success"
		readiness.Summary = "OpenBid can send outbound email through the configured SMTP provider."
		return readiness
	}
}

func (s *Service) Readiness(ctx context.Context) (Readiness, models.EmailSettings, error) {
	if s == nil || s.loader == nil {
		return Readiness{Status: "not_configured", StatusLabel: "Email not configured", StatusTone: "warning"}, models.EmailSettings{}, nil
	}
	settings, err := s.loader.GetEmailSettings(ctx)
	if err != nil {
		return Readiness{}, models.EmailSettings{}, err
	}
	return SettingsReadiness(settings), settings, nil
}

func (s *Service) Send(ctx context.Context, message models.EmailMessage) (models.EmailSendResult, error) {
	if s == nil || s.loader == nil || s.transport == nil {
		return models.EmailSendResult{}, fmt.Errorf("%w: email service is unavailable", ErrEmailConfig)
	}
	settings, err := s.loader.GetEmailSettings(ctx)
	if err != nil {
		return models.EmailSendResult{}, err
	}
	settings = NormalizeSettings(settings)
	readiness := SettingsReadiness(settings)
	if !readiness.Ready {
		return models.EmailSendResult{}, fmt.Errorf("%w: %s", ErrEmailConfig, readinessFailureSummary(readiness))
	}
	message = normalizeMessage(settings, message)
	if err := validateMessage(message); err != nil {
		return models.EmailSendResult{}, err
	}
	result, err := s.transport.Send(ctx, settings, message)
	if err != nil {
		log.Printf("email send failed recipients=%d host=%s mode=%s error=%v", len(allRecipients(message)), settings.SMTPHost, settings.SMTPSecurityMode, err)
		return models.EmailSendResult{}, err
	}
	log.Printf("email send succeeded recipients=%d host=%s mode=%s", result.AcceptedRecipients, settings.SMTPHost, settings.SMTPSecurityMode)
	return result, nil
}

func readinessFailureSummary(readiness Readiness) string {
	parts := []string{}
	if len(readiness.MissingFields) > 0 {
		parts = append(parts, "missing "+strings.Join(readiness.MissingFields, ", "))
	}
	if len(readiness.InvalidFields) > 0 {
		parts = append(parts, "invalid "+strings.Join(readiness.InvalidFields, ", "))
	}
	if len(parts) == 0 {
		return readiness.StatusLabel
	}
	return strings.Join(parts, "; ")
}

func normalizeMessage(settings models.EmailSettings, message models.EmailMessage) models.EmailMessage {
	if strings.TrimSpace(message.FromEmail) == "" {
		message.FromEmail = settings.SMTPFromEmail
	}
	if strings.TrimSpace(message.FromName) == "" {
		message.FromName = settings.SMTPFromName
	}
	if strings.TrimSpace(message.ReplyTo) == "" {
		message.ReplyTo = settings.SMTPReplyTo
	}
	message.Subject = strings.TrimSpace(message.Subject)
	message.TextBody = strings.TrimSpace(message.TextBody)
	message.HTMLBody = strings.TrimSpace(message.HTMLBody)
	return message
}

func validateMessage(message models.EmailMessage) error {
	if len(allRecipients(message)) == 0 {
		return fmt.Errorf("%w: at least one recipient email address is required", ErrEmailConfig)
	}
	for _, address := range allRecipients(message) {
		if !validEmailAddress(address) {
			return fmt.Errorf("%w: recipient email address %q is invalid", ErrEmailConfig, address)
		}
	}
	if strings.TrimSpace(message.FromEmail) == "" || !validEmailAddress(message.FromEmail) {
		return fmt.Errorf("%w: sender/from email address is invalid", ErrEmailConfig)
	}
	if strings.TrimSpace(message.ReplyTo) != "" && !validEmailAddress(message.ReplyTo) {
		return fmt.Errorf("%w: reply-to email address is invalid", ErrEmailConfig)
	}
	if message.TextBody == "" && message.HTMLBody == "" {
		return fmt.Errorf("%w: email body must not be empty", ErrEmailConfig)
	}
	return nil
}

func validEmailAddress(address string) bool {
	parsed, err := netmail.ParseAddress(strings.TrimSpace(address))
	return err == nil && parsed.Address != ""
}

func allRecipients(message models.EmailMessage) []string {
	recipients := make([]string, 0, len(message.To)+len(message.CC)+len(message.BCC))
	for _, values := range [][]string{message.To, message.CC, message.BCC} {
		for _, value := range values {
			value = strings.TrimSpace(value)
			if value != "" {
				recipients = append(recipients, value)
			}
		}
	}
	return recipients
}

type SMTPTransport struct{}

func (SMTPTransport) Send(ctx context.Context, settings models.EmailSettings, message models.EmailMessage) (models.EmailSendResult, error) {
	settings = NormalizeSettings(settings)
	timeout := time.Duration(settings.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if err := ctx.Err(); err != nil {
		return models.EmailSendResult{}, err
	}
	addr := net.JoinHostPort(settings.SMTPHost, strconv.Itoa(settings.SMTPPort))
	dialer := &net.Dialer{Timeout: timeout}
	var conn net.Conn
	var err error
	if settings.SMTPSecurityMode == SecurityTLS {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: settings.SMTPHost, MinVersion: tls.VersionTLS12})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return models.EmailSendResult{}, fmt.Errorf("connect to SMTP server: %w", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(timeout))
	client, err := smtp.NewClient(conn, settings.SMTPHost)
	if err != nil {
		return models.EmailSendResult{}, fmt.Errorf("start SMTP session: %w", err)
	}
	defer client.Close()
	if settings.SMTPSecurityMode == SecuritySTARTTLS {
		ok, _ := client.Extension("STARTTLS")
		if !ok {
			return models.EmailSendResult{}, fmt.Errorf("SMTP server does not advertise STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{ServerName: settings.SMTPHost, MinVersion: tls.VersionTLS12}); err != nil {
			return models.EmailSendResult{}, fmt.Errorf("start TLS: %w", err)
		}
	}
	if settings.SMTPAuthRequired {
		auth := smtp.PlainAuth("", settings.SMTPUsername, settings.SMTPPassword, settings.SMTPHost)
		if err := client.Auth(auth); err != nil {
			return models.EmailSendResult{}, fmt.Errorf("SMTP authentication failed: %w", err)
		}
	}
	if err := client.Mail(strings.TrimSpace(message.FromEmail)); err != nil {
		return models.EmailSendResult{}, fmt.Errorf("set sender: %w", err)
	}
	recipients := allRecipients(message)
	for _, recipient := range recipients {
		if err := client.Rcpt(recipient); err != nil {
			return models.EmailSendResult{}, fmt.Errorf("add recipient %q: %w", recipient, err)
		}
	}
	writer, err := client.Data()
	if err != nil {
		return models.EmailSendResult{}, fmt.Errorf("open message body: %w", err)
	}
	if _, err := io.WriteString(writer, buildRFC5322Message(message)); err != nil {
		_ = writer.Close()
		return models.EmailSendResult{}, fmt.Errorf("write message body: %w", err)
	}
	if err := writer.Close(); err != nil {
		return models.EmailSendResult{}, fmt.Errorf("finish message body: %w", err)
	}
	if err := client.Quit(); err != nil {
		return models.EmailSendResult{}, fmt.Errorf("finish SMTP session: %w", err)
	}
	return models.EmailSendResult{AcceptedRecipients: len(recipients), Message: "Email accepted by SMTP server"}, nil
}

func buildRFC5322Message(message models.EmailMessage) string {
	var body strings.Builder
	writeHeader := func(key, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			body.WriteString(key)
			body.WriteString(": ")
			body.WriteString(value)
			body.WriteString("\r\n")
		}
	}
	writeHeader("From", formatAddress(message.FromEmail, message.FromName))
	writeHeader("To", strings.Join(message.To, ", "))
	if len(message.CC) > 0 {
		writeHeader("Cc", strings.Join(message.CC, ", "))
	}
	writeHeader("Reply-To", message.ReplyTo)
	writeHeader("Subject", mime.QEncoding.Encode("utf-8", message.Subject))
	writeHeader("Date", time.Now().Format(time.RFC1123Z))
	writeHeader("MIME-Version", "1.0")
	if message.HTMLBody != "" && message.TextBody != "" {
		var multipartBody strings.Builder
		writer := multipart.NewWriter(&multipartBody)
		textHeader := textproto.MIMEHeader{}
		textHeader.Set("Content-Type", `text/plain; charset="utf-8"`)
		textPart, _ := writer.CreatePart(textHeader)
		_, _ = io.WriteString(textPart, message.TextBody)
		htmlHeader := textproto.MIMEHeader{}
		htmlHeader.Set("Content-Type", `text/html; charset="utf-8"`)
		htmlPart, _ := writer.CreatePart(htmlHeader)
		_, _ = io.WriteString(htmlPart, message.HTMLBody)
		_ = writer.Close()
		writeHeader("Content-Type", `multipart/alternative; boundary="`+writer.Boundary()+`"`)
		body.WriteString("\r\n")
		body.WriteString(multipartBody.String())
		return body.String()
	}
	if message.HTMLBody != "" {
		writeHeader("Content-Type", `text/html; charset="utf-8"`)
		body.WriteString("\r\n")
		body.WriteString(message.HTMLBody)
		return body.String()
	}
	writeHeader("Content-Type", `text/plain; charset="utf-8"`)
	body.WriteString("\r\n")
	body.WriteString(message.TextBody)
	return body.String()
}

func formatAddress(address, name string) string {
	address = strings.TrimSpace(address)
	name = strings.TrimSpace(name)
	if name == "" {
		return address
	}
	return (&netmail.Address{Name: name, Address: address}).String()
}
