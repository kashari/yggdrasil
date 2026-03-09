package yggdrasil

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"
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

// Default is the package-level singleton. Initialise it with Init.
var Default *Yggdrasil

// Config holds construction options for Yggdrasil.
type Config struct {
	DB          *gorm.DB
	HTTPTimeout time.Duration
}

// Yggdrasil is the central engine handle.
type Yggdrasil struct {
	db *gorm.DB
	mu sync.RWMutex
}

// Init creates the package-level Default instance from cfg.
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

// ── Definitions ───────────────────────────────────────────────────────────────

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

// ── Machines ──────────────────────────────────────────────────────────────────

// Launch starts a new machine from the given definition and returns it.
// name is optional; when set, the machine is also addressable by name.
// Package-level alias delegates to Default.
func Launch(defID, name string, vars map[string]any) (*Machine, error) {
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
	return &m, nil
}

// Fire sends an event to the machine identified by id (UUID string or name).
// Returns true if a matching transition was found and executed.
// Package-level alias delegates to Default.
func Fire(id, event string) (bool, error) { return Default.Fire(id, event) }

func (y *Yggdrasil) Fire(id, event string) (bool, error) {
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

// resolveID parses id as a UUID, falling back to a name lookup in the DB.
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

// ── HTTP routing ──────────────────────────────────────────────────────────────

// Mount registers all Yggdrasil routes on mux using Go 1.22 method+path syntax.
//
//	POST /definitions
//	POST /machines                        body: { definitionId, name?, variables? }
//	GET  /machines                        ?definitionId= &status= &limit=
//	GET  /machines/{id}                   id = UUID or name
//	POST /machines/{id}/event?event=NAME  extra query params become payload
func (y *Yggdrasil) Mount(mux *http.ServeMux) {
	mux.HandleFunc("POST /definitions", y.handleDefine)
	mux.HandleFunc("POST /machines", y.handleLaunch)
	mux.HandleFunc("GET /machines", y.handleFind)
	mux.HandleFunc("GET /machines/{id}", y.handleInspect)
	mux.HandleFunc("POST /machines/{id}/event", y.handleFire)
}

func (y *Yggdrasil) handleDefine(w http.ResponseWriter, r *http.Request) {
	var defs []Definition
	if err := json.NewDecoder(r.Body).Decode(&defs); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := y.Define(defs...); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusCreated)
}

func (y *Yggdrasil) handleLaunch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		DefinitionID string         `json:"definitionId"`
		Name         string         `json:"name"`
		Variables    map[string]any `json:"variables"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	m, err := y.Launch(req.DefinitionID, req.Name, req.Variables)
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			http.Error(w, "definition not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"id": m.ID.String(), "name": m.Name})
}

func (y *Yggdrasil) handleInspect(w http.ResponseWriter, r *http.Request) {
	m, err := y.Inspect(r.PathValue("id"))
	if err != nil {
		if err == gorm.ErrRecordNotFound || err == ErrInstanceNotFound {
			http.Error(w, "machine not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(m)
}

func (y *Yggdrasil) handleFind(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))

	machines, err := y.Find(q.Get("definitionId"), q.Get("status"), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(machines)
}

// handleFire reads the machine id from the path, the event name from ?event=,
// and any additional query params as the event payload.
//
//	POST /machines/order-42/event?event=PAYMENT_RECEIVED&amount=99.99
func (y *Yggdrasil) handleFire(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	q := r.URL.Query()

	event := q.Get("event")
	if event == "" {
		http.Error(w, "missing ?event= query parameter", http.StatusBadRequest)
		return
	}

	// Every query param except "event" becomes payload data.
	var payload map[string]any
	for k, vals := range q {
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

	handled, err := y.FireWith(id, event, payload)
	if err != nil {
		if err == ErrInstanceNotFound {
			http.Error(w, "machine not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if handled {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusConflict)
	}
}
