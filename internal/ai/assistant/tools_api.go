package assistant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// The API tools reach everything else the user can do in the app (settings, signatures,
// mailboxes, teams, members, brands, the widget, their own profile, ...) through the
// documented public API, as the user: find_api reads the operations, api_get reads, and
// api_change proposes one write the user approves. The API's own role checks decide what
// is allowed; sign-in, credentials and owner-only operations stay out of reach.

func apiTools() []*tool {
	return []*tool{
		{
			Name: "find_api",
			Description: "Search opendesk's API for the operations behind any part of the product not covered by the other tools: settings, " +
				"signatures, mailboxes, teams, members and roles, the user's profile, brands, email design and templates, the widget, SLA, " +
				"knowledge base collections, webhooks, notifications and more. Returns matching operations with their method, path, " +
				"parameters and body fields. Call it before api_get or api_change.",
			Schema:  obj(map[string]any{"query": str("What the user wants to read or change, in a few words, e.g. \"mailbox signature\".")}, "query"),
			MinRole: auth.RoleViewer, Active: "Looking up how to do it", Done: "Looked up how to do it",
			Read: findAPI,
		},
		{
			Name:        "api_get",
			Description: "Read from an API operation found with find_api (method GET), as the user.",
			Schema: obj(map[string]any{
				"path":   str("The path with its parameters filled in, e.g. /v1/mailboxes/0b6c…; \"me\" works where the operation says so."),
				"query":  map[string]any{"type": "object", "description": "Query parameters by name.", "additionalProperties": map[string]any{"type": "string"}},
				"fields": arr("Only these fields of the answer (of each item, for a list); leave out to read everything.", str("")),
			}, "path"),
			MinRole: auth.RoleViewer, Active: "Reading", Done: "Read",
			Read: apiGet,
		},
		{
			Name: "api_change", Write: true, MinRole: auth.RoleViewer,
			Description: "Make one change through an API operation found with find_api (POST, PUT, PATCH or DELETE), as the user. " +
				"Send only the fields to change. Needs the user's approval, which shows every field; just call it. " +
				"Ticket changes, notes and replies use their own tools instead.",
			Schema: obj(map[string]any{
				"method": enum("The operation's method.", http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete),
				"path":   str("The path with its parameters filled in."),
				"query":  map[string]any{"type": "object", "description": "Query parameters by name.", "additionalProperties": map[string]any{"type": "string"}},
				"body":   map[string]any{"type": "object", "description": "The JSON body, with the operation's field names."},
			}, "method", "path"),
			Active: "Preparing the change", Done: "Made the change",
			Propose: proposeAPI, Apply: applyAPI,
		},
	}
}

// apiOp is one documented operation, indexed for find_api.
type apiOp struct {
	Method string
	Path   string
	segs   []string
	op     *huma.Operation
	// terms are the operation's weighted search terms; size is their total weight.
	terms map[string]float64
	size  float64
}

// ops lists and indexes the documented operations once. Only operations the tools may
// use are kept, so find_api never offers one api_get or api_change would refuse.
func (s *Service) ops() []apiOp {
	s.opsOnce.Do(func() {
		if s.spec == nil {
			return
		}
		for path, item := range s.spec.Paths {
			for _, m := range []struct {
				method string
				op     *huma.Operation
			}{{http.MethodGet, item.Get}, {http.MethodPost, item.Post}, {http.MethodPut, item.Put}, {http.MethodPatch, item.Patch}, {http.MethodDelete, item.Delete}} {
				if m.op == nil || m.op.Hidden || !reachable(m.method, path) {
					continue
				}
				o := apiOp{Method: m.method, Path: path, segs: strings.Split(strings.Trim(path, "/"), "/"), op: m.op, terms: map[string]float64{}}
				add := func(text string, w float64) {
					for _, t := range terms(text) {
						o.terms[t] += w
						o.size += w
					}
				}
				add(m.op.Summary, 3)
				add(m.op.OperationID, 2)
				add(strings.Join(m.op.Tags, " "), 2)
				for _, sg := range o.segs {
					if !strings.HasPrefix(sg, "{") {
						add(sg, 2)
					}
				}
				add(strings.Join(s.fieldNames(&o), " "), 1)
				add(m.op.Description, 0.5)
				s.apiOps = append(s.apiOps, o)
			}
		}
		sort.Slice(s.apiOps, func(i, j int) bool {
			if s.apiOps[i].Path != s.apiOps[j].Path {
				return s.apiOps[i].Path < s.apiOps[j].Path
			}
			return s.apiOps[i].Method < s.apiOps[j].Method
		})
		s.apiIDF = map[string]float64{}
		df := map[string]int{}
		total := 0.0
		for _, o := range s.apiOps {
			total += o.size
			for t := range o.terms {
				df[t]++
			}
		}
		n := float64(len(s.apiOps))
		for t, c := range df {
			s.apiIDF[t] = math.Log(1 + (n-float64(c)+0.5)/(float64(c)+0.5))
		}
		if n > 0 {
			s.apiAvg = total / n
		}
	})
	return s.apiOps
}

