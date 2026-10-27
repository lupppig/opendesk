package ai

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// fakeTypeSafe mimics TypeSafe's POST /v1/systemone (Jev). Answers default by question
// id (triage, ai_decide, widget relevance) and can be overridden per id.
type fakeTypeSafe struct {
	t   testing.TB
	srv *httptest.Server

	mu        sync.Mutex
	overrides map[string]map[string]any
	failures  []int
	requests  []tsRecorded
}

type tsRecorded struct {
	Auth      string
	Model     string
	State     map[string]any
	Questions map[string]map[string]any
}

func newFakeTypeSafe(t testing.TB) *fakeTypeSafe {
	f := &fakeTypeSafe{t: t, overrides: map[string]map[string]any{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeTypeSafe) set(id string, answer map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.overrides[id] = answer
}

func (f *fakeTypeSafe) fail(statuses ...int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures = append(f.failures, statuses...)
}

func (f *fakeTypeSafe) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.requests)
}

func (f *fakeTypeSafe) last() tsRecorded {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		return tsRecorded{}
	}
	return f.requests[len(f.requests)-1]
}

func choiceAns(choice string, p, conf float64, keys []string) map[string]any {
	probs := map[string]float64{}
	for _, k := range keys {
		probs[k] = (1 - p) / float64(max(len(keys)-1, 1))
	}
	probs[choice] = p
	return map[string]any{"type": "choice", "choice": choice, "probabilities": probs, "confidence": conf}
}

func scoreAns(level, n int, p, conf float64) map[string]any {
	probs := map[string]float64{}
	for i := 0; i < n; i++ {
		probs[strconv.Itoa(i)] = (1 - p) / float64(n-1)
	}
	probs[strconv.Itoa(level)] = p
	return map[string]any{"type": "score", "score": float64(level), "probabilities": probs, "confidence": conf}
}

func noulAns(p float64) map[string]any { return map[string]any{"type": "noul", "noul": p} }

// optionKeys are a choice's option keys in wire order.
func optionKeys(raw json.RawMessage) []string {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	var keys []string
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil
	}
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return keys
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if dec.Decode(&skip) != nil {
			return keys
		}
	}
	return keys
}

func (f *fakeTypeSafe) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	// TypeSafe's own path, or OpenRouter's Decisions API (the same request).
	if r.URL.Path != "/v1/systemone" && !strings.HasSuffix(r.URL.Path, "/alpha/decisions") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var req struct {
		Model     string                     `json:"model"`
		State     map[string]any             `json:"state"`
		Questions map[string]json.RawMessage `json:"questions"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	}
	rec := tsRecorded{Auth: r.Header.Get("Authorization"), Model: req.Model, State: req.State, Questions: map[string]map[string]any{}}
	f.mu.Lock()
	f.requests = append(f.requests, rec)
	if len(f.failures) > 0 {
		st := f.failures[0]
		f.failures = f.failures[1:]
		f.mu.Unlock()
		w.Header().Set("retry-after-ms", "1")
		w.WriteHeader(st)
		return
	}
	overrides := f.overrides
	f.mu.Unlock()
	answers := map[string]any{}
	for id, q := range req.Questions {
		var shape struct {
			Type     string          `json:"type"`
			Criteria json.RawMessage `json:"criteria"`
		}
		_ = json.Unmarshal(q, &shape)
		var parsed map[string]any
		_ = json.Unmarshal(q, &parsed)
		f.mu.Lock()
		f.requests[len(f.requests)-1].Questions[id] = parsed
		f.mu.Unlock()
		if a, ok := overrides[id]; ok {
			answers[id] = a
			continue
		}
		var keys []string
		if shape.Type == "choice" {
			keys = optionKeys(shape.Criteria)
		}
		pick := func(want string) string {
			for _, k := range keys {
				if k == want {
					return k
				}
			}
			for _, k := range keys {
				if k != noneOption {
					return k
				}
			}
			return keys[0]
		}
		switch {
		case id == "priority":
			answers[id] = scoreAns(2, 4, 0.9, 0.86) // high
		case id == "severity":
			answers[id] = scoreAns(2, 4, 0.7, 0.64) // sev2, below the default threshold
		case id == "sla_risk":
			answers[id] = scoreAns(2, 4, 0.8, 0.7)
		case id == "category":
			answers[id] = choiceAns("billing", 0.85, 0.8, keys)
		case id == "sentiment":
			answers[id] = choiceAns("frustrated", 0.85, 0.8, keys)
		case id == "language":
			answers[id] = choiceAns("en", 0.97, 0.96, keys)
		case id == "type_label", id == "team":
			answers[id] = choiceAns(pick(""), 0.92, 0.9, keys)
		case id == "label":
			answers[id] = choiceAns(pick("billing"), 0.95, 0.93, keys)
		case id == "spam":
			answers[id] = noulAns(0.03)
		case id == "needs_human":
			answers[id] = noulAns(0.2)
		case shape.Type == "noul":
			answers[id] = noulAns(0.9)
		case shape.Type == "choice":
			answers[id] = choiceAns(pick(""), 0.9, 0.85, keys)
		default:
			answers[id] = scoreAns(1, 3, 0.9, 0.8)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"model": "jev-1.13.0", "answers": answers,
		"usage": map[string]int{"input_tokens": 512, "output_tokens": 40}})
}
