// Package main implements the Teams plugin for Relicta.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/relicta-tech/relicta-plugin-sdk/helpers"
	"github.com/relicta-tech/relicta-plugin-sdk/plugin"
)

// HTTPClient interface for testability.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// Shared HTTP client for connection reuse across requests.
// Includes security hardening: TLS 1.3+, redirect protection, SSRF prevention.
var defaultHTTPClient HTTPClient = &http.Client{
	Timeout: 10 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		// Limit redirect chain length
		if len(via) >= 3 {
			return fmt.Errorf("too many redirects")
		}
		// Prevent redirect to non-HTTPS
		if req.URL.Scheme != "https" {
			return fmt.Errorf("redirect to non-HTTPS URL not allowed")
		}
		// Prevent redirect away from Microsoft domains (SSRF protection)
		if !isValidMicrosoftHost(req.URL.Host) {
			return fmt.Errorf("redirect away from Microsoft domains not allowed")
		}
		return nil
	},
	Transport: &http.Transport{
		MaxIdleConns:        10,
		MaxIdleConnsPerHost: 5,
		IdleConnTimeout:     90 * time.Second,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13,
		},
	},
}

// TeamsPlugin implements the Microsoft Teams notification plugin.
type TeamsPlugin struct {
	httpClient HTTPClient
}

// Config represents the Teams plugin configuration.
type Config struct {
	// WebhookURL is the Teams incoming webhook URL.
	WebhookURL string `json:"webhook_url,omitempty"`
	// TitleTemplate is the template for the card title (default: "Release {{version}}").
	TitleTemplate string `json:"title_template,omitempty"`
	// IncludeChangelog includes changelog in the notification.
	IncludeChangelog bool `json:"include_changelog"`
	// ThemeColor is the accent color for the card (default: "0076D7" - Teams blue).
	ThemeColor string `json:"theme_color,omitempty"`
	// MentionUsers is a list of user emails to @mention.
	MentionUsers []string `json:"mention_users,omitempty"`
	// NotifyOnSuccess sends notification on successful release.
	NotifyOnSuccess bool `json:"notify_on_success"`
	// NotifyOnError sends notification on failed release.
	NotifyOnError bool `json:"notify_on_error"`
}

// TeamsMessage represents a Microsoft Teams message payload with Adaptive Card.
type TeamsMessage struct {
	Type        string            `json:"type"`
	Attachments []TeamsAttachment `json:"attachments"`
}

// TeamsAttachment represents an attachment in a Teams message.
type TeamsAttachment struct {
	ContentType string       `json:"contentType"`
	ContentURL  *string      `json:"contentUrl,omitempty"`
	Content     AdaptiveCard `json:"content"`
}

// AdaptiveCard represents a Microsoft Adaptive Card.
type AdaptiveCard struct {
	Type    string            `json:"type"`
	Version string            `json:"version"`
	Schema  string            `json:"$schema"`
	Body    []AdaptiveElement `json:"body"`
	Actions []AdaptiveAction  `json:"actions,omitempty"`
	MSTeams *MSTeamsConfig    `json:"msteams,omitempty"`
}

// AdaptiveElement represents an element in an Adaptive Card body.
type AdaptiveElement struct {
	Type      string            `json:"type"`
	Text      string            `json:"text,omitempty"`
	Weight    string            `json:"weight,omitempty"`
	Size      string            `json:"size,omitempty"`
	Wrap      bool              `json:"wrap,omitempty"`
	Color     string            `json:"color,omitempty"`
	Style     string            `json:"style,omitempty"`
	Bleed     bool              `json:"bleed,omitempty"`
	Separator bool              `json:"separator,omitempty"`
	Spacing   string            `json:"spacing,omitempty"`
	Items     []AdaptiveElement `json:"items,omitempty"`
	Columns   []ColumnDefinition`json:"columns,omitempty"`
}

// ColumnDefinition represents a column in a ColumnSet.
type ColumnDefinition struct {
	Type  string            `json:"type"`
	Width string            `json:"width"`
	Items []AdaptiveElement `json:"items"`
}

