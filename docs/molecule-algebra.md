# Molecule Algebra: A Composition Language for Work

> Status: Design Spec v1 - December 2024
>
> "From 'issues in git' to a work composition algebra in 10 weeks."

## Overview

This document defines the **Molecule Algebra** - a declarative language for
composing, transforming, and executing structured work. The algebra enables
mechanical composition of workflows without AI, reserving cognition for
leaf-node execution only.

**Key insight**: Structure is computation. Content is cognition. They're separate.

```
Molecules = Graph Algebra (mechanical, gt executes)
Steps = AI Cognition (agent provides)
```

## Formulas and Cooking

> *"Let's cook."* - Walter White

The molecule algebra lives above molecular chemistry. **Formulas** are the
source code; **cooking** is the process that produces executable protos.

### The Four-Tier Model

```
Formula (.formula.yaml)     ← "The secret formula" (source code)
    ↓ cook                  ← "Let's cook" (pre-expand, flatten)
Proto (frozen in beads)     ← "Pure product" (compiled, flat graph)
    ↓ pour/wisp             ← Instantiate
Mol/Wisp (running)          ← The work flowing
```

| Tier | Name | Format | Nature |
|------|------|--------|--------|
| Source | **Formula** | YAML | Composable, has macros/aspects |
| Compiled | **Proto** | Beads issue | Frozen, flat graph, fast instantiation |
| Running | **Mol/Wisp** | Beads issue | Active, flowing work |

### Why Cook?

Cooking **pre-expands** all composition at "compile time":
- Macros (Rule of Five) expand to flat steps
- Aspects apply to matching pointcuts
- Branches and gates wire up
- Result: pure step graph with no interpretation needed

Instantiation then becomes pure **copy + variable substitution**. Fast, mechanical,
deterministic.

### Formula Format

Formulas are JSON files with `.formula.json` extension. JSON for consistency
with beads (agents create/manage these; humans use visualizers):

```json
{
  "formula": "shiny",
  "description": "Engineer in a Box - the canonical right way",
  "version": 1,
  "steps": [
    {"id": "design", "description": "Think carefully about architecture"},
    {"id": "implement", "needs": ["design"]},
    {"id": "review", "needs": ["implement"]},
    {"id": "test", "needs": ["review"]},
    {"id": "submit", "needs": ["test"]}
  ]
}
```

Formulas with composition:

```json
{
  "formula": "shiny-enterprise",
  "extends": "shiny",
  "version": 1,
  "compose": [
    {"expand": {"target": "implement", "with": "rule-of-five"}},
    {"aspect": {"pointcut": "implement.*", "with": "security-audit"}},
    {"gate": {"before": "submit", "condition": "security-postscan.approved"}}
  ]
}
```

### CLI

```bash
# Cook a formula into a proto
bd cook shiny-enterprise.formula.yaml
# "Cooking shiny-enterprise..."
# "✓ Cooked proto: shiny-enterprise (30 steps)"

# Preview without saving
bd cook shiny-enterprise.formula.yaml --dry-run

# List available formulas
bd formula list

# Show formula details
bd formula show rule-of-five

# Instantiate the cooked proto
bd pour shiny-enterprise --var feature="auth"
```

### Formula Storage

```
~/.beads/formulas/          # User formulas
~/gt/.beads/formulas/       # Town formulas
.beads/formulas/            # Project formulas
```

### Breaking Bad × Mad Max

The naming spans both universes:
- **Formula** = the chemistry, the secret (Breaking Bad, mad scientist)
- **Cook** = the process, the craft (Breaking Bad iconic)
- **Proto** = the pure product, crystallized (the "blue sky")
- Both work in Mad Max (fuel formulas, cooking up schemes)

Heisenberg's formula is worthless without the cook. The cook is where purity
comes from. Same here: formulas define the algebra, but cooking produces the
pure proto.

## The Phases of Matter

Work in Gas Town exists in three phases:

| Phase | Name | Storage | Lifecycle | Use Case |
|-------|------|---------|-----------|----------|
| **Solid** | Proto | `.beads/` (template) | Frozen, reusable | Workflow patterns |
| **Liquid** | Mol | `.beads/` | Flowing, persistent | Project work, audit trail |
| **Vapor** | Wisp | `.beads-wisp/` | Ephemeral, evaporates | Execution scaffolding |

