# Yggdrasil

A finite state machine workflow engine for Go.

## Overview

Yggdrasil provides a database-backed workflow engine with support for hierarchical state machines, event-driven transitions, and HTTP action execution. It can be embedded as a library in existing applications or used standalone with the provided HTTP handlers.

## Installation

```
go get github.com/kashari/yggdrasil
```

Requires Go 1.21+ and a GORM-compatible database (MySQL, PostgreSQL, SQLite).

## Quick Start

```go
package main

import (
    "github.com/kashari/yggdrasil"
    "gorm.io/driver/mysql"
    "gorm.io/gorm"
)

func main() {
    db, _ := gorm.Open(mysql.Open("user:pass@tcp(localhost:3306)/db"), &gorm.Config{})

    ygg, _ := yggdrasil.New(yggdrasil.Config{DB: db})
    ygg.AutoMigrate()

    // Start a workflow
    inst, _ := ygg.StartWorkflow("order-process", map[string]any{
        "orderId": "12345",
    })

    // Send events to drive state transitions
    ygg.SendEvent(inst.ID, "PAYMENT_RECEIVED")
}
```

## Usage

### Initialization

```go
ygg, err := yggdrasil.New(yggdrasil.Config{
    DB:          db,              // required: GORM database connection
    HTTPTimeout: 10 * time.Second, // optional: timeout for HTTP actions (default 5s)
})
```

### Defining Workflows

Workflows are defined with states, transitions, and actions:

```go
def := yggdrasil.WorkflowDefinition{
    ID:           "order-process",
    InitialState: "pending",
    States: []yggdrasil.StateDefinition{
        {StateID: "pending"},
        {StateID: "paid"},
        {StateID: "shipped", IsEndState: true},
    },
    Transitions: []yggdrasil.TransitionDefinition{
        {Source: "pending", Target: "paid", Event: "PAYMENT_RECEIVED"},
        {Source: "paid", Target: "shipped", Event: "SHIPPED"},
    },
}

ygg.CreateDefinition(&def)
```

### Programmatic API

```go
// Create workflow definitions
ygg.CreateDefinition(&def)
ygg.CreateDefinitions([]yggdrasil.WorkflowDefinition{...})

// Retrieve definitions
def, err := ygg.GetDefinition("order-process")

// Start workflow instances
inst, err := ygg.StartWorkflow("order-process", variables)

// Send events
handled, err := ygg.SendEvent(instanceID, "EVENT_NAME")
handled, err := ygg.SendEventWithPayload(instanceID, "EVENT_NAME", payload)

// Query instances
inst, err := ygg.GetInstance(instanceID)
instances, err := ygg.ListInstances("order-process", yggdrasil.StatusActive, 100)
```

### HTTP Handlers

Mount handlers on any router:

```go
// Individual handlers
mux.HandleFunc("POST /workflows/definitions", ygg.HandleCreateDefinitions())
mux.HandleFunc("POST /workflows/instances", ygg.HandleStartInstance())
mux.HandleFunc("POST /workflows/events", ygg.HandleSendEvent())
mux.HandleFunc("GET /workflows/instances", ygg.HandleGetInstance())

// Or register all at once
ygg.RegisterHandlers(mux, "/api/workflows")
```

### Actions

States and transitions can trigger actions:

**HTTP Actions** — Execute HTTP requests with variable substitution:

```go
yggdrasil.ActionDefinition{
    Type:   yggdrasil.ActionTypeHttp,
    Method: "POST",
    URL:    "https://api.example.com/orders/{orderId}/confirm",
}
```

**Child Workflows** — Start nested workflow instances:

```go
yggdrasil.ActionDefinition{
    Type:      yggdrasil.ActionTypeStartChild,
    ProductId: "payment-subprocess",
    Delegate:  true, // parent waits for child completion
}
```

### Shutdown

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
ygg.Shutdown(ctx)
```

## Configuration Examples

Complete workflow definition examples are available in the [examples/](examples/) directory:

- [examples/order-workflow.json](examples/order-workflow.json) — E-commerce order processing with child workflows and HTTP notifications
- [examples/approval-workflow.json](examples/approval-workflow.json) — Document approval flow with review cycles

Load definitions via the HTTP API:

```
curl -X POST http://localhost:8080/api/workflows/definitions \
  -H "Content-Type: application/json" \
  -d @examples/order-workflow.json
```

Or load programmatically:

```go
data, _ := os.ReadFile("examples/order-workflow.json")
var defs []yggdrasil.WorkflowDefinition
json.Unmarshal(data, &defs)
ygg.CreateDefinitions(defs)
```

## License

MIT
