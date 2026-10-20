package widget

import (
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/domain"
	"github.com/useopendesk/opendesk/internal/helpdesk/hd"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/storage"
)

// maxUploadBody leaves room for the multipart envelope around a 25 MB file.
const maxUploadBody = hd.MaxAttachmentBytes + 1<<20

type attachmentUploadForm struct {
	File huma.FormFile `form:"file" required:"true" doc:"The file, at most 25 MB"`
}

type attachmentUploadInput struct {
	Origin  string `header:"Origin"`
	RawBody huma.MultipartFormFiles[attachmentUploadForm]
}

type attachmentUploadOutput struct {
	Body domain.Attachment
}

// limitBody caps the request body before huma parses the multipart form.
func limitBody(api huma.API) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		r, w := humachi.Unwrap(ctx)
		if r.ContentLength > maxUploadBody {
			_ = huma.WriteErr(api, ctx, http.StatusRequestEntityTooLarge, "Files can be at most 25 MB.")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, maxUploadBody)
		next(ctx)
	}
}

func (h *handler) registerAttachments(api huma.API, r chi.Router) {
	huma.Register(api, huma.Operation{OperationID: "upload-attachment", Method: http.MethodPost, Path: "/v1/attachments",
		Summary: "Upload an attachment", Description: "Stores a file (max 25 MB) as an unattached upload. Send its id in attachment_ids to attach it.",
		Tags: []string{"Attachments"}, DefaultStatus: http.StatusCreated, MaxBodyBytes: maxUploadBody,
		Middlewares: huma.Middlewares{limitBody(api)}},
		func(ctx context.Context, in *attachmentUploadInput) (*attachmentUploadOutput, error) {
			defer removeForm(in.RawBody.Form)
			p, err := auth.RequireWorkspace(ctx, auth.RoleAgent, auth.ScopeTicketsWrite)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			a, err := h.save(ctx, in, hd.Upload{WorkspaceID: p.WorkspaceID, UploadedBy: p.ActorUserID(), UploadedByAPIKey: p.ActorAPIKeyID()})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &attachmentUploadOutput{a}, nil
		})

	huma.Register(api, huma.Operation{OperationID: "widget-upload-attachment", Method: http.MethodPost, Path: "/v1/widget/attachments",
		Summary: "Upload a file from the chat widget", Tags: []string{"Widget"}, DefaultStatus: http.StatusCreated,
		MaxBodyBytes: maxUploadBody, Middlewares: huma.Middlewares{limitBody(api)}},
		func(ctx context.Context, in *attachmentUploadInput) (*attachmentUploadOutput, error) {
			defer removeForm(in.RawBody.Form)
			p, g, err := h.visitorWidget(ctx, in.Origin)
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			if !takesUploads(g.Settings) {
				return nil, errAttachmentsOff
			}
			if err := h.allowWrite(p, "upload", uploadLimit); err != nil {
				return nil, err
			}
			a, err := h.save(ctx, in, hd.Upload{WorkspaceID: p.WorkspaceID, UploadedByContact: &p.ContactID,
				Accept: visitorAccept(g.Settings)})
			if err != nil {
				return nil, httpx.Wrap(err)
			}
			return &attachmentUploadOutput{a}, nil
		})

	r.Get("/v1/attachments/{id}", h.download)
}

func removeForm(f *multipart.Form) {
	if f != nil {
		_ = f.RemoveAll()
	}
}

func (h *handler) save(ctx context.Context, in *attachmentUploadInput, up hd.Upload) (domain.Attachment, error) {
	data := in.RawBody.Data()
	if data == nil || data.File.File == nil || !data.File.IsSet {
		return domain.Attachment{}, httpx.Validation("file is required")
	}
	defer data.File.Close()
	up.Filename, up.Size, up.Body = data.File.Filename, data.File.Size, data.File.File
	return hd.SaveUpload(ctx, h.d.Storage, h.d.DB.Pool, up)
}

type storedAttachment struct {
	WorkspaceID      uuid.UUID
	Filename         string
	ContentType      string
	Size             int64
	Key              string
	UploaderContact  *uuid.UUID
	UploaderUser     *uuid.UUID
	UploaderKey      *uuid.UUID
	TicketContact    *uuid.UUID
	MessageKind      *string
	TicketDeleted    bool
	TicketIsAttached bool
	TicketTeam       *uuid.UUID
	TicketAssignee   *uuid.UUID
}

func loadStored(ctx context.Context, q db.Querier, id uuid.UUID) (storedAttachment, error) {
	var a storedAttachment
	err := q.QueryRow(ctx, `SELECT a.workspace_id, a.filename, a.content_type, a.size_bytes, a.storage_key,
		a.uploaded_by_contact_id, a.uploaded_by, a.uploaded_by_api_key_id, t.contact_id, m.kind,
		COALESCE(t.deleted_at IS NOT NULL, false), m.id IS NOT NULL, t.team_id, t.assignee_id
		FROM attachments a LEFT JOIN messages m ON m.id = a.message_id LEFT JOIN tickets t ON t.id = m.ticket_id
		WHERE a.id = $1`, id).Scan(&a.WorkspaceID, &a.Filename, &a.ContentType, &a.Size, &a.Key,
		&a.UploaderContact, &a.UploaderUser, &a.UploaderKey, &a.TicketContact, &a.MessageKind, &a.TicketDeleted,
		&a.TicketIsAttached, &a.TicketTeam, &a.TicketAssignee)
	return a, err
}

