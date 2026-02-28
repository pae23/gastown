# Federated Execution Environments

> Mini-spec for environment-aware molecule steps in a Gas Town federation.

**Status**: Draft
**Related**: [federation.md](federation.md) | [model-aware-molecules.md](model-aware-molecules.md)

---

## 1. Problème et périmètre

Un step de molécule déclare aujourd'hui *quel modèle* il veut. Ce design étend ce principe à *dans quel environnement* il tourne : quels outils sont disponibles, quelle politique réseau s'applique, quels secrets sont visibles.

**Ce document ne couvre pas** comment l'environnement est créé (container, VM, bare metal — c'est la responsabilité du town qui l'héberge). Il couvre uniquement :

- le modèle de données pour décrire un environnement
- comment un town annonce ses capacités à la fédération
- comment un step exprime ses besoins
- comment la fédération route le step vers le bon town

---

## 2. Concept central : l'Environment Profile

Un `EnvProfile` est une déclaration de ce qu'un town peut fournir pour exécuter un step. Il est défini par le town localement, et annoncé à ses pairs fédérés.

```toml
# ~/.gt/envs.toml  (déclaré par chaque town)

[envs.python-isolated]
description = "Python 3.12 sans accès réseau"
tools       = ["git", "python3.12", "uv", "make"]
network     = "isolated"
secrets     = []                      # aucun secret injecté par défaut
tags        = ["python", "isolated"]

[envs.node-web]
description = "Node.js avec accès npm registry"
tools       = ["git", "node22", "npm", "pnpm"]
network     = "restricted:registry.npmjs.org,github.com"
secrets     = ["GITHUB_TOKEN"]
tags        = ["node", "web"]

[envs.secure-sandbox]
description = "Environnement vide, réseau coupé, zéro secret"
tools       = ["git"]
network     = "isolated"
secrets     = []
tags        = ["sandbox", "untrusted"]

[envs.full]
description = "Environnement standard du town (défaut)"
tools       = []                      # liste vide = "ce qui est sur la machine"
network     = "full"
secrets     = ["ANTHROPIC_API_KEY", "GITHUB_TOKEN"]
tags        = ["default"]
```

### Champs d'un profil

| Champ | Type | Description |
|---|---|---|
| `description` | string | Texte libre |
| `tools` | []string | Exécutables disponibles. Vide = pas de contrainte |
| `network` | string | `"isolated"` · `"full"` · `"restricted:<allowlist>"` |
| `secrets` | []string | Noms des vars d'environnement injectées dans le step |
| `tags` | []string | Labels libres pour le matching par capacité |
| `resources` | table | Optionnel : cpu, memory, timeout |

---

## 3. Ce qu'un step déclare

Extension naturelle du schéma de step (cf. `model-aware-molecules.md`) :

```toml
[[steps]]
id    = "run-tests"
title = "Run test suite"
model = "auto"

# Option A : nom exact d'un profil (résolu localement ou dans la fédération)
env = "python-isolated"

# Option B : matching par capacités (le runtime trouve un profil compatible)
env_tools   = ["python3", "make"]
env_network = "isolated"
env_tags    = ["python"]
```

`env` et `env_tools/env_network/env_tags` sont mutuellement exclusifs.
Un step sans contrainte d'environnement utilise le profil `"full"` du town local — comportement identique à aujourd'hui.

### Priorité de résolution

```
1. env = "nom-exact" dans le town local         → exécution locale
2. env = "nom-exact" dans un town fédéré        → délégation
3. env_tools/env_tags : matching dans town local → exécution locale
4. env_tools/env_tags : matching dans fédération → délégation
5. Aucun match                                   → step bloqué, erreur
```

---

## 4. Annonce des capacités dans la fédération

Chaque town publie un **manifeste de capacités** dans ses beads (bead de type `town-capabilities`, un par town). Ce bead est synchronisé via les remotes Dolt existants.

```
Bead: hq-capabilities
Type: town-capabilities
Slots:
  hop_id   = "hop://alice@example.com/main-town"
  profiles = <JSON des noms et tags de chaque EnvProfile>
  updated  = <timestamp>
```

Le manifeste ne contient **pas** les secrets ni les détails internes des profils — uniquement les noms, tags, tools et politique réseau. Ce qui suffit pour le routing.

```bash
# Consulter les capacités d'un town fédéré
gt remote capabilities hop://alice@example.com/main-town

# Résultat :
# Profiles:
#   python-isolated  [python, isolated]  tools: git, python3.12, uv
#   node-web         [node, web]         tools: git, node22, npm
```

---

## 5. Routing fédéré d'un step

Le routing se fait en deux passes, sans protocole nouveau — en s'appuyant sur le système de délégation et mail existant.

### Passe 1 : résolution locale

