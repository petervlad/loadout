package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/petervlad/loadout/internal/account"
	"github.com/petervlad/loadout/internal/pack"
	"github.com/petervlad/loadout/internal/registry"
)

// Deletes is what the delete page needs from the account service; nil
// leaves out its routes and the package page's Delete section.
type Deletes interface {
	PackageFootprint(ctx context.Context, actor account.Actor, id pack.ID) (registry.PackageFootprint, error)
	DeletePackage(ctx context.Context, actor account.Actor, id pack.ID, reason string) (registry.PackageFootprint, error)
}

var _ Deletes = (*account.Service)(nil)

// deleteView is the body of delete.html.
type deleteView struct {
	ID string
	// Back is the package page.
	Back string
	// Facts is what goes.
	Facts deleteFacts
	// Readers says who may have the package installed: the registry keeps
	// no record of installs.
	Readers string
	// Seen is the fingerprint of the list shown, sent back with the form so
	// a change in between is noticed.
	Seen string
	// Confirm and Reason are what the person typed, kept after an error.
	Confirm string
	Reason  string
}

// openProposal is one open proposal in the footprint.
type openProposal struct {
	ID   string
	Path string
}

// canDelete reports whether the viewer may delete id at once: they are an
// admin, which is a role in every namespace, not always the page's (a team's
// page lists the project's packages too). The service checks again on every
// request.
func (h *handler) canDelete(t team, id pack.ID) bool {
	if h.ui.Deletes == nil {
		return false
	}
	for _, p := range t.places {
		if p.Namespace.Name == id.Team() {
			return p.Access.CanDeletePackage()
		}
	}
	return false
}

// deleteTarget is the package in the path, at the page's layer or above like
// the package page, or a 404. It reads no releases, so a package that only
// has proposals can be deleted by its address.
func (h *handler) deleteTarget(w http.ResponseWriter, r *http.Request, t team) (pack.ID, bool) {
	id, err := pack.NewID(r.PathValue("team"), r.PathValue("name"))
	if err != nil {
		h.notFound(w, r, t.user)
		return pack.ID{}, false
	}
	depth, _ := t.pr.Depth(t.ns())
	if d, ok := t.pr.Depth(id.Team()); !ok || d < depth {
		h.notFound(w, r, t.user)
		return pack.ID{}, false
	}
	return id, true
}

func packagePath(t team, id pack.ID) string {
	return "/t/" + t.ns() + "/p/" + id.Team() + "/" + id.Name()
}

// deletePage shows what deleting the package removes, and the form that
// asks to type its id.
func (h *handler) deletePage(w http.ResponseWriter, r *http.Request, t team) {
	id, ok := h.deleteTarget(w, r, t)
	if !ok {
		return
	}
	fp, err := h.ui.Deletes.PackageFootprint(r.Context(), t.actor(), id)
	if err != nil {
		h.refused(w, r, t, err, packagePath(t, id))
		return
	}
	h.renderDelete(w, r, t, http.StatusOK, fp, "", "", "", "")
}

// deletePackage deletes the package once the typed id and the list the
// person saw both match.
func (h *handler) deletePackage(w http.ResponseWriter, r *http.Request, t team) {
	id, ok := h.deleteTarget(w, r, t)
	if !ok {
		return
	}
	ctx, actor := r.Context(), t.actor()
	// Asked first, so a refused person gets 403 whatever they typed and a
	// second submit, after the package is gone, gets the "already deleted" page.
	fp, err := h.ui.Deletes.PackageFootprint(ctx, actor, id)
	if errors.Is(err, registry.ErrNotFound) {
		h.deletedGone(w, r, t, id)
		return
	}
	if err != nil {
		h.refused(w, r, t, err, packagePath(t, id))
		return
	}
	confirm, reason := strings.TrimSpace(r.PostFormValue("confirm")), strings.TrimSpace(r.PostFormValue("reason"))
	if reason == "" {
		h.renderDelete(w, r, t, http.StatusBadRequest, fp, confirm, reason, "Say why "+id.String()+" is deleted. The record of the delete keeps it.", "delete-reason")
		return
	}
	if confirm != id.String() {
		h.renderDelete(w, r, t, http.StatusBadRequest, fp, confirm, reason, "Type "+id.String()+" exactly to delete it.", "delete-confirm")
		return
	}
	if r.PostFormValue("seen") != fingerprint(fp) {
		h.renderDelete(w, r, t, http.StatusConflict, fp, confirm, reason, "Something changed since you opened this page. Check the list again.", "")
		return
	}
	removed, err := h.ui.Deletes.DeletePackage(ctx, actor, id, reason)
	switch {
	case errors.Is(err, registry.ErrNotFound):
		h.deletedGone(w, r, t, id) // someone else was faster
		return
	case errors.Is(err, account.ErrBackupFailed):
		h.log.ErrorContext(ctx, "web delete stopped: no backup", "package", id.String(), "err", err)
		h.explain(w, r, t, http.StatusInternalServerError, "Nothing was deleted",
			"loadout could not save a copy of the database first, so it deleted nothing. Ask whoever runs the registry to check its backup folder, then try again.", packagePath(t, id))
		return
	case err != nil:
		h.refused(w, r, t, err, packagePath(t, id))
		return
	}
	back(w, r, "/t/"+t.ns(), "deleted", url.Values{
		"package":   {id.String()},
		"releases":  {strconv.Itoa(len(removed.Releases))},
		"proposals": {strconv.Itoa(removed.Proposals)},
		"reviews":   {strconv.Itoa(removed.Reviews)},
		"prompts":   {strconv.Itoa(removed.Prompts)},
	})
}

