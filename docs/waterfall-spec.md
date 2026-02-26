# Gastown Telemetry Waterfall — Specification

## Vue d'ensemble

Le waterfall Gastown est une vue style Chrome DevTools > Network qui montre,
sur une timeline horizontale, **toutes les actions effectuées dans une instance
Gastown** — depuis le niveau instance jusqu'au moindre appel bd ou mail d'un
agent individuel.

Hiérarchie naturelle :

```
Instance Gastown (hostname:gt)
  └─ Rig: wyvern
       └─ Agent: polecat / wyvern-Toast  [run.id = UUID]
            ├─ prompt.send
            ├─ agent.event [LLM text / tool_use / tool_result / thinking]
            │    ├─ bd.call
            │    └─ mail
            └─ session.stop
  └─ Rig: mol
       └─ Agent: witness / witness
  └─ Town-level
       ├─ Agent: mayor / mayor
       └─ Agent: deacon / deacon
```

---

## 1. Modèle de données

### 1.1 Instance Gastown (niveau racine de la vue globale)

L'instance est identifiée par `instance = hostname:basename(town_root)`,
par exemple `"laptop:gt"`. Elle est dérivée au moment du spawn de chaque agent
et embarquée dans `agent.instantiate`.

| Champ | Source | Description |
|-------|--------|-------------|
| `instance` | `agent.instantiate` | `hostname:basename(town_root)` |
| `town_root` | `agent.instantiate` | chemin absolu du town root (ex. `/Users/pa/gt`) |

### 1.2 Run GASTOWN (racine du waterfall par agent)

Chaque spawn d'agent génère un UUID unique — le **run.id** — clé primaire du
waterfall d'un run individuel.

| Champ | Source | Description |
|-------|--------|-------------|
| `run.id` | généré au spawn | UUID v4, GT_RUN |
| `instance` | `agent.instantiate` | identifiant instance Gastown |
| `town_root` | `agent.instantiate` | chemin town root |
| `agent_type` | `agent.instantiate` | `"claudecode"`, `"opencode"`, … |
| `role` | `agent.instantiate` | rôle Gastown : mayor / polecat / witness / refinery / crew / deacon / dog / boot |
| `agent_name` | `agent.instantiate` | nom spécifique (ex. `"wyvern-Toast"`) ; = role pour les singletons |
| `session_id` | `agent.instantiate` | nom de la pane tmux (TIMOX) |
| `rig` | `agent.instantiate` | nom du rig ; vide pour les agents town-level |
| `started_at` | timestamp OTel | RFC3339 |

### 1.3 Événements

Tous les événements sont des OTel log records exportés vers VictoriaLogs.
Chaque record porte `run.id` en attribut.

---

#### `agent.instantiate` — racine du run

Émis une fois par spawn d'agent. Première ligne du waterfall.

| Attribut | Type | Description |
|----------|------|-------------|
| `run.id` | string | UUID run |
| `instance` | string | `hostname:basename(town_root)` |
| `town_root` | string | chemin absolu du town root |
| `agent_type` | string | `"claudecode"` / `"opencode"` / … |
| `role` | string | `"polecat"` / `"witness"` / `"mayor"` / `"refinery"` / `"crew"` / `"deacon"` / `"dog"` / `"boot"` |
| `agent_name` | string | nom de l'agent Gastown |
| `session_id` | string | nom tmux de la pane (TIMOX) |
| `rig` | string | nom du rig (vide = town-level) |

---

#### `session.start` / `session.stop` — cycle de vie tmux

| Attribut | Type | Description |
|----------|------|-------------|
| `run.id` | string | UUID run |
| `session_id` | string | nom tmux |
| `role` | string | rôle Gastown |
| `status` | string | `"ok"` / `"error"` |

---

#### `prime` — injection de contexte

`gt prime` fournit à l'agent son contexte de démarrage (formule TOML rendue).

| Attribut | Type | Description |
|----------|------|-------------|
| `run.id` | string | UUID run |
| `role` | string | rôle Gastown |
| `hook_mode` | bool | true si invoqué depuis un hook |
| `formula` | string | formule rendue complète (via `prime.context`) |
| `status` | string | `"ok"` / `"error"` |

---

#### `prompt.send` — prompt daemon → agent

Chaque `gt sendkeys` vers la pane tmux de l'agent.

