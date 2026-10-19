package collab

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Labels beyond Gmail (contracts/collab.md "Labels and colours"): Outlook categories are
// a Microsoft account's labels, and a label's colour is set at the provider first.
//
// A category is a label whose remote id is "category:<name>" (messages carry categories
// by name). The account's master list (/me/outlook/masterCategories, which needs
// MailboxSettings.Read) gives the categories and their colours; a category a message
// carries that the list lacks (Outlook allows that) is a label too, kept while a message
// carries it. Without the master list (an account connected before Collab asked for
// MailboxSettings.ReadWrite), the categories are the ones seen on messages, without
// colours, until the account is connected again.

const categoryPrefix = "category:"

func isCategory(remoteID string) bool { return strings.HasPrefix(remoteID, categoryPrefix) }

func categoryName(remoteID string) string { return strings.TrimPrefix(remoteID, categoryPrefix) }

// graphPresets are Outlook's category colours as hex backgrounds.
var graphPresets = map[string]string{
	"preset0": "#e74856", "preset1": "#f7630c", "preset2": "#8e562e", "preset3": "#ffb900", "preset4": "#13a10e",
	"preset5": "#00b294", "preset6": "#9da34f", "preset7": "#0078d4", "preset8": "#8764b8", "preset9": "#c30052",
	"preset10": "#69797e", "preset11": "#4a5459", "preset12": "#a0aeb2", "preset13": "#7a7574", "preset14": "#1f1f1f",
	"preset15": "#a4262c", "preset16": "#ca5010", "preset17": "#603d30", "preset18": "#c19c00", "preset19": "#0b6a0b",
	"preset20": "#005b70", "preset21": "#5c6a2e", "preset22": "#004e8c", "preset23": "#5c2e91", "preset24": "#77004d",
}

// tonePresets maps opendesk's tones to Outlook category presets.
var tonePresets = map[string]string{
	"gray": "preset12", "red": "preset0", "orange": "preset1", "amber": "preset3", "green": "preset4",
	"teal": "preset5", "blue": "preset7", "indigo": "preset22", "violet": "preset8", "pink": "preset9",
}

// gmailColor is a pair from Gmail's label palette (Gmail refuses any other colour).
type gmailColor struct {
	Background string `json:"backgroundColor"`
	Text       string `json:"textColor"`
}

// toneGmail maps opendesk's tones to Gmail palette pairs.
var toneGmail = map[string]gmailColor{
	"gray": {"#999999", "#ffffff"}, "red": {"#fb4c2f", "#ffffff"}, "orange": {"#ffad47", "#ffffff"},
	"amber": {"#fad165", "#000000"}, "green": {"#16a766", "#ffffff"}, "teal": {"#2da2bb", "#ffffff"},
	"blue": {"#4a86e8", "#ffffff"}, "indigo": {"#285bac", "#ffffff"}, "violet": {"#a479e2", "#ffffff"},
	"pink": {"#f691b3", "#ffffff"},
}

// labelColorer is a provider whose labels take colours.
type labelColorer interface {
	SetLabelColor(ctx context.Context, f RemoteFolder, tone string) (RemoteFolder, error)
}

// labelCreator is a provider that makes labels apart from folders (Microsoft categories).
type labelCreator interface {
	CreateLabel(ctx context.Context, name, tone string) (RemoteFolder, error)
}

// SetLabelColor sets a Gmail label's colour to a palette pair.
func (g *gmailProvider) SetLabelColor(ctx context.Context, f RemoteFolder, tone string) (RemoteFolder, error) {
	c, ok := toneGmail[tone]
	if !ok {
		return RemoteFolder{}, &providerError{msg: "Choose one of the label colours.", permanent: true}
	}
	var l gmailLabel
	if err := g.do(ctx, http.MethodPatch, "/labels/"+url.PathEscape(f.RemoteID), map[string]any{"color": c}, &l); err != nil {
		return RemoteFolder{}, err
	}
	out, ok := l.remote()
	if !ok {
		return RemoteFolder{}, &providerError{msg: "Gmail did not change the label."}
	}
	if out.Color == "" {
		out.Color = c.Background
	}
	return out, nil
}

type graphCategory struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Color       string `json:"color"`
}

func (c graphCategory) remote() RemoteFolder {
	return RemoteFolder{RemoteID: categoryPrefix + c.DisplayName, Name: c.DisplayName, Path: c.DisplayName, Kind: KindLabel, Color: graphPresets[c.Color]}
}

// masterCategories reads the account's category list; ok is false when the account may
// not read it (no MailboxSettings scope) or Microsoft did not answer.
func (g *graphProvider) masterCategories(ctx context.Context) ([]graphCategory, bool) {
	var out struct {
		Value []graphCategory `json:"value"`
	}
	if err := g.do(ctx, http.MethodGet, "/me/outlook/masterCategories", nil, &out); err != nil {
		var pe *providerError
		if !errors.Is(err, errReconnect) && !(errors.As(err, &pe) && pe.status == http.StatusForbidden) {
			g.s.log.Info("collab categories not read", "account_id", g.a.ID, "reason", safeError(err))
		}
		return nil, false
	}
	return out.Value, true
}