// AdaptiveAction represents an action in an Adaptive Card.
type AdaptiveAction struct {
	Type  string `json:"type"`
	Title string `json:"title"`
	URL   string `json:"url,omitempty"`
}

// MSTeamsConfig represents Teams-specific configuration.
type MSTeamsConfig struct {
	Width    string        `json:"width,omitempty"`
	Entities []TeamsEntity `json:"entities,omitempty"`
}

// TeamsEntity represents a Teams entity (like a mention).
type TeamsEntity struct {
	Type      string              `json:"type"`
	Text      string              `json:"text"`
	Mentioned *TeamsMentionedUser `json:"mentioned"`
}

// TeamsMentionedUser represents a mentioned user in Teams.
type TeamsMentionedUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Default values for configuration.
const (
	DefaultTitleTemplate = "Release {{version}}"
	DefaultThemeColor    = "0076D7" // Teams blue
	ColorSuccess         = "28A745" // Green
	ColorError           = "DC3545" // Red
)

// GetInfo returns plugin metadata.
func (p *TeamsPlugin) GetInfo() plugin.Info {
	return plugin.Info{
		Name:        "teams",
		Version:     "2.0.0",
		Description: "Send release notifications to Microsoft Teams",
		Author:      "Relicta Team",
		Hooks: []plugin.Hook{
			plugin.HookPostPublish,
			plugin.HookOnSuccess,
			plugin.HookOnError,
		},
		ConfigSchema: `{
			"type": "object",
			"properties": {
				"webhook_url": {"type": "string", "description": "Teams incoming webhook URL (or use TEAMS_WEBHOOK_URL env)"},
				"title_template": {"type": "string", "description": "Template for card title", "default": "Release {{version}}"},
				"include_changelog": {"type": "boolean", "description": "Include changelog in message", "default": true},
				"theme_color": {"type": "string", "description": "Accent color for the card (hex without #)", "default": "0076D7"},
				"mention_users": {"type": "array", "items": {"type": "string"}, "description": "User emails to @mention"},
				"notify_on_success": {"type": "boolean", "description": "Notify on success", "default": true},
				"notify_on_error": {"type": "boolean", "description": "Notify on error", "default": true}
			},
			"required": ["webhook_url"]
		}`,
	}
}

// Execute runs the plugin for a given hook.
func (p *TeamsPlugin) Execute(ctx context.Context, req plugin.ExecuteRequest) (*plugin.ExecuteResponse, error) {
	cfg := p.parseConfig(req.Config)

	switch req.Hook {
	case plugin.HookPostPublish, plugin.HookOnSuccess:
		if !cfg.NotifyOnSuccess {
			return &plugin.ExecuteResponse{
				Success: true,
				Message: "Success notification disabled",
			}, nil
		}
		return p.sendSuccessNotification(ctx, cfg, req.Context, req.DryRun)

	case plugin.HookOnError:
		if !cfg.NotifyOnError {
			return &plugin.ExecuteResponse{
				Success: true,
				Message: "Error notification disabled",
			}, nil
		}
		return p.sendErrorNotification(ctx, cfg, req.Context, req.DryRun)

	default:
		return &plugin.ExecuteResponse{
			Success: true,
			Message: fmt.Sprintf("Hook %s not handled", req.Hook),
		}, nil
	}
}

