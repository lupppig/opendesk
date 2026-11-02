package ai

import (
	"encoding/json"
	"fmt"
	"hash/fnv"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fakeLLM mimics the Anthropic Messages API (JSON and SSE), the OpenAI chat
// completions API (JSON and SSE) and an OpenAI-compatible embeddings endpoint.
type fakeLLM struct {
	t   testing.TB
	srv *httptest.Server

	mu sync.Mutex
	// replies by feature (matched on the system prompt); a func gets the request.
	replies map[string]string
	// queued replies by feature, used once each before replies. A line "@tool name
	// {json}" in a reply is a tool call (Anthropic only).
	queued map[string][]string
	// failures queued before success: HTTP statuses returned in order.
	failures []int
	// requests received, decoded.
	requests   []map[string]any
	embedCalls int
	chatCalls  int
	// rejectSchema makes the OpenAI endpoint refuse response_format json_schema.
	rejectSchema bool
}

func newFake(t testing.TB) *fakeLLM {
	f := &fakeLLM{t: t, replies: map[string]string{}, queued: map[string][]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeLLM) set(feature, reply string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.replies[feature] = reply
}

func (f *fakeLLM) queue(feature string, replies ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.queued[feature] = append(f.queued[feature], replies...)
}

func (f *fakeLLM) fail(statuses ...int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures = append(f.failures, statuses...)
}

func (f *fakeLLM) last() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		return nil
	}
	return f.requests[len(f.requests)-1]
}

func (f *fakeLLM) calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.chatCalls
}

// feature identifies which prompt a request carries.
func feature(system string) string {
	switch {
	case strings.Contains(system, "You triage"):
		return "triage"
	case strings.Contains(system, "You answer typed questions"):
		return "decisions"
	case strings.Contains(system, "You summarize"):
		return "summary"
	case strings.Contains(system, "reply-writing assistant"):
		return "draft"
	case strings.Contains(system, "copilot"):
		return "ask"
	case strings.Contains(system, "You edit support replies"):
		return "rewrite"
	case strings.Contains(system, "You translate"):
		return "translate"
	case strings.Contains(system, "company's website chat"):
		return "widget"
	case strings.Contains(system, "company's support email"):
		return "email"
	case strings.Contains(system, "help center articles"):
		return "kb"
	case strings.Contains(system, "You grade answers"):
		return "judge"
	case strings.Contains(system, "An automation asked you to work one ticket"):
		return "procedure"
	}
	return "unknown"
}

var defaultReplies = map[string]string{
	"triage":    `{"tags":["billing"],"suggested_tags":[],"priority":"high","sentiment":"frustrated","language":"en","category":"billing","summary":"Customer was charged twice."}`,
	"summary":   `{"summary":"Customer was double charged and wants a refund.","key_points":["Order 1234","Charged twice on Oct 3"],"sentiment":"negative"}`,
	"draft":     "Hi Jane,\n\nSorry about the double charge. You can see refunds in [Refund policy](http://app.test/help/x/articles/refunds).\n\n- Step one\n- Step two\n\nBest,\nAnna",
	"ask":       "The customer was charged **twice** (message 1).",
	"rewrite":   "<p>Hello, thanks for waiting.</p>",
	"translate": `{"translation":"Hallo Welt","source_language":"en"}`,
	"kb":        `{"title":"Get a refund","body_html":"<h2>Steps</h2><ol><li>Open billing</li></ol><script>x</script>","excerpt":"How refunds work."}`,
	"widget":    "You can reset your password from the sign-in page [1].",
	"procedure": "Nothing to do for this ticket.",
	"email":     "Hi Jane,\n\nYou can reset your password from the sign-in page [1].\n\nBest regards,\nOllie\nAcme",
}

func (f *fakeLLM) serve(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(r.Body)
	var body map[string]any
	_ = json.Unmarshal(raw, &body)
	f.mu.Lock()
	if strings.HasSuffix(r.URL.Path, "/embeddings") {
		f.embedCalls++
		f.mu.Unlock()
		f.embeddings(w, body)
		return
	}
	f.chatCalls++
	f.requests = append(f.requests, body)
	if len(f.failures) > 0 {
		st := f.failures[0]
		f.failures = f.failures[1:]
		f.mu.Unlock()
		w.Header().Set("Retry-After", "0")
		w.WriteHeader(st)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"overloaded_error","message":"busy"}}`)
		return
	}
	reject := f.rejectSchema
	f.mu.Unlock()

	system := ""
	switch s := body["system"].(type) {
	case []any:
		for _, b := range s {
			if m, ok := b.(map[string]any); ok {
				system += fmt.Sprint(m["text"])
			}
		}
	}
	if msgs, ok := body["messages"].([]any); ok && system == "" {
		for _, m := range msgs {
			if mm, ok := m.(map[string]any); ok && mm["role"] == "system" {
				system += fmt.Sprint(mm["content"])
			}
		}
	}
	feat := feature(system)
	f.mu.Lock()
	reply, ok := f.replies[feat]
	if q := f.queued[feat]; len(q) > 0 {
		reply, ok = q[0], true
		f.queued[feat] = q[1:]
	}
	f.mu.Unlock()
	if !ok {
		reply = defaultReplies[feat]
	}
	model := fmt.Sprint(body["model"])
	stream, _ := body["stream"].(bool)
	if strings.HasSuffix(r.URL.Path, "/chat/completions") {
		if rf, ok := body["response_format"].(map[string]any); ok && reject && rf["type"] == "json_schema" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"error":{"message":"response_format json_schema unsupported"}}`)
			return
		}
		f.openai(w, model, reply, stream)
		return
	}
	f.anthropic(w, model, reply, stream)
}