// categories are the account's categories as labels: the master list, plus the stored
// categories messages still carry (all stored ones when the list cannot be read).
func (g *graphProvider) categories(ctx context.Context) ([]RemoteFolder, error) {
	list, ok := g.masterCategories(ctx)
	out := []RemoteFolder{}
	seen := map[string]bool{}
	for _, c := range list {
		if c.DisplayName == "" || seen[strings.ToLower(c.DisplayName)] {
			continue
		}
		seen[strings.ToLower(c.DisplayName)] = true
		out = append(out, c.remote())
	}
	rows, err := g.s.d.DB.Pool.Query(ctx, `SELECT `+folderColumns+` FROM collab_folders f WHERE f.account_id = $1 AND f.kind = 'label'
		AND (NOT $2 OR EXISTS (SELECT 1 FROM collab_message_folders mf WHERE mf.folder_id = f.id)) ORDER BY f.path`, g.a.ID, ok)
	if err != nil {
		return nil, err
	}
	stored, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (*folderRow, error) { return scanFolder(r) })
	if err != nil {
		return nil, err
	}
	for _, f := range stored {
		if !isCategory(f.RemoteID) || seen[strings.ToLower(categoryName(f.RemoteID))] {
			continue
		}
		seen[strings.ToLower(categoryName(f.RemoteID))] = true
		r := f.remote()
		if ok {
			r.Color = "" // not in the master list: no colour of its own
		}
		out = append(out, r)
	}
	return out, nil
}

// findCategory is the master category with the label's name.
func (g *graphProvider) findCategory(ctx context.Context, f RemoteFolder) (*graphCategory, error) {
	var out struct {
		Value []graphCategory `json:"value"`
	}
	if err := g.do(ctx, http.MethodGet, "/me/outlook/masterCategories", nil, &out); err != nil {
		return nil, err
	}
	name := categoryName(f.RemoteID)
	for _, c := range out.Value {
		if strings.EqualFold(c.DisplayName, name) {
			return &c, nil
		}
	}
	return nil, nil
}

// CreateLabel adds a master category.
func (g *graphProvider) CreateLabel(ctx context.Context, name, tone string) (RemoteFolder, error) {
	preset := "none"
	if p, ok := tonePresets[tone]; ok {
		preset = p
	}
	var c graphCategory
	if err := g.do(ctx, http.MethodPost, "/me/outlook/masterCategories", map[string]string{"displayName": name, "color": preset}, &c); err != nil {
		return RemoteFolder{}, err
	}
	if c.DisplayName == "" {
		c.DisplayName = name
	}
	return c.remote(), nil
}

// SetLabelColor sets a category's preset colour; a category only messages carry joins
// the master list with it.
func (g *graphProvider) SetLabelColor(ctx context.Context, f RemoteFolder, tone string) (RemoteFolder, error) {
	preset, ok := tonePresets[tone]
	if !ok {
		return RemoteFolder{}, &providerError{msg: "Choose one of the label colours.", permanent: true}
	}
	c, err := g.findCategory(ctx, f)
	if err != nil {
		return RemoteFolder{}, err
	}
	if c == nil {
		return g.CreateLabel(ctx, categoryName(f.RemoteID), tone)
	}
	var out graphCategory
	if err := g.do(ctx, http.MethodPatch, "/me/outlook/masterCategories/"+url.PathEscape(c.ID), map[string]string{"color": preset}, &out); err != nil {
		return RemoteFolder{}, err
	}
	if out.DisplayName == "" {
		out = *c
		out.Color = preset
	}
	return out.remote(), nil
}

// deleteCategory removes a master category; messages keep carrying its name at Microsoft.
func (g *graphProvider) deleteCategory(ctx context.Context, f RemoteFolder) error {
	c, err := g.findCategory(ctx, f)
	if err != nil || c == nil {
		return err
	}
	err = g.do(ctx, http.MethodDelete, "/me/outlook/masterCategories/"+url.PathEscape(c.ID), nil, nil)
	if isNotFound(err) {
		return nil
	}
	return err
}

// applyCategories adds or removes a category on messages (their categories PATCHed).
func (g *graphProvider) applyCategories(ctx context.Context, op ApplyOp) error {
	if op.Target == nil || !isCategory(op.Target.RemoteID) {
		return errLabels
	}
	name := categoryName(op.Target.RemoteID)
	for _, m := range op.Messages {
		path := "/me/messages/" + url.PathEscape(m.RemoteID)
		var cur struct {
			Categories []string `json:"categories"`
		}
		err := g.do(ctx, http.MethodGet, path+"?$select=categories", nil, &cur)
		if isNotFound(err) {
			continue
		}
		if err != nil {
			return err
		}
		has := slices.IndexFunc(cur.Categories, func(c string) bool { return strings.EqualFold(c, name) }) >= 0
		next := slices.Clone(cur.Categories)
		switch {
		case op.Action == ActionLabel && !has:
			next = append(next, name)
		case op.Action == ActionUnlabel && has:
			next = slices.DeleteFunc(next, func(c string) bool { return strings.EqualFold(c, name) })
		default:
			continue
		}
		if next == nil {
			next = []string{}
		}
		if err := g.do(ctx, http.MethodPatch, path, map[string]any{"categories": next}, nil); err != nil && !isNotFound(err) {
			return err
		}
	}
	return nil
}

// categoryIDs maps a Graph message's categories to stored labels, storing the ones not
// seen before (in tx).
func (p *pass) categoryIDs(ctx context.Context, tx pgx.Tx, names []string) ([]uuid.UUID, error) {
	out := []uuid.UUID{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		rid := categoryPrefix + name
		f := p.folders[rid]
		if f == nil {
			for r, x := range p.folders {
				if isCategory(r) && strings.EqualFold(categoryName(r), name) {
					f = x
					break
				}
			}
		}
		if f == nil {
			if len(p.folders) >= maxFolders {
				continue
			}
			var err error
			if f, err = p.s.upsertFolder(ctx, tx, p.a, RemoteFolder{RemoteID: rid, Name: name, Path: name, Kind: KindLabel}); err != nil {
				return nil, err
			}
			p.folders[rid] = f
			p.foldersChanged = true
		}
		out = addID(out, f.ID)
	}
	return out, nil
}
