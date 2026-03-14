package analyzer

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/kashari/draupnir"
	"github.com/kashari/yggdrasil/model"
	"gorm.io/gorm"
)

// Handler groups the four analyzer HTTP handlers together.
// It holds a reference to the GORM db and function pointers to avoid
// importing the parent yggdrasil package (which would create a circular dep).
type Handler struct {
	DB        *gorm.DB
	Blueprint func(defID string) (*model.WorkflowDefinition, error)
	ResolveID func(id string) (uuid.UUID, error)
	Inspect   func(id string) (*model.WorkflowInstance, error)
}

// Register mounts all analyzer routes on the given draupnir router.
//
//	GET /definitions/:id/analyze  — plain-text journey
//	GET /definitions/:id/diagram  — SVG diagram (self-contained HTML)
//	GET /machines/:id/report      — instance HTML report
func (h *Handler) Register(r *draupnir.Router) {
	r.GET("/definitions/:id/analyze", h.handleAnalyze)
	r.GET("/definitions/:id/diagram", h.handleDiagram)
	r.GET("/machines/:id/report", h.handleReport)
}

// handleAnalyze returns a plain-text journey of the workflow definition.
func (h *Handler) handleAnalyze(ctx *draupnir.Context) {
	def, err := h.Blueprint(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusNotFound, map[string]string{"error": "definition not found: " + err.Error()})
		return
	}

	ctx.String(http.StatusOK, "%s", DefinitionJourney(def))
}

// handleDiagram returns a self-contained HTML page with an SVG diagram.
func (h *Handler) handleDiagram(ctx *draupnir.Context) {
	def, err := h.Blueprint(ctx.Param("id"))
	if err != nil {
		ctx.JSON(http.StatusNotFound, map[string]string{"error": "definition not found: " + err.Error()})
		return
	}

	ctx.HTML(http.StatusOK, DiagramHTML(def))
}

// handleReport returns a self-contained HTML report for a machine instance.
func (h *Handler) handleReport(ctx *draupnir.Context) {
	id := ctx.Param("id")

	inst, err := h.Inspect(id)
	if err != nil {
		ctx.JSON(http.StatusNotFound, map[string]string{"error": "machine not found: " + err.Error()})
		return
	}

	def, err := h.Blueprint(inst.WorkflowDefID)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, map[string]string{"error": "definition not found: " + err.Error()})
		return
	}

	uid, err := h.ResolveID(id)
	if err != nil {
		ctx.JSON(http.StatusNotFound, map[string]string{"error": "cannot resolve machine id"})
		return
	}

	htmlOut, err := InstanceReport(h.DB, uid, inst, def)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	ctx.HTML(http.StatusOK, htmlOut)
}