// terms splits text into lowercase search terms with a light plural stem
// ("signatures" and "signature" meet; "signature_html" is two terms).
func terms(text string) []string {
	words := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9')
	})
	out := words[:0]
	for _, w := range words {
		if len(w) < 2 || stopWords[w] {
			continue
		}
		switch {
		case len(w) > 4 && strings.HasSuffix(w, "ies"):
			w = w[:len(w)-3] + "y"
		case len(w) > 3 && strings.HasSuffix(w, "s") && !strings.HasSuffix(w, "ss"):
			w = w[:len(w)-1]
		}
		out = append(out, w)
	}
	return out
}

var stopWords = map[string]bool{"the": true, "an": true, "of": true, "to": true, "for": true, "and": true, "or": true, "in": true,
	"on": true, "my": true, "our": true, "with": true, "by": true, "is": true, "it": true, "v1": true, "this": true, "that": true}

// aliases are the words people use for what the API names otherwise.
var aliases = map[string][]string{
	"org": {"workspace"}, "organization": {"workspace"}, "organisation": {"workspace"}, "company": {"workspace", "brand"},
	"inbox": {"mailbox"}, "address": {"mailbox"}, "email": {"mailbox"}, "queue": {"team"}, "group": {"team"}, "department": {"team"},
	"teammate": {"member"}, "agent": {"member"}, "user": {"member"}, "colleague": {"member"}, "staff": {"member"}, "role": {"member"},
	"signoff": {"signature"}, "footer": {"signature", "template"}, "layout": {"template"}, "design": {"template"},
	"branding": {"brand", "template"}, "logo": {"brand"}, "label": {"tag"}, "article": {"kb"}, "help": {"kb"}, "center": {"kb"},
	"autoreply": {"auto", "reply"}, "ooo": {"auto", "reply"}, "vacation": {"auto", "reply"}, "chat": {"widget"},
	"profile": {"member", "profile"}, "title": {"profile"}, "phone": {"profile"}, "pronoun": {"profile"},
	"invite": {"invitation"}, "rule": {"automation"}, "trigger": {"automation"}, "macro": {"saved", "reply"},
	"canned": {"saved", "reply"}, "rename": {"update"}, "change": {"update"}, "edit": {"update"}, "set": {"update"},
	"slack": {"support", "channel"}, "discord": {"support", "channel"}, "telegram": {"support", "channel"},
}

