package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/jackc/pgx/v5"
	"github.com/spf13/cobra"

	"github.com/useopendesk/opendesk/internal/admin"
	"github.com/useopendesk/opendesk/internal/config"
	"github.com/useopendesk/opendesk/internal/db"
	"github.com/useopendesk/opendesk/internal/httpx"
)

// adminCommand manages the instance admins (contracts/admin.md). It runs where the
// instance runs, with the same configuration as serve; every change is in the admin
// audit log with actor cli.
func adminCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "admin", Short: "Manage the instance admins who can open the admin panel"}

	grant := &cobra.Command{
		Use:   "grant <email>",
		Short: "Make an account an instance admin, or change its role",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			role, _ := cmd.Flags().GetString("role")
			return withDB(func(ctx context.Context, d *db.DB) error {
				var x admin.InstanceAdmin
				var existed bool
				err := d.Tx(ctx, func(tx pgx.Tx) error {
					var err error
					x, existed, err = admin.Grant(ctx, tx, args[0], admin.Role(role), admin.CLIActor)
					return err
				})
				if err != nil {
					return cliError(err)
				}
				verb := "is now"
				if existed {
					verb = "now has the role"
				}
				fmt.Printf("%s %s %s\n", x.User.Email, verb, x.Role)
				if !x.TOTPEnabled {
					fmt.Println("They sign in to the admin panel only after turning on two-factor authentication in the app.")
				}
				return nil
			})
		},
	}
	grant.Flags().String("role", string(admin.RoleSuper), "viewer, support or super")

	revoke := &cobra.Command{
		Use:   "revoke <email>",
		Short: "Remove an instance admin and end their admin sessions",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return withDB(func(ctx context.Context, d *db.DB) error {
				err := d.Tx(ctx, func(tx pgx.Tx) error {
					uid, err := admin.UserIDByEmail(ctx, tx, args[0])
					if err != nil {
						return err
					}
					return admin.Revoke(ctx, tx, uid, admin.CLIActor)
				})
				if err != nil {
					return cliError(err)
				}
				fmt.Printf("%s is no longer an instance admin\n", args[0])
				return nil
			})
		},
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List the instance admins",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return withDB(func(ctx context.Context, d *db.DB) error {
				admins, err := admin.ListAdmins(ctx, d.Pool)
				if err != nil {
					return err
				}
				if len(admins) == 0 {
					fmt.Println("No instance admins yet. Add one with: opendesk admin grant <email>")
					return nil
				}
				w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
				fmt.Fprintln(w, "EMAIL\tROLE\t2FA\tSINCE")
				for _, a := range admins {
					twoFA := "off"
					if a.TOTPEnabled {
						twoFA = "on"
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", a.User.Email, a.Role, twoFA, a.CreatedAt.Format("2006-01-02"))
				}
				return w.Flush()
			})
		},
	}

	cmd.AddCommand(grant, revoke, list, testWorkspaceCommand())
	return cmd
}

func withDB(fn func(context.Context, *db.DB) error) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	d, err := db.Open(ctx, cfg.DatabaseURL, cfg.DatabaseURLDirect)
	if err != nil {
		return err
	}
	defer d.Close()
	return fn(ctx, d)
}

// cliError shows an API error's sentence instead of its HTTP shape.
func cliError(err error) error {
	var he *httpx.Error
	if errors.As(err, &he) {
		return errors.New(he.Detail)
	}
	return err
}