func (h *handler) deletedGone(w http.ResponseWriter, r *http.Request, t team, id pack.ID) {
	back(w, r, "/t/"+t.ns(), "deleted-gone", url.Values{"package": {id.String()}})
}

func (h *handler) renderDelete(w http.ResponseWriter, r *http.Request, t team, status int, fp registry.PackageFootprint, confirm, reason, errText, field string) {
	v := deleteView{
		ID:      fp.ID.String(),
		Back:    packagePath(t, fp.ID),
		Facts:   h.factsOf(t, fp),
		Readers: readersOf(t, fp.ID),
		Seen:    fingerprint(fp),
		Confirm: confirm,
		Reason:  reason,
	}
	h.renderTeam(w, r, status, "delete.html", "packages", t, uiView{
		Title: "Delete " + v.ID,
		Error: errText,
		Field: field,
		Page:  v,
	})
}

// readersOf says who may have id installed. The registry records no
// installs, so it names those who could: everyone who reads the package's
// namespace, which includes the teams of a project.
func readersOf(t team, id pack.ID) string {
	var teams []string
	for _, p := range t.places {
		if p.Namespace.Parent == id.Team() {
			teams = append(teams, p.Namespace.Name)
		}
	}
	s := "loadout does not record who installed it. Anyone who reads " + id.Team()
	if len(teams) > 0 {
		s += ", which includes its teams " + strings.Join(teams, ", ") + ","
	}
	return s + " may have it."
}

// fingerprint is a short digest of what the delete page listed. The form
// carries it back, so a release or proposal added in between is not deleted
// unseen.
func fingerprint(fp registry.PackageFootprint) string {
	h := sha256.New()
	add := func(s string) { h.Write([]byte(s + "\n")) }
	for _, v := range fp.Releases {
		add("release " + v.String())
	}
	for _, id := range fp.Open {
		add("open " + id)
	}
	for _, c := range fp.Courses {
		add("course " + c)
	}
	for _, id := range fp.Deletions {
		add("request " + id)
	}
	add("counts " + strconv.Itoa(fp.Proposals) + " " + strconv.Itoa(fp.Reviews) + " " + strconv.Itoa(fp.Prompts))
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// packagesNotice is the line shown on the packages page after a delete. The
// query is display only, like the email on the settings page: it changes no
// state, and the counts are clamped to numbers.
func packagesNotice(q url.Values) string {
	id, err := pack.ParseID(q.Get("package"))
	if err != nil {
		return ""
	}
	switch q.Get("done") {
	case "deleted":
		count := func(key, noun string) string {
			n, err := strconv.Atoi(q.Get(key))
			if err != nil || n < 0 {
				n = 0
			}
			return plural(n, noun)
		}
		return id.String() + " was deleted: " + count("releases", "release") + ", " + count("proposals", "proposal") + ", " +
			count("reviews", "review") + ", " + count("prompts", "prompt") + ". A copy of the database from before is in the backup folder."
	case "deleted-gone":
		return id.String() + " was already deleted."
	}
	return ""
}
