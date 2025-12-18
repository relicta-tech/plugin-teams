// Package main contains tests for the Teams plugin.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/relicta-tech/relicta-plugin-sdk/plugin"
)

// MockHTTPClient implements HTTPClient for testing.
type MockHTTPClient struct {
	DoFunc func(req *http.Request) (*http.Response, error)
}

// Do implements the HTTPClient interface.
func (m *MockHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if m.DoFunc != nil {
		return m.DoFunc(req)
	}
	return nil, errors.New("DoFunc not set")
}

func TestGetInfo(t *testing.T) {
	t.Parallel()

	p := &TeamsPlugin{}
	info := p.GetInfo()

	tests := []struct {
		name     string
		got      string
		expected string
	}{
		{
			name:     "plugin name",
			got:      info.Name,
			expected: "teams",
		},
		{
			name:     "plugin version",
			got:      info.Version,
			expected: "2.0.0",
		},
		{
			name:     "plugin description",
			got:      info.Description,
			expected: "Send release notifications to Microsoft Teams",
		},
		{
			name:     "plugin author",
			got:      info.Author,
			expected: "Relicta Team",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, tc.got)
			}
		})
	}

	// Verify hooks
	t.Run("hooks contains expected hooks", func(t *testing.T) {
		expectedHooks := []plugin.Hook{
			plugin.HookPostPublish,
			plugin.HookOnSuccess,
			plugin.HookOnError,
		}

		if len(info.Hooks) != len(expectedHooks) {
			t.Errorf("expected %d hooks, got %d", len(expectedHooks), len(info.Hooks))
			return
		}

		for i, h := range expectedHooks {
			if info.Hooks[i] != h {
				t.Errorf("expected hook[%d] to be %q, got %q", i, h, info.Hooks[i])
			}
		}
	})

	// Verify config schema is valid JSON
	t.Run("config schema is valid JSON", func(t *testing.T) {
		if info.ConfigSchema == "" {
			t.Error("config schema should not be empty")
			return
		}

		var schema map[string]any
		if err := json.Unmarshal([]byte(info.ConfigSchema), &schema); err != nil {
			t.Errorf("config schema is not valid JSON: %v", err)
		}
	})
}

