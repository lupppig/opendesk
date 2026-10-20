package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/spf13/cobra"

	"github.com/useopendesk/opendesk/internal/admin"
	"github.com/useopendesk/opendesk/internal/app"
	"github.com/useopendesk/opendesk/internal/auth"
	"github.com/useopendesk/opendesk/internal/config"
	"github.com/useopendesk/opendesk/internal/core"
	"github.com/useopendesk/opendesk/internal/crypto"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/deps"
	"github.com/useopendesk/opendesk/internal/httpx"
	"github.com/useopendesk/opendesk/internal/jobs"
)

// testWorkspaceCommand creates, lists and deletes test workspaces (contracts/admin.md
// "Test workspaces") from the machine that runs the instance, with actor cli in the admin
// audit log. Handy for local demo data.
func testWorkspaceCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "test-workspace", Short: "Create, list and delete test workspaces with sample data"}

	create := &cobra.Command{
		Use:   "create",
		Short: "Create a test workspace and print its owner's credentials once",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			f := cmd.Flags()
			name, _ := f.GetString("name")
			email, _ := f.GetString("owner-email")
			ownerName, _ := f.GetString("owner-name")
			planName, _ := f.GetString("plan")
			size, _ := f.GetString("sample")
			kinds, _ := f.GetStringSlice("kinds")
			deleteAfter, _ := f.GetInt("delete-after")
			passwordDays, _ := f.GetInt("password-days")
			reason, _ := f.GetString("reason")
			queue, _ := f.GetBool("queue")
			if email == "" {
				email = "owner-" + crypto.RandomString(6) + "@test" + admin.TestEmailSuffix
			}
			in := admin.TestWorkspaceInput{Name: name, OwnerEmail: email, OwnerName: ownerName, PasswordDays: passwordDays, Plan: planName,
				Reason: reason, SeedInline: !queue}
			if deleteAfter > 0 {
				in.DeleteAfterDays = &deleteAfter
			}
			if size != "" {
				in.Sample = &admin.SampleRequest{Size: size, Kinds: kinds}
			}
			return withDeps(func(ctx context.Context, d *deps.Deps) error {
				res, err := admin.CreateTestWorkspace(ctx, d, in, admin.CLIActor)
				if err != nil {
					return cliError(err)
				}
				w := res.Workspace
				fmt.Printf("Created test workspace %s (%s), plan %s\n", w.Workspace.Name, w.Workspace.ID, w.Plan)
				if w.TestExpiresAt != nil {
					fmt.Printf("Deleted on %s\n", w.TestExpiresAt.Local().Format(time.DateTime))
				} else {
					fmt.Println("Kept until deleted")
				}
				fmt.Println()
				fmt.Printf("  Sign in   %s\n", res.SignInURL)
				fmt.Printf("  Email     %s\n", res.Owner.Email)
				if res.Owner.Password != nil {
					fmt.Printf("  Password  %s\n", *res.Owner.Password)
					fmt.Printf("  Expires   %s\n", res.Owner.PasswordExpiresAt.Local().Format(time.DateTime))
					fmt.Println("\nThe password is shown only now. Renew it from the admin panel if it is lost.")
				} else {
					fmt.Println("  (an existing account: it signs in with its own password)")
				}
				if in.Sample == nil {
					return nil
				}
				if queue {
					fmt.Println("\nSample data is queued; the instance's workers add it.")
					return nil
				}
				fmt.Printf("\nAdding %s sample data (%s)...\n", in.Sample.Size, strings.Join(w.SampleData.Kinds, ", "))
				err = admin.RunSeed(ctx, d, jobs.SeedTestWorkspaceArgs{WorkspaceID: w.Workspace.ID, Size: in.Sample.Size, Kinds: w.SampleData.Kinds, Batch: 1})
				if err != nil {
					return fmt.Errorf("sample data: %w", err)
				}
				list, err := admin.ListTestWorkspaces(ctx, d.DB.Pool, w.CreatedAt.Add(-time.Microsecond), uuid.Nil, 50)
				if err != nil {
					return err
				}
				for _, x := range list {
					if x.Workspace.ID == w.Workspace.ID && x.SampleData.Counts != nil {
						c := x.SampleData.Counts
						var parts []string
						for _, p := range []struct {
							n    int
							what string
						}{{c.Tickets, "tickets"}, {c.Messages, "messages"}, {c.Chats, "chats"}, {c.Contacts, "contacts"}, {c.Companies, "companies"},
							{c.Articles, "articles"}, {c.Members, "teammates"}, {c.Teams, "teams"}, {c.MailThreads, "mail threads"}} {
							if p.n > 0 {
								parts = append(parts, fmt.Sprintf("%d %s", p.n, p.what))
							}
						}
						fmt.Printf("Added %s\n", strings.Join(parts, ", "))
					}
				}
				return nil
			})
		},
	}
	cf := create.Flags()
	cf.String("name", "", "workspace name (required)")
	cf.String("owner-email", "", "an existing account's email, or a new test account's ending in .invalid (default: a new owner-…@test.invalid)")
	cf.String("owner-name", "", "a new test account's name")
	cf.String("plan", "free", "free, pro or collab")
	cf.String("sample", "", "add sample data: small, realistic or large")
	cf.StringSlice("kinds", admin.SampleKinds, "kinds of sample data: "+strings.Join(admin.SampleKinds, ","))
	cf.Int("delete-after", admin.DefaultDeleteAfterDays, "delete the workspace after this many days (0 keeps it)")
	cf.Int("password-days", admin.DefaultPasswordDays, "how long a new account's password works, in days (1..2)")
	cf.String("reason", "Demo data from the command line", "why it exists, for the admin audit log")
	cf.Bool("queue", false, "queue the sample data for the instance's workers instead of adding it now")
	_ = create.MarkFlagRequired("name")

	list := &cobra.Command{
		Use:   "list",
		Short: "List the test workspaces, oldest first",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return withDB(func(ctx context.Context, d *db.DB) error {
				list, err := admin.ListTestWorkspaces(ctx, d.Pool, time.Time{}, uuid.Nil, 1000)
				if err != nil {
					return err
				}
				if len(list) == 0 {
					fmt.Println("No test workspaces. Create one with: opendesk admin test-workspace create --name Demo --sample realistic")
					return nil
				}
				w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
				fmt.Fprintln(w, "ID\tNAME\tOWNER\tPLAN\tSAMPLE DATA\tDELETED ON\tCREATED")
				for _, x := range list {
					owner, expires := "-", "kept"
					if x.Owner != nil {
						owner = x.Owner.Email
					}
					if x.TestExpiresAt != nil {
						expires = x.TestExpiresAt.Local().Format("2006-01-02")
					}
					sample := x.SampleData.Status
					if x.SampleData.Size != "" {
						sample += " (" + x.SampleData.Size + ")"
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", x.Workspace.ID, x.Workspace.Name, owner, x.Plan, sample, expires,
						x.CreatedAt.Local().Format("2006-01-02"))
				}
				return w.Flush()
			})
		},
	}

	del := &cobra.Command{
		Use:   "delete <id or slug>",
		Short: "Delete a test workspace with all its data, and its test accounts left in no other workspace",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return withDeps(func(ctx context.Context, d *deps.Deps) error {
				ops := core.NewOps(d)
				var removals []core.Removal
				var name string
				var accounts int
				err := d.DB.Tx(ctx, func(tx pgx.Tx) error {
					var wid uuid.UUID
					var test bool
					byID, _ := uuid.Parse(args[0])
					err := tx.QueryRow(ctx, `SELECT id, name, test FROM workspaces WHERE id = $1 OR slug = $2 FOR UPDATE`, byID, args[0]).Scan(&wid, &name, &test)
					if db.IsNotFound(err) {
						return httpx.NotFound("Workspace")
					}
					if err != nil {
						return err
					}
					if !test {
						return httpx.E(409, "not_test", "This is not a test workspace; delete it from the admin panel.")
					}
					if removals, accounts, err = admin.DeleteTestWorkspace(ctx, tx, ops, &auth.Principal{}, wid); err != nil {
						return err
					}
					return admin.WriteAudit(ctx, tx, admin.CLIActor, "workspace.deleted", "workspace", wid.String(),
						map[string]any{"test": true, "accounts_deleted": accounts})
				})
				if err != nil {
					return cliError(err)
				}
				for _, r := range removals {
					ops.Finish(r)
				}
				fmt.Printf("Deleted %s and %d test account(s)\n", name, accounts)
				return nil
			})
		},
	}

	cmd.AddCommand(create, list, del)
	return cmd
}

func withDeps(fn func(context.Context, *deps.Deps) error) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	d, cleanup, err := app.BuildTool(ctx, cfg, app.NewLogger(cfg))
	if err != nil {
		return err
	}
	defer cleanup()
	return fn(ctx, d)
}
