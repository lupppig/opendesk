package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/integrations"
)

// Connected tools come from the workspace's integrations (MCP servers). They run as
// the workspace's connection; a tool runs as a read only while its server marks it
// read-only and no admin set it to ask first. Every other tool is a write and waits for
// the user's approval like every other change.

// toolFailure is a tool that ran and failed: Summary is safe for the panel, Content
// (already inside a data block) is what the model reads.
type toolFailure struct {
	Summary string
	Content string
}

func (e *toolFailure) Error() string { return e.Summary }

// externalArgs are a proposed connected tool call as stored with the action. Args is
// the arguments' JSON text, kept as a string so storage never reorders or re-encodes
// it: Apply sends exactly the text the preview showed.
type externalArgs struct {
	IntegrationID uuid.UUID       `json:"integration_id"`
	Remote        string          `json:"remote"`
	Args          string          `json:"args,omitempty"`
	Input         json.RawMessage `json:"input,omitempty"` // proposals stored before Args
	TicketID      string          `json:"ticket_id,omitempty"`
}

// input is the arguments the call sends.
func (a externalArgs) input() json.RawMessage {
	if a.Args != "" {
		return json.RawMessage(a.Args)
	}
	return a.Input
}

// maxExternalArgs bounds a proposed connected tool call's arguments.
const maxExternalArgs = 64 << 10

// external loads the run's connected tools once. A failure leaves the run with the
// built-in tools only.
func (rc *runCtx) external(ctx context.Context) map[string]*tool {
	if rc.ext != nil {
		return rc.ext
	}
	rc.ext = map[string]*tool{}
	if !rc.p.Role.AtLeast(auth.RoleAgent) {
		return rc.ext
	}
	ts, err := integrations.For(rc.svc.d).Tools(ctx, rc.p.WorkspaceID)
	if err != nil {
		rc.svc.d.Log.Warn("load connected tools", "workspace_id", rc.p.WorkspaceID, "err", err)
		return rc.ext
	}
	for _, x := range ts {
		rc.ext[x.Name] = externalTool(x)
		rc.extOrder = append(rc.extOrder, x.Name)
	}
	return rc.ext
}

// lookup finds a built-in or connected tool by name.
func (rc *runCtx) lookup(ctx context.Context, name string) *tool {
	if t := rc.svc.tools[name]; t != nil {
		return t
	}
	if integrations.IsTool(name) {
		return rc.external(ctx)[name]
	}
	return nil
}

func externalTool(x integrations.Tool) *tool {
	t := &tool{Name: x.Name, Description: x.Description, Schema: x.Schema, Write: !x.ReadOnly, MinRole: auth.RoleAgent,
		Active: "Checking " + x.Integration, Done: "Checked " + x.Integration, ext: &x}
	if x.ReadOnly {
		t.Read = func(ctx context.Context, rc *runCtx, in json.RawMessage) (output, error) {
			text, err := callExternal(ctx, rc, x, x.IntegrationID, x.Remote, in, integrations.CallRead)
			if err != nil {
				return output{}, err
			}
			return output{Content: block(x.Name, clip(text, 20000)), Summary: "Read from " + x.Integration, Refs: linkRefs(text, x.Hosts, 3)}, nil
		}
		// Apply stays: a call the user approved while the tool asked first still runs.
		t.Apply = applyExternal(x)
		return t
	}
	t.Active, t.Done = "Working in "+x.Integration, "Done in "+x.Integration
	t.Apply = applyExternal(x)
	t.Propose = func(ctx context.Context, rc *runCtx, in json.RawMessage) (any, Preview, error) {
		var fields map[string]any
		if len(in) == 0 || string(in) == "null" {
			in = json.RawMessage(`{}`)
		}
		if err := json.Unmarshal(in, &fields); err != nil || fields == nil {
			return nil, Preview{}, httpx.Validation("the tool arguments must be a JSON object")
		}
		if len(in) > maxExternalArgs {
			return nil, Preview{}, httpx.Validation("the tool arguments are larger than 64 KB")
		}
		if k := repeatedKey(in); k != "" {
			return nil, Preview{}, httpx.Validation(fmt.Sprintf("the tool arguments name %q twice", k))
		}
		args := externalArgs{IntegrationID: x.IntegrationID, Remote: x.Remote, Args: string(in)}
		if rc.page != nil && uuidOK(rc.page.TicketID) {
			args.TicketID = rc.page.TicketID
		}
		return args, externalPreview(x, fields, in), nil
	}
	return t
}