func TestValidate(t *testing.T) {
	t.Parallel()

	// Clear any environment variable that might interfere
	origEnv := os.Getenv("TEAMS_WEBHOOK_URL")
	_ = os.Unsetenv("TEAMS_WEBHOOK_URL")
	t.Cleanup(func() {
		if origEnv != "" {
			_ = os.Setenv("TEAMS_WEBHOOK_URL", origEnv)
		}
	})

	tests := []struct {
		name        string
		config      map[string]any
		envWebhook  string
		wantValid   bool
		wantErrCode string
		wantErrMsg  string
	}{
		{
			name:        "missing_webhook",
			config:      map[string]any{},
			wantValid:   false,
			wantErrCode: "required",
			wantErrMsg:  "required",
		},
		{
			name: "empty_webhook",
			config: map[string]any{
				"webhook_url": "",
			},
			wantValid:   false,
			wantErrCode: "required",
		},
		{
			name: "invalid_url",
			config: map[string]any{
				"webhook_url": "not-a-url",
			},
			wantValid:   false,
			wantErrCode: "format",
		},
		{
			name: "http_not_https",
			config: map[string]any{
				"webhook_url": "http://example.webhook.office.com/webhooks/123",
			},
			wantValid:   false,
			wantErrCode: "format",
			wantErrMsg:  "HTTPS",
		},
		{
			name: "wrong_domain",
			config: map[string]any{
				"webhook_url": "https://example.com/webhook/123",
			},
			wantValid:   false,
			wantErrCode: "format",
			wantErrMsg:  "webhook.office.com",
		},
		{
			name: "valid_webhook_office_com",
			config: map[string]any{
				"webhook_url": "https://example.webhook.office.com/webhookb2/abc123/IncomingWebhook/def456/ghi789",
			},
			wantValid: true,
		},
		{
			name: "valid_logic_azure_com",
			config: map[string]any{
				"webhook_url": "https://prod-00.logic.azure.com:443/workflows/abc123/triggers/manual/paths/invoke",
			},
			wantValid: true,
		},
		{
			name:       "webhook_from_env",
			config:     map[string]any{},
			envWebhook: "https://example.webhook.office.com/webhookb2/abc123/IncomingWebhook/def456/ghi789",
			wantValid:  true,
		},
		{
			name: "config_overrides_env",
			config: map[string]any{
				"webhook_url": "https://example.webhook.office.com/webhookb2/config/IncomingWebhook/config/config",
			},
			envWebhook: "https://example.webhook.office.com/webhookb2/env/IncomingWebhook/env/env",
			wantValid:  true,
		},
		{
			name: "valid_with_all_options",
			config: map[string]any{
				"webhook_url":       "https://example.webhook.office.com/webhookb2/abc123/IncomingWebhook/def456/ghi789",
				"title_template":    "New Release {{version}}",
				"include_changelog": true,
				"theme_color":       "FF5733",
				"mention_users":     []string{"user1@example.com", "user2@example.com"},
				"notify_on_success": true,
				"notify_on_error":   true,
			},
			wantValid: true,
		},
		{
			name: "invalid_theme_color_too_short",
			config: map[string]any{
				"webhook_url": "https://example.webhook.office.com/webhookb2/abc123/IncomingWebhook/def456/ghi789",
				"theme_color": "FFF",
			},
			wantValid:   false,
			wantErrCode: "format",
			wantErrMsg:  "6-character",
		},
		{
			name: "invalid_theme_color_non_hex",
			config: map[string]any{
				"webhook_url": "https://example.webhook.office.com/webhookb2/abc123/IncomingWebhook/def456/ghi789",
				"theme_color": "GGGGGG",
			},
			wantValid:   false,
			wantErrCode: "format",
			wantErrMsg:  "hexadecimal",
		},
		{
			name: "valid_theme_color_with_hash",
			config: map[string]any{
				"webhook_url": "https://example.webhook.office.com/webhookb2/abc123/IncomingWebhook/def456/ghi789",
				"theme_color": "#0076D7",
			},
			wantValid: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variable if needed
			if tt.envWebhook != "" {
				_ = os.Setenv("TEAMS_WEBHOOK_URL", tt.envWebhook)
				defer func() { _ = os.Unsetenv("TEAMS_WEBHOOK_URL") }()
			}

			p := &TeamsPlugin{}
			resp, err := p.Validate(context.Background(), tt.config)

			if err != nil {
				t.Fatalf("Validate returned unexpected error: %v", err)
			}

			if resp.Valid != tt.wantValid {
				t.Errorf("expected Valid=%v, got %v", tt.wantValid, resp.Valid)
				if len(resp.Errors) > 0 {
					t.Logf("errors: %+v", resp.Errors)
				}
			}

			if !tt.wantValid {
				if len(resp.Errors) == 0 {
					t.Error("expected validation errors, got none")
					return
				}

				if tt.wantErrCode != "" {
					found := false
					for _, e := range resp.Errors {
						if e.Code == tt.wantErrCode {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected error code %q, got %+v", tt.wantErrCode, resp.Errors)
					}
				}

				if tt.wantErrMsg != "" {
					found := false
					for _, e := range resp.Errors {
						if strings.Contains(e.Message, tt.wantErrMsg) {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected error message containing %q, got %+v", tt.wantErrMsg, resp.Errors)
					}
				}
			}
		})
	}
}

func TestParseConfig(t *testing.T) {
	t.Parallel()

	// Clear environment variables for consistent testing
	origEnv := os.Getenv("TEAMS_WEBHOOK_URL")
	_ = os.Unsetenv("TEAMS_WEBHOOK_URL")
	t.Cleanup(func() {
		if origEnv != "" {
			_ = os.Setenv("TEAMS_WEBHOOK_URL", origEnv)
		}
	})

	tests := []struct {
		name           string
		config         map[string]any
		envWebhook     string
		expectedConfig *Config
	}{
		{
			name:   "empty_config_uses_defaults",
			config: map[string]any{},
			expectedConfig: &Config{
				WebhookURL:       "",
				TitleTemplate:    DefaultTitleTemplate,
				IncludeChangelog: true,
				ThemeColor:       DefaultThemeColor,
				MentionUsers:     nil,
				NotifyOnSuccess:  true,
				NotifyOnError:    true,
			},
		},
		{
			name:       "webhook_from_env",
			config:     map[string]any{},
			envWebhook: "https://example.webhook.office.com/webhookb2/env/IncomingWebhook/env/env",
			expectedConfig: &Config{
				WebhookURL:       "https://example.webhook.office.com/webhookb2/env/IncomingWebhook/env/env",
				TitleTemplate:    DefaultTitleTemplate,
				IncludeChangelog: true,
				ThemeColor:       DefaultThemeColor,
				MentionUsers:     nil,
				NotifyOnSuccess:  true,
				NotifyOnError:    true,
			},
		},
		{
			name: "config_overrides_env",
			config: map[string]any{
				"webhook_url": "https://example.webhook.office.com/webhookb2/config/IncomingWebhook/config/config",
			},
			envWebhook: "https://example.webhook.office.com/webhookb2/env/IncomingWebhook/env/env",
			expectedConfig: &Config{
				WebhookURL:       "https://example.webhook.office.com/webhookb2/config/IncomingWebhook/config/config",
				TitleTemplate:    DefaultTitleTemplate,
				IncludeChangelog: true,
				ThemeColor:       DefaultThemeColor,
				MentionUsers:     nil,
				NotifyOnSuccess:  true,
				NotifyOnError:    true,
			},
		},
		{
			name: "all_options",
			config: map[string]any{
				"webhook_url":       "https://example.webhook.office.com/webhookb2/123/IncomingWebhook/456/789",
				"title_template":    "New Release: {{version}}",
				"include_changelog": false,
				"theme_color":       "FF5733",
				"mention_users":     []any{"user1@example.com", "user2@example.com"},
				"notify_on_success": false,
				"notify_on_error":   false,
			},
			expectedConfig: &Config{
				WebhookURL:       "https://example.webhook.office.com/webhookb2/123/IncomingWebhook/456/789",
				TitleTemplate:    "New Release: {{version}}",
				IncludeChangelog: false,
				ThemeColor:       "FF5733",
				MentionUsers:     []string{"user1@example.com", "user2@example.com"},
				NotifyOnSuccess:  false,
				NotifyOnError:    false,
			},
		},
		{
			name: "boolean_as_string",
			config: map[string]any{
				"webhook_url":       "https://example.webhook.office.com/webhookb2/123/IncomingWebhook/456/789",
				"include_changelog": "true",
				"notify_on_success": "false",
			},
			expectedConfig: &Config{
				WebhookURL:       "https://example.webhook.office.com/webhookb2/123/IncomingWebhook/456/789",
				TitleTemplate:    DefaultTitleTemplate,
				IncludeChangelog: true,
				ThemeColor:       DefaultThemeColor,
				MentionUsers:     nil,
				NotifyOnSuccess:  false,
				NotifyOnError:    true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envWebhook != "" {
				_ = os.Setenv("TEAMS_WEBHOOK_URL", tt.envWebhook)
				defer func() { _ = os.Unsetenv("TEAMS_WEBHOOK_URL") }()
			}

			p := &TeamsPlugin{}
			cfg := p.parseConfig(tt.config)

			if cfg.WebhookURL != tt.expectedConfig.WebhookURL {
				t.Errorf("WebhookURL: expected %q, got %q", tt.expectedConfig.WebhookURL, cfg.WebhookURL)
			}
			if cfg.TitleTemplate != tt.expectedConfig.TitleTemplate {
				t.Errorf("TitleTemplate: expected %q, got %q", tt.expectedConfig.TitleTemplate, cfg.TitleTemplate)
			}
			if cfg.IncludeChangelog != tt.expectedConfig.IncludeChangelog {
				t.Errorf("IncludeChangelog: expected %v, got %v", tt.expectedConfig.IncludeChangelog, cfg.IncludeChangelog)
			}
			if cfg.ThemeColor != tt.expectedConfig.ThemeColor {
				t.Errorf("ThemeColor: expected %q, got %q", tt.expectedConfig.ThemeColor, cfg.ThemeColor)
			}
			if cfg.NotifyOnSuccess != tt.expectedConfig.NotifyOnSuccess {
				t.Errorf("NotifyOnSuccess: expected %v, got %v", tt.expectedConfig.NotifyOnSuccess, cfg.NotifyOnSuccess)
			}
			if cfg.NotifyOnError != tt.expectedConfig.NotifyOnError {
				t.Errorf("NotifyOnError: expected %v, got %v", tt.expectedConfig.NotifyOnError, cfg.NotifyOnError)
			}

			// Compare mention_users
			if len(cfg.MentionUsers) != len(tt.expectedConfig.MentionUsers) {
				t.Errorf("MentionUsers length: expected %d, got %d", len(tt.expectedConfig.MentionUsers), len(cfg.MentionUsers))
			} else {
				for i, m := range tt.expectedConfig.MentionUsers {
					if cfg.MentionUsers[i] != m {
						t.Errorf("MentionUsers[%d]: expected %q, got %q", i, m, cfg.MentionUsers[i])
					}
				}
			}
		})
	}
}

func TestExecuteDryRun(t *testing.T) {
	t.Parallel()

	baseContext := plugin.ReleaseContext{
		Version:       "1.2.3",
		TagName:       "v1.2.3",
		ReleaseType:   "minor",
		Branch:        "main",
		CommitSHA:     "abc123",
		RepositoryURL: "https://github.com/test/repo",
		ReleaseNotes:  "Test release notes",
		Changes: &plugin.CategorizedChanges{
			Features: []plugin.ConventionalCommit{
				{Hash: "abc", Description: "new feature"},
			},
			Fixes: []plugin.ConventionalCommit{
				{Hash: "def", Description: "bug fix"},
			},
		},
	}

	tests := []struct {
		name          string
		hook          plugin.Hook
		config        map[string]any
		dryRun        bool
		wantSuccess   bool
		wantMsgPrefix string
		wantOutputKey string
		wantOutputVal any
	}{
		{
			name:   "dry_run_post_publish",
			hook:   plugin.HookPostPublish,
			dryRun: true,
			config: map[string]any{
				"webhook_url":       "https://example.webhook.office.com/webhookb2/123/IncomingWebhook/456/789",
				"notify_on_success": true,
			},
			wantSuccess:   true,
			wantMsgPrefix: "Would send Teams success notification",
			wantOutputKey: "version",
			wantOutputVal: "1.2.3",
		},
		{
			name:   "dry_run_on_success",
			hook:   plugin.HookOnSuccess,
			dryRun: true,
			config: map[string]any{
				"webhook_url":       "https://example.webhook.office.com/webhookb2/123/IncomingWebhook/456/789",
				"notify_on_success": true,
			},
			wantSuccess:   true,
			wantMsgPrefix: "Would send Teams success notification",
		},
		{
			name:   "dry_run_on_error",
			hook:   plugin.HookOnError,
			dryRun: true,
			config: map[string]any{
				"webhook_url":     "https://example.webhook.office.com/webhookb2/123/IncomingWebhook/456/789",
				"notify_on_error": true,
			},
			wantSuccess:   true,
			wantMsgPrefix: "Would send Teams error notification",
		},
		{
			name:   "success_notification_disabled",
			hook:   plugin.HookPostPublish,
			dryRun: true,
			config: map[string]any{
				"webhook_url":       "https://example.webhook.office.com/webhookb2/123/IncomingWebhook/456/789",
				"notify_on_success": false,
			},
			wantSuccess:   true,
			wantMsgPrefix: "Success notification disabled",
		},
		{
			name:   "error_notification_disabled",
			hook:   plugin.HookOnError,
			dryRun: true,
			config: map[string]any{
				"webhook_url":     "https://example.webhook.office.com/webhookb2/123/IncomingWebhook/456/789",
				"notify_on_error": false,
			},
			wantSuccess:   true,
			wantMsgPrefix: "Error notification disabled",
		},
		{
			name:   "unhandled_hook",
			hook:   plugin.HookPreInit,
			dryRun: true,
			config: map[string]any{
				"webhook_url": "https://example.webhook.office.com/webhookb2/123/IncomingWebhook/456/789",
			},
			wantSuccess:   true,
			wantMsgPrefix: "Hook pre-init not handled",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &TeamsPlugin{}
			req := plugin.ExecuteRequest{
				Hook:    tt.hook,
				Config:  tt.config,
				Context: baseContext,
				DryRun:  tt.dryRun,
			}

			resp, err := p.Execute(context.Background(), req)
			if err != nil {
				t.Fatalf("Execute returned unexpected error: %v", err)
			}

			if resp.Success != tt.wantSuccess {
				t.Errorf("expected Success=%v, got %v (error: %s)", tt.wantSuccess, resp.Success, resp.Error)
			}

			if tt.wantMsgPrefix != "" && !strings.HasPrefix(resp.Message, tt.wantMsgPrefix) {
				t.Errorf("expected message to start with %q, got %q", tt.wantMsgPrefix, resp.Message)
			}

			if tt.wantOutputKey != "" {
				if resp.Outputs == nil {
					t.Error("expected outputs map, got nil")
				} else if resp.Outputs[tt.wantOutputKey] != tt.wantOutputVal {
					t.Errorf("expected output[%s]=%v, got %v", tt.wantOutputKey, tt.wantOutputVal, resp.Outputs[tt.wantOutputKey])
				}
			}
		})
	}
}

