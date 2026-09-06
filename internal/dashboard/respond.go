package dashboard

import (
	"net/http"

	"github.com/a-h/templ"

	"github.com/firemanx07/slay-push/internal/dashboard/templates"
)

// renderPage writes component as the HTML response.
func renderPage(w http.ResponseWriter, r *http.Request, component templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = component.Render(r.Context(), w)
}

// errMsg and okMsg build the form-result Message a page template renders as
// a color-coded callout.
func errMsg(text string) templates.Message { return templates.Message{Text: text, Kind: "error"} }
func okMsg(text string) templates.Message  { return templates.Message{Text: text, Kind: "success"} }