// sendSuccessNotification sends a success notification to Teams.
func (p *TeamsPlugin) sendSuccessNotification(ctx context.Context, cfg *Config, releaseCtx plugin.ReleaseContext, dryRun bool) (*plugin.ExecuteResponse, error) {
	title := p.buildTitle(cfg.TitleTemplate, releaseCtx.Version)

	// Build card body elements
	body := []AdaptiveElement{
		{
			Type:   "TextBlock",
			Text:   title,
			Weight: "bolder",
			Size:   "large",
			Color:  "good",
		},
	}

	// Add version info container
	infoItems := []AdaptiveElement{
		{
			Type: "ColumnSet",
			Columns: []ColumnDefinition{
				{
					Type:  "Column",
					Width: "auto",
					Items: []AdaptiveElement{
						{Type: "TextBlock", Text: "Version:", Weight: "bolder"},
						{Type: "TextBlock", Text: "Type:", Weight: "bolder"},
						{Type: "TextBlock", Text: "Branch:", Weight: "bolder"},
						{Type: "TextBlock", Text: "Tag:", Weight: "bolder"},
					},
				},
				{
					Type:  "Column",
					Width: "stretch",
					Items: []AdaptiveElement{
						{Type: "TextBlock", Text: releaseCtx.Version},
						{Type: "TextBlock", Text: cases.Title(language.English).String(releaseCtx.ReleaseType)},
						{Type: "TextBlock", Text: releaseCtx.Branch},
						{Type: "TextBlock", Text: releaseCtx.TagName},
					},
				},
			},
		},
	}
	body = append(body, infoItems...)

	// Add changes summary if available
	if releaseCtx.Changes != nil {
		features := len(releaseCtx.Changes.Features)
		fixes := len(releaseCtx.Changes.Fixes)
		breaking := len(releaseCtx.Changes.Breaking)

		summary := fmt.Sprintf("%d features, %d fixes", features, fixes)
		if breaking > 0 {
			summary += fmt.Sprintf(", **%d breaking changes**", breaking)
		}

		body = append(body, AdaptiveElement{
			Type:      "TextBlock",
			Text:      "Changes: " + summary,
			Separator: true,
			Spacing:   "medium",
		})
	}

	// Add changelog if enabled
	if cfg.IncludeChangelog && releaseCtx.ReleaseNotes != "" {
		notes := releaseCtx.ReleaseNotes
		// Truncate if too long (Teams has limits on card size)
		if len(notes) > 2000 {
			notes = notes[:2000] + "..."
		}
		// Escape HTML to prevent XSS attacks
		notes = html.EscapeString(notes)

		body = append(body, AdaptiveElement{
			Type:      "TextBlock",
			Text:      notes,
			Wrap:      true,
			Separator: true,
			Spacing:   "medium",
		})
	}

	// Add mention text if users specified
	if len(cfg.MentionUsers) > 0 {
		mentionText := p.buildMentionText(cfg.MentionUsers)
		body = append(body, AdaptiveElement{
			Type:    "TextBlock",
			Text:    mentionText,
			Spacing: "medium",
		})
	}

	// Build actions
	var actions []AdaptiveAction
	if releaseCtx.RepositoryURL != "" && releaseCtx.TagName != "" {
		releaseURL := fmt.Sprintf("%s/releases/tag/%s", strings.TrimSuffix(releaseCtx.RepositoryURL, ".git"), releaseCtx.TagName)
		actions = append(actions, AdaptiveAction{
			Type:  "Action.OpenUrl",
			Title: "View Release",
			URL:   releaseURL,
		})
	}

	// Build the message
	msg := p.buildTeamsMessage(body, actions, cfg.MentionUsers, ColorSuccess)

	if dryRun {
		return &plugin.ExecuteResponse{
			Success: true,
			Message: "Would send Teams success notification",
			Outputs: map[string]any{
				"version": releaseCtx.Version,
			},
		}, nil
	}

	if err := p.sendMessage(ctx, cfg.WebhookURL, msg); err != nil {
		return &plugin.ExecuteResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to send Teams message: %v", err),
		}, nil
	}

	return &plugin.ExecuteResponse{
		Success: true,
		Message: "Sent Teams success notification",
	}, nil
}