### Phase Transitions

```
                    ┌─────────────────┐
                    │     PROTO       │
                    │    (solid)      │
                    └────────┬────────┘
                             │
              ┌──────────────┼──────────────┐
              │              │              │
            pour           wisp          bond
              │              │              │
              ▼              ▼              ▼
      ┌───────────────┐  ┌───────────────┐  ┌───────────────┐
      │      MOL      │  │     WISP      │  │   COMPOUND    │
      │   (liquid)    │  │    (vapor)    │  │    PROTO      │
      └───────┬───────┘  └───────┬───────┘  └───────────────┘
              │                  │
           squash             squash/burn
              │                  │
              ▼                  ▼
      ┌───────────────┐  ┌───────────────┐
      │    DIGEST     │  │  (evaporates) │
      │  (condensed)  │  │   or digest   │
      └───────────────┘  └───────────────┘
```

### Phase Verbs

| Verb | Transition | Effect |
|------|------------|--------|
| `pour` | solid → liquid | Create persistent mol |
| `wisp` | solid → vapor | Create ephemeral wisp |
| `bond` | any + any | Combine (polymorphic) |
| `squash` | liquid/vapor → digest | Condense to record |
| `burn` | vapor → nothing | Discard without record |

## The Mol/Wisp Decision

**"Is this the work, or is this wrapping the work?"**

| Spawn as Mol when... | Spawn as Wisp when... |
|---------------------|----------------------|
| This IS the work item | This SHAPES execution |
| Multiple agents coordinate | Single agent executes |
| Stakeholders track progress | Only outcome matters |
| Cross-rig visibility needed | Local execution detail |
| CV/audit trail value | Scaffolding, process |

The `--on` flag implies wisp: `gt sling shiny gastown/Toast --on gt-abc123`

## Graph Primitives

### Steps

A step is a unit of work with:
- `id`: Unique identifier within molecule
- `description`: What to do (consumed by agent)
- `needs`: Dependencies (steps that must complete first)
- `output`: Structured result (available to conditions)

```yaml
- id: implement
  description: Write the authentication module
  needs: [design]
```

### Edges

Dependencies between steps:
- `needs`: Hard dependency (must complete first)
- `blocks`: Inverse of needs (this step blocks that one)
- `conditional`: Only if condition met

### Molecules

A molecule is a DAG of steps:

```yaml
molecule: shiny
description: Engineer in a Box - the canonical right way

steps:
  - id: design
    description: Think carefully about architecture

  - id: implement
    description: Write the code
    needs: [design]

  - id: review
    description: Code review
    needs: [implement]

  - id: test
    description: Run tests
    needs: [review]

  - id: submit
    description: Submit for merge
    needs: [test]
```

## Composition Operators

### Sequential Composition

```yaml
# A then B
sequence:
  - step-a
  - step-b
```

Or implicitly via `needs`:
```yaml
- id: b
  needs: [a]
```

### Parallel Composition

Steps without dependencies run in parallel:
```yaml
- id: unit-tests
  needs: [implement]

- id: integration-tests
  needs: [implement]

- id: review
  needs: [unit-tests, integration-tests]  # Waits for both
```

### Branching

Add parallel paths that rejoin:

```yaml
compose:
  - branch:
      from: implement
      steps: [perf-test, load-test, chaos-test]
      join: review
```

Produces:
```
implement ─┬─ perf-test ──┬─ review
           ├─ load-test ──┤
           └─ chaos-test ─┘
```

### Looping

Fixed iteration:
```yaml
compose:
  - loop:
      count: 5
      body: [refine]
```

Conditional iteration:
```yaml
compose:
  - loop:
      step: review
      until: "review.output.approved == true"
      max: 3  # Safety bound
```

### Gates

Wait for condition before proceeding:
```yaml
compose:
  - gate:
      before: submit
      condition: "security-scan.output.passed == true"
```

## Advice Operators (Lisp-style!)

Inspired by Lisp advice and AOP, these operators inject behavior
without modifying the original molecule.

### Before

Insert step before target:
```yaml
compose:
  - advice:
      target: review
      before: security-scan
```

### After

Insert step after target:
```yaml
compose:
  - advice:
      target: implement
      after: run-linter
```

### Around

Wrap target with before/after:
```yaml
compose:
  - advice:
      target: "*.implement"
      around:
        before: log-start
        after: log-end
```