Au moment de `gt mol execute` (ou du dispatch d'un step par le refinery), le router :

1. Charge les `EnvProfile` locaux depuis `~/.gt/envs.toml`
2. Vérifie si le step peut s'exécuter localement
3. Si oui → exécution locale normale (agent tmux existant)

### Passe 2 : délégation fédérée

Si aucun profil local ne satisfait les contraintes :

1. Query des manifestes des towns fédérés connus (`gt remote list`)
2. Sélection du town le plus adapté (matching tags + tools + network ; tiebreak : latence, charge déclarée)
3. Création d'un bead de délégation sur le town distant via le mécanisme `AddDelegation` existant
4. Envoi d'un message mail au mayor du town distant avec le step à exécuter
5. Le town distant instancie le step dans ses propres beads, l'exécute, et notifie à la complétion
6. Le town local reçoit la notification, marque le step comme complété dans la molécule

```
Town local                          Town distant
    │                                    │
    │── mail: "exécute step X" ─────────▶│
    │                                    │── crée bead step
    │                                    │── spawn agent
    │                                    │── step s'exécute
    │                                    │── step complété
    │◀─ mail: "step X complété" ─────────│
    │                                    │
    │── step marqué done dans molécule   │
```

Le step délégué apparaît dans la molécule locale comme n'importe quel autre step — il a juste un attribut `delegated_to` pointant vers le HOP URI du town distant.

---

## 6. Modèle de sécurité

**Principe** : le town distant est souverain sur son environnement. Le town local ne peut pas inspecter, modifier, ni contourner les contraintes du profil distant.

| Propriété | Garantie |
|---|---|
| **Isolation réseau** | Enforced par le town distant (pas de promesse sur parole) |
| **Secrets** | Jamais transmis par la fédération. Pre-provisionnés sur le town distant |
| **Outillage** | Le town distant certifie son manifeste ; le town local lui fait confiance |
| **Contenu du step** | Le step (description, instructions) est transmis. Les credentials non |
| **Résultats** | Retournés via Dolt sync + mail. Pas de canal direct |

Un town choisit explicitement quels profils il expose à la fédération (champ `federated = true` sur le profil). Les profils non fédérés restent internes.

```toml
[envs.secure-sandbox]
# ...
federated = true    # visible par les pairs fédérés

[envs.internal-gpu]
# ...
federated = false   # usage interne uniquement (défaut)
```

---

## 7. Extension du modèle de données Step

Ajout au schéma `Step` existant (cf. `internal/formula/types.go`) :

```go
// Execution environment constraints (all optional).
// Env is a named profile: resolved locally first, then in the federation.
Env        string   `toml:"env"`
// EnvTools requires specific executables to be available.
EnvTools   []string `toml:"env_tools"`
// EnvNetwork requires a specific network policy.
// Values: "isolated", "full", "restricted:<host1>,<host2>"
EnvNetwork string   `toml:"env_network"`
// EnvTags requires an environment with all listed tags.
EnvTags    []string `toml:"env_tags"`
```

`Env` et `EnvTools/EnvNetwork/EnvTags` sont mutuellement exclusifs (validation au parse).

---

## 8. Exemple de molécule multi-town

```toml
formula = "mol-secure-pipeline"
version = 1

# Step 1 : analyse du code — peut tourner n'importe où
[[steps]]
id    = "analyze"
title = "Analyze codebase"
model = "claude-sonnet-4-5"

# Step 2 : exécution de tests dans un sandbox isolé
# → sera délégué au town "forge" si indisponible localement
[[steps]]
id         = "test"
title      = "Run tests in isolation"
needs      = ["analyze"]
env        = "python-isolated"
model      = "auto"
min_swe    = 50

# Step 3 : synthèse — retour sur le town local
[[steps]]
id    = "report"
title = "Synthesize results"
needs = ["test"]
model = "claude-sonnet-4-5"
```

---

## 9. Questions ouvertes

| Question | Discussion |
|---|---|
| **Sélection du town distant** | Sur quel critère choisir entre deux towns qui satisfont les mêmes contraintes ? Charge déclarée, latence, affinité organisationnelle (HOP entity) ? |
| **Annulation d'un step délégué** | Si la molécule est `burn`ée en local, comment notifier le town distant d'annuler le step en cours ? |
| **Résultats structurés** | Les résultats d'un step sont aujourd'hui dans les beads (description du bead complété). Suffit-il pour les cas cross-town, ou faut-il un slot dédié ? |
| **Version des profils** | Un profil change (outil mis à jour) — comment invalider les matchings en cache dans les towns pairs ? |
| **Confiance transitoire** | Town A délègue à Town B qui redélègue à Town C — doit-on permettre la délégation en chaîne, et jusqu'où ? |
