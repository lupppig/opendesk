// Package fake is an in-memory Voys API for tests: token checks, the user's customer,
// caller ID numbers and Voys Intelligence transcripts and summaries. Tests never call the
// real Voys.
package fake

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Client is the customer every token of the fake belongs to, unless it is a partner's.
const Client = "b2a1430d-00fc-40c2-9882-93eab4df58fe"

// Voys is the fake API. Set Limit to answer 429 to everything.
type Voys struct {
	Limit bool

	mu          sync.Mutex
	srv         *httptest.Server
	tokens      map[string]user
	transcripts map[string]string
	summaries   map[string]string
	requests    []string
}

type user struct {
	uuid, client, name string
}

// New starts a fake answering to token, a user of the customer "Lamp & Twist BV".
func New(t testing.TB, token string) *Voys {
	f := &Voys{tokens: map[string]user{token: {"b9e3af06-62c5-4922-aba0-177df05c62f1", Client, "Lamp & Twist BV"}},
		transcripts: map[string]string{}, summaries: map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

// API is the platform API base, the Adapter's APIURL.
func (f *Voys) API() string { return f.srv.URL + "/api/v2" }

// Storage is the transcription storage base, the Adapter's StorageURL.
func (f *Voys) Storage() string { return f.srv.URL + "/transcription-storage" }

// AddPartner adds a token of a partner user, who belongs to no customer.
func (f *Voys) AddPartner(token string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens[token] = user{uuid: "19bbd1d8-8cd2-45bc-ac3a-a8e14e353f4f"}
}

// AddClient adds a token of a user of another customer.
func (f *Voys) AddClient(token, client, name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens[token] = user{"0596da13-824e-4ed5-a1cc-713709e0750f", client, name}
}

// SetIntelligence stores a call's transcript and summary, as Voys Intelligence does after it.
func (f *Voys) SetIntelligence(callID, transcript, summary string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.transcripts[callID], f.summaries[callID] = transcript, summary
}

// Requests returns "METHOD /path" of every request so far.
func (f *Voys) Requests() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.requests...)
}

func (f *Voys) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
	if f.Limit {
		reply(w, http.StatusTooManyRequests, map[string]any{"detail": "Request was throttled."})
		return
	}
	u, ok := f.tokens[strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")]
	if !ok || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") {
		reply(w, http.StatusUnauthorized, map[string]any{"code": "unauthorized", "message": "You are not authorized to perform this action."})
		return
	}
	path := r.URL.Path
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	switch {
	case path == "/api/v2/users/auth-context":
		var client any
		if u.client != "" {
			client = u.client
		}
		reply(w, 200, map[string]any{"uuid": u.uuid, "client_uuid": client, "partner_uuid": nil, "is_superuser": false,
			"permissions": []string{"phoneaccount.view_phoneaccount"}})
	case path == "/api/v2/user/details":
		// Voys redirects to the user's own details; opendesk's client does not follow it.
		http.Redirect(w, r, "/api/v2/user/"+u.uuid+"/details", http.StatusFound)
	case path == "/api/v2/user/"+u.uuid+"/details":
		reply(w, 200, map[string]any{"id": "668679", "uuid": u.uuid, "client": map[string]any{"uuid": u.client, "name": u.name}})
	case path == "/api/v2/clients/"+u.client+"/callerid_numbers" && u.client != "":
		if r.Header.Get("Accept") != "application/json;version=2" {
			reply(w, 200, map[string]any{"items": []string{"+31332022074", "+31332022075"}, "count": 2})
			return
		}
		page := r.URL.Query().Get("page")
		if page == "" || page == "1" {
			reply(w, 200, map[string]any{"items": []map[string]any{{"number": "+31332022074", "description": "Support desk"}},
				"next": f.srv.URL + path + "?page=2", "count": 2})
			return
		}
		reply(w, 200, map[string]any{"items": []map[string]any{{"number": "+31332022075", "description": "Sales"}, {"number": "610", "description": "Jane"}},
			"count": 2})
	case strings.HasPrefix(path, "/transcription-storage/clients/"):
		rest := strings.TrimPrefix(path, "/transcription-storage/clients/")
		parts := strings.Split(rest, "/")
		if len(parts) != 4 || parts[1] != "calls" {
			reply(w, 404, map[string]any{"detail": "Not found"})
			return
		}
		if parts[0] != u.client {
			reply(w, 403, map[string]any{"detail": "Forbidden"})
			return
		}
		switch parts[3] {
		case "transcriptions":
			text, ok := f.transcripts[parts[2]]
			if !ok {
				reply(w, 404, map[string]any{"detail": "Not found"})
				return
			}
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte(text))
		case "summaries":
			text, ok := f.summaries[parts[2]]
			if !ok {
				reply(w, 404, map[string]any{"detail": "Not found"})
				return
			}
			reply(w, 200, map[string]any{"summary": text})
		default:
			reply(w, 404, map[string]any{"detail": "Not found"})
		}
	default:
		reply(w, 404, map[string]any{"code": "not_found", "message": "Not found."})
	}
}

func reply(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
