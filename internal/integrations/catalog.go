package integrations

import (
	"os"
	"strings"
)

// Provider is one entry of the catalog: a vendor's own remote MCP server, connected in
// one step. Anything else connects as "custom" with a URL.
type Provider struct {
	Key         string `json:"key"`
	Name        string `json:"name"`
	Category    string `json:"category" enum:"issues,docs,chat,crm,payments,automation,files"`
	Description string `json:"description"`
	URL         string `json:"url"`
	// Auth is how the workspace signs in: "oauth" (the server's own sign-in) or
	// "token" (an access token the admin pastes).
	Auth string `json:"auth" enum:"oauth,token"`
	// TokenHelp says where to create the token (auth "token").
	TokenHelp string `json:"token_help,omitempty"`
	TokenURL  string `json:"token_url,omitempty"`
	// Hosts are where this provider's links live; a link Ollie creates on one of them
	// is attached to the ticket it was made from.
	Hosts []string `json:"-"`
	// AppSignIn means the server signs in only with an app the operator registered in
	// advance (MCP_<KEY>_CLIENT_ID): with one it is "oauth", without one Auth applies.
	AppSignIn bool `json:"-"`
	// SecretInForm sends the registered app's secret as form fields at the token
	// endpoint, for servers that publish no client authentication methods.
	SecretInForm bool `json:"-"`
	// Starters are one-click asks offered on a ticket once connected.
	Starters []Starter `json:"starters"`
}

// Starter is a ready-made request to Ollie about the open ticket.
type Starter struct {
	Label  string `json:"label"`
	Prompt string `json:"prompt"`
}

const linkBack = " Link it to this ticket when it is created."

