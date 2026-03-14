package yggdrasil

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/kashari/draupnir"
	"github.com/kashari/golog"
	"github.com/kashari/yggdrasil/analyzer"
	"github.com/kashari/yggdrasil/engine"
	"github.com/kashari/yggdrasil/model"
	"gorm.io/datatypes"
	"gorm.io/gorm"
)

// Type aliases so callers import only this package.
type (
	Machine              = model.WorkflowInstance
	Definition           = model.WorkflowDefinition
	StateDefinition      = model.StateDefinition
	TransitionDefinition = model.TransitionDefinition
	ActionDefinition     = model.ActionDefinition
)

const (
	StatusActive    = model.StatusActive
	StatusCompleted = model.StatusCompleted
	StatusFailed    = model.StatusFailed
	StatusWaiting   = model.StatusWaiting
)

const (
	ActionTypeHttp       = model.ActionTypeHttp
	ActionTypeStartChild = model.ActionTypeStartChild
)

var Default *Yggdrasil

type Config struct {
	DB          *gorm.DB
	HTTPTimeout time.Duration
}

type Yggdrasil struct {
	db *gorm.DB
	mu sync.RWMutex
}

func Init(cfg Config) error {
	y, err := New(cfg)
	if err != nil {
		return err
	}
	Default = y
	return nil
}

// New constructs a new Yggdrasil. Use Init for the singleton pattern.
func New(cfg Config) (*Yggdrasil, error) {
	if cfg.DB == nil {
		return nil, ErrNilDatabase
	}

	timeout := cfg.HTTPTimeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}
	engine.HTTPTimeout = timeout

	return &Yggdrasil{db: cfg.DB}, nil
}

// AutoMigrate runs database schema migrations.
// Package-level alias delegates to Default.
func AutoMigrate() error { return Default.AutoMigrate() }

func (y *Yggdrasil) AutoMigrate() error {
	return y.db.AutoMigrate(
		&model.WorkflowDefinition{},
		&model.StateDefinition{},
		&model.TransitionDefinition{},
		&model.ActionDefinition{},
		&model.WorkflowInstance{},
		&model.TransitionHistory{},
	)
}

// DB returns the underlying gorm.DB connection.
func (y *Yggdrasil) DB() *gorm.DB { return y.db }

// Shutdown signals all running machines to stop, honouring ctx cancellation.
func (y *Yggdrasil) Shutdown(ctx context.Context) error {
	engine.Machines.Range(func(key, value any) bool {
		if m, ok := value.(*engine.Machine); ok {
			select {
			case m.Stop() <- struct{}{}:
			case <-ctx.Done():
				return false
			}
		}
		return true
	})
	return ctx.Err()
}

// Define saves one or more machine definitions in a single transaction.
func Define(defs ...Definition) error { return Default.Define(defs...) }