// search ranks the operations for a query with BM25 over their weighted terms.
func (s *Service) search(query string, limit int) []*apiOp {
	ops := s.ops()
	q := map[string]float64{}
	for _, t := range terms(query) {
		q[t] = 1
		for _, a := range aliases[t] {
			if q[a] == 0 {
				q[a] = 0.6
			}
		}
	}
	const k1, b = 1.2, 0.75
	type hit struct {
		o     *apiOp
		score float64
	}
	var hits []hit
	for i := range ops {
		o := &ops[i]
		score := 0.0
		for t, w := range q {
			tf := o.terms[t]
			if tf == 0 {
				continue
			}
			score += w * s.apiIDF[t] * tf * (k1 + 1) / (tf + k1*(1-b+b*o.size/s.apiAvg))
		}
		if score > 0 {
			hits = append(hits, hit{o, score})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool { return hits[i].score > hits[j].score })
	out := make([]*apiOp, 0, limit)
	for _, h := range hits {
		if len(out) == limit {
			break
		}
		out = append(out, h.o)
	}
	return out
}

// catalog names the API's areas for the system prompt, so the first search is a good one.
func (s *Service) catalog() string {
	count := map[string]int{}
	for _, o := range s.ops() {
		tag := "Other"
		if len(o.op.Tags) > 0 {
			tag = o.op.Tags[0]
		}
		count[tag]++
	}
	tags := make([]string, 0, len(count))
	for t := range count {
		tags = append(tags, t)
	}
	sort.Strings(tags)
	return strings.Join(tags, ", ")
}

// Prefixes the tools never reach: sign-in, the operator's panel, Ollie itself and what a
// person shares with teammates' Ollie, and the routes visitors, devices and other servers
// call.
var apiClosed = []string{"/v1/admin", "/v1/auth", "/v1/assistant", "/v1/me/assistant-peer", "/v1/public", "/v1/push", "/v1/oauth", "/v1/realtime",
	"/v1/inbound", "/v1/widget/session", "/v1/widget/conversations", "/v1/widget/attachments", "/v1/widget/config"}

// Writes that stay with the person: credentials, sessions and devices, money, owner-only
// operations, and messages to customers (draft_reply shows those as such). An empty
// method closes every write to the path.
var apiNoWrite = []struct {
	method string
	re     *regexp.Regexp
}{
	{http.MethodDelete, regexp.MustCompile(`^/v1/me$`)},
	{"", regexp.MustCompile(`^/v1/me/(password|passkeys|sessions|devices|web-push|heartbeat|support-channel-link-code)(/|$)`)},
	{http.MethodDelete, regexp.MustCompile(`^/v1/workspace$`)},
	{"", regexp.MustCompile(`^/v1/workspace/(transfer|widget-secret)(/|$)`)},
	{"", regexp.MustCompile(`^/v1/widgets/[^/]+/secret(/|$)`)},
	{"", regexp.MustCompile(`^/v1/api-keys$`)},
	{"", regexp.MustCompile(`^/v1/billing(/|$)`)},
	{"", regexp.MustCompile(`^/v1/ai/provider(/|$)`)},
	{"", regexp.MustCompile(`^/v1/invitations/(accept|lookup)$`)},
	{"", regexp.MustCompile(`^/v1/attachments$`)},
	{"", regexp.MustCompile(`^/v1/imports(/|$)`)},
	{"", regexp.MustCompile(`^/v1/tickets/[^/]+/(messages|threads/[^/]+/messages|ai/suggested-reply/[^/]+/send)$`)},
}

// apiExternal are writes that reach someone outside the workspace.
var apiExternal = []*regexp.Regexp{
	regexp.MustCompile(`^/v1/collab/messages$`),
	regexp.MustCompile(`^/v1/collab/drafts/[^/]+/send$`),
	regexp.MustCompile(`^/v1/invitations(/[^/]+/resend)?$`),
	regexp.MustCompile(`^/v1/templates/test$`),
	regexp.MustCompile(`^/v1/webhooks/[^/]+/(test|verify)$`),
	regexp.MustCompile(`^/v1/support-channels/connections/[^/]+/test$`),
}

// secretKey matches field names that carry a credential; they are never sent by the
// tools and never shown to the model.
var secretKey = regexp.MustCompile(`(?i)(secret|token|password|passwd|api_?key|private_?key|credential)`)

func reachable(method, path string) bool {
	for _, p := range apiClosed {
		if path == p || strings.HasPrefix(path, p+"/") {
			return false
		}
	}
	if method == http.MethodGet {
		return true
	}
	for _, c := range apiNoWrite {
		if (c.method == "" || c.method == method) && c.re.MatchString(path) {
			return false
		}
	}
	return true
}

var pathSeg = regexp.MustCompile(`^[A-Za-z0-9._~@:%+-]+$`)

// match finds the documented operation for a concrete path, preferring the template
// with the most literal segments (/v1/tickets/bulk over /v1/tickets/{id}).
func (s *Service) match(method, raw string) (*apiOp, string, error) {
	p := strings.TrimSpace(raw)
	if strings.Contains(p, "?") {
		return nil, "", httpx.Validation("put query parameters in query, not in the path")
	}
	if !strings.HasPrefix(p, "/v1/") {
		return nil, "", httpx.Validation("path must start with /v1/")
	}
	segs := strings.Split(strings.Trim(p, "/"), "/")
	for i, sg := range segs {
		if strings.HasPrefix(sg, "#") && ticketNumber.MatchString(sg) {
			segs[i] = url.PathEscape(sg)
			continue
		}
		if sg == "." || sg == ".." || !pathSeg.MatchString(sg) {
			return nil, "", httpx.Validation("path has an invalid segment: " + clip(sg, 40))
		}
	}
	path := "/" + strings.Join(segs, "/")
	var best *apiOp
	bestLit := -1
	ops := s.ops()
	for i := range ops {
		o := &ops[i]
		if o.Method != method || len(o.segs) != len(segs) {
			continue
		}
		lit, ok := 0, true
		for j, t := range o.segs {
			if strings.HasPrefix(t, "{") {
				continue
			}
			if t != segs[j] {
				ok = false
				break
			}
			lit++
		}
		if ok && lit > bestLit {
			best, bestLit = o, lit
		}
	}
	if best == nil {
		return nil, "", httpx.Validation(fmt.Sprintf("no %s operation has the path %s; find_api lists them", method, clip(path, 120)))
	}
	if !reachable(method, best.Path) {
		return nil, "", httpx.Validation("this operation is for the user to do themselves in the app")
	}
	return best, path, nil
}

// --- find_api ---------------------------------------------------------------------------

var opQuery = regexp.MustCompile(`(?i)^(GET|POST|PUT|PATCH|DELETE)\s+(/v1/\S+)$`)

// findAPI answers with a short list: the best two operations in full, the rest one line
// each. "METHOD /path" asks for that one operation in full.
func findAPI(_ context.Context, rc *runCtx, in json.RawMessage) (output, error) {
	a, err := decode[struct {
		Query string `json:"query"`
	}](in)
	if err != nil {
		return output{}, err
	}
	s := rc.svc
	query := strings.TrimSpace(a.Query)
	if m := opQuery.FindStringSubmatch(query); m != nil {
		method := strings.ToUpper(m[1])
		for i, o := range s.ops() {
			if o.Method == method && o.Path == m[2] {
				return output{Content: block("find_api", s.describeOp(&s.ops()[i])), Summary: o.op.Summary}, nil
			}
		}
		o, _, err := s.match(method, m[2])
		if err != nil {
			return output{}, err
		}
		return output{Content: block("find_api", s.describeOp(o)), Summary: o.op.Summary}, nil
	}
	if len(terms(query)) == 0 {
		return output{}, httpx.Validation("query is empty")
	}
	hits := s.search(query, 8)
	var b strings.Builder
	if len(hits) == 0 {
		b.WriteString("No operation matches. Try other words.\n")
	}
	for i, o := range hits {
		if i < 2 {
			b.WriteString(s.describeOp(o))
			continue
		}
		if i == 2 {
			b.WriteString("Also:\n")
		}
		fmt.Fprintf(&b, "%s %s: %s\n", o.Method, o.Path, o.op.Summary)
	}
	if len(hits) > 2 {
		b.WriteString(`Search "METHOD /path" for one of these in full.` + "\n")
	}
	return output{Content: block("find_api", clip(b.String(), 8000)), Summary: plural(len(hits), "operation", "operations")}, nil
}

// fieldNames are an operation's parameter and top-level body field names.
func (s *Service) fieldNames(o *apiOp) []string {
	var out []string
	for _, p := range o.op.Parameters {
		out = append(out, p.Name)
	}
	if sc := s.bodySchema(o); sc != nil {
		for k := range sc.Properties {
			out = append(out, k)
		}
	}
	return out
}

func (s *Service) resolve(sc *huma.Schema) *huma.Schema {
	for i := 0; sc != nil && sc.Ref != "" && i < 4; i++ {
		if s.spec.Components == nil || s.spec.Components.Schemas == nil {
			return nil
		}
		sc = s.spec.Components.Schemas.SchemaFromRef(sc.Ref)
	}
	return sc
}

func (s *Service) bodySchema(o *apiOp) *huma.Schema {
	rb := o.op.RequestBody
	if rb == nil {
		return nil
	}
	for ct, mt := range rb.Content {
		if strings.Contains(ct, "json") && mt != nil {
			return s.resolve(mt.Schema)
		}
	}
	return nil
}

func (s *Service) describeOp(o *apiOp) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s: %s\n", o.Method, o.Path, o.op.Summary)
	if d := strings.TrimSpace(o.op.Description); d != "" {
		fmt.Fprintf(&b, "  %s\n", clip(strings.Join(strings.Fields(d), " "), 600))
	}
	for _, p := range o.op.Parameters {
		if p.In != "path" && p.In != "query" {
			continue
		}
		fmt.Fprintf(&b, "  %s %s%s", p.In, p.Name, typeOf(s.resolve(p.Schema)))
		if p.Required && p.In == "query" {
			b.WriteString(", required")
		}
		if d := strings.TrimSpace(p.Description); d != "" {
			fmt.Fprintf(&b, ": %s", clip(d, 200))
		}
		b.WriteString("\n")
	}
	if sc := s.bodySchema(o); sc != nil && len(sc.Properties) > 0 {
		b.WriteString("  body:\n")
		s.writeProps(&b, sc, 2, 0)
	}
	return b.String()
}