| Attribut | Type | Description |
|----------|------|-------------|
| `run.id` | string | UUID run |
| `session` | string | nom tmux |
| `keys_len` | int | longueur du prompt en octets |
| `debounce_ms` | int | délai debounce appliqué |
| `status` | string | `"ok"` / `"error"` |

> **À ajouter (priorité 1)** : inclure le texte complet du prompt dans
> `keys` — voir §3.

---

#### `agent.event` — échanges LLM (AGT events)

Un record par bloc de contenu dans la conversation Claude (JSONL).
**Contenu intégral, aucune troncature.**

| Attribut | Type | Description |
|----------|------|-------------|
| `run.id` | string | UUID run |
| `session` | string | nom tmux |
| `native_session_id` | string | UUID JSONL Claude Code |
| `agent_type` | string | nom de l'adaptateur |
| `event_type` | string | `"text"` / `"tool_use"` / `"tool_result"` / `"thinking"` |
| `role` | string | `"assistant"` / `"user"` |
| `content` | string | contenu intégral — texte LLM, JSON tool, résultat outil |

Pour `tool_use` : `content = "<tool_name>: <json_input_complet>"`
Pour `tool_result` : `content = <sortie complète de l'outil>`

---

#### `bd.call` — appels bd CLI (BD events)

Chaque invocation du CLI `bd` (Beads/Biddy), que ce soit par le daemon Go
ou par l'agent en shell.

| Attribut | Type | Description |
|----------|------|-------------|
| `run.id` | string | UUID run |
| `subcommand` | string | sous-commande bd (`"ready"`, `"update"`, `"create"`, …) |
| `args` | string | liste complète des arguments |
| `duration_ms` | float | durée wall-clock en ms |
| `stdout` | string | sortie stdout complète (opt-in : `GT_LOG_BD_OUTPUT=true`) |
| `stderr` | string | sortie stderr complète (opt-in) |
| `status` | string | `"ok"` / `"error"` |

> La corrélation entre un `agent.event[tool_use]` appelant `bd` et le
> `bd.call` correspondant se fait par fenêtre temporelle + `session_id`.

---

#### `mail` — opérations mail

Toutes les opérations sur le système de mail Gastown, avec le contenu complet
du message.

| Attribut | Type | Description |
|----------|------|-------------|
| `run.id` | string | UUID run |
| `operation` | string | `"send"`, `"read"`, `"archive"`, `"list"`, `"delete"`, … |
| `msg.id` | string | identifiant du message |
| `msg.from` | string | expéditeur |
| `msg.to` | string | destinataire(s), séparés par virgule |
| `msg.subject` | string | sujet |
| `msg.body` | string | corps complet du message — aucune troncature |
| `msg.thread_id` | string | ID du fil de discussion |
| `msg.priority` | string | `"high"` / `"normal"` / `"low"` |
| `msg.type` | string | type de message (`"work"`, `"notify"`, `"queue"`, …) |
| `status` | string | `"ok"` / `"error"` |

Utiliser `RecordMailMessage(ctx, operation, MailMessageInfo{…}, err)` pour les
opérations où le message est disponible (send, read, deliver). Utiliser
`RecordMail(ctx, operation, err)` pour les opérations sans contenu (list,
archive-by-id).

---

#### Autres événements (portent tous `run.id`)

| Événement | Attributs clés |
|-----------|---------------|
| `sling` | `bead`, `target`, `status` |
| `nudge` | `target`, `status` |
| `done` | `exit_type` (COMPLETED / ESCALATED / DEFERRED), `status` |
| `polecat.spawn` | `name`, `status` |
| `polecat.remove` | `name`, `status` |
| `formula.instantiate` | `formula_name`, `bead_id`, `status` |
| `convoy.create` | `bead_id`, `status` |
| `daemon.restart` | `agent_type` |
| `pane.output` | `session`, `content` (opt-in : `GT_LOG_PANE_OUTPUT=true`) |

---

## 2. Hiérarchie du waterfall (nesting)