// mayRead decides access: a signed link, a member or key of the owning workspace who may
// see the ticket carrying it (contracts/teams.md "Visibility") or who uploaded it while it
// is not sent yet, or the visitor who uploaded the file or whose conversation carries it
// in a public reply.
func mayRead(r *http.Request, q db.Querier, id uuid.UUID, a storedAttachment) (bool, error) {
	query := r.URL.Query()
	if exp, sig := query.Get("exp"), query.Get("sig"); exp != "" && sig != "" && domain.VerifyAttachmentSig(id, exp, sig) {
		return true, nil
	}
	p := auth.FromContext(r.Context())
	if p == nil || p.WorkspaceID != a.WorkspaceID {
		return false, nil
	}
	switch p.Kind {
	case auth.KindUser, auth.KindAPIKey:
		if p.Kind == auth.KindUser && !p.Role.Valid() || p.Kind == auth.KindAPIKey && !p.HasScope(auth.ScopeTicketsRead) {
			return false, nil
		}
		if !a.TicketIsAttached {
			// An upload not yet sent is its uploader's alone (a private Collab draft's
			// pictures included), as attaching it is.
			if p.Kind == auth.KindUser {
				return a.UploaderUser != nil && *a.UploaderUser == p.UserID, nil
			}
			return a.UploaderKey != nil && *a.UploaderKey == p.APIKeyID, nil
		}
		return domain.CanSee(r.Context(), q, p, a.TicketTeam, a.TicketAssignee)
	case auth.KindVisitor:
		if a.UploaderContact != nil && *a.UploaderContact == p.ContactID {
			return true, nil
		}
		return a.TicketContact != nil && *a.TicketContact == p.ContactID && !a.TicketDeleted &&
			a.MessageKind != nil && *a.MessageKind == "reply", nil
	}
	return false, nil
}

func (h *handler) download(w http.ResponseWriter, r *http.Request) {
	id, ok := parseUUID(chi.URLParam(r, "id"))
	if !ok {
		writeProblem(w, r, http.StatusNotFound, "not_found", "Attachment not found")
		return
	}
	ctx := r.Context()
	a, err := loadStored(ctx, h.d.DB.Pool, id)
	if err != nil {
		if !db.IsNotFound(err) {
			h.d.Log.Error("load attachment", "err", err, "request_id", httpx.RequestIDFrom(ctx))
			writeProblem(w, r, http.StatusInternalServerError, "internal", "Something went wrong on our side.")
			return
		}
		writeProblem(w, r, http.StatusNotFound, "not_found", "Attachment not found")
		return
	}
	ok, err = mayRead(r, h.d.DB.Pool, id, a)
	if err != nil {
		h.d.Log.Error("attachment access", "err", err, "request_id", httpx.RequestIDFrom(ctx))
		writeProblem(w, r, http.StatusInternalServerError, "internal", "Something went wrong on our side.")
		return
	}
	if !ok {
		writeProblem(w, r, http.StatusNotFound, "not_found", "Attachment not found")
		return
	}
	if signed, err := h.d.Storage.SignedURL(ctx, a.Key, a.Filename, 5*time.Minute); err == nil && signed != "" {
		w.Header().Set("Cache-Control", "private, no-store")
		http.Redirect(w, r, signed, http.StatusFound)
		return
	} else if err != nil {
		h.d.Log.Warn("sign attachment url", "err", err)
	}
	body, err := h.d.Storage.Get(ctx, a.Key)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeProblem(w, r, http.StatusNotFound, "not_found", "Attachment not found")
			return
		}
		h.d.Log.Error("read attachment", "err", err, "request_id", httpx.RequestIDFrom(ctx))
		writeProblem(w, r, http.StatusInternalServerError, "internal", "Something went wrong on our side.")
		return
	}
	defer body.Close()
	hdr := w.Header()
	ct := a.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	hdr.Set("Content-Type", ct)
	hdr.Set("Content-Length", strconv.FormatInt(a.Size, 10))
	hdr.Set("Content-Disposition", contentDisposition(a.Filename, hd.InlineSafe(ct)))
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Cache-Control", "private, no-store")
	hdr.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	hdr.Set("Cross-Origin-Resource-Policy", "cross-origin")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, body)
}

// contentDisposition builds an RFC 6266 header with an ASCII fallback and the
// RFC 5987 UTF-8 name.
func contentDisposition(filename string, inline bool) string {
	kind := "attachment"
	if inline {
		kind = "inline"
	}
	ascii := strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '"' || r == '\\' || r == ';' {
			return '_'
		}
		return r
	}, filename)
	if ascii == "" {
		ascii = "file"
	}
	return kind + `; filename="` + ascii + `"; filename*=UTF-8''` + url.PathEscape(filename)
}