func (s *Service) writeProps(b *strings.Builder, sc *huma.Schema, indent, depth int) {
	keys := make([]string, 0, len(sc.Properties))
	for k := range sc.Properties {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pad := strings.Repeat("  ", indent)
	for _, k := range keys {
		if strings.HasPrefix(k, "$") {
			continue
		}
		p := s.resolve(sc.Properties[k])
		if p == nil || p.ReadOnly {
			continue
		}
		fmt.Fprintf(b, "%s- %s%s", pad, k, typeOf(p))
		if contains(sc.Required, k) {
			b.WriteString(", required")
		}
		if d := strings.TrimSpace(p.Description); d != "" {
			fmt.Fprintf(b, ": %s", clip(strings.Join(strings.Fields(d), " "), 240))
		}
		b.WriteString("\n")
		inner := p
		if p.Type == "array" {
			inner = s.resolve(p.Items)
		}
		if inner != nil && len(inner.Properties) > 0 && depth < 2 {
			s.writeProps(b, inner, indent+1, depth+1)
		}
	}
}

func typeOf(p *huma.Schema) string {
	if p == nil {
		return ""
	}
	var parts []string
	t := p.Type
	if t == "array" && p.Items != nil && p.Items.Type != "" {
		t = "array of " + p.Items.Type
	}
	if t != "" {
		if p.Nullable {
			t += " or null"
		}
		parts = append(parts, t)
	}
	if len(p.Enum) > 0 {
		vals := make([]string, 0, len(p.Enum))
		for _, v := range p.Enum {
			vals = append(vals, fmt.Sprint(v))
		}
		parts = append(parts, "one of "+strings.Join(vals, ", "))
	}
	if p.MaxLength != nil {
		parts = append(parts, fmt.Sprintf("max %d chars", *p.MaxLength))
	}
	if len(parts) == 0 {
		return ""
	}
	return " (" + strings.Join(parts, "; ") + ")"
}

// --- api_get ----------------------------------------------------------------------------

type apiArgs struct {
	Method string            `json:"method"`
	Path   string            `json:"path"`
	Query  map[string]string `json:"query,omitempty"`
	Body   map[string]any    `json:"body,omitempty"`
	Fields []string          `json:"fields,omitempty"`
	// Stored with a proposal: the documented path it matched.
	Op string `json:"op,omitempty"`
}

func (a apiArgs) values() url.Values {
	if len(a.Query) == 0 {
		return nil
	}
	q := url.Values{}
	for k, v := range a.Query {
		q.Set(k, v)
	}
	return q
}

func apiGet(ctx context.Context, rc *runCtx, in json.RawMessage) (output, error) {
	a, err := decode[apiArgs](in)
	if err != nil {
		return output{}, err
	}
	o, path, err := rc.svc.match(http.MethodGet, a.Path)
	if err != nil {
		return output{}, err
	}
	var raw json.RawMessage
	if err := rc.read.get(ctx, path, a.values(), &raw); err != nil {
		return output{}, notJSON(err)
	}
	return output{Content: block("api_get", clip(redacted(raw, a.Fields...), 20000)), Summary: o.op.Summary}, nil
}

// notJSON names a non-JSON answer (a download) instead of failing on it.
func notJSON(err error) error {
	var se *json.SyntaxError
	if errors.As(err, &se) {
		return httpx.Validation("this operation does not answer JSON")
	}
	return err
}

// redacted is a JSON answer with every credential-like value removed, cut down to
// fields when given.
func redacted(raw json.RawMessage, fields ...string) string {
	if len(raw) == 0 {
		return "(no content)"
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return "(not JSON)"
	}
	if len(fields) > 0 {
		v = project(v, fields)
	}
	out, _ := json.Marshal(scrub(v))
	return string(out)
}

// project keeps the named fields of an object, of each item of a list, or of each item
// of a page's data (keeping its cursor).
func project(v any, fields []string) any {
	pick := func(x any) any {
		m, ok := x.(map[string]any)
		if !ok {
			return x
		}
		out := map[string]any{}
		for _, f := range fields {
			if val, ok := m[f]; ok {
				out[f] = val
			}
		}
		return out
	}
	switch x := v.(type) {
	case []any:
		for i := range x {
			x[i] = pick(x[i])
		}
		return x
	case map[string]any:
		if data, ok := x["data"].([]any); ok {
			for i := range data {
				data[i] = pick(data[i])
			}
			return x
		}
		return pick(x)
	}
	return v
}

func scrub(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if secretKey.MatchString(k) {
				switch val.(type) {
				case bool, nil, float64:
				default:
					x[k] = "[hidden]"
					continue
				}
			}
			x[k] = scrub(val)
		}
	case []any:
		for i := range x {
			x[i] = scrub(x[i])
		}
	case string:
		return scrubURL(x)
	}
	return v
}