```
agent.instantiate                    ← racine GASTOWN (1 par run)
  ├─ session.start                   ← démarrage lifecycle
  ├─ prime / prime.context           ← injection contexte
  ├─ prompt.send                     ← daemon envoie un message à l'agent
  │
  ├─ agent.event [user/text]         ← l'agent reçoit un message texte
  ├─ agent.event [user/tool_result]  ← résultat d'outil reçu par l'agent
  │
  ├─ agent.event [assistant/thinking]  ← pensée interne (extended thinking)
  ├─ agent.event [assistant/text]      ← réponse texte de l'agent
  ├─ agent.event [assistant/tool_use]  ← l'agent appelle un outil
  │    ↳ bd.call                         si tool = bd
  │    ↳ mail                            si tool = mail
  │    ↳ sling                           si tool = gt sling
  │    ↳ nudge                           si tool = gt nudge
  │
  ├─ done                            ← fin de travail (COMPLETED/ESCALATED/…)
  └─ session.stop                    ← fin lifecycle
```

Les logs OTel ne portant pas de parent span ID natif, la hiérarchie est
reconstruite côté frontend par :
1. groupement sur `run.id`
2. ordonnancement chronologique par `_time`
3. règles de nesting définies en §4.3

---

## 3. Priorités d'enrichissement (par ordre décroissant)

### P1 — Contenu complet (implémentés en partie, à compléter)

| # | Quoi | Où ajouter | Impact |
|---|------|-----------|--------|
| 1 | **Texte complet du prompt** dans `prompt.send` (attribut `keys`) | `tmux.go:RecordPromptSend` | Voir exactement ce que le daemon a dit à l'agent |
| 2 | **Corps complet des mails** (send + deliver) | Appeler `RecordMailMessage` depuis `mail/router.go` et `mail/delivery.go` | Lire les messages échangés entre agents |
| 3 | **Stdout/stderr bd complets** (déjà opt-in, sans troncature maintenant) | `GT_LOG_BD_OUTPUT=true` | Résultats complets des appels bd |
| 4 | **Formule prime rendue** (déjà dans `prime.context`) | Déjà implémenté via `RecordPrimeContext` | Contexte de démarrage de l'agent |

### P2 — Corrélation et identité

| # | Quoi | Où ajouter | Impact |
|---|------|-----------|--------|
| 5 | **`GT_INSTANCE`** comme env var explicite (plutôt que dérivé) | `subprocess.go` + spawn managers | Instance Gastown identifiable de l'extérieur |
| 6 | **Bead ID du travail en cours** dans `agent.instantiate` | Passer depuis polecat spawn (`opts.Issue`) | Lier run → bead |
| 7 | **Git branch + commit** au moment du spawn | `session_manager.go` (déjà `polecatGitBranch`) | Savoir sur quelle branche tournait l'agent |
| 8 | **Token usage** (input/output tokens) depuis les JSONL Claude | Parser les champs `usage` du JSONL | Coût et context window par run |

### P3 — Métriques et durée

| # | Quoi | Où ajouter | Impact |
|---|------|-----------|--------|
| 9 | **Durée des tours LLM** (premier `assistant` → dernier `user/tool_result`) | Calculé côté frontend ou enrichi dans l'adaptateur | Latence perçue |
| 10 | **Nombre de retries bd** (exit code non-zéro + retry) | `beads/beads.go:run()` | Détecter l'instabilité bd |
| 11 | **Taille des messages mail** (`msg.body_len`) en métrique séparée | `RecordMailMessage` | Histogramme de taille des mails |
| 12 | **Durée totale du run** (instantiate → session.stop) | Calculé frontend | Temps de travail par agent |

### P4 — Contexte système