### Pattern Matching

Target supports glob patterns:
```yaml
# All implement steps in any molecule
target: "*.implement"

# All steps in shiny
target: "shiny.*"

# Specific step
target: "shiny.review"

# All steps (wildcard)
target: "*"
```

## Expansion Operators (Macros!)

Expansion operators transform structure at bond time.

### Simple Expansion

Apply a template to a target step:
```yaml
compose:
  - expand:
      target: implement
      with: rule-of-five
```

The `rule-of-five` template:
```yaml
molecule: rule-of-five
type: expansion
description: Jeffrey's Rule - iterate 4-5 times for convergence

template:
  - id: "{target}.draft"
    description: "Initial attempt: {target.description}"

  - id: "{target}.refine-1"
    description: "Refine for correctness"
    needs: ["{target}.draft"]

  - id: "{target}.refine-2"
    description: "Refine for clarity"
    needs: ["{target}.refine-1"]

  - id: "{target}.refine-3"
    description: "Refine for edge cases"
    needs: ["{target}.refine-2"]

  - id: "{target}.refine-4"
    description: "Final polish"
    needs: ["{target}.refine-3"]
```

Result: `implement` becomes 5 steps with proper dependency wiring.

### Map Expansion

Apply template to all matching steps:
```yaml
compose:
  - map:
      select: "shiny.*"
      with: rule-of-five
```

All 5 shiny steps get R5 treatment → 25 total steps.

## Aspects (AOP)

Cross-cutting concerns applied to multiple join points:

```yaml
aspect: security-audit
description: Security scanning at implementation boundaries

pointcuts:
  - glob("*.implement")
  - glob("*.submit")

advice:
  around:
    before:
      - step: security-prescan
        args: { target: "{step.id}" }
    after:
      - step: security-postscan
        args: { target: "{step.id}" }
      - gate:
          condition: "security-postscan.output.approved == true"
```

Apply aspects at bond time:
```bash
bd bond shiny --with-aspect security-audit --with-aspect logging
```

## Selection Operators

For targeting steps in advice/expansion:

| Selector | Matches |
|----------|---------|
| `step("review")` | Specific step by ID |
| `glob("*.implement")` | Pattern match |
| `glob("shiny.*")` | All steps in molecule |
| `filter(status == "open")` | Predicate match |
| `children(step)` | Direct children |
| `descendants(step)` | All descendants |

## Conditions

Conditions are evaluated mechanically (no AI):

```yaml
# Step status
"step.status == 'complete'"

# Step output (structured)
"step.output.approved == true"
"step.output.errors.count == 0"

# Aggregates
"steps.complete >= 3"
"children(step).all(status == 'complete')"

# External checks
"file.exists('go.mod')"
"env.CI == 'true'"
```

Conditions are intentionally limited to keep evaluation decidable.

## Runtime Dynamic Expansion

For discovered work at runtime (Christmas Ornament pattern):

```yaml
step: survey-workers
on-complete:
  for-each: output.discovered_workers
  bond: polecat-arm
  with-vars:
    polecat: "{item.name}"
    rig: "{item.rig}"
```

The `for-each` evaluates against step output, bonding N instances dynamically.
Still declarative, still mechanical.

## Polymorphic Bond Operator

`bond` combines molecules with context-aware phase behavior:

| Operands | Result |
|----------|--------|
| proto + proto | compound proto (frozen) |
| proto + mol | spawn proto as mol, attach |
| proto + wisp | spawn proto as wisp, attach |
| mol + mol | link via edges |
| wisp + wisp | link via edges |
| mol + wisp | reference link (cross-phase) |
| expansion + workflow | expanded proto (macro) |
| aspect + molecule | advised molecule |

Phase override flags:
- `--pour`: Force spawn as mol
- `--wisp`: Force spawn as wisp

## Complete Example: Shiny-Enterprise

```yaml
molecule: shiny-enterprise
extends: shiny
description: Full enterprise engineering workflow

compose:
  # Apply Rule of Five to implement step
  - expand:
      target: implement
      with: rule-of-five

  # Security aspect on all implementation steps
  - aspect:
      pointcut: "implement.*"
      with: security-audit

  # Gate on security approval before submit
  - gate:
      before: submit
      condition: "security-postscan.approved == true"

  # Parallel performance testing branch
  - branch:
      from: implement.refine-4
      steps: [perf-test, load-test, chaos-test]
      join: review

  # Loop review until approved (max 3 attempts)
  - loop:
      step: review
      until: "review.output.approved == true"
      max: 3

  # Logging on all steps
  - advice:
      target: "*"
      before: log-start
      after: log-end
```