func TestExecuteUnhandledHook(t *testing.T) {
	t.Parallel()

	unhandledHooks := []struct {
		name string
		hook plugin.Hook
	}{
		{"PreInit", plugin.HookPreInit},
		{"PostInit", plugin.HookPostInit},
		{"PrePlan", plugin.HookPrePlan},
		{"PostPlan", plugin.HookPostPlan},
		{"PreVersion", plugin.HookPreVersion},
		{"PostVersion", plugin.HookPostVersion},
		{"PreNotes", plugin.HookPreNotes},
		{"PostNotes", plugin.HookPostNotes},
		{"PreApprove", plugin.HookPreApprove},
		{"PostApprove", plugin.HookPostApprove},
		{"PrePublish", plugin.HookPrePublish},
	}

	for _, tc := range unhandledHooks {
		t.Run(tc.name, func(t *testing.T) {
			p := &TeamsPlugin{}
			req := plugin.ExecuteRequest{
				Hook: tc.hook,
				Config: map[string]any{
					"webhook_url": "https://example.webhook.office.com/webhookb2/123/IncomingWebhook/456/789",
				},
				Context: plugin.ReleaseContext{
					Version: "1.0.0",
					TagName: "v1.0.0",
				},
				DryRun: false,
			}

			resp, err := p.Execute(context.Background(), req)

			if err != nil {
				t.Fatalf("unexpected error for hook %s: %v", tc.hook, err)
			}

			if !resp.Success {
				t.Errorf("expected success=true for unhandled hook %s, got success=false", tc.hook)
			}

			expectedMessage := "Hook " + string(tc.hook) + " not handled"
			if resp.Message != expectedMessage {
				t.Errorf("expected message=%q, got message=%q", expectedMessage, resp.Message)
			}
		})
	}
}

func TestValidateTeamsWebhookURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		url     string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "empty_url",
			url:     "",
			wantErr: true,
			errMsg:  "required",
		},
		{
			name:    "invalid_url",
			url:     "://invalid",
			wantErr: true,
			errMsg:  "invalid URL",
		},
		{
			name:    "http_scheme",
			url:     "http://example.webhook.office.com/webhookb2/123/IncomingWebhook/456/789",
			wantErr: true,
			errMsg:  "HTTPS",
		},
		{
			name:    "wrong_host",
			url:     "https://evil.com/webhook/123",
			wantErr: true,
			errMsg:  "webhook.office.com",
		},
		{
			name:    "valid_webhook_office_com",
			url:     "https://example.webhook.office.com/webhookb2/abc123/IncomingWebhook/def456/ghi789",
			wantErr: false,
		},
		{
			name:    "valid_logic_azure_com",
			url:     "https://prod-00.logic.azure.com:443/workflows/abc123/triggers/manual/paths/invoke",
			wantErr: false,
		},
		{
			name:    "valid_with_query_params",
			url:     "https://example.webhook.office.com/webhookb2/123/IncomingWebhook/456/789?wait=true",
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateTeamsWebhookURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
					return
				}
				if tt.errMsg != "" && !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestIsValidMicrosoftHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		host  string
		valid bool
	}{
		{
			name:  "valid_webhook_office_com",
			host:  "example.webhook.office.com",
			valid: true,
		},
		{
			name:  "valid_logic_azure_com",
			host:  "prod-00.logic.azure.com",
			valid: true,
		},
		{
			name:  "valid_logic_azure_com_with_port",
			host:  "prod-00.logic.azure.com:443",
			valid: true,
		},
		{
			name:  "invalid_evil_domain",
			host:  "evil.com",
			valid: false,
		},
		{
			name:  "invalid_office365_com",
			host:  "example.office365.com",
			valid: false,
		},
		{
			name:  "invalid_suffix_match_attempt",
			host:  "webhook.office.com.evil.com",
			valid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidMicrosoftHost(tt.host)
			if result != tt.valid {
				t.Errorf("isValidMicrosoftHost(%q) = %v, want %v", tt.host, result, tt.valid)
			}
		})
	}
}

func TestBuildTitle(t *testing.T) {
	t.Parallel()

	p := &TeamsPlugin{}

	tests := []struct {
		name     string
		template string
		version  string
		want     string
	}{
		{
			name:     "default_template",
			template: "",
			version:  "1.2.3",
			want:     "Release 1.2.3",
		},
		{
			name:     "custom_template",
			template: "New Release {{version}} Published",
			version:  "2.0.0",
			want:     "New Release 2.0.0 Published",
		},
		{
			name:     "template_without_placeholder",
			template: "New Release",
			version:  "1.0.0",
			want:     "New Release",
		},
		{
			name:     "multiple_placeholders",
			template: "{{version}} - Release {{version}}",
			version:  "3.0.0",
			want:     "3.0.0 - Release 3.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.buildTitle(tt.template, tt.version)
			if got != tt.want {
				t.Errorf("buildTitle(%q, %q) = %q, want %q", tt.template, tt.version, got, tt.want)
			}
		})
	}
}

func TestBuildMentionText(t *testing.T) {
	t.Parallel()

	p := &TeamsPlugin{}

	tests := []struct {
		name  string
		users []string
		want  string
	}{
		{
			name:  "empty_users",
			users: nil,
			want:  "",
		},
		{
			name:  "single_user",
			users: []string{"user@example.com"},
			want:  "cc: <at>user@example.com</at>",
		},
		{
			name:  "multiple_users",
			users: []string{"user1@example.com", "user2@example.com"},
			want:  "cc: <at>user1@example.com</at> <at>user2@example.com</at>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := p.buildMentionText(tt.users)
			if got != tt.want {
				t.Errorf("buildMentionText(%v) = %q, want %q", tt.users, got, tt.want)
			}
		})
	}
}

func TestBuildTeamsMessage(t *testing.T) {
	t.Parallel()

	p := &TeamsPlugin{}

	t.Run("basic_message", func(t *testing.T) {
		body := []AdaptiveElement{
			{Type: "TextBlock", Text: "Test Title", Weight: "bolder"},
		}

		msg := p.buildTeamsMessage(body, nil, nil, ColorSuccess)

		if msg.Type != "message" {
			t.Errorf("expected type 'message', got %q", msg.Type)
		}

		if len(msg.Attachments) != 1 {
			t.Fatalf("expected 1 attachment, got %d", len(msg.Attachments))
		}

		att := msg.Attachments[0]
		if att.ContentType != "application/vnd.microsoft.card.adaptive" {
			t.Errorf("expected content type 'application/vnd.microsoft.card.adaptive', got %q", att.ContentType)
		}

		card := att.Content
		if card.Type != "AdaptiveCard" {
			t.Errorf("expected card type 'AdaptiveCard', got %q", card.Type)
		}

		if card.Version != "1.2" {
			t.Errorf("expected version '1.2', got %q", card.Version)
		}

		if len(card.Body) != 1 {
			t.Errorf("expected 1 body element, got %d", len(card.Body))
		}

		if card.MSTeams != nil {
			t.Error("expected MSTeams to be nil without mentions")
		}
	})

	t.Run("message_with_actions", func(t *testing.T) {
		body := []AdaptiveElement{
			{Type: "TextBlock", Text: "Test"},
		}
		actions := []AdaptiveAction{
			{Type: "Action.OpenUrl", Title: "View", URL: "https://example.com"},
		}

		msg := p.buildTeamsMessage(body, actions, nil, ColorSuccess)
		card := msg.Attachments[0].Content

		if len(card.Actions) != 1 {
			t.Fatalf("expected 1 action, got %d", len(card.Actions))
		}

		action := card.Actions[0]
		if action.Type != "Action.OpenUrl" {
			t.Errorf("expected action type 'Action.OpenUrl', got %q", action.Type)
		}
	})

	t.Run("message_with_mentions", func(t *testing.T) {
		body := []AdaptiveElement{
			{Type: "TextBlock", Text: "Test"},
		}
		mentionUsers := []string{"user1@example.com", "user2@example.com"}

		msg := p.buildTeamsMessage(body, nil, mentionUsers, ColorSuccess)
		card := msg.Attachments[0].Content

		if card.MSTeams == nil {
			t.Fatal("expected MSTeams config to be set")
		}

		if card.MSTeams.Width != "Full" {
			t.Errorf("expected width 'Full', got %q", card.MSTeams.Width)
		}

		if len(card.MSTeams.Entities) != 2 {
			t.Fatalf("expected 2 entities, got %d", len(card.MSTeams.Entities))
		}

		entity := card.MSTeams.Entities[0]
		if entity.Type != "mention" {
			t.Errorf("expected entity type 'mention', got %q", entity.Type)
		}

		if entity.Mentioned.ID != "user1@example.com" {
			t.Errorf("expected mentioned ID 'user1@example.com', got %q", entity.Mentioned.ID)
		}
	})
}

