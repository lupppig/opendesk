// Package wiki is the team's internal wiki (contracts/wiki.md): pages nested under each
// other, with their history and search. Members read it; it is never public and never
// reaches customers, the chat widget or the email agent.
package wiki

import (
	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/useopendesk/opendesk/internal/deps"
)

// Register adds this package's routes.
func Register(d *deps.Deps, api huma.API, _ chi.Router) {
	registerPages(d, api)
	registerRevisions(d, api)
	registerSearch(d, api)
}
