package mail

import (
	"context"
	"errors"
	"strings"
	"testing"

	"openbid/internal/models"
)

type staticSettingsLoader struct {
	settings models.EmailSettings
}

func (l staticSettingsLoader) GetEmailSettings(context.Context) (models.EmailSettings, error) {
	return l.settings, nil
}

type fakeTransport struct {
	calls int
	last  models.EmailMessage
	err   error
}

func (f *fakeTransport) Send(_ context.Context, _ models.EmailSettings, message models.EmailMessage) (models.EmailSendResult, error) {
	f.calls++
	f.last = message
	if f.err != nil {
		return models.EmailSendResult{}, f.err
	}
	return models.EmailSendResult{AcceptedRecipients: len(message.To), Message: "accepted"}, nil
}

func readySettings() models.EmailSettings {
	return models.EmailSettings{
		Enabled:          true,
		SMTPHost:         "smtp.example.org",
		SMTPPort:         587,
		SMTPSecurityMode: SecuritySTARTTLS,
		SMTPAuthRequired: true,
		SMTPUsername:     "openbid",
		SMTPPassword:     "app-password",
		SMTPFromEmail:    "alerts@example.org",
		TimeoutSeconds:   10,
	}
}

func TestSettingsReadinessRequiredAndOptionalFields(t *testing.T) {
	readiness := SettingsReadiness(models.EmailSettings{Enabled: true, SMTPAuthRequired: true})
	if readiness.Ready || readiness.Status != "partial" {
		t.Fatalf("expected partial unreadiness, got %#v", readiness)
	}
	for _, want := range []string{"SMTP host", "SMTP password or app password", "SMTP username", "Sender/from email address"} {
		if !contains(readiness.MissingFields, want) {
			t.Fatalf("expected missing field %q in %#v", want, readiness.MissingFields)
		}
	}
	ready := SettingsReadiness(readySettings())
	if !ready.Ready || ready.Status != "ready" || len(ready.MissingFields) != 0 || len(ready.InvalidFields) != 0 {
		t.Fatalf("expected ready settings, got %#v", ready)
	}
	invalidOptional := readySettings()
	invalidOptional.SMTPReplyTo = "not-an-email"
	if got := SettingsReadiness(invalidOptional); got.Ready || !contains(got.InvalidFields, "Reply-to address is invalid") {
		t.Fatalf("expected invalid optional reply-to to block readiness, got %#v", got)
	}
}

func TestServiceSendBlocksDisabledOrMissingConfig(t *testing.T) {
	settings := readySettings()
	settings.Enabled = false
	transport := &fakeTransport{}
	service := NewService(staticSettingsLoader{settings: settings}, transport)
	_, err := service.Send(t.Context(), models.EmailMessage{
		To:       []string{"ops@example.org"},
		Subject:  "Test",
		TextBody: "Hello",
	})
	if !errors.Is(err, ErrEmailConfig) {
		t.Fatalf("expected config error, got %v", err)
	}
	if transport.calls != 0 {
		t.Fatalf("disabled email should not call transport, got %d calls", transport.calls)
	}
}

func TestServiceSendUsesTransportAndDefaultsSender(t *testing.T) {
	transport := &fakeTransport{}
	service := NewService(staticSettingsLoader{settings: readySettings()}, transport)
	result, err := service.Send(t.Context(), models.EmailMessage{
		To:       []string{"ops@example.org"},
		Subject:  "Smart alert",
		TextBody: "A tender matched.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.AcceptedRecipients != 1 || transport.calls != 1 {
		t.Fatalf("expected one accepted recipient and one transport call, result=%#v calls=%d", result, transport.calls)
	}
	if transport.last.FromEmail != "alerts@example.org" {
		t.Fatalf("expected sender default from settings, got %#v", transport.last)
	}
}

func TestServiceSendValidatesRecipientsAndBody(t *testing.T) {
	service := NewService(staticSettingsLoader{settings: readySettings()}, &fakeTransport{})
	_, err := service.Send(t.Context(), models.EmailMessage{Subject: "No recipient", TextBody: "Hello"})
	if !errors.Is(err, ErrEmailConfig) || !strings.Contains(err.Error(), "recipient") {
		t.Fatalf("expected recipient validation error, got %v", err)
	}
	_, err = service.Send(t.Context(), models.EmailMessage{To: []string{"ops@example.org"}, Subject: "No body"})
	if !errors.Is(err, ErrEmailConfig) || !strings.Contains(err.Error(), "body") {
		t.Fatalf("expected body validation error, got %v", err)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
