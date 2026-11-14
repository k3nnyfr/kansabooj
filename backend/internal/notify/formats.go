package notify

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// payload renders an event for a notifier type.
func payload(typ, lang string, e *Event) ([]byte, error) {
	m := render(e, lang)
	switch typ {
	case "slack":
		return json.Marshal(slack(m))
	case "googlechat":
		return json.Marshal(googleChat(m, e))
	case "teams":
		return json.Marshal(teams(m))
	case "generic":
		return json.Marshal(struct {
			*Event
			Title    string `json:"title"`
			Summary  string `json:"summary"`
			Language string `json:"language"`
		}{e, m.Title, m.Subtitle, lang})
	}
	return nil, fmt.Errorf("type de notifier %q inconnu", typ)
}

// Slack incoming webhook, Block Kit.
func slack(m message) map[string]any {
	blocks := []any{
		map[string]any{"type": "header", "text": map[string]any{"type": "plain_text", "text": truncate(m.Title, 150)}},
		map[string]any{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": slackEscape(m.Subtitle)}},
	}
	if len(m.Lines) > 0 {
		var b strings.Builder
		for _, l := range m.Lines {
			b.WriteString("• " + slackEscape(l) + "\n")
		}
		blocks = append(blocks, map[string]any{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": truncate(b.String(), 3000)}})
	}
	if m.Footer != "" {
		blocks = append(blocks, map[string]any{"type": "context", "elements": []any{map[string]any{"type": "mrkdwn", "text": slackEscape(m.Footer)}}})
	}
	if m.Link != "" {
		blocks = append(blocks, map[string]any{"type": "actions", "elements": []any{
			map[string]any{"type": "button", "text": map[string]any{"type": "plain_text", "text": m.Button}, "url": m.Link},
		}})
	}
	return map[string]any{"text": m.Title, "blocks": blocks}
}

func slackEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// Google Chat incoming webhook, cardsV2.
func googleChat(m message, e *Event) map[string]any {
	widgets := []any{map[string]any{"textParagraph": map[string]any{"text": htmlEscape(m.Subtitle)}}}
	if len(m.Lines) > 0 {
		var b strings.Builder
		for _, l := range m.Lines {
			b.WriteString(htmlEscape(l) + "<br>")
		}
		widgets = append(widgets, map[string]any{"textParagraph": map[string]any{"text": b.String()}})
	}
	if m.Link != "" {
		widgets = append(widgets, map[string]any{"buttonList": map[string]any{"buttons": []any{
			map[string]any{"text": m.Button, "onClick": map[string]any{"openLink": map[string]any{"url": m.Link}}},
		}}})
	}
	return map[string]any{
		"text": m.Title,
		"cardsV2": []any{map[string]any{
			"cardId": fmt.Sprintf("kansabooj-%s-%s-%d", e.Kind, e.APIID, e.ChangeID),
			"card": map[string]any{
				"header":   map[string]any{"title": m.Title, "subtitle": m.Footer},
				"sections": []any{map[string]any{"widgets": widgets}},
			},
		}},
	}
}

func htmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// Microsoft Teams (Workflows / Power Automate webhook), Adaptive Card.
func teams(m message) map[string]any {
	body := []any{
		map[string]any{"type": "TextBlock", "text": m.Title, "weight": "Bolder", "size": "Medium", "wrap": true},
		map[string]any{"type": "TextBlock", "text": m.Subtitle, "wrap": true, "isSubtle": true},
	}
	for _, l := range m.Lines {
		body = append(body, map[string]any{"type": "TextBlock", "text": "- " + l, "wrap": true, "spacing": "None"})
	}
	if m.Footer != "" {
		body = append(body, map[string]any{"type": "TextBlock", "text": m.Footer, "isSubtle": true, "size": "Small"})
	}
	card := map[string]any{
		"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
		"type":    "AdaptiveCard", "version": "1.4", "body": body,
	}
	if m.Link != "" {
		card["actions"] = []any{map[string]any{"type": "Action.OpenUrl", "title": m.Button, "url": m.Link}}
	}
	return map[string]any{"type": "message", "attachments": []any{
		map[string]any{"contentType": "application/vnd.microsoft.card.adaptive", "content": card},
	}}
}

// Sign returns the X-Kansabooj-Signature header value for a generic webhook body.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n-1]) + "…"
	}
	return s
}