func (y *Yggdrasil) Define(defs ...Definition) error {
	return y.db.Transaction(func(tx *gorm.DB) error {
		for i := range defs {
			if err := tx.Save(&defs[i]).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// Blueprint returns a definition with all its states, transitions, and actions preloaded.
func (y *Yggdrasil) Blueprint(id string) (*Definition, error) {
	var def Definition
	err := y.db.Preload("States.EntryActions").
		Preload("States.ExitActions").
		Preload("Transitions.Actions").
		First(&def, "id = ?", id).Error
	if err != nil {
		return nil, err
	}
	return &def, nil
}

// Launch starts a new machine from the given definition and returns it.
// name is optional; when set, the machine is also addressable by name.
// Package-level alias delegates to Default.
func Launch(defID, name string, vars map[string]any) (*Machine, error) {
	golog.Info("Launching machine from definition {} with name '{}'", defID, name)
	return Default.Launch(defID, name, vars)
}

func (y *Yggdrasil) Launch(defID, name string, vars map[string]any) (*Machine, error) {
	var def Definition
	if err := y.db.First(&def, "id = ?", defID).Error; err != nil {
		return nil, err
	}

	vJSON, err := json.Marshal(vars)
	if err != nil {
		return nil, err
	}

	m := Machine{
		Name:          name,
		WorkflowDefID: defID,
		CurrentState:  def.InitialState,
		Status:        StatusActive,
		Variables:     datatypes.JSON(vJSON),
	}

	if err := y.db.Create(&m).Error; err != nil {
		return nil, err
	}

	engine.Spawn(y.db, m.ID)
	golog.Info("Machine launched with ID {}", m.ID)
	return &m, nil
}

// Fire sends an event to the machine identified by id (UUID string or name).
// Returns true if a matching transition was found and executed.
// Package-level alias delegates to Default.
func Fire(id, event string) (bool, error) { return Default.Fire(id, event) }

func (y *Yggdrasil) Fire(id, event string) (bool, error) {
	golog.Info("Sending event '{}' to machine '{}'", event, id)
	return y.FireWith(id, event, nil)
}

// FireWith sends an event with an optional payload map.
// Package-level alias delegates to Default.
func FireWith(id, event string, payload map[string]any) (bool, error) {
	return Default.FireWith(id, event, payload)
}

func (y *Yggdrasil) FireWith(id, event string, payload map[string]any) (bool, error) {
	uid, err := y.resolveID(id)
	if err != nil {
		return false, err
	}

	m := engine.Spawn(y.db, uid)
	if m == nil {
		return false, ErrInstanceNotFound
	}

	ack := make(chan bool)
	m.Inbox() <- engine.Event{Name: event, Payload: payload, Ack: ack}
	golog.Info("Event {} sent to machine '{}'", event, id)
	return <-ack, nil
}

// Inspect returns the current persisted state of a machine by UUID or name.
func (y *Yggdrasil) Inspect(id string) (*Machine, error) {
	var m Machine

	if uid, err := uuid.Parse(id); err == nil {
		if err := y.db.First(&m, "id = ?", uid).Error; err != nil {
			return nil, err
		}
	} else {
		if err := y.db.First(&m, "name = ?", id).Error; err != nil {
			return nil, err
		}
	}

	return &m, nil
}

// Find returns machines filtered by definition ID, status, and limit.
// Any filter left as zero value is ignored.
func (y *Yggdrasil) Find(defID, status string, limit int) ([]Machine, error) {
	query := y.db.Model(&Machine{})
	if defID != "" {
		query = query.Where("workflow_def_id = ?", defID)
	}
	if status != "" {
		query = query.Where("status = ?", status)
	}
	if limit > 0 {
		query = query.Limit(limit)
	}

	var machines []Machine
	if err := query.Order("created_at DESC").Find(&machines).Error; err != nil {
		return nil, err
	}
	return machines, nil
}

// Resume restarts a persisted machine that was stopped but is not in a terminal state.
// If the machine is already running, this is a no-op and returns the current instance.
func Resume(id string) (*Machine, error) { return Default.Resume(id) }

func (y *Yggdrasil) Resume(id string) (*Machine, error) {
	uid, err := y.resolveID(id)
	if err != nil {
		return nil, err
	}

	var inst Machine
	if err := y.db.First(&inst, "id = ?", uid).Error; err != nil {
		return nil, ErrInstanceNotFound
	}

	if inst.Status == StatusCompleted || inst.Status == StatusFailed {
		return nil, ErrMachineTerminated
	}

	// Already running — idempotent.
	if _, running := engine.Machines.Load(uid); running {
		return &inst, nil
	}

	engine.Spawn(y.db, uid)
	golog.Info("Machine {} ({}) resumed", inst.Name, uid)
	return &inst, nil
}

// AvailableEvents returns all valid events that can be sent to the machine from
// its current state, formatted as "EVENT -> TARGET_STATE".
func AvailableEvents(id string) ([]string, error) { return Default.AvailableEvents(id) }

func (y *Yggdrasil) AvailableEvents(id string) ([]string, error) {
	inst, err := y.Inspect(id)
	if err != nil {
		return nil, err
	}

	def, err := y.Blueprint(inst.WorkflowDefID)
	if err != nil {
		return nil, err
	}

	var events []string
	for _, t := range def.Transitions {
		if t.Source == inst.CurrentState || (t.IsCommon && t.Source == "*") {
			events = append(events, fmt.Sprintf("%s -> %s", t.Event, t.Target))
		}
	}
	return events, nil
}

func (y *Yggdrasil) ResolveID(id string) (uuid.UUID, error) {
	return y.resolveID(id)
}

func (y *Yggdrasil) resolveID(id string) (uuid.UUID, error) {
	if uid, err := uuid.Parse(id); err == nil {
		return uid, nil
	}

	var m Machine
	if err := y.db.Select("id").First(&m, "name = ?", id).Error; err != nil {
		return uuid.Nil, ErrInstanceNotFound
	}
	return m.ID, nil
}

func (y *Yggdrasil) Draupnir() *draupnir.Router {
	router := draupnir.New().
		WithFileLogging(fmt.Sprintf("Workflow %s.log", time.Now().Format("Monday 1 January 2006"))).
		WithRateLimiter(100, 5*time.Second).
		WithWorkerPool(20)

	router.POST("/definitions", y.handleDefine)
	router.POST("/machines/start", y.handleLaunch)
	router.GET("/machines", y.handleFind)
	router.GET("/machines/:id", y.handleInspect)
	router.POST("/machines/:id/event", y.handleFire)
	router.POST("/machines/:id/stop", y.handleStopMachine)
	router.POST("/machines/:id/resume", y.handleResume)
	router.GET("/machines/:id/events", y.handleAvailableEvents)

	ah := &analyzer.Handler{
		DB:        y.db,
		Blueprint: y.Blueprint,
		ResolveID: y.ResolveID,
		Inspect:   y.Inspect,
	}
	ah.Register(router)

	return router
}

func (y *Yggdrasil) handleStopMachine(ctx *draupnir.Context) {
	id := ctx.Param("id")

	uid, err := y.resolveID(id)
	if err != nil {
		ctx.JSON(http.StatusNotFound, map[string]string{"error": "machine not found"})
		return
	}

	m := engine.Spawn(y.db, uid)
	if m == nil {
		ctx.JSON(http.StatusNotFound, map[string]string{"error": "machine not found"})
		return
	}

	m.Stop() <- struct{}{}
	ctx.JSON(http.StatusOK, map[string]string{"message": "Stop signal sent to machine."})
}

func (y *Yggdrasil) handleDefine(ctx *draupnir.Context) {
	var defs []Definition

	if err := ctx.BindJSON(&defs); err != nil {
		ctx.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	if err := y.Define(defs...); err != nil {
		ctx.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	ctx.JSON(http.StatusCreated, map[string]string{"status": "definitions saved"})
}

func (y *Yggdrasil) handleLaunch(ctx *draupnir.Context) {
	var req struct {
		DefinitionID string         `json:"definitionId"`
		Name         string         `json:"name"`
		Variables    map[string]any `json:"variables"`
	}

	if err := ctx.BindJSON(&req); err != nil {
		ctx.JSON(http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	m, err := y.Launch(req.DefinitionID, req.Name, req.Variables)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			ctx.JSON(http.StatusNotFound, map[string]string{"error": "definition not found"})
			return
		}
		ctx.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusCreated, map[string]string{"id": m.ID.String(), "name": m.Name})
}

func (y *Yggdrasil) handleInspect(ctx *draupnir.Context) {
	m, err := y.Inspect(ctx.Param("id"))
	if err != nil {
		if err == gorm.ErrRecordNotFound || err == ErrInstanceNotFound {
			ctx.JSON(http.StatusNotFound, map[string]string{"error": "machine not found"})
			return
		}
		ctx.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, m)
}

func (y *Yggdrasil) handleFind(ctx *draupnir.Context) {
	q := ctx.Query("limit")
	limit, _ := strconv.Atoi(q)

	machines, err := y.Find(ctx.Query("definitionId"), ctx.Query("status"), limit)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, machines)
}

func (y *Yggdrasil) handleFire(ctx *draupnir.Context) {
	id := ctx.Param("id")
	q := ctx.Query("event")

	if q == "" {
		ctx.JSON(http.StatusBadRequest, map[string]string{"error": "missing ?event= query parameter"})
		return
	}

	queryParams := ctx.Request.URL.Query()

	var payload map[string]any
	for k, vals := range queryParams {
		if k == "event" {
			continue
		}
		if payload == nil {
			payload = make(map[string]any)
		}
		if len(vals) == 1 {
			payload[k] = vals[0]
		} else {
			payload[k] = vals
		}
	}

	handled, err := y.FireWith(id, q, payload)
	if err != nil {
		if err == ErrInstanceNotFound {
			ctx.JSON(http.StatusNotFound, map[string]string{"error": "machine not found"})
			return
		}
		ctx.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	if handled {
		ctx.JSON(http.StatusOK, map[string]string{"message": fmt.Sprintf("Event %s sent successfully.", q)})
	} else {
		possibleEvents, _ := y.AvailableEvents(id)
		golog.Warn("Fired event '{}' was not accepted by machine '{}'. Possible events: {}", q, id, possibleEvents)
		ctx.JSON(http.StatusConflict, map[string]string{"error": fmt.Sprintf("Event %s is not accepted by the state machine, possible events: %v", q, possibleEvents)})
	}
}

func (y *Yggdrasil) handleResume(ctx *draupnir.Context) {
	id := ctx.Param("id")

	_, err := y.Resume(id)
	if err != nil {
		switch err {
		case ErrInstanceNotFound:
			ctx.JSON(http.StatusNotFound, map[string]string{"error": "machine not found"})
		case ErrMachineTerminated:
			ctx.JSON(http.StatusConflict, map[string]string{"error": "machine is in a terminal state and cannot be resumed"})
		default:
			ctx.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
		return
	}

	ctx.JSON(http.StatusOK, map[string]string{"message": "Machine resumed."})
}

func (y *Yggdrasil) handleAvailableEvents(ctx *draupnir.Context) {
	id := ctx.Param("id")

	events, err := y.AvailableEvents(id)
	if err != nil {
		if err == ErrInstanceNotFound {
			ctx.JSON(http.StatusNotFound, map[string]string{"error": "machine not found"})
			return
		}
		ctx.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	ctx.JSON(http.StatusOK, events)
}
