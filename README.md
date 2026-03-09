# Yggdrasil

A finite state machine workflow engine for Go.

## Overview

Yggdrasil provides a database-backed workflow engine with support for hierarchical state machines, event-driven transitions, and HTTP action execution. It can be embedded as a library in existing applications or used standalone with the built-in HTTP router integration.

## Installation

```
go get github.com/kashari/yggdrasil
```

Requires Go 1.22+ and a GORM-compatible database (MySQL, PostgreSQL, SQLite).

## Quick Start

```go
package main

import (
    "github.com/kashari/yggdrasil"
    "gorm.io/driver/sqlite"
    "gorm.io/gorm"
)

func main() {
    db, _ := gorm.Open(sqlite.Open("ygg.db"), &gorm.Config{})

    ygg, _ := yggdrasil.New(yggdrasil.Config{DB: db})
    ygg.AutoMigrate()

    // Launch a machine instance
    m, _ := ygg.Launch("order-process", "order-42", map[string]any{
        "orderId": "42",
    })

    // Fire events to drive state transitions
    ygg.Fire(m.ID.String(), "PAYMENT_RECEIVED")

    // Machines are also addressable by name
    ygg.Fire("order-42", "SHIPPED")
}
```

## Usage

### Initialization

```go
// Instance pattern
ygg, err := yggdrasil.New(yggdrasil.Config{
    DB:          db,               // required: GORM database connection
    HTTPTimeout: 10 * time.Second, // optional: timeout for HTTP actions (default 5s)
})

// Singleton pattern — sets yggdrasil.Default, enables package-level helpers
err := yggdrasil.Init(yggdrasil.Config{DB: db})
yggdrasil.AutoMigrate()
```

### Defining Machines

Definitions describe states, transitions, and actions:

```go
def := yggdrasil.Definition{
    ID:           "order-process",
    InitialState: "pending",
    States: []yggdrasil.StateDefinition{
        {StateID: "pending"},
        {StateID: "paid"},
        {StateID: "shipped", IsEndState: true},
    },
    Transitions: []yggdrasil.TransitionDefinition{
        {Source: "pending", Target: "paid",    Event: "PAYMENT_RECEIVED"},
        {Source: "paid",    Target: "shipped", Event: "SHIPPED"},
    },
}

ygg.Define(def)
// or multiple at once:
ygg.Define(def1, def2, def3)
```

### Programmatic API

```go
// Save definitions
ygg.Define(defs...)

// Inspect a definition with all states/transitions preloaded
def, err := ygg.Blueprint("order-process")

// Launch a machine (name is optional but enables name-based addressing)
m, err := ygg.Launch("order-process", "order-42", variables)

// Fire events — id can be a UUID string or the machine name
handled, err := ygg.Fire("order-42", "PAYMENT_RECEIVED")
handled, err := ygg.FireWith("order-42", "PAYMENT_RECEIVED", payload)

// Inspect / list machines
m, err   := ygg.Inspect("order-42")          // by name or UUID
ms, err  := ygg.Find("order-process", yggdrasil.StatusActive, 100)
```

Package-level aliases (`Define`, `Launch`, `Fire`, `FireWith`, `AutoMigrate`) delegate to `yggdrasil.Default` and are available after calling `Init`.

### HTTP Routing

Mount all routes on any `*http.ServeMux` (uses Go 1.22 method+path patterns):

```go
mux := http.NewServeMux()
ygg.Mount(mux)
http.ListenAndServe(":8080", mux)
```

Registered routes:

| Method | Path                        | Description                                    |
|--------|-----------------------------|------------------------------------------------|
| POST   | `/definitions`              | Upsert one or more definitions (JSON array)    |
| POST   | `/machines`                 | Launch a new machine instance                  |
| GET    | `/machines`                 | List machines (`?definitionId=&status=&limit=`) |
| GET    | `/machines/{id}`            | Inspect a machine by UUID or name              |
| POST   | `/machines/{id}/event`      | Fire an event (`?event=NAME&key=val…`)         |

Example requests:

```sh
# Define a workflow
curl -X POST http://localhost:8080/definitions \
  -H "Content-Type: application/json" \
  -d '[{"id":"order-process","initialState":"pending",...}]'

# Launch a machine
curl -X POST http://localhost:8080/machines \
  -H "Content-Type: application/json" \
  -d '{"definitionId":"order-process","name":"order-42","variables":{"orderId":"42"}}'

# Fire an event (extra query params become the payload)
curl -X POST "http://localhost:8080/machines/order-42/event?event=PAYMENT_RECEIVED&amount=99.99"

# Inspect
curl http://localhost:8080/machines/order-42
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

**Child Machines** — Start nested machine instances:

```go
yggdrasil.ActionDefinition{
    Type:      yggdrasil.ActionTypeStartChild,
    ProductId: "payment-subprocess",
    Delegate:  true, // parent waits for child to complete
}
```

### Shutdown

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
ygg.Shutdown(ctx)
```

## License

MIT