gt compiles this to ~30+ steps with proper dependencies.
Agent executes. AI provides cognition for each step.
Structure is pure algebra.

## The Grammar

```
MOLECULE  ::= 'molecule:' ID steps compose?

STEPS     ::= step+
STEP      ::= 'id:' ID 'description:' TEXT needs?
NEEDS     ::= 'needs:' '[' ID+ ']'

COMPOSE   ::= 'compose:' rule+
RULE      ::= advice | insert | branch | loop | gate | expand | aspect

ADVICE    ::= 'advice:' target before? after? around?
TARGET    ::= 'target:' PATTERN
BEFORE    ::= 'before:' STEP_REF
AFTER     ::= 'after:' STEP_REF
AROUND    ::= 'around:' '{' before? after? '}'

BRANCH    ::= 'branch:' from steps join
LOOP      ::= 'loop:' (count | until) body max?
GATE      ::= 'gate:' before? condition

EXPAND    ::= 'expand:' target 'with:' TEMPLATE
MAP       ::= 'map:' select 'with:' TEMPLATE

ASPECT    ::= 'aspect:' ID pointcuts advice
POINTCUTS ::= 'pointcuts:' selector+
SELECTOR  ::= glob | filter | step

CONDITION ::= field OP value | aggregate OP value | external
FIELD     ::= step '.' attr | 'output' '.' path
AGGREGATE ::= 'children' | 'descendants' | 'steps' '.' stat
EXTERNAL  ::= 'file.exists' | 'env.' key
```

## Decidability

The algebra is intentionally restricted:
- Loops have max iteration bounds
- Conditions limited to step/output inspection
- No recursion in expansion templates
- No arbitrary code execution

This keeps evaluation decidable and safe for mechanical execution.

## What This Enables

1. **Composition without AI**: gt compiles molecule algebra mechanically
2. **Marketplace of primitives**: Aspects, wrappers, expansions as tradeable units
3. **Deterministic expansion**: Same input → same graph, always
4. **AI for content only**: Agents execute steps, don't construct structure
5. **Inspection/debugging**: See full expanded graph before execution
6. **Optimization**: gt can parallelize, dedupe, optimize the graph
7. **Roles/Companies in a box**: Compose arbitrary organizational workflows

## The Vision

```
               ┌─────────────────────────────────────────┐
               │           MOL MALL (Marketplace)        │
               │  ┌─────────┐ ┌─────────┐ ┌───────────┐  │
               │  │ Shiny   │ │ Rule of │ │ Security  │  │
               │  │ Formula │ │ Five    │ │ Aspect    │  │
               │  └─────────┘ └─────────┘ └───────────┘  │
               │  ┌─────────┐ ┌─────────┐ ┌───────────┐  │
               │  │Planning │ │ Release │ │ Company   │  │
               │  │ Formula │ │ Formula │ │ Onboard   │  │
               │  └─────────┘ └─────────┘ └───────────┘  │
               └─────────────────────────────────────────┘
                              │
                              ▼ compose
               ┌─────────────────────────────────────────┐
               │       YOUR ORGANIZATION FORMULA          │
               │                                          │
               │  Planning + Shiny + R5 + Security +     │
               │  Release + Onboarding = Company in Box   │
               │                                          │
               └─────────────────────────────────────────┘
                              │
                              ▼ cook ("Let's cook")
               ┌─────────────────────────────────────────┐
               │              PURE PROTO                  │
               │  Pre-expanded, flat graph, 99.1% pure   │
               │  No macros, no aspects, just steps      │
               └─────────────────────────────────────────┘
                              │
                              ▼ pour/wisp
               ┌─────────────────────────────────────────┐
               │              GAS TOWN                    │
               │  Polecats execute. Wisps evaporate.     │
               │  Mols persist. Digests accumulate.      │
               │  Work gets done.                         │
               └─────────────────────────────────────────┘
```

From issues in git → work composition algebra → companies in a box.

---

*The formula is the secret. The cook is the craft. The work gets done.*
