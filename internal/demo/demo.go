// Package demo writes the fake world of the demo registry: two projects with
// two teams each, seven people and five weeks of work, all made through the same
// services the registry runs on, so every page has something to show. It
// runs on a clock it moves back, so the history spreads over weeks.
//
// Everything in it is invented. The accounts share one password that the
// demo's sign-in page prints, which is why demo mode only starts on a
// localhost address (config.Login.Demo).
package demo

// Password is the one password of every demo account.
const Password = "demo-loadout-2026"

// Account is a demo person as the sign-in page lists them.
type Account struct {
	Email string
	// Role says what the person can do, in a few words.
	Role string
}

// people are the demo accounts, in the order the sign-in page lists them.
var people = []Account{
	{alice, "maintainer of lime, member of koala and pear"},
	{bogdan, "member of lime"},
	{olga, "owner of acme"},
	{chen, "owner of globex"},
	{dana, "maintainer of pear"},
	{emil, "member of otter"},
	{ada, "admin of the registry"},
}

// Accounts returns the demo accounts. The caller may keep the slice.
func Accounts() []Account { return append([]Account(nil), people...) }

// Names of the demo's namespaces: two projects with two teams each.
const (
	Acme   = "acme"
	Lime   = "lime"
	Koala  = "koala"
	Globex = "globex"
	Pear   = "pear"
	Otter  = "otter"
)