// applyExternal runs an approved connected tool call, or one that runs without asking
// (autoRunning), with its stored arguments.
func applyExternal(x integrations.Tool) func(ctx context.Context, rc *runCtx, w *caller, raw json.RawMessage, key string) (output, error) {
	return func(ctx context.Context, rc *runCtx, w *caller, raw json.RawMessage, key string) (output, error) {
		a, err := decode[externalArgs](raw)
		if err != nil {
			return output{}, err
		}
		mode := integrations.CallApproved
		if autoRunning(ctx) {
			mode = integrations.CallAuto
		}
		text, err := callExternal(ctx, rc, x, a.IntegrationID, a.Remote, a.input(), mode)
		if err != nil {
			return output{}, err
		}
		out := output{Content: block(x.Name, clip(text, 20000)), Summary: "Done in " + x.Integration, Refs: linkRefs(text, x.Hosts, 3)}
		if a.TicketID == "" || !creates(a.Remote) || len(out.Refs) == 0 {
			return out, nil
		}
		// Something made from a ticket is linked back to it.
		first := out.Refs[0]
		var in map[string]any
		_ = json.Unmarshal(a.input(), &in)
		title := firstString(in, "title", "name", "summary", "subject")
		body := map[string]any{"url": first.ID, "title": title, "integration_id": a.IntegrationID}
		if err := w.do(ctx, http.MethodPost, "/v1/tickets/"+a.TicketID+"/links", nil, body, nil, key+"-link"); err == nil {
			out.Summary = "Created in " + x.Integration + " and linked to the ticket"
			out.Content += "\n\nThe new item was linked to the open ticket."
			if title != "" {
				out.Refs[0].Label = clip(title, 120)
			}
		}
		return out, nil
	}
}

// callExternal runs one connected tool and returns its text. The server's own words
// stay inside a data block, both for results and for errors. mode says why it may run;
// the integration checks it against how the workspace has the tool now.
func callExternal(ctx context.Context, rc *runCtx, x integrations.Tool, iid uuid.UUID, remote string, in json.RawMessage, mode integrations.CallMode) (string, error) {
	if len(in) == 0 || string(in) == "null" {
		in = json.RawMessage(`{}`)
	}
	res, err := integrations.For(rc.svc.d).Call(ctx, rc.p.WorkspaceID, iid, remote, in, mode)
	if err != nil {
		var re *integrations.RemoteError
		if errors.As(err, &re) {
			return "", &toolFailure{Summary: x.Integration + " refused the request",
				Content: block(x.Name, "The tool failed: "+clip(re.Message, 2000))}
		}
		return "", &toolFailure{Summary: clip(capitalize(err.Error()), 200), Content: clip(capitalize(err.Error()), 300)}
	}
	text := res.Text()
	if res.IsError {
		return "", &toolFailure{Summary: x.Integration + " answered with an error",
			Content: block(x.Name, "The tool reported an error:\n"+clip(text, 4000))}
	}
	if text == "" {
		text = "(no content)"
	}
	return text, nil
}

// externalPreview summarizes a connected tool call (at most 12 fields, values cut
// short) and carries its complete arguments, raw being the JSON text Apply sends.
// Omitted counts the fields the summary leaves out and Clipped marks a value cut short.
func externalPreview(x integrations.Tool, fields map[string]any, raw json.RawMessage) Preview {
	pv := Preview{Title: x.Integration + ": " + x.Title, System: x.Integration, Arguments: indentJSON(raw)}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	shown := 0
	for _, k := range keys {
		v := previewValue(fields[k])
		if v == "" {
			continue
		}
		if pv.Body == "" && (strings.Contains(v, "\n") || len([]rune(v)) > 160) {
			pv.Body = clipped(&pv, v, 6000)
			shown++
			continue
		}
		if len(pv.Changes) == 12 {
			continue
		}
		pv.Changes = append(pv.Changes, Change{Field: humanizeKey(k), To: clipped(&pv, v, 300)})
		shown++
	}
	pv.Omitted = len(keys) - shown
	return pv
}

