package tickets

import (
	"context"
	"html"
	"strings"

	"github.com/useopendesk/opendesk/internal/ai/bots"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/templates"
)

// keyBot is the bot the calling API key speaks as (contracts/api.md "API keys"): what it
// writes on a ticket is shown and sent as that bot. Nil for anyone else, and for a key
// that writes as itself.
func keyBot(ctx context.Context, q db.Querier, p *auth.Principal) (*bots.Bot, error) {
	if p.Kind != auth.KindAPIKey {
		return nil, nil
	}
	return bots.ForAPIKey(ctx, q, p.WorkspaceID, p.APIKeyID)
}

// asKeyBot marks a message an API key writes: its key, and the bot it speaks as with the
// bot's name as the sender's.
func asKeyBot(nm *domain.NewMessage, p *auth.Principal, bot *bots.Bot) {
	nm.APIKeyID = p.ActorAPIKeyID()
	if bot != nil {
		nm.BotID, nm.FromName = bot.IDPtr(), bot.Name
	}
}

// withSignOff ends an email a bot sends with its sign-off (contracts/bots.md), as its own
// paragraph, unless the body already ends with it. The sign-off is the bot's own words,
// never a template: it is not filled, now or when the email is sent.
func withSignOff(bodyHTML, bodyText string, bot *bots.Bot) (string, string) {
	if bot == nil {
		return bodyHTML, bodyText
	}
	signOff := strings.TrimSpace(bot.Personality.SignOff)
	if signOff == "" || strings.HasSuffix(flat(bodyText), flat(signOff)) {
		return bodyHTML, bodyText
	}
	lines := strings.Split(strings.ReplaceAll(signOff, "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = templates.Inert(html.EscapeString(strings.TrimSpace(l)))
	}
	bodyHTML += "<p>" + strings.Join(lines, "<br>") + "</p>"
	return bodyHTML, hd.HTMLToText(bodyHTML)
}

// flat is text compared without case and with its whitespace collapsed.
func flat(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}
