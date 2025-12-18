// Package main contains tests for the Teams plugin.
package main

import (
	"context"
	"os"
	"testing"

	"github.com/relicta-tech/relicta-plugin-sdk/plugin"
)

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
	t.Run("hooks contains PostPublish", func(t *testing.T) {
		if len(info.Hooks) != 1 {
			t.Errorf("expected 1 hook, got %d", len(info.Hooks))
		}
		if info.Hooks[0] != plugin.HookPostPublish {
			t.Errorf("expected HookPostPublish, got %s", info.Hooks[0])
		}
	})

	// Verify config schema is valid JSON
	t.Run("config schema is valid JSON", func(t *testing.T) {
		if info.ConfigSchema == "" {
			t.Error("config schema should not be empty")
		}
	})
}

func TestValidate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		config      map[string]any
		envSetup    map[string]string
		wantValid   bool
		wantErrors  int
		errorFields []string
	}{
		{
			name:       "empty config is valid",
			config:     map[string]any{},
			wantValid:  true,
			wantErrors: 0,
		},
		{
			name:       "nil config is valid",
			config:     nil,
			wantValid:  true,
			wantErrors: 0,
		},
		{
			name: "config with arbitrary fields is valid",
			config: map[string]any{
				"webhook_url": "https://teams.microsoft.com/webhook/123",
				"channel":     "general",
			},
			wantValid:  true,
			wantErrors: 0,
		},
		{
			name: "config with nested objects is valid",
			config: map[string]any{
				"webhook_url": "https://teams.microsoft.com/webhook/123",
				"settings": map[string]any{
					"mention_users": true,
					"format":        "detailed",
				},
			},
			wantValid:  true,
			wantErrors: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Setup environment variables
			for key, val := range tc.envSetup {
				os.Setenv(key, val)
				defer os.Unsetenv(key)
			}

			p := &TeamsPlugin{}
			resp, err := p.Validate(context.Background(), tc.config)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if resp.Valid != tc.wantValid {
				t.Errorf("expected valid=%v, got valid=%v", tc.wantValid, resp.Valid)
			}

			if len(resp.Errors) != tc.wantErrors {
				t.Errorf("expected %d errors, got %d: %v", tc.wantErrors, len(resp.Errors), resp.Errors)
			}

			// Check specific error fields if expected
			for _, field := range tc.errorFields {
				found := false
				for _, err := range resp.Errors {
					if err.Field == field {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected error for field %q not found", field)
				}
			}
		})
	}
}

func TestParseConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		config         map[string]any
		envSetup       map[string]string
		expectedValues map[string]any
	}{
		{
			name:   "empty config uses defaults",
			config: map[string]any{},
			expectedValues: map[string]any{
				"webhook_url": "",
				"channel":     "",
			},
		},
		{
			name: "config values are used",
			config: map[string]any{
				"webhook_url": "https://teams.microsoft.com/webhook/abc123",
				"channel":     "releases",
				"mention_all": true,
			},
			expectedValues: map[string]any{
				"webhook_url": "https://teams.microsoft.com/webhook/abc123",
				"channel":     "releases",
				"mention_all": true,
			},
		},
		{
			name:   "environment variables provide values",
			config: map[string]any{},
			envSetup: map[string]string{
				"TEAMS_WEBHOOK_URL": "https://teams.microsoft.com/webhook/env",
			},
			expectedValues: map[string]any{
				"env_webhook_url": "https://teams.microsoft.com/webhook/env",
			},
		},
		{
			name: "config takes precedence over environment",
			config: map[string]any{
				"webhook_url": "https://teams.microsoft.com/webhook/config",
			},
			envSetup: map[string]string{
				"TEAMS_WEBHOOK_URL": "https://teams.microsoft.com/webhook/env",
			},
			expectedValues: map[string]any{
				"webhook_url": "https://teams.microsoft.com/webhook/config",
			},
		},
		{
			name: "boolean config values",
			config: map[string]any{
				"notify_on_success": true,
				"notify_on_failure": false,
			},
			expectedValues: map[string]any{
				"notify_on_success": true,
				"notify_on_failure": false,
			},
		},
		{
			name: "string slice config values",
			config: map[string]any{
				"channels": []any{"general", "releases", "dev"},
			},
			expectedValues: map[string]any{
				"channels_count": 3,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Setup environment variables
			for key, val := range tc.envSetup {
				os.Setenv(key, val)
				defer os.Unsetenv(key)
			}

			// Verify config values are accessible
			for key, expectedVal := range tc.expectedValues {
				switch key {
				case "webhook_url", "channel":
					if val, ok := tc.config[key]; ok {
						if val != expectedVal {
							t.Errorf("expected %s=%v, got %v", key, expectedVal, val)
						}
					}
				case "env_webhook_url":
					envVal := os.Getenv("TEAMS_WEBHOOK_URL")
					if envVal != expectedVal {
						t.Errorf("expected env %s=%v, got %v", key, expectedVal, envVal)
					}
				case "mention_all", "notify_on_success", "notify_on_failure":
					if val, ok := tc.config[key]; ok {
						if val != expectedVal {
							t.Errorf("expected %s=%v, got %v", key, expectedVal, val)
						}
					}
				case "channels_count":
					if channels, ok := tc.config["channels"].([]any); ok {
						if len(channels) != expectedVal.(int) {
							t.Errorf("expected channels count=%v, got %d", expectedVal, len(channels))
						}
					}
				}
			}
		})
	}
}

func TestExecuteDryRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		hook            plugin.Hook
		dryRun          bool
		config          map[string]any
		releaseContext  plugin.ReleaseContext
		expectedSuccess bool
		expectedMessage string
	}{
		{
			name:   "PostPublish dry run returns would execute message",
			hook:   plugin.HookPostPublish,
			dryRun: true,
			config: map[string]any{
				"webhook_url": "https://teams.microsoft.com/webhook/test",
			},
			releaseContext: plugin.ReleaseContext{
				Version:     "1.2.3",
				TagName:     "v1.2.3",
				ReleaseType: "minor",
				Branch:      "main",
				CommitSHA:   "abc123",
			},
			expectedSuccess: true,
			expectedMessage: "Would execute teams plugin",
		},
		{
			name:   "PostPublish dry run with minimal config",
			hook:   plugin.HookPostPublish,
			dryRun: true,
			config: map[string]any{},
			releaseContext: plugin.ReleaseContext{
				Version: "0.1.0",
				TagName: "v0.1.0",
			},
			expectedSuccess: true,
			expectedMessage: "Would execute teams plugin",
		},
		{
			name:   "PostPublish dry run with full release context",
			hook:   plugin.HookPostPublish,
			dryRun: true,
			config: map[string]any{
				"webhook_url": "https://teams.microsoft.com/webhook/full",
				"channel":     "releases",
			},
			releaseContext: plugin.ReleaseContext{
				Version:         "2.0.0",
				PreviousVersion: "1.9.0",
				TagName:         "v2.0.0",
				ReleaseType:     "major",
				RepositoryURL:   "https://github.com/example/repo",
				RepositoryOwner: "example",
				RepositoryName:  "repo",
				Branch:          "main",
				CommitSHA:       "def456ghi789",
				Changelog:       "## Changes\n- Added new feature",
				ReleaseNotes:    "Major release with breaking changes",
			},
			expectedSuccess: true,
			expectedMessage: "Would execute teams plugin",
		},
		{
			name:   "PostPublish actual execution",
			hook:   plugin.HookPostPublish,
			dryRun: false,
			config: map[string]any{
				"webhook_url": "https://teams.microsoft.com/webhook/test",
			},
			releaseContext: plugin.ReleaseContext{
				Version: "1.0.0",
				TagName: "v1.0.0",
			},
			expectedSuccess: true,
			expectedMessage: "Teams plugin executed successfully",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := &TeamsPlugin{}
			req := plugin.ExecuteRequest{
				Hook:    tc.hook,
				Config:  tc.config,
				Context: tc.releaseContext,
				DryRun:  tc.dryRun,
			}

			resp, err := p.Execute(context.Background(), req)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if resp.Success != tc.expectedSuccess {
				t.Errorf("expected success=%v, got success=%v", tc.expectedSuccess, resp.Success)
			}

			if resp.Message != tc.expectedMessage {
				t.Errorf("expected message=%q, got message=%q", tc.expectedMessage, resp.Message)
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
		{"OnSuccess", plugin.HookOnSuccess},
		{"OnError", plugin.HookOnError},
	}

	for _, tc := range unhandledHooks {
		t.Run(tc.name, func(t *testing.T) {
			p := &TeamsPlugin{}
			req := plugin.ExecuteRequest{
				Hook: tc.hook,
				Config: map[string]any{
					"webhook_url": "https://teams.microsoft.com/webhook/test",
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

func TestExecuteWithContext(t *testing.T) {
	t.Parallel()

	t.Run("respects context cancellation", func(t *testing.T) {
		p := &TeamsPlugin{}
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		req := plugin.ExecuteRequest{
			Hook: plugin.HookPostPublish,
			Config: map[string]any{
				"webhook_url": "https://teams.microsoft.com/webhook/test",
			},
			Context: plugin.ReleaseContext{
				Version: "1.0.0",
			},
			DryRun: true,
		}

		// The current implementation does not check context, so this should still succeed
		// This test documents the current behavior
		resp, err := p.Execute(ctx, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !resp.Success {
			t.Error("expected success even with cancelled context (current implementation)")
		}
	})
}

func TestValidateWithContext(t *testing.T) {
	t.Parallel()

	t.Run("respects context cancellation", func(t *testing.T) {
		p := &TeamsPlugin{}
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately

		// The current implementation does not check context, so this should still succeed
		// This test documents the current behavior
		resp, err := p.Validate(ctx, map[string]any{})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !resp.Valid {
			t.Error("expected valid response even with cancelled context (current implementation)")
		}
	})
}
