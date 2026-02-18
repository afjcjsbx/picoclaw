package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/smtp"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap"
	"github.com/sipeed/picoclaw/pkg/config"
)

type MockSMTPSender struct {
	LastAddr   string
	LastFrom   string
	LastTo     []string
	LastMsg    []byte
	ShouldFail bool
}

func (m *MockSMTPSender) SendMail(addr string, a smtp.Auth, from string, to []string, msg []byte) error {
	if m.ShouldFail {
		return errors.New("simulated smtp error")
	}
	m.LastAddr = addr
	m.LastFrom = from
	m.LastTo = to
	m.LastMsg = msg
	return nil
}

type MockIMAPClient struct {
	Messages   []*imap.Message
	SearchUIDs []uint32
	FailLogin  bool
	FailSelect bool
	FailFetch  bool
	FailSearch bool
}

func (m *MockIMAPClient) Login(username, password string) error {
	if m.FailLogin {
		return errors.New("login failed")
	}
	return nil
}

func (m *MockIMAPClient) Logout() error {
	return nil
}

func (m *MockIMAPClient) Select(mbox string, readonly bool) (*imap.MailboxStatus, error) {
	if m.FailSelect {
		return nil, errors.New("select failed")
	}
	return &imap.MailboxStatus{
		Name:     mbox,
		Messages: uint32(len(m.Messages)),
	}, nil
}

func (m *MockIMAPClient) Fetch(seqset *imap.SeqSet, items []imap.FetchItem, ch chan *imap.Message) error {
	if m.FailFetch {
		return errors.New("fetch failed")
	}

	for _, msg := range m.Messages {
		ch <- msg
	}

	return nil
}

func (m *MockIMAPClient) Search(criteria *imap.SearchCriteria) ([]uint32, error) {
	if m.FailSearch {
		return nil, errors.New("search failed")
	}
	return m.SearchUIDs, nil
}

// Helper to create fake IMAP messages with a readable body
func createMockMessage(uid uint32, subject, body string) *imap.Message {
	msg := &imap.Message{
		Uid: uid,
		Envelope: &imap.Envelope{
			Subject: subject,
			Date:    time.Now(),
			From:    []*imap.Address{{PersonalName: "Test User", MailboxName: "test", HostName: "example.com"}},
		},
		Body: make(map[*imap.BodySectionName]imap.Literal),
	}

	// Create a raw body in email format (headers + body)
	rawMail := fmt.Sprintf("Content-Type: text/plain\r\n\r\n%s", body)
	section := &imap.BodySectionName{}
	msg.Body[section] = bytes.NewBufferString(rawMail)

	return msg
}

func getTestConfig() config.EmailToolConfig {
	return config.EmailToolConfig{
		Enabled: true,
		Accounts: map[string]config.EmailAccountConfig{
			"default": {
				Username:   "user@test.com",
				Password:   "pass",
				SMTPServer: "smtp.test.com",
				SMTPPort:   587,
				IMAPServer: "imap.test.com",
				IMAPPort:   993,
			},
		},
	}
}

func createToolWithMocks(cfg config.EmailToolConfig) (*EmailTool, *MockSMTPSender, *MockIMAPClient) {
	mockSMTP := &MockSMTPSender{}
	mockIMAP := &MockIMAPClient{}

	tool := NewEmailTool(cfg)

	// Inject Mocks
	tool.smtpSender = mockSMTP

	// Inject Mock Connector
	tool.imapConnector = func(addr string) (IMAPClient, error) {
		return mockIMAP, nil
	}

	return tool, mockSMTP, mockIMAP
}

func TestEmailTool_Basics(t *testing.T) {
	tool, _, _ := createToolWithMocks(getTestConfig())

	if tool.Name() != "email" {
		t.Errorf("Expected name 'email', got %s", tool.Name())
	}

	// Test Disabled
	cfg := getTestConfig()
	cfg.Enabled = false
	toolDisabled, _, _ := createToolWithMocks(cfg)
	res := toolDisabled.Execute(context.Background(), map[string]interface{}{"action": "read"})
	if !res.IsError {
		t.Error("Expected error when disabled")
	}
}