// sendErrorNotification sends an error notification to Teams.
func (p *TeamsPlugin) sendErrorNotification(ctx context.Context, cfg *Config, releaseCtx plugin.ReleaseContext, dryRun bool) (*plugin.ExecuteResponse, error) {
	title := fmt.Sprintf("Release %s Failed", releaseCtx.Version)

	// Build card body elements
	body := []AdaptiveElement{
		{
			Type:   "TextBlock",
			Text:   title,
			Weight: "bolder",
			Size:   "large",
			Color:  "attention",
		},
		{
			Type: "ColumnSet",
			Columns: []ColumnDefinition{
				{
					Type:  "Column",
					Width: "auto",
					Items: []AdaptiveElement{
						{Type: "TextBlock", Text: "Version:", Weight: "bolder"},
						{Type: "TextBlock", Text: "Branch:", Weight: "bolder"},
					},
				},
				{
					Type:  "Column",
					Width: "stretch",
					Items: []AdaptiveElement{
						{Type: "TextBlock", Text: releaseCtx.Version},
						{Type: "TextBlock", Text: releaseCtx.Branch},
					},
				},
			},
		},
	}

	// Add mention text if users specified
	if len(cfg.MentionUsers) > 0 {
		mentionText := p.buildMentionText(cfg.MentionUsers)
		body = append(body, AdaptiveElement{
			Type:    "TextBlock",
			Text:    mentionText,
			Spacing: "medium",
		})
	}

	msg := p.buildTeamsMessage(body, nil, cfg.MentionUsers, ColorError)

	if dryRun {
		return &plugin.ExecuteResponse{
			Success: true,
			Message: "Would send Teams error notification",
		}, nil
	}

	if err := p.sendMessage(ctx, cfg.WebhookURL, msg); err != nil {
		return &plugin.ExecuteResponse{
			Success: false,
			Error:   fmt.Sprintf("failed to send Teams message: %v", err),
		}, nil
	}

	return &plugin.ExecuteResponse{
		Success: true,
		Message: "Sent Teams error notification",
	}, nil
}

// buildTeamsMessage builds the complete Teams message with Adaptive Card.
func (p *TeamsPlugin) buildTeamsMessage(body []AdaptiveElement, actions []AdaptiveAction, mentionUsers []string, _ string) TeamsMessage {
	card := AdaptiveCard{
		Type:    "AdaptiveCard",
		Version: "1.2",
		Schema:  "http://adaptivecards.io/schemas/adaptive-card.json",
		Body:    body,
		Actions: actions,
	}

	// Add Teams-specific entities for mentions
	if len(mentionUsers) > 0 {
		entities := make([]TeamsEntity, 0, len(mentionUsers))
		for _, email := range mentionUsers {
			entities = append(entities, TeamsEntity{
				Type: "mention",
				Text: fmt.Sprintf("<at>%s</at>", email),
				Mentioned: &TeamsMentionedUser{
					ID:   email,
					Name: email,
				},
			})
		}
		card.MSTeams = &MSTeamsConfig{
			Width:    "Full",
			Entities: entities,
		}
	}

	return TeamsMessage{
		Type: "message",
		Attachments: []TeamsAttachment{
			{
				ContentType: "application/vnd.microsoft.card.adaptive",
				Content:     card,
			},
		},
	}
}

// buildTitle builds the card title from template.
func (p *TeamsPlugin) buildTitle(template, version string) string {
	if template == "" {
		template = DefaultTitleTemplate
	}
	return strings.ReplaceAll(template, "{{version}}", version)
}

// buildMentionText builds the mention text for users.
func (p *TeamsPlugin) buildMentionText(users []string) string {
	if len(users) == 0 {
		return ""
	}

	var mentions []string
	for _, user := range users {
		mentions = append(mentions, fmt.Sprintf("<at>%s</at>", user))
	}
	return "cc: " + strings.Join(mentions, " ")
}

// sendMessage sends a message to Teams.
func (p *TeamsPlugin) sendMessage(ctx context.Context, webhookURL string, msg TeamsMessage) error {
	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, webhookURL, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	client := p.getHTTPClient()
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Teams returns 200 OK on success
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("teams returned status %d", resp.StatusCode)
	}

	return nil
}

// getHTTPClient returns the HTTP client to use.
func (p *TeamsPlugin) getHTTPClient() HTTPClient {
	if p.httpClient != nil {
		return p.httpClient
	}
	return defaultHTTPClient
}

