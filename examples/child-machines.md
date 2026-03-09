# Example: Child Machines (Parent–Child Workflows)

Yggdrasil supports hierarchical machines. A parent machine can start a child
machine as part of a transition action and optionally **wait** for it to finish
before continuing.

---

## When to use this

- An order machine delegates payment processing to a dedicated payment machine.
- A hiring pipeline delegates background checks to a sub-machine.
- Any step that is itself a multi-state process with its own lifecycle.

---

## How it works

1. A `START_CHILD` action on a transition or state creates a new machine
   instance based on a child definition.
2. If `Delegate: true`, the parent moves to `WAITING_FOR_CHILD` status and
   ignores all non-system events until the child completes.
3. When the child reaches a terminal state it fires `CHILD_COMPLETED` to the
   parent automatically.
4. The parent resumes from wherever the `CHILD_COMPLETED` transition points.

---

## 1 — Define the child machine

The payment sub-flow: authorise → capture or decline.

```json
{
  "ID": "payment-flow",
  "InitialState": "AUTHORISING",
  "States": [
    { "StateID": "AUTHORISING", "IsEndState": false },
    { "StateID": "CAPTURED",    "IsEndState": true  },
    { "StateID": "DECLINED",    "IsEndState": true  }
  ],
  "Transitions": [
    { "Source": "AUTHORISING", "Target": "CAPTURED", "Event": "CAPTURE" },
    { "Source": "AUTHORISING", "Target": "DECLINED", "Event": "DECLINE" }
  ]
}
```

## 2 — Define the parent machine

The order flow delegates payment to `payment-flow` and waits.

```json
{
  "ID": "order-flow-delegating",
  "InitialState": "PENDING",
  "States": [
    { "StateID": "PENDING",    "IsEndState": false },
    { "StateID": "PROCESSING", "IsEndState": false },
    { "StateID": "COMPLETE",   "IsEndState": true  },
    { "StateID": "FAILED",     "IsEndState": true  }
  ],
  "Transitions": [
    {
      "Source": "PENDING",
      "Target": "PROCESSING",
      "Event": "START_PAYMENT",
      "Actions": [
        {
          "Type":      "START_CHILD",
          "ProductId": "payment-flow",
          "Delegate":  true
        }
      ]
    },
    { "Source": "PROCESSING", "Target": "COMPLETE", "Event": "CHILD_COMPLETED" },
    { "Source": "*",          "Target": "FAILED",   "Event": "CANCEL", "IsCommon": true }
  ]
}
```

> **Note:** `CHILD_COMPLETED` is the reserved event Yggdrasil sends to the
> parent automatically when the child reaches a terminal state.

### Register both definitions

```go
yggdrasil.Define(paymentDef, orderDef)
```

---

## 3 — Launch the parent machine

```bash
curl -X POST http://localhost:8080/machines \
  -H "Content-Type: application/json" \
  -d '{"definitionId":"order-flow-delegating","name":"order-2001","variables":{"orderId":"2001"}}'
# CurrentState → PENDING
```

```go
m, _ := yggdrasil.Launch("order-flow-delegating", "order-2001", map[string]any{
    "orderId": "2001",
})
```

---

## 4 — Trigger the delegating transition

Firing `START_PAYMENT` on the parent:
- Runs the `START_CHILD` action → creates and spawns a `payment-flow` instance.
- Sets parent status to `WAITING_FOR_CHILD`.
- Parent now ignores all events except `CHILD_*` and `SYS_*`.

```bash
curl -X POST "http://localhost:8080/machines/order-2001/event?event=START_PAYMENT"
```

```go
yggdrasil.Fire("order-2001", "START_PAYMENT")
```

Inspect the parent — note the status:

```bash
curl http://localhost:8080/machines/order-2001
# → { "CurrentState": "PROCESSING", "Status": "WAITING_FOR_CHILD", ... }
```

---

## 5 — Drive the child to completion

The child machine's UUID was stored as `ParentInstanceID` on its instance. You
can find it by listing machines filtered to the child definition:

```bash
curl "http://localhost:8080/machines?definitionId=payment-flow&status=ACTIVE"
# → [{ "ID": "<child-uuid>", ... }]

curl -X POST "http://localhost:8080/machines/<child-uuid>/event?event=CAPTURE"
# Child reaches CAPTURED (terminal) → fires CHILD_COMPLETED to parent
```

```go
children, _ := yggdrasil.Default.Find("payment-flow", yggdrasil.StatusActive, 1)
yggdrasil.Fire(children[0].ID.String(), "CAPTURE")
```

---

## 6 — Parent resumes automatically

Once `CHILD_COMPLETED` arrives:
- Parent status flips back to `ACTIVE`.
- The `CHILD_COMPLETED` transition fires: `PROCESSING → COMPLETE`.
- Parent reaches its terminal state and stops.

```bash
curl http://localhost:8080/machines/order-2001
# → { "CurrentState": "COMPLETE", "Status": "COMPLETED" }
```

---

## Sequence diagram

```
Parent (order-2001)          Child (payment-flow instance)
        │                               │
  PENDING                               │
        │──── START_PAYMENT ────►       │
        │   [START_CHILD action]        │
        │──────────────────────────► AUTHORISING
        │                               │
  WAITING_FOR_CHILD                     │
        │                         ── CAPTURE ──►
        │                               │
        │                           CAPTURED (terminal)
        │                               │
        │◄──── CHILD_COMPLETED ─────────┘
        │
  COMPLETE (terminal)
```
