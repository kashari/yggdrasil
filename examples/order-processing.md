# Example: Order Processing

An end-to-end walkthrough of an e-commerce order flow — from defining the
machine to watching it reach a terminal state.

---

## States

```
PENDING ──► PAYMENT_PROCESSING ──► CONFIRMED ──► SHIPPED ──► DELIVERED ✓
    │                │
    └───── CANCELLED ✓ ◄────────────────────────────────────────────────┘
                             (can cancel from any state via common transition)
```

| State                | Terminal? | Description                          |
|----------------------|-----------|--------------------------------------|
| `PENDING`            | no        | Order created, awaiting payment      |
| `PAYMENT_PROCESSING` | no        | Payment gateway call in progress     |
| `CONFIRMED`          | no        | Payment succeeded, preparing shipment|
| `SHIPPED`            | no        | Package handed to courier            |
| `DELIVERED`          | **yes**   | Customer received the package        |
| `CANCELLED`          | **yes**   | Order cancelled at any point         |

---

## 1 — Register the definition

The definition describes the machine's shape. Save it once; launch it many times.

### As JSON (e.g. loaded from a file or config service)

```json
[
  {
    "ID": "order-flow",
    "InitialState": "PENDING",
    "States": [
      { "StateID": "PENDING",            "IsEndState": false },
      { "StateID": "PAYMENT_PROCESSING", "IsEndState": false },
      { "StateID": "CONFIRMED",          "IsEndState": false },
      { "StateID": "SHIPPED",            "IsEndState": false },
      { "StateID": "DELIVERED",          "IsEndState": true  },
      { "StateID": "CANCELLED",          "IsEndState": true  }
    ],
    "Transitions": [
      { "Source": "PENDING",            "Target": "PAYMENT_PROCESSING", "Event": "SUBMIT_PAYMENT" },
      { "Source": "PAYMENT_PROCESSING", "Target": "CONFIRMED",          "Event": "PAYMENT_SUCCESS" },
      { "Source": "PAYMENT_PROCESSING", "Target": "CANCELLED",          "Event": "PAYMENT_FAILED"  },
      { "Source": "CONFIRMED",          "Target": "SHIPPED",            "Event": "SHIP"            },
      { "Source": "SHIPPED",            "Target": "DELIVERED",          "Event": "DELIVER"         },
      { "Source": "*",                  "Target": "CANCELLED",          "Event": "CANCEL", "IsCommon": true }
    ]
  }
]
```

### Via HTTP

```bash
curl -X POST http://localhost:8080/definitions \
  -H "Content-Type: application/json" \
  -d @order-flow.json
# → 201 Created
```

### Via Go

```go
err := yggdrasil.Define(yggdrasil.Definition{
    ID:           "order-flow",
    InitialState: "PENDING",
    States: []yggdrasil.StateDefinition{
        {StateID: "PENDING"},
        {StateID: "PAYMENT_PROCESSING"},
        {StateID: "CONFIRMED"},
        {StateID: "SHIPPED"},
        {StateID: "DELIVERED", IsEndState: true},
        {StateID: "CANCELLED", IsEndState: true},
    },
    Transitions: []yggdrasil.TransitionDefinition{
        {Source: "PENDING",            Target: "PAYMENT_PROCESSING", Event: "SUBMIT_PAYMENT"},
        {Source: "PAYMENT_PROCESSING", Target: "CONFIRMED",          Event: "PAYMENT_SUCCESS"},
        {Source: "PAYMENT_PROCESSING", Target: "CANCELLED",          Event: "PAYMENT_FAILED"},
        {Source: "CONFIRMED",          Target: "SHIPPED",            Event: "SHIP"},
        {Source: "SHIPPED",            Target: "DELIVERED",          Event: "DELIVER"},
        {Source: "*",                  Target: "CANCELLED",          Event: "CANCEL", IsCommon: true},
    },
})
```

---

## 2 — Launch a machine