// scrubURL is s without the parts of a URL that can carry a credential, when s is an
// absolute http(s) URL: no userinfo, every query (and fragment) value [hidden] with its
// name kept, and every path segment that looks like a credential [hidden]. Anything
// else is returned as it is.
func scrubURL(s string) string {
	if len(s) > 8192 || strings.ContainsAny(s, " \t\r\n") {
		return s
	}
	lower := strings.ToLower(s)
	if !strings.HasPrefix(lower, "https://") && !strings.HasPrefix(lower, "http://") {
		return s
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return s
	}
	var b strings.Builder
	b.WriteString(u.Scheme + "://" + u.Host)
	segs := strings.Split(u.EscapedPath(), "/")
	for i, seg := range segs {
		if credentialSegment(seg) {
			segs[i] = "[hidden]"
		}
	}
	b.WriteString(strings.Join(segs, "/"))
	if u.RawQuery != "" || u.ForceQuery {
		b.WriteString("?" + hideValues(u.RawQuery))
	}
	if f := u.EscapedFragment(); f != "" {
		b.WriteString("#")
		if strings.Contains(f, "=") {
			b.WriteString(hideValues(f))
		} else if credentialSegment(f) {
			b.WriteString("[hidden]")
		} else {
			b.WriteString(f)
		}
	}
	return b.String()
}