// toolLines splits a scripted reply into its text and "@tool name {json}" calls.
func toolLines(reply string) (string, [][2]string) {
	var text []string
	var calls [][2]string
	for _, line := range strings.Split(reply, "\n") {
		if rest, ok := strings.CutPrefix(line, "@tool "); ok {
			name, input, _ := strings.Cut(rest, " ")
			if strings.TrimSpace(input) == "" {
				input = "{}"
			}
			calls = append(calls, [2]string{name, input})
			continue
		}
		text = append(text, line)
	}
	return strings.TrimSpace(strings.Join(text, "\n")), calls
}

func (f *fakeLLM) anthropic(w http.ResponseWriter, model, text string, stream bool) {
	usage := map[string]any{"input_tokens": 120, "output_tokens": 30, "cache_read_input_tokens": 0, "cache_creation_input_tokens": 0}
	text, calls := toolLines(text)
	stop := "end_turn"
	if len(calls) > 0 {
		stop = "tool_use"
	}
	if !stream {
		w.Header().Set("Content-Type", "application/json")
		content := []any{map[string]any{"type": "text", "text": text}}
		for i, c := range calls {
			content = append(content, map[string]any{"type": "tool_use", "id": fmt.Sprintf("toolu_%d", i), "name": c[0], "input": json.RawMessage(c[1])})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "msg_1", "type": "message", "role": "assistant", "model": model,
			"content": content, "stop_reason": stop, "stop_sequence": nil, "usage": usage})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	fl, _ := w.(http.Flusher)
	ev := func(name string, data any) {
		raw, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, raw)
		if fl != nil {
			fl.Flush()
		}
	}
	ev("message_start", map[string]any{"type": "message_start", "message": map[string]any{"id": "msg_1", "type": "message", "role": "assistant",
		"model": model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil, "usage": usage}})
	ev("content_block_start", map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "text", "text": ""}})
	for _, part := range splitN(text, 3) {
		ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "text_delta", "text": part}})
	}
	ev("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	for i, c := range calls {
		idx := i + 1
		ev("content_block_start", map[string]any{"type": "content_block_start", "index": idx,
			"content_block": map[string]any{"type": "tool_use", "id": fmt.Sprintf("toolu_%d", i), "name": c[0], "input": map[string]any{}}})
		ev("content_block_delta", map[string]any{"type": "content_block_delta", "index": idx, "delta": map[string]any{"type": "input_json_delta", "partial_json": c[1]}})
		ev("content_block_stop", map[string]any{"type": "content_block_stop", "index": idx})
	}
	ev("message_delta", map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": 30}})
	ev("message_stop", map[string]any{"type": "message_stop"})
}

func (f *fakeLLM) openai(w http.ResponseWriter, model, text string, stream bool) {
	if !stream {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "c1", "model": model, "choices": []any{map[string]any{
			"index": 0, "message": map[string]any{"role": "assistant", "content": text}, "finish_reason": "stop"}},
			"usage": map[string]any{"prompt_tokens": 50, "completion_tokens": 10}})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	for _, part := range splitN(text, 3) {
		raw, _ := json.Marshal(map[string]any{"model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{"content": part}}}})
		fmt.Fprintf(w, "data: %s\n\n", raw)
	}
	raw, _ := json.Marshal(map[string]any{"model": model, "choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}}})
	fmt.Fprintf(w, "data: %s\n\n", raw)
	raw, _ = json.Marshal(map[string]any{"model": model, "choices": []any{}, "usage": map[string]any{"prompt_tokens": 50, "completion_tokens": 10}})
	fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", raw)
}

// embeddings returns bag-of-words vectors: texts sharing words are similar.
func (f *fakeLLM) embeddings(w http.ResponseWriter, body map[string]any) {
	inputs, _ := body["input"].([]any)
	var data []any
	for i, in := range inputs {
		data = append(data, map[string]any{"index": i, "embedding": bow(fmt.Sprint(in))})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "model": body["model"]})
}

func bow(s string) []float32 {
	v := make([]float32, 64)
	for _, w := range keywords(s) {
		h := fnv.New32a()
		_, _ = h.Write([]byte(w))
		v[h.Sum32()%64]++
	}
	var n float64
	for _, x := range v {
		n += float64(x * x)
	}
	if n == 0 {
		v[0] = 1
		return v
	}
	for i := range v {
		v[i] = float32(float64(v[i]) / math.Sqrt(n))
	}
	return v
}

func splitN(s string, n int) []string {
	r := []rune(s)
	if len(r) < n {
		return []string{s}
	}
	size := len(r) / n
	var out []string
	for i := 0; i < n; i++ {
		end := (i + 1) * size
		if i == n-1 {
			end = len(r)
		}
		out = append(out, string(r[i*size:end]))
	}
	return out
}
