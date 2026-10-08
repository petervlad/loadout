package cli

import (
	"context"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/petervlad/loadout/internal/config"
	"github.com/petervlad/loadout/internal/mail"
)

// MailSender delivers one email; it is what `mail test` needs of a mailer.
type MailSender interface {
	Send(ctx context.Context, m mail.Message) error
}

func newMailCmd(d Deps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "mail",
		Short: "Check the registry's email (run on the registry host)",
	}
	cmd.AddCommand(newMailTestCmd(d))
	return cmd
}

func newMailTestCmd(d Deps) *cobra.Command {
	var to string
	cmd := &cobra.Command{
		Use:   "test",
		Short: "Send one test mail with the registry's mail settings",
		Long: `Send one message through the mail settings in this environment and wait for
the mail server's answer, so a wrong host, password or sender shows now and
not when the first person signs up. It prints "sent", or the server's
refusal; the password is never printed.

On the registry host, run it with the service's environment, for example:
  sudo systemd-run --pipe --wait --uid=loadout \
    --property=EnvironmentFile=/etc/loadout/loadout.env \
    /usr/local/bin/loadout mail test --to you@example.com`,
		Example: "  loadout mail test --to you@example.com",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runMailTest(cmd, d, to)
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "the address to send the test mail to (required)")
	_ = cmd.MarkFlagRequired("to") // only fails for an unknown flag name
	return quiet(cmd)
}

func runMailTest(cmd *cobra.Command, d Deps, to string) error {
	if d.NewMailer == nil {
		return errors.New("this build cannot send mail")
	}
	login := d.Config.Login
	if login.PublicURL == "" {
		return errors.New("LOADOUT_PUBLIC_URL is not set, so the registry sends no mail; set it and the SMTP variables, then run this again")
	}
	msg := mail.Message{
		To:      to,
		Subject: "loadout test mail",
		Text:    "loadout test mail from " + login.PublicURL + "\n\nIf you can read this, sign-in mail works.\n",
	}
	if err := msg.Validate(); err != nil {
		return err
	}
	sender, kind, err := d.NewMailer(login)
	if err != nil {
		return err
	}
	if err := refuseUnsent(login, kind); err != nil {
		return err
	}
	if err := sender.Send(cmd.Context(), msg); err != nil {
		return fmt.Errorf("the mail server did not accept the message: %w", err)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "sent")
	return nil
}

// refuseUnsent explains why a mailer of this kind cannot prove anything.
func refuseUnsent(login config.Login, kind string) error {
	switch {
	case login.Simple:
		return errors.New("LOADOUT_LOGIN=simple sends no mail; unset it to test email")
	case kind == "log":
		return errors.New("LOADOUT_DEV=1 only logs mail and sends none; set LOADOUT_SMTP_HOST and the other SMTP variables to test real delivery")
	case kind == "off":
		return errors.New("email is off; set LOADOUT_SMTP_HOST, LOADOUT_SMTP_USER, LOADOUT_SMTP_PASSWORD and LOADOUT_MAIL_FROM first")
	}
	return nil
}
