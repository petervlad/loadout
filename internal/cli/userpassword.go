package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/password"
)

// maxPasswordInput bounds what is read from a pipe: a password is at most
// password.MaxLength characters, and four bytes each is the widest.
const maxPasswordInput = 4*password.MaxLength + 2

func newUserPasswordCmd(d Deps, dataDir *string) *cobra.Command {
	var email string
	cmd := &cobra.Command{
		Use:   "password",
		Short: "Set a user's password from standard input (creates the account)",
		Long: "Set the password of an account on this registry host, creating it when it does not exist.\n" +
			"The password is read from standard input, never from a flag, so it stays out of shell history\n" +
			"and process lists: a terminal asks for it without echo, a pipe supplies one line.\n" +
			"The address is marked verified and the user's other sessions end.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			pw, err := readNewPassword(cmd.InOrStdin(), cmd.ErrOrStderr())
			if err != nil {
				return err
			}
			var u account.User
			err = withRegistry(cmd.Context(), d, *dataDir, func(reg Registry) error {
				var err error
				u, err = reg.Accounts.SetPasswordAsOperator(cmd.Context(), account.Operator(), email, pw)
				return err
			})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "password set for %s\n", u.Email)
			return nil
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "the user's email address (required)")
	_ = cmd.MarkFlagRequired("email") // only fails for an unknown flag name
	return cmd
}

// readNewPassword reads the password from in: on a terminal twice, without
// echo; from a pipe, the first line.
func readNewPassword(in io.Reader, prompt io.Writer) (string, error) {
	if f, ok := in.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		return promptPassword(int(f.Fd()), prompt)
	}
	line, err := bufio.NewReader(io.LimitReader(in, maxPasswordInput)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("read password: %w", err)
	}
	pw := strings.TrimRight(line, "\r\n")
	if pw == "" {
		return "", errors.New("no password on standard input; pipe one line into the command")
	}
	return pw, nil
}

func promptPassword(fd int, prompt io.Writer) (string, error) {
	ask := func(label string) (string, error) {
		fmt.Fprint(prompt, label)
		b, err := term.ReadPassword(fd)
		fmt.Fprintln(prompt)
		if err != nil {
			return "", fmt.Errorf("read password: %w", err)
		}
		return string(b), nil
	}
	first, err := ask("New password: ")
	if err != nil {
		return "", err
	}
	again, err := ask("Again: ")
	if err != nil {
		return "", err
	}
	if first != again {
		return "", errors.New("the two passwords differ; nothing was changed")
	}
	return first, nil
}