| # | Quoi | Où ajouter | Impact |
|---|------|-----------|--------|
| 13 | **Agent bead ID** (bead Gastown de l'agent lui-même) | `agent.instantiate` | Lier run → agent bead → historique |
| 14 | **Convoy/formula ID** si le travail vient d'un convoy | Depuis `opts.ConvoyID` si disponible | Tracer depuis la demande initiale |
| 15 | **Opérations fichier** (lecture/écriture par l'agent) | Tool_use content déjà disponible dans `agent.event` | Aucun code à ajouter |
| 16 | **Escalades** (escalate events) | `cmd/escalate.go` → `RecordEscalate` | Détecter les blocages |

---

## 4. API VictoriaLogs

### 4.1 Récupérer un run complet

```
GET /select/logsql/query?query=run.id:<uuid>&limit=10000
```

Retourne tous les records du run, triés par `_time`.

### 4.2 Lister les runs récents (vue instance)

```
GET /select/logsql/query
  ?query=_msg:agent.instantiate AND instance:<hostname>:gt AND _time:[now-1h, now]
  &limit=100
```

Un record `agent.instantiate` par run.

### 4.3 Filtrer par rig

```
GET /select/logsql/query?query=_msg:agent.instantiate AND rig:<rig-name>
```

### 4.4 Filtrer par rôle

```
GET /select/logsql/query?query=_msg:agent.instantiate AND role:polecat
```

### 4.5 Champs à indexer dans VictoriaLogs

```
run.id, instance, town_root, session_id, rig, role, agent_type,
event_type, msg.thread_id, msg.from, msg.to
```

---

## 5. Spec composant frontend waterfall

### 5.1 Vue globale (niveau instance)

Vue principale : liste des runs actifs et récents pour une instance Gastown.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│ 🏗 INSTANCE: laptop:gt   town: /Users/pa/gt                                │
├─────────────────────────────────────────────────────────────────────────────┤
│ ▼ Rig: wyvern                                                               │
│   ● polecat/wyvern-Toast  run:6ba7b8…  claudecode  14:32:01  [en cours]    │
│   ● witness/witness       run:9f2c1d…  claudecode  14:28:45  [en cours]    │
│ ▼ Rig: mol                                                                  │
│   ● polecat/mol-Nux       run:3e8a0c…  claudecode  14:30:12  ✓ 4m32s       │
│ ▼ Town                                                                      │
│   ● mayor/mayor           run:1a2b3c…  claudecode  09:15:00  [en cours]    │
└─────────────────────────────────────────────────────────────────────────────┘
```

### 5.2 Vue waterfall d'un run

```
┌──────────────────────────────────────────────────────────────────────────────────────┐
│ RUN 6ba7b810…  polecat/wyvern-Toast  rig:wyvern  claudecode  14:32:01 → 14:36:33    │
├──────────────────────────────────────────────────────────────────────────────────────┤
│ ▼ Type          │ Détail                     │ 0s     1s      10s     30s    4m32s  │
│─────────────────────────────────────────────────────────────────────────────────────│
│ ■ instantiate   │ claudecode/wyvern-Toast     │ ●                                   │
│ ■ session       │ start                       │ ├───────────────────────────────── │
│ ■ prime         │ polecat formula (2 KB)      │   ●                                │
│ ■ prompt        │ "You have bead gt-abc…"     │     ●                              │
│ ▶ thinking      │ [assistant] 847 chars       │       ──────                       │
│ ▶ text          │ [assistant] "I'll start…"   │            ─────                   │
│ ▶ tool_use      │ bd ready --rig wyvern       │                 ─                  │
│   ■ bd.call     │ ready (38ms) ✓              │                  ████              │
│ ▶ tool_result   │ [user] 3 issues found       │                      ──            │
│ ▶ tool_use      │ Read: src/main.go           │                        ─           │
│   ■ tool_result │ [user] 342 lines            │                         ─────      │
│ ▶ text          │ [assistant] "Here's the…"   │                              ───── │
│ ■ mail          │ send → witness (142 chars)  │                                  ● │
│ ■ done          │ COMPLETED                   │                                   ●│
│ ■ session       │ stop                        │                                   ─│
└──────────────────────────────────────────────────────────────────────────────────────┘
```

### 5.3 Codes couleur

| Événement | Couleur |
|-----------|---------|
| `agent.instantiate` | violet |
| `session.start` / `session.stop` | gris |
| `prime` / `prime.context` | bleu |
| `prompt.send` | cyan |
| `agent.event` thinking | lavande |
| `agent.event` text assistant | vert foncé |
| `agent.event` tool_use | orange |
| `agent.event` tool_result | orange clair |
| `agent.event` user | vert |
| `bd.call` | rouge |
| `mail` | jaune |
| `sling` / `nudge` | rose |
| `done` COMPLETED | vert vif |
| `done` ESCALATED / DEFERRED | orange vif |
| statut `"error"` | bordure rouge vif |

### 5.4 Règles de nesting

1. `agent.instantiate` → racine absolue.
2. `session.start` / `session.stop` → enfants directs, couvrent tout le run.
3. `prime` / `prime.context` → enfants directs, juste après `session.start`.
4. `prompt.send` → enfants directs.
5. `agent.event[tool_use]` → enfants directs ; les `bd.call` et `mail` tombant
   dans la fenêtre `[ts_tool_use, ts_tool_use + duration]` sont affichés
   en enfants imbriqués.
6. `agent.event[tool_result]` → enfant du `tool_use` précédent du même tour.
7. Tout événement sans parent inférable → affiché à plat.

### 5.5 Sources de durée

| Événement | Source |
|-----------|--------|
| `bd.call` | `duration_ms` (exact) |
| tour LLM | `ts(dernier tool_result du tour) - ts(premier thinking/text)` |
| run complet | `ts(session.stop) - ts(agent.instantiate)` |
| `session` | `ts(session.stop) - ts(session.start)` |
| points (prime, prompt, done) | point fixe 8px |

### 5.6 Shape de l'API (TypeScript)

```typescript
interface WaterfallEvent {
  id:        string;       // ID interne VictoriaLogs
  run_id:    string;       // UUID run GASTOWN
  body:      string;       // nom d'événement ("bd.call", "agent.event", …)
  timestamp: string;       // RFC3339
  severity:  "info" | "error";
  attrs: {
    // Présents sur tous les événements
    instance?:          string;
    town_root?:         string;
    session_id?:        string;
    rig?:               string;
    role?:              string;
    agent_type?:        string;
    agent_name?:        string;
    status?:            string;
    // agent.event
    event_type?:        string;
    "agent.role"?:      string;  // "assistant" | "user"
    content?:           string;  // contenu intégral
    native_session_id?: string;
    // bd.call
    subcommand?:        string;
    args?:              string;
    duration_ms?:       number;
    stdout?:            string;
    stderr?:            string;
    // mail
    "msg.id"?:          string;
    "msg.from"?:        string;
    "msg.to"?:          string;
    "msg.subject"?:     string;
    "msg.body"?:        string;
    "msg.thread_id"?:   string;
    "msg.priority"?:    string;
    "msg.type"?:        string;
    // prime
    formula?:           string;
    hook_mode?:         boolean;
    [key: string]:      unknown;
  };
}

interface WaterfallRun {
  run_id:     string;
  instance:   string;
  town_root:  string;
  agent_type: string;
  role:       string;
  agent_name: string;
  session_id: string;
  rig:        string;
  started_at: string;
  ended_at?:  string;    // présent si session.stop reçu
  duration_ms?: number;
  events:     WaterfallEvent[];
}

interface WaterfallInstance {
  instance:  string;
  town_root: string;
  runs:      WaterfallRun[];
}
```

---

## 6. Variables d'environnement

| Variable | Où positionné | Rôle |
|----------|--------------|------|
| `GT_RUN` | env tmux session + subprocess | UUID run, clé waterfall |
| `GT_OTEL_LOGS_URL` | démarrage daemon | endpoint VictoriaLogs OTLP |
| `GT_OTEL_METRICS_URL` | démarrage daemon | endpoint VictoriaMetrics OTLP |
| `GT_LOG_AGENT_OUTPUT` | opérateur | opt-in streaming JSONL Claude |
| `GT_LOG_BD_OUTPUT` | opérateur | opt-in contenu bd stdout/stderr |
| `GT_LOG_PANE_OUTPUT` | opérateur | opt-in sortie brute pane tmux |

`GT_RUN` est aussi surfacé en `gt.run_id` dans `OTEL_RESOURCE_ATTRIBUTES`
pour tous les subprocessus `bd`, corrélant leur télémétrie propre au run parent.

---

## 7. Statut d'implémentation

| Composant | Statut |
|-----------|--------|
| `run.id` généré au spawn (lifecycle, polecat, witness, refinery) | ✅ |
| `GT_RUN` propagé env tmux + subprocess `agent-log` | ✅ |
| `GT_RUN` dans `OTEL_RESOURCE_ATTRIBUTES` pour bd | ✅ |
| `run.id` injecté dans chaque événement OTel | ✅ |
| `agent.instantiate` avec `instance`, `role`, `town_root` | ✅ |
| `RecordMailMessage` avec contenu complet | ✅ (appels à ajouter dans mail/) |
| Contenu agent.event sans troncature | ✅ |
| Contenu bd stdout/stderr sans troncature | ✅ |
| Texte complet du prompt dans `prompt.send` | ⬜ P1 |
| `RecordMailMessage` appelé depuis mail/router + delivery | ⬜ P2 |
| Bead ID du travail dans `agent.instantiate` | ⬜ P2 |
| Token usage depuis JSONL | ⬜ P3 |
| Frontend waterfall | ⬜ à construire selon ce spec |