// hideValues replaces the value of each name=value pair with [hidden]; a bare part is
// hidden when it looks like a credential.
func hideValues(raw string) string {
	parts := strings.Split(raw, "&")
	for i, p := range parts {
		if name, val, ok := strings.Cut(p, "="); ok {
			if val != "" {
				parts[i] = name + "=[hidden]"
			}
		} else if credentialSegment(p) {
			parts[i] = "[hidden]"
		}
	}
	return strings.Join(parts, "&")
}

var (
	uuidLike  = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)
	tokenRun  = regexp.MustCompile(`[A-Za-z0-9_-]{20,}`)
	tokenPart = regexp.MustCompile(`[_-]`)
)

// credentialSegment says whether a URL path segment looks like a credential: a run of
// 20 or more letters, digits, '-' and '_' that mixes letters and digits, and either
// holds an unbroken stretch of 16 or more such characters or is 32 or more long with
// upper and lower case. Ids in the UUID form are not counted, so ordinary slugs
// ("how-to-reset-2fa-in-2024") and record ids survive; tokens such as a webhook's
// path secret do not.
func credentialSegment(seg string) bool {
	if dec, err := url.PathUnescape(seg); err == nil {
		seg = dec
	}
	seg = uuidLike.ReplaceAllString(seg, ".")
	for _, run := range tokenRun.FindAllString(seg, -1) {
		if !hasLetter(run) || !strings.ContainsAny(run, "0123456789") {
			continue
		}
		longest := 0
		for _, p := range tokenPart.Split(run, -1) {
			if len(p) >= 16 && hasLetter(p) && strings.ContainsAny(p, "0123456789") {
				return true
			}
			longest = max(longest, len(p))
		}
		if len(run) >= 32 && longest >= 10 && strings.ToLower(run) != run && strings.ToUpper(run) != run {
			return true
		}
	}
	return false
}

func hasLetter(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') })
}

// --- api_change -------------------------------------------------------------------------

func hasSecret(v any) string {
	switch x := v.(type) {
	case map[string]any:
		for k, val := range x {
			if secretKey.MatchString(k) {
				return k
			}
			if f := hasSecret(val); f != "" {
				return f
			}
		}
	case []any:
		for _, val := range x {
			if f := hasSecret(val); f != "" {
				return f
			}
		}
	}
	return ""
}

