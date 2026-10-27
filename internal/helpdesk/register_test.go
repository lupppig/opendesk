package helpdesk_test

import (
	"encoding/json"
	"testing"

	"github.com/riverqueue/river"

	"github.com/useopendesk/opendesk/internal/helpdesk"
	"github.com/useopendesk/opendesk/internal/helpdesk/hdtest"
)

// Every subpackage registers on one API, as in the server: operation ids and schema
// names must be unique across them, and every contract route must exist.
func TestRegisterAll(t *testing.T) {
	e := hdtest.New(t, helpdesk.Register)
	res := e.Do("GET", "/openapi.json", hdtest.Auth{}, nil).Want(t, 200)
	var spec struct {
		Paths map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(res.Body, &spec); err != nil {
		t.Fatal(err)
	}
	want := map[string][]string{
		"/v1/tickets":                      {"get", "post"},
		"/v1/tickets/counts":               {"get"},
		"/v1/tickets/summary":              {"get"},
		"/v1/tickets/{id}":                 {"get", "patch", "delete"},
		"/v1/tickets/bulk":                 {"post"},
		"/v1/tickets/{id}/merge":           {"post"},
		"/v1/tickets/{id}/messages":        {"get", "post"},
		"/v1/tickets/{id}/activities":      {"get"},
		"/v1/messages/{id}/original":       {"get"},
		"/v1/contacts":                     {"get", "post"},
		"/v1/contacts/{id}":                {"get", "patch", "delete"},
		"/v1/contacts/{id}/tickets":        {"get"},
		"/v1/companies":                    {"get", "post"},
		"/v1/companies/{id}":               {"get", "patch", "delete"},
		"/v1/tags":                         {"get", "post"},
		"/v1/tags/{id}":                    {"patch", "delete"},
		"/v1/saved-replies":                {"get", "post"},
		"/v1/saved-replies/{id}":           {"get", "patch", "delete"},
		"/v1/views":                        {"get", "post"},
		"/v1/views/{id}":                   {"patch", "delete"},
		"/v1/sla-policies":                 {"get", "post"},
		"/v1/sla-policies/{id}":            {"get", "patch", "delete"},
		"/v1/automations":                  {"get", "post"},
		"/v1/automations/{id}":             {"get", "patch", "delete"},
		"/v1/kb/collections":               {"get", "post"},
		"/v1/kb/collections/{id}":          {"get", "patch", "delete"},
		"/v1/kb/articles":                  {"get", "post"},
		"/v1/kb/articles/{id}":             {"get", "patch", "delete"},
		"/v1/wiki/pages":                   {"get", "post"},
		"/v1/wiki/pages/{id}":              {"get", "patch", "delete"},
		"/v1/wiki/pages/{id}/move":         {"post"},
		"/v1/wiki/pages/{id}/revisions":    {"get"},
		"/v1/wiki/search":                  {"get"},
		"/v1/search":                       {"get"},
		"/v1/reports/overview":             {"get"},
		"/v1/reports/agents":               {"get"},
		"/v1/attachments":                  {"post"},
		"/v1/public/help/{workspace_slug}": {"get"},
		"/v1/public/help/{workspace_slug}/collections/{slug}": {"get"},
		"/v1/public/help/{workspace_slug}/articles/{slug}":    {"get"},
		"/v1/public/help/{workspace_slug}/search":             {"get"},
		"/v1/public/csat/survey":                              {"post"},
		"/v1/public/csat/rating":                              {"post"},
		"/v1/widget/config":                                   {"get"},
		"/v1/widget/session":                                  {"post"},
		"/v1/widget/conversations":                            {"get", "post"},
		"/v1/widget/conversations/{id}/messages":              {"get", "post"},
		"/v1/widget/attachments":                              {"post"},
		"/v1/widget/settings":                                 {"get", "patch"},
		"/v1/contacts/{id}/avatar":                            {"put", "delete"},
		"/v1/contacts/{id}/avatar/refresh":                    {"post"},
		"/v1/companies/{id}/avatar":                           {"put", "delete"},
		"/v1/companies/{id}/avatar/refresh":                   {"post"},
		"/v1/assignment":                                      {"get", "patch"},
		"/v1/triage":                                          {"get", "patch"},
		"/v1/business-hours":                                  {"get", "put"},
		"/v1/brands/{id}/help-domain":                         {"put", "delete"},
		"/v1/brands/{id}/help-domain/check":                   {"post"},
		"/v1/imports":                                         {"get", "post"},
		"/v1/imports/check":                                   {"post"},
		"/v1/imports/csv":                                     {"post"},
		"/v1/imports/{id}":                                    {"get"},
		"/v1/imports/{id}/resume":                             {"post"},
		"/v1/imports/{id}/undo":                               {"post"},
		"/v1/imports/{id}/credentials":                        {"delete"},
		"/v1/wiki/pages/{id}/revisions/{revisionId}":          {"get"},
		"/v1/wiki/pages/{id}/revisions/{revisionId}/restore":  {"post"},
	}
	for path, methods := range want {
		for _, m := range methods {
			if _, ok := spec.Paths[path][m]; !ok {
				t.Errorf("missing %s %s", m, path)
			}
		}
	}
	// The download route is raw chi (redirect or stream), outside the spec.
	e.Do("GET", "/v1/attachments/00000000-0000-0000-0000-000000000000", hdtest.Auth{}, nil).Want(t, 404)
	e.Do("GET", "/v1/images/00000000-0000-0000-0000-000000000000", hdtest.Auth{}, nil).Want(t, 404)

	w := river.NewWorkers()
	helpdesk.RegisterWorkers(e.D, w)
	// The help domain and import sweeps run everywhere; the Cloudflare reconcile only with Cloudflare for SaaS.
	if n := len(helpdesk.Periodic(e.D)); n != 7 {
		t.Fatalf("periodic jobs %d", n)
	}
}