// Catalog is in display order. URLs are the vendors' documented endpoints.
var Catalog = []Provider{
	{Key: "linear", Name: "Linear", Category: "issues", Description: "Create and update issues, and look up projects and cycles.",
		URL: "https://mcp.linear.app/mcp", Auth: "oauth", Hosts: []string{"linear.app"},
		Starters: []Starter{
			{"Create a Linear issue", "Create a Linear issue from this ticket: a short title, what the customer reported, steps to reproduce if known, and the impact." + linkBack},
			{"Find related Linear issues", "Search Linear for issues related to this ticket and tell me if one already covers it."},
		}},
	{Key: "github", Name: "GitHub", Category: "issues", Description: "Open issues, read pull requests and search code.",
		URL: "https://api.githubcopilot.com/mcp/", Auth: "token", Hosts: []string{"github.com"}, AppSignIn: true, SecretInForm: true,
		TokenHelp: "A fine-grained personal access token with access to the repositories Ollie may use.",
		TokenURL:  "https://github.com/settings/personal-access-tokens/new",
		Starters: []Starter{
			{"Open a GitHub issue", "Open a GitHub issue for this ticket in the right repository: a clear title, what happened, what was expected, and steps to reproduce." + linkBack},
			{"Find related GitHub issues", "Search GitHub issues and pull requests related to this ticket."},
		}},
	{Key: "atlassian", Name: "Jira and Confluence", Category: "issues", Description: "Create Jira issues and search Confluence pages.",
		URL: "https://mcp.atlassian.com/v1/mcp", Auth: "oauth", Hosts: []string{"atlassian.net"},
		Starters: []Starter{
			{"Create a Jira issue", "Create a Jira issue from this ticket with a clear summary, description and the customer's impact." + linkBack},
			{"Search Confluence", "Search Confluence for pages that help answer this ticket."},
		}},
	{Key: "sentry", Name: "Sentry", Category: "issues", Description: "Look up errors, their frequency and the releases they appeared in.",
		URL: "https://mcp.sentry.dev/mcp", Auth: "oauth", Hosts: []string{"sentry.io"},
		Starters: []Starter{{"Find the error in Sentry", "Look in Sentry for errors that match what this customer describes and summarize what you find."}}},
	{Key: "notion", Name: "Notion", Category: "docs", Description: "Search pages and add notes to your workspace.",
		URL: "https://mcp.notion.com/mcp", Auth: "oauth", Hosts: []string{"notion.so", "notion.site"},
		Starters: []Starter{{"Search Notion", "Search Notion for internal docs that help answer this ticket."}}},
	{Key: "asana", Name: "Asana", Category: "issues", Description: "Create tasks and follow up on projects.",
		URL: "https://mcp.asana.com/sse", Auth: "oauth", Hosts: []string{"app.asana.com"},
		Starters: []Starter{{"Create an Asana task", "Create an Asana task for the follow-up this ticket needs." + linkBack}}},
	{Key: "monday", Name: "monday.com", Category: "issues", Description: "Create items on boards and update their status.",
		URL: "https://mcp.monday.com/sse", Auth: "oauth", Hosts: []string{"monday.com"},
		Starters: []Starter{{"Create a monday.com item", "Create a monday.com item for this ticket on the right board." + linkBack}}},
	{Key: "slack", Name: "Slack", Category: "chat", Description: "Post to channels and search conversations.",
		URL: "https://mcp.slack.com/mcp", Auth: "oauth", Hosts: []string{"slack.com"},
		Starters: []Starter{{"Ask the team in Slack", "Post a short summary of this ticket to the right Slack channel and ask the team for help."}}},
	{Key: "intercom", Name: "Intercom", Category: "chat", Description: "Look up conversations and contacts in Intercom.",
		URL: "https://mcp.intercom.com/mcp", Auth: "oauth",
		Starters: []Starter{{"Check Intercom history", "Look up this customer in Intercom and summarize their past conversations."}}},
	{Key: "close", Name: "Close", Category: "crm", Description: "Look up leads, deals and activity in your CRM.",
		URL: "https://mcp.close.com/mcp", Auth: "oauth",
		Starters: []Starter{{"Check the customer in Close", "Look up this customer in Close and tell me about their account and open deals."}}},
	{Key: "stripe", Name: "Stripe", Category: "payments", Description: "Look up customers, subscriptions, invoices and refunds.",
		URL: "https://mcp.stripe.com", Auth: "oauth", Hosts: []string{"dashboard.stripe.com"},
		Starters: []Starter{{"Check billing in Stripe", "Look up this customer in Stripe and summarize their subscription, recent invoices and any failed payments."}}},
	{Key: "paypal", Name: "PayPal", Category: "payments", Description: "Look up transactions and invoices.",
		URL: "https://mcp.paypal.com/mcp", Auth: "oauth",
		Starters: []Starter{{"Check PayPal transactions", "Look up this customer's recent PayPal transactions."}}},
	{Key: "square", Name: "Square", Category: "payments", Description: "Look up orders, payments and customers.",
		URL: "https://mcp.squareup.com/sse", Auth: "oauth",
		Starters: []Starter{{"Check Square orders", "Look up this customer's recent Square orders and payments."}}},
	{Key: "box", Name: "Box", Category: "files", Description: "Search and read files.",
		URL: "https://mcp.box.com/", Auth: "oauth", Hosts: []string{"app.box.com"},
		Starters: []Starter{{"Search Box", "Search Box for files that help answer this ticket."}}},
	{Key: "zapier", Name: "Zapier", Category: "automation", Description: "Reach thousands of apps through the actions you enable in Zapier.",
		URL: "https://mcp.zapier.com/api/mcp/mcp", Auth: "oauth",
		Starters: []Starter{}},
}

// CustomKey is the provider of an integration added by URL.
const CustomKey = "custom"

func provider(key string) (Provider, bool) {
	for _, p := range Catalog {
		if p.Key == key {
			return resolved(p), true
		}
	}
	return Provider{}, false
}

// catalog is the Catalog as this instance offers it.
func catalog() []Provider {
	out := make([]Provider, len(Catalog))
	for i, p := range Catalog {
		out[i] = resolved(p)
	}
	return out
}

// resolved applies the instance's registered apps: a provider that signs in with
// one does so once it is set, and asks for a token until then.
func resolved(p Provider) Provider {
	if p.AppSignIn {
		if id, _ := preRegistered(p.Key); id != "" {
			p.Auth, p.TokenHelp, p.TokenURL = "oauth", "", ""
		}
	}
	return p
}

// preRegistered is an OAuth client an operator registered with a provider ahead of
// time (MCP_<KEY>_CLIENT_ID and MCP_<KEY>_CLIENT_SECRET), for servers that do not
// accept client metadata documents or dynamic registration.
func preRegistered(key string) (id, secret string) {
	k := "MCP_" + strings.ToUpper(key)
	return strings.TrimSpace(os.Getenv(k + "_CLIENT_ID")), strings.TrimSpace(os.Getenv(k + "_CLIENT_SECRET"))
}