// clipped is clip, marking the preview when the value was cut short.
func clipped(pv *Preview, v string, n int) string {
	out := clip(v, n)
	if out != strings.TrimSpace(v) {
		pv.Clipped = true
	}
	return out
}

// indentJSON is raw indented as it is: json.Indent changes only the whitespace, so
// nothing is reordered or re-encoded.
func indentJSON(raw []byte) string {
	var b bytes.Buffer
	if json.Indent(&b, raw, "", "  ") != nil {
		return string(raw)
	}
	return b.String()
}

// repeatedKey is a key an object in raw names twice, at any depth, or "". A repeated
// key means different things to different readers, so such arguments are refused
// instead of shown one way and read another.
func repeatedKey(raw []byte) string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	// One frame per open object (seen != nil) or array; key: the object's next token is a key.
	type frame struct {
		seen map[string]bool
		key  bool
	}
	var stack []*frame
	for {
		tok, err := dec.Token()
		if err != nil {
			return ""
		}
		if n := len(stack); n > 0 && stack[n-1].seen != nil && stack[n-1].key {
			if k, ok := tok.(string); ok {
				if stack[n-1].seen[k] {
					return k
				}
				stack[n-1].seen[k] = true
				stack[n-1].key = false
				continue
			}
		}
		if d, ok := tok.(json.Delim); ok {
			switch d {
			case '{':
				stack = append(stack, &frame{seen: map[string]bool{}, key: true})
				continue
			case '[':
				stack = append(stack, &frame{})
				continue
			default: // a '}' or ']' closes a value
				stack = stack[:len(stack)-1]
			}
		}
		// A value ended: an enclosing object expects a key next.
		if n := len(stack); n > 0 && stack[n-1].seen != nil {
			stack[n-1].key = true
		}
	}
}

func previewValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	case bool:
		if t {
			return "yes"
		}
		return "no"
	case float64:
		return strings.TrimSuffix(strings.TrimRight(fmt.Sprintf("%f", t), "0"), ".")
	case []any:
		parts := make([]string, 0, len(t))
		for _, e := range t {
			if s := previewValue(e); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	default:
		raw, _ := json.Marshal(t)
		return string(raw)
	}
}

func humanizeKey(k string) string {
	k = strings.NewReplacer("_", " ", "-", " ").Replace(k)
	// camelCase → camel case
	var b strings.Builder
	for i, r := range k {
		if i > 0 && r >= 'A' && r <= 'Z' {
			b.WriteByte(' ')
		}
		b.WriteRune(r)
	}
	return capitalize(strings.ToLower(strings.Join(strings.Fields(b.String()), " ")))
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

var createVerb = regexp.MustCompile(`(?i)(^|[_\-.])(create|new|open|add|file|submit|report)`)

// creates says whether a tool name reads as making something new.
func creates(remote string) bool { return createVerb.MatchString(remote) }

var urlPattern = regexp.MustCompile(`https://[^\s"'<>()\[\]{}]+`)

// linkRefs are the links on the provider's hosts in a tool's output, first first.
func linkRefs(text string, hosts []string, limit int) []Ref {
	if len(hosts) == 0 {
		return nil
	}
	var out []Ref
	for _, m := range urlPattern.FindAllString(text, 50) {
		m = strings.TrimRight(m, ".,;:!?")
		u, err := url.Parse(m)
		if err != nil || u.User != nil {
			continue
		}
		h := strings.ToLower(u.Hostname())
		if !slices.ContainsFunc(hosts, func(host string) bool { return h == host || strings.HasSuffix(h, "."+host) }) {
			continue
		}
		if slices.ContainsFunc(out, func(r Ref) bool { return r.ID == m }) {
			continue
		}
		out = append(out, Ref{Kind: "external", ID: m, Label: clip(u.Host+u.Path, 80)})
		if len(out) == limit {
			break
		}
	}
	return out
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := m[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