func TestSendMessageWithMockHTTPClient(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		statusCode     int
		wantErr        bool
		wantErrContain string
	}{
		{
			name:       "success_200",
			statusCode: http.StatusOK,
			wantErr:    false,
		},
		{
			name:           "error_400",
			statusCode:     http.StatusBadRequest,
			wantErr:        true,
			wantErrContain: "status 400",
		},
		{
			name:           "error_401",
			statusCode:     http.StatusUnauthorized,
			wantErr:        true,
			wantErrContain: "status 401",
		},
		{
			name:           "error_500",
			statusCode:     http.StatusInternalServerError,
			wantErr:        true,
			wantErrContain: "status 500",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var receivedPayload TeamsMessage

			mockClient := &MockHTTPClient{
				DoFunc: func(req *http.Request) (*http.Response, error) {
					// Verify HTTP method
					if req.Method != http.MethodPost {
						t.Errorf("expected POST, got %s", req.Method)
					}

					// Verify Content-Type
					if ct := req.Header.Get("Content-Type"); ct != "application/json" {
						t.Errorf("expected Content-Type application/json, got %s", ct)
					}

					// Parse body
					body, err := io.ReadAll(req.Body)
					if err != nil {
						t.Errorf("failed to read body: %v", err)
					}
					defer func() { _ = req.Body.Close() }()

					if err := json.Unmarshal(body, &receivedPayload); err != nil {
						t.Errorf("failed to unmarshal payload: %v", err)
					}

					return &http.Response{
						StatusCode: tt.statusCode,
						Body:       io.NopCloser(bytes.NewReader(nil)),
					}, nil
				},
			}

			p := &TeamsPlugin{httpClient: mockClient}

			msg := TeamsMessage{
				Type: "message",
				Attachments: []TeamsAttachment{
					{
						ContentType: "application/vnd.microsoft.card.adaptive",
						Content: AdaptiveCard{
							Type:    "AdaptiveCard",
							Version: "1.2",
							Schema:  "http://adaptivecards.io/schemas/adaptive-card.json",
							Body: []AdaptiveElement{
								{Type: "TextBlock", Text: "Test"},
							},
						},
					},
				},
			}

			err := p.sendMessage(context.Background(), "https://example.webhook.office.com/webhookb2/123/IncomingWebhook/456/789", msg)

			if tt.wantErr {
				if err == nil {
					t.Error("expected error, got nil")
					return
				}
				if tt.wantErrContain != "" && !strings.Contains(err.Error(), tt.wantErrContain) {
					t.Errorf("expected error containing %q, got %q", tt.wantErrContain, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

func TestSendMessageNetworkError(t *testing.T) {
	t.Parallel()

	mockClient := &MockHTTPClient{
		DoFunc: func(req *http.Request) (*http.Response, error) {
			return nil, errors.New("network error")
		},
	}

	p := &TeamsPlugin{httpClient: mockClient}

	msg := TeamsMessage{
		Type:        "message",
		Attachments: []TeamsAttachment{},
	}

	err := p.sendMessage(context.Background(), "https://example.webhook.office.com/webhookb2/123/IncomingWebhook/456/789", msg)
	if err == nil {
		t.Error("expected error, got nil")
	}

	if !strings.Contains(err.Error(), "failed to send request") {
		t.Errorf("expected error about failed request, got %q", err.Error())
	}
}

func TestSendMessageInvalidURL(t *testing.T) {
	t.Parallel()

	p := &TeamsPlugin{}

	msg := TeamsMessage{
		Type:        "message",
		Attachments: []TeamsAttachment{},
	}

	// Invalid URL that cannot be parsed
	err := p.sendMessage(context.Background(), "://invalid-url", msg)
	if err == nil {
		t.Error("expected error for invalid URL, got nil")
	}
}

func TestSendMessageContextCancelled(t *testing.T) {
	t.Parallel()

	mockClient := &MockHTTPClient{
		DoFunc: func(req *http.Request) (*http.Response, error) {
			// Check if context is cancelled
			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			default:
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(nil)),
			}, nil
		},
	}

	p := &TeamsPlugin{httpClient: mockClient}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	msg := TeamsMessage{
		Type:        "message",
		Attachments: []TeamsAttachment{},
	}

	err := p.sendMessage(ctx, "https://example.webhook.office.com/webhookb2/123/IncomingWebhook/456/789", msg)
	if err == nil {
		t.Error("expected error for cancelled context, got nil")
	}
}

func TestExecuteWithMockHTTPClient(t *testing.T) {
	t.Parallel()

	var receivedPayload TeamsMessage

	mockClient := &MockHTTPClient{
		DoFunc: func(req *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(req.Body)
			defer func() { _ = req.Body.Close() }()
			_ = json.Unmarshal(body, &receivedPayload)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(nil)),
			}, nil
		},
	}

	tests := []struct {
		name        string
		hook        plugin.Hook
		dryRun      bool
		wantSuccess bool
		wantMessage string
	}{
		{
			name:        "actual_success_notification",
			hook:        plugin.HookPostPublish,
			dryRun:      false,
			wantSuccess: true,
			wantMessage: "Sent Teams success notification",
		},
		{
			name:        "actual_error_notification",
			hook:        plugin.HookOnError,
			dryRun:      false,
			wantSuccess: true,
			wantMessage: "Sent Teams error notification",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &TeamsPlugin{httpClient: mockClient}

			req := plugin.ExecuteRequest{
				Hook: tt.hook,
				Config: map[string]any{
					"webhook_url":       "https://example.webhook.office.com/webhookb2/123/IncomingWebhook/456/789",
					"notify_on_success": true,
					"notify_on_error":   true,
					"title_template":    "Release {{version}}",
				},
				Context: plugin.ReleaseContext{
					Version:       "1.0.0",
					TagName:       "v1.0.0",
					ReleaseType:   "patch",
					Branch:        "main",
					RepositoryURL: "https://github.com/test/repo",
				},
				DryRun: tt.dryRun,
			}

			resp, err := p.Execute(context.Background(), req)
			if err != nil {
				t.Fatalf("Execute returned unexpected error: %v", err)
			}

			if resp.Success != tt.wantSuccess {
				t.Errorf("expected Success=%v, got %v (error: %s)", tt.wantSuccess, resp.Success, resp.Error)
			}

			if resp.Message != tt.wantMessage {
				t.Errorf("expected message %q, got %q", tt.wantMessage, resp.Message)
			}

			// Verify the payload structure
			if receivedPayload.Type != "message" {
				t.Errorf("expected message type 'message', got %q", receivedPayload.Type)
			}
		})
	}
}

