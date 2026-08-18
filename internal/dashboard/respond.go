package dashboard

import (
	"net/http"

	"github.com/a-h/templ"
)

// renderPage writes component as the HTML response.
func renderPage(w http.ResponseWriter, r *http.Request, component templ.Component) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = component.Render(r.Context(), w)
}
