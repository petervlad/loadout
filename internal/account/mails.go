package account

import (
	"bytes"
	"html/template"
	"strings"

	"github.com/petervlad/loadout/internal/mail"
)

var codeHTML = template.Must(template.New("code").Parse(`<!doctype html>
<html><body style="font-family:-apple-system,system-ui,sans-serif;color:#1d1d1f">
<p>{{.Intro}}</p>
<p style="font-size:28px;letter-spacing:4px;font-weight:600">{{.Code}}</p>
<p>Or open this link: <a href="{{.Link}}">{{.Action}}</a></p>
<p style="color:#6e6e73">The code and the link work once, for 15 minutes. If you did not ask for this, ignore this mail.</p>
</body></html>`))

var noticeHTML = template.Must(template.New("notice").Parse(`<!doctype html>
<html><body style="font-family:-apple-system,system-ui,sans-serif;color:#1d1d1f">
<p>Someone tried to create a loadout account with this address, but it already has one.</p>
<p>{{if .Link}}<a href="{{.Link}}">Sign in instead</a>, with your password or Google.{{else}}Sign in instead, with your password or Google.{{end}}</p>
<p style="color:#6e6e73">If it was not you, you can ignore this mail; nothing changed.</p>
</body></html>`))

// codeMail is the mail with a code and a link that complete a challenge.
func codeMail(to string, purpose Purpose, code, link string) mail.Message {
	subject, intro, action := "Your loadout sign-in code", "Enter this code to finish signing in to loadout:", "Finish signing in"
	if purpose == PurposeVerifySignup {
		subject, intro, action = "Confirm your loadout account", "Enter this code to confirm your email address:", "Confirm my address"
	}
	text := intro + "\n\n    " + code + "\n\nOr open this link:\n" + link +
		"\n\nThe code and the link work once, for 15 minutes. If you did not ask for this, ignore this mail.\n"
	return mail.Message{To: to, Subject: subject, Text: text, HTML: render(codeHTML, map[string]string{"Intro": intro, "Code": code, "Link": link, "Action": action})}
}

// existingAccountMail tells the owner of an address that someone tried to
// sign it up again.
func existingAccountMail(to, publicURL string) mail.Message {
	link := ""
	if publicURL != "" {
		link = publicURL + "/auth/login"
	}
	text := "Someone tried to create a loadout account with this address, but it already has one.\n\n" +
		"Sign in instead, with your password or Google"
	if link != "" {
		text += ":\n" + link
	}
	text += "\n\nIf it was not you, you can ignore this mail; nothing changed.\n"
	return mail.Message{To: to, Subject: "You already have a loadout account", Text: text, HTML: render(noticeHTML, map[string]string{"Link": link})}
}

// render executes an HTML template; the data is plain strings, so it only
// fails on a broken template, which the tests would catch. The text body
// is enough in that case.
func render(t *template.Template, data any) string {
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return ""
	}
	return buf.String()
}

var inviteHTML = template.Must(template.New("invite").Parse(`<!doctype html>
<html><body style="font-family:-apple-system,system-ui,sans-serif;color:#1d1d1f">
<p>{{.Inviter}} added you to {{.Project}} on loadout, where your team shares its AI agents, skills and knowledge bases. Your roles:</p>
<ul>{{range .Roles}}<li>{{.}}</li>{{end}}</ul>
<p>{{if .Link}}<a href="{{.Link}}">Sign in or create your account</a>{{else}}Sign in or create your account{{end}} with this address ({{.Email}}), using Google or a password. Then run <code>loadout login</code> in a terminal to install your team's packages.</p>
<p style="color:#6e6e73">If you did not expect this, you can ignore this mail.</p>
</body></html>`))

// invitationMail tells a person which roles they were given, by whom, and
// where to sign in. It reads the same whether or not they have an account.
func invitationMail(to, inviter string, inv Invitation) mail.Message {
	roles := make([]string, 0, len(inv.Grants))
	for _, g := range inv.Grants {
		roles = append(roles, strings.ToUpper(string(g.Role)[:1])+string(g.Role)[1:]+" of "+g.Namespace)
	}
	var b strings.Builder
	b.WriteString(inviter + " added you to " + inv.Project + " on loadout, where your team shares its AI agents, skills and knowledge bases. Your roles:\n\n")
	for _, r := range roles {
		b.WriteString("  - " + r + "\n")
	}
	b.WriteString("\nSign in or create your account with this address (" + to + "), using Google or a password")
	if inv.Link != "" {
		b.WriteString(":\n" + inv.Link)
	} else {
		b.WriteString(".")
	}
	b.WriteString("\n\nThen run loadout login in a terminal to install your team's packages.\n\nIf you did not expect this, you can ignore this mail.\n")
	return mail.Message{
		To:      to,
		Subject: "You are invited to " + inv.Project + " on loadout",
		Text:    b.String(),
		HTML:    render(inviteHTML, map[string]any{"Inviter": inviter, "Project": inv.Project, "Roles": roles, "Link": inv.Link, "Email": to}),
	}
}