func TestExecuteWithFailedHTTPCall(t *testing.T) {
	t.Parallel()

	mockClient := &MockHTTPClient{
		DoFunc: func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusInternalServerError,
				Body:       io.NopCloser(bytes.NewReader(nil)),
			}, nil
		},
	}

	tests := []struct {
		name string
		hook plugin.Hook
	}{
		{
			name: "failed_success_notification",
			hook: plugin.HookPostPublish,
		},
		{
			name: "failed_error_notification",
			hook: plugin.HookOnError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &TeamsPlugin{httpClient: mockClient}

			req := plugin.ExecuteRequest{
				Hook: tt.hook,
				Config: map[string]any{
					"webhook_url":       "https://example.webhook.office.com/webhookb2/123/IncomingWebhook/456/789",
					"notify_on_success": true,
					"notify_on_error":   true,
				},
				Context: plugin.ReleaseContext{
					Version:     "1.0.0",
					TagName:     "v1.0.0",
					ReleaseType: "patch",
					Branch:      "main",
				},
				DryRun: false,
			}

			resp, err := p.Execute(context.Background(), req)
			if err != nil {
				t.Fatalf("Execute returned unexpected error: %v", err)
			}

			// Should return failure in response
			if resp.Success {
				t.Error("expected Success=false for failed HTTP call")
			}

			if resp.Error == "" {
				t.Error("expected error message, got empty string")
			}

			if !strings.Contains(resp.Error, "failed to send Teams message") {
				t.Errorf("expected error about failed Teams message, got %q", resp.Error)
			}
		})
	}
}

func TestTeamsMessageStructure(t *testing.T) {
	t.Parallel()

	t.Run("success_message_with_all_fields", func(t *testing.T) {
		var receivedPayload TeamsMessage

		mockClient := &MockHTTPClient{
			DoFunc: func(req *http.Request) (*http.Response, error) {
				body, _ := io.ReadAll(req.Body)
				defer func() { _ = req.Body.Close() }()
				_ = json.Unmarshal(body, &receivedPayload)
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(nil)),
				}, nil
			},
		}

		p := &TeamsPlugin{httpClient: mockClient}

		cfg := &Config{
			WebhookURL:       "https://example.webhook.office.com/webhookb2/123/IncomingWebhook/456/789",
			TitleTemplate:    "Release {{version}}",
			IncludeChangelog: true,
			ThemeColor:       "FF5733",
			MentionUsers:     []string{"user@example.com"},
			NotifyOnSuccess:  true,
		}

		releaseCtx := plugin.ReleaseContext{
			Version:       "2.0.0",
			TagName:       "v2.0.0",
			ReleaseType:   "major",
			Branch:        "main",
			RepositoryURL: "https://github.com/test/repo",
			ReleaseNotes:  "## Changes\n- Feature A\n- Bug fix B",
			Changes: &plugin.CategorizedChanges{
				Features: []plugin.ConventionalCommit{
					{Description: "Feature A"},
				},
				Fixes: []plugin.ConventionalCommit{
					{Description: "Bug fix B"},
				},
				Breaking: []plugin.ConventionalCommit{
					{Description: "Breaking change"},
				},
			},
		}

		resp, err := p.sendSuccessNotification(context.Background(), cfg, releaseCtx, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !resp.Success {
			t.Errorf("expected success, got failure: %s", resp.Error)
		}

		// Verify message structure
		if receivedPayload.Type != "message" {
			t.Errorf("expected type 'message', got %q", receivedPayload.Type)
		}

		if len(receivedPayload.Attachments) != 1 {
			t.Fatalf("expected 1 attachment, got %d", len(receivedPayload.Attachments))
		}

		card := receivedPayload.Attachments[0].Content
		if card.Type != "AdaptiveCard" {
			t.Errorf("expected card type 'AdaptiveCard', got %q", card.Type)
		}

		// Check for actions
		if len(card.Actions) == 0 {
			t.Error("expected actions to be present")
		}

		// Check for mentions config
		if card.MSTeams == nil {
			t.Error("expected MSTeams config to be set for mentions")
		}
	})
}

func TestReleaseNoteTruncation(t *testing.T) {
	t.Parallel()

	var receivedPayload TeamsMessage

	mockClient := &MockHTTPClient{
		DoFunc: func(req *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(req.Body)
			defer func() { _ = req.Body.Close() }()
			_ = json.Unmarshal(body, &receivedPayload)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(nil)),
			}, nil
		},
	}

	p := &TeamsPlugin{httpClient: mockClient}

	// Create a very long release note
	longNote := strings.Repeat("A", 3000)

	cfg := &Config{
		WebhookURL:       "https://example.webhook.office.com/webhookb2/123/IncomingWebhook/456/789",
		NotifyOnSuccess:  true,
		IncludeChangelog: true,
	}

	releaseCtx := plugin.ReleaseContext{
		Version:      "1.0.0",
		TagName:      "v1.0.0",
		ReleaseType:  "major",
		Branch:       "main",
		ReleaseNotes: longNote,
	}

	resp, err := p.sendSuccessNotification(context.Background(), cfg, releaseCtx, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.Success {
		t.Errorf("expected success, got failure: %s", resp.Error)
	}

	// Find the changelog text block and verify truncation
	card := receivedPayload.Attachments[0].Content
	var changelogText string
	for _, elem := range card.Body {
		if elem.Type == "TextBlock" && strings.HasPrefix(elem.Text, "AAA") {
			changelogText = elem.Text
			break
		}
	}

	if changelogText == "" {
		t.Error("expected changelog text block to be present")
		return
	}

	if len(changelogText) > 2003 { // 2000 + "..."
		t.Errorf("expected truncated changelog (max 2003 chars), got %d chars", len(changelogText))
	}

	if !strings.HasSuffix(changelogText, "...") {
		t.Error("expected changelog to end with '...' after truncation")
	}
}