// parseConfig parses the plugin configuration.
func (p *TeamsPlugin) parseConfig(raw map[string]any) *Config {
	parser := helpers.NewConfigParser(raw)

	return &Config{
		WebhookURL:       parser.GetString("webhook_url", "TEAMS_WEBHOOK_URL", ""),
		TitleTemplate:    parser.GetString("title_template", "", DefaultTitleTemplate),
		IncludeChangelog: parser.GetBool("include_changelog", true),
		ThemeColor:       parser.GetString("theme_color", "", DefaultThemeColor),
		MentionUsers:     parser.GetStringSlice("mention_users", nil),
		NotifyOnSuccess:  parser.GetBool("notify_on_success", true),
		NotifyOnError:    parser.GetBool("notify_on_error", true),
	}
}

// isValidMicrosoftHost checks if the host is a valid Microsoft domain for webhooks.
func isValidMicrosoftHost(host string) bool {
	// Strip port if present (e.g., "prod-00.logic.azure.com:443" -> "prod-00.logic.azure.com")
	hostname := host
	if colonIdx := strings.LastIndex(host, ":"); colonIdx != -1 {
		// Check if this looks like a port (not an IPv6 address)
		if !strings.Contains(host, "[") {
			hostname = host[:colonIdx]
		}
	}

	// Valid domains for Teams webhooks
	validSuffixes := []string{
		".webhook.office.com",
		".logic.azure.com",
	}

	for _, suffix := range validSuffixes {
		if strings.HasSuffix(hostname, suffix) {
			return true
		}
	}
	return false
}

// validateTeamsWebhookURL validates a Microsoft Teams webhook URL.
func validateTeamsWebhookURL(webhookURL string) error {
	if webhookURL == "" {
		return fmt.Errorf("webhook URL is required")
	}

	parsed, err := url.Parse(webhookURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	if parsed.Scheme != "https" {
		return fmt.Errorf("webhook URL must use HTTPS")
	}

	if !isValidMicrosoftHost(parsed.Host) {
		return fmt.Errorf("webhook URL must be on *.webhook.office.com or *.logic.azure.com domain")
	}

	return nil
}

// Validate validates the plugin configuration.
func (p *TeamsPlugin) Validate(_ context.Context, config map[string]any) (*plugin.ValidateResponse, error) {
	vb := helpers.NewValidationBuilder()

	// Get webhook URL with env fallback
	parser := helpers.NewConfigParser(config)
	webhook := parser.GetString("webhook_url", "TEAMS_WEBHOOK_URL", "")

	// Check environment fallback if not in config
	if webhook == "" {
		webhook = os.Getenv("TEAMS_WEBHOOK_URL")
	}

	if webhook == "" {
		vb.AddErrorWithCode("webhook_url",
			"Teams webhook URL is required (set TEAMS_WEBHOOK_URL env var or configure webhook_url)",
			"required")
	} else {
		if err := validateTeamsWebhookURL(webhook); err != nil {
			vb.AddErrorWithCode("webhook_url", err.Error(), "format")
		}
	}

	// Validate theme_color if provided
	themeColor := parser.GetString("theme_color", "", "")
	if themeColor != "" {
		// Remove # if present
		themeColor = strings.TrimPrefix(themeColor, "#")
		if len(themeColor) != 6 {
			vb.AddErrorWithCode("theme_color", "theme_color must be a 6-character hex color (e.g., '0076D7')", "format")
		} else {
			// Check if valid hex
			for _, c := range themeColor {
				isDigit := c >= '0' && c <= '9'
				isLowerHex := c >= 'a' && c <= 'f'
				isUpperHex := c >= 'A' && c <= 'F'
				if !isDigit && !isLowerHex && !isUpperHex {
					vb.AddErrorWithCode("theme_color", "theme_color must contain only hexadecimal characters", "format")
					break
				}
			}
		}
	}

	return vb.Build(), nil
}
