package collab

import (
	"context"
	"encoding/json"
)

// Provider is one account's connection to its mail provider: Gmail, Microsoft Graph or
// IMAP/SMTP. Methods return errors whose text is safe to store and show (no tokens,
// passwords or message content); a refused grant or login wraps errReconnect.
//
// The sync engine (sync.go) reads changes through the provider-specific sync methods;
// everything else goes through this interface.
type Provider interface {
	// Folders lists the account's folders and labels, at most maxFolders.
	Folders(ctx context.Context) ([]RemoteFolder, error)
	// Apply makes an action at the provider for the given messages.
	Apply(ctx context.Context, op ApplyOp) error
	// Send sends a raw RFC 822 message (Bcc header included) from the account and reports
	// where the provider keeps the sent copy.
	Send(ctx context.Context, raw []byte, info SendInfo) (SentRef, error)
	// FetchRaw reads one message's raw RFC 822 source.
	FetchRaw(ctx context.Context, ref MessageRef) ([]byte, error)
	// CreateFolder, RenameFolder and DeleteFolder manage user folders and labels.
	CreateFolder(ctx context.Context, name string, parent *RemoteFolder) (RemoteFolder, error)
	RenameFolder(ctx context.Context, f RemoteFolder, name string) (RemoteFolder, error)
	DeleteFolder(ctx context.Context, f RemoteFolder) error
	// Close ends any connection the provider holds (IMAP).
	Close()
}

// RemoteFolder is a folder or label at the provider.
type RemoteFolder struct {
	RemoteID string // Gmail label id, Graph folder id, IMAP mailbox name
	Name     string // the last part of the path
	Path     string // "Clients/Acme"
	Role     string // a Role* constant, or "" for a user folder
	Kind     string // KindFolder or KindLabel
	Color    string // Gmail label background colour, "" otherwise
}

// MessageRef locates one message at the provider.
type MessageRef struct {
	RemoteID       string          // collab_messages.remote_id
	ThreadRemoteID string          // collab_threads.remote_id
	Location       json.RawMessage // collab_messages.location (IMAP: folder and UID)
}

// ApplyOp is one action on messages at the provider (contracts/collab.md "Actions").
type ApplyOp struct {
	Action   string
	Messages []MessageRef
	// Target is the folder or label of move, label and unlabel.
	Target *RemoteFolder
	// Special are the account's special folders by role (inbox, archive, trash, spam),
	// for the actions that move to them.
	Special map[string]RemoteFolder
}

// SendInfo is what a provider needs besides the raw message.
type SendInfo struct {
	From           string   // the account's address
	Recipients     []string // every To, Cc and Bcc address (SMTP envelope)
	ThreadRemoteID string   // the conversation a reply belongs to ("" for new mail)
}

// SentRef is where the provider keeps a message it sent; empty fields when it does not say.
type SentRef struct {
	RemoteID       string
	ThreadRemoteID string
	Location       json.RawMessage
	// FolderRemoteIDs are the folders or labels the sent copy is in (Sent).
	FolderRemoteIDs []string
}