func TestHTMLEscapingInReleaseNotes(t *testing.T) {
	t.Parallel()

	var receivedPayload TeamsMessage

	mockClient := &MockHTTPClient{
		DoFunc: func(req *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(req.Body)
			defer func() { _ = req.Body.Close() }()
			_ = json.Unmarshal(body, &receivedPayload)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(nil)),
			}, nil
		},
	}

	p := &TeamsPlugin{httpClient: mockClient}

	// Release notes with HTML-like content
	releaseNotes := "<script>alert('xss')</script> & special <chars>"

	cfg := &Config{
		WebhookURL:       "https://example.webhook.office.com/webhookb2/123/IncomingWebhook/456/789",
		NotifyOnSuccess:  true,
		IncludeChangelog: true,
	}

	releaseCtx := plugin.ReleaseContext{
		Version:      "1.0.0",
		TagName:      "v1.0.0",
		ReleaseType:  "patch",
		Branch:       "main",
		ReleaseNotes: releaseNotes,
	}

	resp, err := p.sendSuccessNotification(context.Background(), cfg, releaseCtx, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.Success {
		t.Errorf("expected success, got failure: %s", resp.Error)
	}

	// Find the changelog text block and verify HTML escaping
	card := receivedPayload.Attachments[0].Content
	for _, elem := range card.Body {
		if elem.Type == "TextBlock" && strings.Contains(elem.Text, "script") {
			// Verify HTML entities are escaped
			if strings.Contains(elem.Text, "<script>") {
				t.Error("expected <script> to be escaped")
			}
			if !strings.Contains(elem.Text, "&lt;script&gt;") {
				t.Error("expected escaped HTML entities")
			}
			break
		}
	}
}

func TestNilConfig(t *testing.T) {
	t.Parallel()

	p := &TeamsPlugin{}

	// Test with nil config map
	cfg := p.parseConfig(nil)

	// Should return defaults
	if cfg.TitleTemplate != DefaultTitleTemplate {
		t.Errorf("expected default title template %q, got %q", DefaultTitleTemplate, cfg.TitleTemplate)
	}

	if cfg.ThemeColor != DefaultThemeColor {
		t.Errorf("expected default theme color %q, got %q", DefaultThemeColor, cfg.ThemeColor)
	}

	if !cfg.NotifyOnSuccess {
		t.Error("expected NotifyOnSuccess=true by default")
	}

	if !cfg.NotifyOnError {
		t.Error("expected NotifyOnError=true by default")
	}

	if !cfg.IncludeChangelog {
		t.Error("expected IncludeChangelog=true by default")
	}
}

func TestValidateWithNilConfig(t *testing.T) {
	t.Parallel()

	// Clear environment variable
	origEnv := os.Getenv("TEAMS_WEBHOOK_URL")
	_ = os.Unsetenv("TEAMS_WEBHOOK_URL")
	defer func() {
		if origEnv != "" {
			_ = os.Setenv("TEAMS_WEBHOOK_URL", origEnv)
		}
	}()

	p := &TeamsPlugin{}

	resp, err := p.Validate(context.Background(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if resp.Valid {
		t.Error("expected validation to fail with nil config")
	}

	if len(resp.Errors) == 0 {
		t.Error("expected validation errors")
	}
}

func TestTeamsMessageJSON(t *testing.T) {
	t.Parallel()

	msg := TeamsMessage{
		Type: "message",
		Attachments: []TeamsAttachment{
			{
				ContentType: "application/vnd.microsoft.card.adaptive",
				Content: AdaptiveCard{
					Type:    "AdaptiveCard",
					Version: "1.2",
					Schema:  "http://adaptivecards.io/schemas/adaptive-card.json",
					Body: []AdaptiveElement{
						{
							Type:   "TextBlock",
							Text:   "Release v1.0.0",
							Weight: "bolder",
							Size:   "large",
						},
						{
							Type: "ColumnSet",
							Columns: []ColumnDefinition{
								{
									Type:  "Column",
									Width: "auto",
									Items: []AdaptiveElement{
										{Type: "TextBlock", Text: "Version:"},
									},
								},
								{
									Type:  "Column",
									Width: "stretch",
									Items: []AdaptiveElement{
										{Type: "TextBlock", Text: "1.0.0"},
									},
								},
							},
						},
					},
					Actions: []AdaptiveAction{
						{
							Type:  "Action.OpenUrl",
							Title: "View Release",
							URL:   "https://github.com/test/repo/releases/tag/v1.0.0",
						},
					},
					MSTeams: &MSTeamsConfig{
						Width: "Full",
						Entities: []TeamsEntity{
							{
								Type: "mention",
								Text: "<at>user@example.com</at>",
								Mentioned: &TeamsMentionedUser{
									ID:   "user@example.com",
									Name: "user@example.com",
								},
							},
						},
					},
				},
			},
		},
	}

	data, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("failed to marshal message: %v", err)
	}

	// Verify JSON structure
	var parsed map[string]any
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("failed to unmarshal JSON: %v", err)
	}

	if parsed["type"] != "message" {
		t.Errorf("expected type 'message', got %v", parsed["type"])
	}

	attachments, ok := parsed["attachments"].([]any)
	if !ok || len(attachments) != 1 {
		t.Error("expected one attachment")
		return
	}

	attachment := attachments[0].(map[string]any)
	if attachment["contentType"] != "application/vnd.microsoft.card.adaptive" {
		t.Errorf("expected contentType 'application/vnd.microsoft.card.adaptive', got %v", attachment["contentType"])
	}

	content := attachment["content"].(map[string]any)
	if content["type"] != "AdaptiveCard" {
		t.Errorf("expected card type 'AdaptiveCard', got %v", content["type"])
	}

	if content["$schema"] != "http://adaptivecards.io/schemas/adaptive-card.json" {
		t.Errorf("expected schema 'http://adaptivecards.io/schemas/adaptive-card.json', got %v", content["$schema"])
	}
}