func TestEmailTool_SendEmail(t *testing.T) {
	tool, mockSMTP, _ := createToolWithMocks(getTestConfig())
	ctx := context.Background()

	// Success
	args := map[string]interface{}{
		"action":  "send",
		"to":      "friend@example.com",
		"subject": "Hello",
		"body":    "World",
	}

	res := tool.Execute(ctx, args)
	if res.IsError {
		t.Errorf("Unexpected error: %v", res.ForLLM)
	}

	if mockSMTP.LastAddr != "smtp.test.com:587" {
		t.Errorf("Wrong SMTP address: %s", mockSMTP.LastAddr)
	}
	if !strings.Contains(string(mockSMTP.LastMsg), "Subject: Hello") {
		t.Errorf("Message body missing subject")
	}

	// Missing Args
	res = tool.Execute(ctx, map[string]interface{}{"action": "send"})
	if !res.IsError {
		t.Error("Expected error for missing args")
	}

	// SMTP Failure
	mockSMTP.ShouldFail = true
	res = tool.Execute(ctx, args)
	if !res.IsError {
		t.Error("Expected error when SMTP fails")
	}
}

func TestEmailTool_ReadEmails(t *testing.T) {
	tool, _, mockIMAP := createToolWithMocks(getTestConfig())
	ctx := context.Background()

	// Setup mock messages
	mockIMAP.Messages = []*imap.Message{
		createMockMessage(1, "Test 1", "Body 1"),
		createMockMessage(2, "Test 2", "Body 2"),
	}

	// Success Read
	args := map[string]interface{}{
		"action": "read",
		"limit":  2,
	}
	res := tool.Execute(ctx, args)
	if res.IsError {
		t.Errorf("Read failed: %v", res.ForLLM)
	}

	// Check output
	if !strings.Contains(res.ForLLM, "Subject: Test 1") {
		t.Error("Output missing subject 1")
	}
	if !strings.Contains(res.ForLLM, "Body 2") {
		t.Error("Output missing body 2")
	}

	// Empty Inbox
	mockIMAP.Messages = []*imap.Message{}
	res = tool.Execute(ctx, args)

	if res.IsError || !strings.Contains(res.ForLLM, "No messages") {
		t.Errorf("Expected 'No messages' in ForLLM, got: %s", res.ForLLM)
	}

	// Login Failure
	mockIMAP.FailLogin = true
	res = tool.Execute(ctx, args)
	if !res.IsError {
		t.Error("Expected error on login failure")
	}
}

func TestEmailTool_SearchEmails(t *testing.T) {
	tool, _, mockIMAP := createToolWithMocks(getTestConfig())
	ctx := context.Background()

	// Setup: Search returns UID 10, Fetch returns the message for UID 10
	mockIMAP.SearchUIDs = []uint32{10}
	mockIMAP.Messages = []*imap.Message{
		createMockMessage(10, "Found Me", "Hidden Content"),
	}

	// Success Search
	args := map[string]interface{}{
		"action": "search",
		"query":  "Found",
	}
	res := tool.Execute(ctx, args)
	if res.IsError {
		t.Errorf("Search failed: %v", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Hidden Content") {
		t.Error("Search did not return expected content")
	}

	// No Results
	mockIMAP.SearchUIDs = []uint32{}
	res = tool.Execute(ctx, args)
	if res.IsError || !strings.Contains(res.ForLLM, "No emails found") {
		t.Errorf("Expected 'No emails found' in ForLLM, got: %s", res.ForLLM)
	}
}

func TestEmailTool_ListAccounts(t *testing.T) {
	tool, _, _ := createToolWithMocks(getTestConfig())

	res := tool.Execute(context.Background(), map[string]interface{}{
		"action": "list_accounts",
	})

	if res.IsError {
		t.Error("List accounts failed")
	}

	if !strings.Contains(res.ForLLM, "default") {
		t.Error("List should contain 'default' account")
	}
}