Each real-world order gets its own machine instance. Give it a human-readable
name so you can address it directly without keeping the UUID around.

### Via HTTP

```bash
curl -X POST http://localhost:8080/machines \
  -H "Content-Type: application/json" \
  -d '{"definitionId":"order-flow","name":"order-1001","variables":{"orderId":"1001","customer":"alice"}}'
# → {"id":"<uuid>","name":"order-1001"}
```

### Via Go

```go
m, err := yggdrasil.Launch("order-flow", "order-1001", map[string]any{
    "orderId":  "1001",
    "customer": "alice",
})
// m.CurrentState == "PENDING"
// m.Status       == "ACTIVE"
```

---

## 3 — Drive it through states

Fire events to move the machine from state to state.
A `200 OK` means the transition fired; `409 Conflict` means no matching
transition exists in the current state.

### Via HTTP — step by step

```bash
# Customer submits payment
curl -X POST "http://localhost:8080/machines/order-1001/event?event=SUBMIT_PAYMENT"
# CurrentState → PAYMENT_PROCESSING

# Payment gateway confirms success
curl -X POST "http://localhost:8080/machines/order-1001/event?event=PAYMENT_SUCCESS"
# CurrentState → CONFIRMED

# Warehouse ships the package
curl -X POST "http://localhost:8080/machines/order-1001/event?event=SHIP&trackingId=DHL-999"
# CurrentState → SHIPPED   (trackingId arrives in the event payload)

# Courier marks delivered
curl -X POST "http://localhost:8080/machines/order-1001/event?event=DELIVER"
# CurrentState → DELIVERED  (terminal — machine stops)
```

### Via Go — step by step

```go
yggdrasil.Fire("order-1001", "SUBMIT_PAYMENT")
yggdrasil.Fire("order-1001", "PAYMENT_SUCCESS")
yggdrasil.FireWith("order-1001", "SHIP", map[string]any{"trackingId": "DHL-999"})
yggdrasil.Fire("order-1001", "DELIVER")
```

---

## 4 — Inspect state at any point

```bash
curl http://localhost:8080/machines/order-1001
```

```json
{
  "ID": "<uuid>",
  "Name": "order-1001",
  "CurrentState": "DELIVERED",
  "Status": "COMPLETED",
  "Variables": {"orderId":"1001","customer":"alice"},
  ...
}
```

```go
m, _ := yggdrasil.Default.Inspect("order-1001")
fmt.Println(m.CurrentState) // DELIVERED
fmt.Println(m.Status)       // COMPLETED
```

---

## 5 — Cancel from any state (common transition)

Because `CANCEL` is defined with `Source: "*"` and `IsCommon: true`, it can
fire regardless of the current state.

```bash
curl -X POST "http://localhost:8080/machines/order-1001/event?event=CANCEL"
```

```go
yggdrasil.Fire("order-1001", "CANCEL")
```

---

## 6 — Query all active orders

```bash
curl "http://localhost:8080/machines?definitionId=order-flow&status=ACTIVE"
```

```go
active, _ := yggdrasil.Default.Find("order-flow", yggdrasil.StatusActive, 0)
```

---

## Full Go bootstrap example

```go
package main

import (
    "log"
    "net/http"

    "github.com/kashari/yggdrasil"
    "gorm.io/driver/sqlite"
    "gorm.io/gorm"
)

func main() {
    db, _ := gorm.Open(sqlite.Open("orders.db"), &gorm.Config{})

    if err := yggdrasil.Init(yggdrasil.Config{DB: db}); err != nil {
        log.Fatal(err)
    }
    yggdrasil.AutoMigrate()

    // Register the definition once at startup.
    yggdrasil.Define(/* ... definition from above ... */)

    // Mount all routes.
    mux := http.NewServeMux()
    yggdrasil.Default.Mount(mux)

    log.Fatal(http.ListenAndServe(":8080", mux))
}
```