func TestContextCancellation(t *testing.T) {
	t.Parallel()

	p := &TeamsPlugin{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	req := plugin.ExecuteRequest{
		Hook: plugin.HookPostPublish,
		Config: map[string]any{
			"webhook_url":       "https://example.webhook.office.com/webhookb2/123/IncomingWebhook/456/789",
			"notify_on_success": true,
		},
		Context: plugin.ReleaseContext{
			Version:     "1.0.0",
			TagName:     "v1.0.0",
			ReleaseType: "major",
			Branch:      "main",
		},
		DryRun: true, // Use dry run to avoid actual HTTP calls
	}

	// With dry run, context cancellation should not affect the result
	resp, err := p.Execute(ctx, req)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !resp.Success {
		t.Errorf("expected success in dry run, got failure: %s", resp.Error)
	}
}

func TestChangeSummaryGeneration(t *testing.T) {
	t.Parallel()

	var receivedPayload TeamsMessage

	mockClient := &MockHTTPClient{
		DoFunc: func(req *http.Request) (*http.Response, error) {
			body, _ := io.ReadAll(req.Body)
			defer func() { _ = req.Body.Close() }()
			_ = json.Unmarshal(body, &receivedPayload)
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewReader(nil)),
			}, nil
		},
	}

	tests := []struct {
		name           string
		changes        *plugin.CategorizedChanges
		expectSummary  bool
		summaryContain string
	}{
		{
			name:          "nil_changes",
			changes:       nil,
			expectSummary: false,
		},
		{
			name:          "empty_changes",
			changes:       &plugin.CategorizedChanges{},
			expectSummary: true,
		},
		{
			name: "with_features_and_fixes",
			changes: &plugin.CategorizedChanges{
				Features: []plugin.ConventionalCommit{{Description: "feat1"}},
				Fixes:    []plugin.ConventionalCommit{{Description: "fix1"}, {Description: "fix2"}},
			},
			expectSummary:  true,
			summaryContain: "1 features, 2 fixes",
		},
		{
			name: "with_breaking_changes",
			changes: &plugin.CategorizedChanges{
				Features: []plugin.ConventionalCommit{{Description: "feat1"}},
				Fixes:    []plugin.ConventionalCommit{{Description: "fix1"}},
				Breaking: []plugin.ConventionalCommit{{Description: "breaking1"}},
			},
			expectSummary:  true,
			summaryContain: "1 breaking changes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &TeamsPlugin{httpClient: mockClient}

			cfg := &Config{
				WebhookURL:      "https://example.webhook.office.com/webhookb2/123/IncomingWebhook/456/789",
				NotifyOnSuccess: true,
			}

			releaseCtx := plugin.ReleaseContext{
				Version:     "1.0.0",
				TagName:     "v1.0.0",
				ReleaseType: "minor",
				Branch:      "main",
				Changes:     tt.changes,
			}

			resp, err := p.sendSuccessNotification(context.Background(), cfg, releaseCtx, false)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if !resp.Success {
				t.Errorf("expected success, got failure: %s", resp.Error)
			}

			// Check for summary in body
			card := receivedPayload.Attachments[0].Content
			foundSummary := false
			for _, elem := range card.Body {
				if elem.Type == "TextBlock" && strings.HasPrefix(elem.Text, "Changes:") {
					foundSummary = true
					if tt.summaryContain != "" && !strings.Contains(elem.Text, tt.summaryContain) {
						t.Errorf("expected summary to contain %q, got %q", tt.summaryContain, elem.Text)
					}
					break
				}
			}

			if tt.expectSummary && !foundSummary {
				t.Error("expected summary to be present")
			}
		})
	}
}

func TestWithTestServer(t *testing.T) {
	t.Parallel()

	var receivedPayload TeamsMessage

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}

		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", ct)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("failed to read request body: %v", err)
		}
		defer func() { _ = r.Body.Close() }()

		if err := json.Unmarshal(body, &receivedPayload); err != nil {
			t.Errorf("failed to unmarshal payload: %v", err)
		}

		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// Create a test client that uses the test server
	testClient := &http.Client{
		Timeout: 10 * time.Second,
	}

	p := &TeamsPlugin{httpClient: testClient}

	msg := TeamsMessage{
		Type: "message",
		Attachments: []TeamsAttachment{
			{
				ContentType: "application/vnd.microsoft.card.adaptive",
				Content: AdaptiveCard{
					Type:    "AdaptiveCard",
					Version: "1.2",
					Schema:  "http://adaptivecards.io/schemas/adaptive-card.json",
					Body: []AdaptiveElement{
						{Type: "TextBlock", Text: "Test"},
					},
				},
			},
		},
	}

	err := p.sendMessage(context.Background(), server.URL, msg)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify received payload
	if receivedPayload.Type != "message" {
		t.Errorf("expected type 'message', got %q", receivedPayload.Type)
	}
}

func TestGetHTTPClient(t *testing.T) {
	t.Parallel()

	t.Run("returns_custom_client_if_set", func(t *testing.T) {
		mockClient := &MockHTTPClient{}
		p := &TeamsPlugin{httpClient: mockClient}

		client := p.getHTTPClient()
		if client != mockClient {
			t.Error("expected custom client to be returned")
		}
	})

	t.Run("returns_default_client_if_not_set", func(t *testing.T) {
		p := &TeamsPlugin{}

		client := p.getHTTPClient()
		if client == nil {
			t.Error("expected default client to be returned")
		}
		if client != defaultHTTPClient {
			t.Error("expected default HTTP client")
		}
	})
}

func TestColorConstants(t *testing.T) {
	t.Parallel()

	t.Run("default_theme_color", func(t *testing.T) {
		if DefaultThemeColor != "0076D7" {
			t.Errorf("expected DefaultThemeColor='0076D7', got %q", DefaultThemeColor)
		}
	})

	t.Run("success_color", func(t *testing.T) {
		if ColorSuccess != "28A745" {
			t.Errorf("expected ColorSuccess='28A745', got %q", ColorSuccess)
		}
	})

	t.Run("error_color", func(t *testing.T) {
		if ColorError != "DC3545" {
			t.Errorf("expected ColorError='DC3545', got %q", ColorError)
		}
	})

	t.Run("default_title_template", func(t *testing.T) {
		if DefaultTitleTemplate != "Release {{version}}" {
			t.Errorf("expected DefaultTitleTemplate='Release {{version}}', got %q", DefaultTitleTemplate)
		}
	})
}