func proposeAPI(ctx context.Context, rc *runCtx, in json.RawMessage) (any, Preview, error) {
	a, err := decode[apiArgs](in)
	if err != nil {
		return nil, Preview{}, err
	}
	a.Method = strings.ToUpper(strings.TrimSpace(a.Method))
	switch a.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return nil, Preview{}, httpx.Validation("method must be POST, PUT, PATCH or DELETE; reads use api_get")
	}
	o, path, err := rc.svc.match(a.Method, a.Path)
	if err != nil {
		return nil, Preview{}, err
	}
	if f := hasSecret(map[string]any(a.Body)); f != "" {
		return nil, Preview{}, httpx.Validation(fmt.Sprintf("%q is a credential; the user enters credentials in the app themselves", f))
	}
	if raw, _ := json.Marshal(a.Body); len(raw) > 64<<10 {
		return nil, Preview{}, httpx.Validation("the body is larger than 64 KB")
	}
	a.Path, a.Op = path, o.Path

	pv := Preview{Title: o.op.Summary, Summary: a.Method + " " + path}
	if pv.Title == "" {
		pv.Title = a.Method + " " + o.Path
	}
	for _, re := range apiExternal {
		if re.MatchString(path) {
			pv.External = true
		}
	}
	// What the path holds now, when the same path reads it: the from side, and the name
	// of what is changed or deleted.
	var cur map[string]any
	if a.Method != http.MethodPost {
		if g, _, err := rc.svc.match(http.MethodGet, path); err == nil && g.Path == o.Path {
			var raw json.RawMessage
			if rc.read.get(ctx, path, nil, &raw) == nil {
				_ = json.Unmarshal(raw, &cur)
			}
		}
	}
	for _, k := range []string{"name", "title", "email", "subject"} {
		if v, ok := cur[k].(string); ok && strings.TrimSpace(v) != "" {
			pv.Summary = clip(v, 120)
			break
		}
	}
	keys := make([]string, 0, len(a.Body))
	for k := range a.Body {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		from := ""
		if cur != nil {
			if v, ok := cur[k]; ok {
				from = show(v)
			}
		}
		to := show(a.Body[k])
		if from == to {
			pv.Omitted++ // sent, and the same as now
			continue
		}
		pv.Changes = append(pv.Changes, Change{Field: k, From: clipped(&pv, from, 2000), To: clipped(&pv, to, 2000)})
	}
	qkeys := make([]string, 0, len(a.Query))
	for k := range a.Query {
		qkeys = append(qkeys, k)
	}
	sort.Strings(qkeys)
	for _, k := range qkeys {
		pv.Changes = append(pv.Changes, Change{Field: k, To: clipped(&pv, a.Query[k], 200)})
	}
	if a.Method != http.MethodDelete && len(a.Body) > 0 && len(pv.Changes) == 0 {
		return nil, Preview{}, httpx.Validation("these values are already set; there is nothing to change")
	}
	pv.Arguments = a.request()
	return a, pv, nil
}

// request is the request applyAPI sends, as indented JSON for the approval card. The
// body is encoded as the API receives it (a body without fields is not sent).
func (a apiArgs) request() string {
	r := struct {
		Method string            `json:"method"`
		Path   string            `json:"path"`
		Query  map[string]string `json:"query,omitempty"`
		Body   map[string]any    `json:"body,omitempty"`
	}{a.Method, a.Path, a.Query, a.Body}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if enc.Encode(r) != nil {
		return ""
	}
	return strings.TrimSpace(b.String())
}

func applyAPI(ctx context.Context, rc *runCtx, w *caller, raw json.RawMessage, key string) (output, error) {
	var a apiArgs
	if err := json.Unmarshal(raw, &a); err != nil {
		return output{}, err
	}
	o, path, err := rc.svc.match(a.Method, a.Path)
	if err != nil {
		return output{}, err
	}
	if o.Path != a.Op {
		return output{}, httpx.Validation("this operation changed since it was proposed")
	}
	var body any
	if a.Body != nil {
		body = a.Body
	}
	var res json.RawMessage
	if err := w.do(ctx, a.Method, path, a.values(), body, &res, key); err != nil {
		return output{}, notJSON(err)
	}
	return output{Content: block("api_change", clip(redacted(res), 8000)), Summary: o.op.Summary}, nil
}
