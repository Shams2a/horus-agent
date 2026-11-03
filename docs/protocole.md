# Contrat agent ↔ Network (v1)

Ce document est la référence commune à l'agent (`horus-agent`, Go) et au backend Network (FastAPI). Toute évolution passe par ici avant d'être codée d'un côté ou de l'autre.

Le cadrage produit vit dans la carte « Monitoring » du board « Fonctionnalités ». Rappel des principes qui contraignent ce contrat :

- l'agent surveille seulement sa machine en v1, sous Linux ;
- toutes les connexions partent de l'agent, Network ne contacte jamais la machine ;
- l'agent n'exécute aucune commande reçue. La seule instruction acceptée est « passer à la version X », limitée aux versions officielles signées et plus récentes ;
- chaque mesure dit quel agent observe et quelle cible, pour accueillir plus tard un mode sonde sans migration.

## 1. Généralités

| Élément | Valeur |
|---|---|
| Adresse de base | `https://networkapi.<domaine>/agent/v1`. La commande d'installation la fournit (`--api-url https://networkapi.<domaine>`), puis l'agent la garde dans sa configuration (`api_url`) |
| Format | JSON en UTF-8, `Content-Type: application/json` |
| Dates | RFC 3339 en UTC, par exemple `2026-11-03T18:42:00Z` |
| Tailles | octets (`*_bytes`), débits en octets par seconde (`*_bps`) |
| Pourcentages | nombre décimal entre 0 et 100 |
| Corps de requête | 1 Mio au plus |
| Identification | en-tête `User-Agent: horus-agent/<version> (linux; <arch>)` sur chaque requête |
| Proxy | l'agent respecte `HTTPS_PROXY` et `NO_PROXY` |

Un champ absent veut dire « non mesuré » (collecteur désactivé ou information indisponible), jamais zéro.

Les erreurs renvoient toujours :

```json
{ "error": { "code": "token_expired", "message": "Le jeton a expiré." } }
```

Le champ `code` est stable et sert au programme. Le champ `message` s'adresse à un humain et peut changer.

## 2. Identité de l'agent

À l'installation, l'agent génère une paire de clés **Ed25519** sur la machine.

- La clé privée reste dans `/var/lib/horus-agent/agent.key` (droits `0600`, propriétaire `horus-agent`). Elle ne quitte jamais la machine.
- La clé publique (32 octets, en base64 standard) est envoyée à l'inscription. Network ne stocke que celle-ci.

### Identifiant de machine

L'agent n'envoie jamais `/etc/machine-id` en clair (systemd le considère comme confidentiel). Il envoie :

```
machine_hash = hex(sha256("horus-agent:" + contenu de /etc/machine-id sans espace ni retour à la ligne))
```

Network s'en sert pour reconnaître une réinstallation sur la même machine. Deux VM clonées ont le même `machine_hash` : c'est attendu, et l'assistant d'installation le signale.

### Signature des requêtes

Toutes les requêtes après l'inscription sont signées. En-têtes :

| En-tête | Contenu |
|---|---|
| `X-Horus-Agent` | identifiant de l'agent (UUID) |
| `X-Horus-Timestamp` | heure d'envoi en millisecondes depuis l'époque Unix |
| `X-Horus-Signature` | base64 standard de la signature Ed25519 du texte canonique |

Texte canonique, lignes séparées par `\n`, sans `\n` final :

```
<MÉTHODE en majuscules>
<chemin complet sans domaine ni paramètres, ex. /agent/v1/report>
<valeur de X-Horus-Timestamp>
<hex minuscule de sha256(corps brut de la requête)>
```

Pour une requête sans corps, on prend le sha256 de la chaîne vide.

Network refuse la requête (`401 signature_invalid`) si :

- la signature ne correspond pas à la clé publique de l'agent ;
- l'horodatage s'écarte de plus de 5 minutes de l'heure du serveur ;
- l'horodatage n'est pas strictement supérieur au dernier accepté pour cet agent (protection contre le rejeu). L'agent garantit cette croissance en mémoire : `ts = max(maintenant, dernier_ts + 1)`.

Un vecteur de test se trouve en annexe A. Les deux implémentations doivent le valider.

## 3. Inscription

### 3.1 Jeton d'inscription

Il est créé dans l'admin Network par une personne qui a `hnetwork.monitoring.manage`.

- Format : `hn_enr_` suivi de 32 caractères base62.
- Usage unique, valable 1 heure.
- Network stocke uniquement son sha256.
- Il peut porter un projet présélectionné.

La liste des projets proposés à l'installation correspond aux projets de l'organisation sur lesquels **la personne qui a créé le jeton** a le droit d'édition (placer une machine modifie le canvas).

### 3.2 `POST /agent/v1/enroll/preview`

Il vérifie le jeton **sans le consommer**, pour que l'assistant puisse poser ses questions. Ce point d'accès n'est pas signé.

Requête :

```json
{ "token": "hn_enr_…", "machine_hash": "9f2c…" }
```

Réponse `200` :

```json
{
  "org": { "id": "…", "name": "IFCV" },
  "expires_at": "2026-11-03T19:42:00Z",
  "preselected_project": { "id": "…", "name": "Infra IFCV" },
  "projects": [ { "id": "…", "name": "Infra IFCV" }, { "id": "…", "name": "Labo" } ],
  "existing_agent": { "id": "…", "name": "srv-web-01", "last_seen_at": "2026-11-01T08:00:00Z" }
}
```

`preselected_project` et `existing_agent` valent `null` s'il n'y en a pas. `existing_agent` est renseigné quand un agent de la même organisation, non révoqué, a le même `machine_hash`.

### 3.3 `POST /agent/v1/enroll`

Il consomme le jeton. Ce point d'accès n'est pas signé : c'est lui qui enregistre la clé.

Requête :

```json
{
  "token": "hn_enr_…",
  "public_key": "<base64, 32 octets>",
  "machine_hash": "9f2c…",
  "agent_version": "1.0.0",
  "facts": { "…": "voir § 5" },
  "collectors": ["system", "systemd", "docker"],
  "options": {
    "multi_project": false,
    "allow_remote_update": true
  },
  "project_id": "…",
  "replace_agent_id": "…"
}
```

- `collectors` reprend les cases cochées dans l'assistant. Valeurs v1 : `system` (processeur, mémoire, disques, réseau), `systemd`, `docker`.
- `project_id` est facultatif. Il doit figurer dans la liste du préchargement. Network relie alors l'agent à ce projet, la machine apparaissant dans l'onglet Monitoring du menu Ajouter de ce projet, en attente de placement sur le canvas.
- `replace_agent_id` est facultatif. Il reprend l'agent existant signalé par le préchargement : Network remplace sa clé publique, ses options et sa fiche, et conserve son identifiant et ses liens vers les nœuds.

Réponse `201` :

```json
{
  "agent_id": "…",
  "org": { "id": "…", "name": "IFCV" },
  "report_interval_s": 30,
  "agent_url": "https://network.<domaine>/org/…/agents/…"
}
```

`agent_url` est le lien que l'assistant affiche à la fin.

Erreurs des deux points d'accès :

| Statut | `code` | Cas |
|---|---|---|
| 400 | `invalid_request` | corps mal formé, clé publique invalide, collecteur inconnu |
| 401 | `token_invalid` | jeton inconnu |
| 401 | `token_expired` | jeton expiré |
| 401 | `token_used` | jeton déjà consommé (uniquement sur `enroll`) |
| 403 | `monitoring_not_installed` | l'organisation n'a pas installé l'intégration Monitoring de la Marketplace (le jeton n'est pas consommé) |
| 403 | `agent_quota_reached` | limite d'agents du plan atteinte |
| 403 | `project_not_allowed` | `project_id` absent de la liste autorisée |
| 503 | `service_unavailable` | Network ne peut pas vérifier l'intégration pour le moment, réessayer plus tard |
| 409 | `replace_mismatch` | `replace_agent_id` ne correspond pas au même `machine_hash` ou à la même organisation |

## 4. Envoi des mesures et signal de vie

### `POST /agent/v1/report` (signé)

L'agent l'appelle toutes les `report_interval_s` secondes (30 par défaut). Chaque appel compte comme un signal de vie, même sans mesure.

Requête :

```json
{
  "agent_version": "1.0.0",
  "sent_at": "2026-11-03T18:42:30Z",
  "facts": { "…": "voir § 5, seulement si elle a changé" },
  "samples": [
    {
      "target": { "kind": "self" },
      "ts": "2026-11-03T18:42:00Z",
      "metrics": { "…": "voir § 6" }
    }
  ]
}
```

- `facts` n'est envoyé qu'au premier envoi après le démarrage, quand la fiche change, et au moins une fois par heure.
- L'agent relève les mesures toutes les 60 secondes. Un envoi contient donc en général zéro ou une mesure.
- **Mise en tampon** : tant qu'un envoi échoue, l'agent garde ses mesures en mémoire, sur 24 heures au plus (au-delà, il supprime les plus anciennes). Au retour du réseau, il les renvoie par paquets de 500 mesures au plus, dans l'ordre chronologique.
- `target.kind` vaut toujours `self` en v1. Le mode sonde ajoutera par exemple `{ "kind": "host", "address": "10.0.0.5" }`.
- Network ignore une mesure dont `ts` est postérieur de plus de 5 minutes à l'heure du serveur, ou antérieur de plus de 7 jours. Le reste de l'envoi est accepté.

Réponse `200` :

```json
{
  "server_time": "2026-11-03T18:42:30Z",
  "report_interval_s": 30,
  "update": { "version": "1.1.0" },
  "uninstall": false
}
```

- `report_interval_s` permet à Network de ralentir un agent. L'agent applique la nouvelle valeur, bornée entre 15 et 300 secondes.
- `update` vaut `null`, sauf si quelqu'un a demandé une mise à jour dans l'admin **et** si l'agent l'autorise (`allow_remote_update`). Il ne contient qu'un numéro de version (voir § 7).
- `uninstall` vaut `true` quand un admin a demandé la désinstallation depuis Network (voir § 7 bis). Absent : `false`. Network ne le demande qu'aux agents qui annoncent la capacité `remote_uninstall`.

Erreurs :

| Statut | `code` | Réaction de l'agent |
|---|---|---|
| 400 | `invalid_request` | il journalise, abandonne cet envoi et ses mesures, et continue |
| 401 | `signature_invalid` | il journalise et réessaie avec un délai croissant. Il ne relance pas l'inscription tout seul |
| 410 | `agent_revoked` | il arrête d'envoyer, journalise « agent révoqué depuis Network » et quitte avec le code 78, qui empêche systemd de le relancer (§ 8) |
| 413 | `payload_too_large` | il divise le paquet et réessaie |
| 429 ou 5xx | — | il réessaie avec un délai croissant (5 s, 10 s, 20 s, 40 s, puis toutes les minutes), en continuant de mettre en tampon |

### `POST /agent/v1/uninstall` (signé)

`horus-agent uninstall` l'envoie avant de tout supprimer. Corps : `{}`. Réponse : `204`. Network marque l'agent comme désinstallé. Un échec réseau ne bloque pas la désinstallation.

L'agent l'envoie aussi pour confirmer une désinstallation demandée depuis Network (§ 7 bis). Network supprime alors l'agent et ses données.

## 5. Fiche de la machine (`facts`)

```json
{
  "hostname": "srv-web-01",
  "os": { "id": "debian", "name": "Debian GNU/Linux", "version": "12" },
  "kernel": "6.1.0-26-amd64",
  "arch": "amd64",
  "cpu": { "model": "Intel(R) Xeon(R) E-2236", "cores": 4 },
  "memory_total_bytes": 8589934592,
  "virtualization": "lxc",
  "boot_time": "2026-10-28T06:12:00Z",
  "interfaces": [
    { "name": "eth0", "mac": "bc:24:11:aa:bb:cc", "addresses": ["10.0.0.5/24", "fe80::1/64"] }
  ],
  "disks": [ { "mount": "/", "fs": "ext4", "total_bytes": 34359738368 } ],
  "collectors": ["system", "systemd"],
  "allow_remote_update": true,
  "capabilities": ["remote_uninstall"]
}
```

- `virtualization` vaut `physical`, `vm`, `lxc`, `container` ou `unknown`. WSL 2 compte comme `vm`.
- `capabilities` (0.2.0 et suivantes) liste ce que l'installation permet. `remote_uninstall` : le service d'aide root est installé (§ 7 et § 8), la désinstallation peut être demandée depuis Network. Absent : aucune capacité (installations 0.1.x).
- Les interfaces de boucle locale (`lo`) et les interfaces virtuelles de conteneurs (`veth*`, `docker0`, `br-*`) sont exclues.
- Les disques retenus sont les systèmes de fichiers réels. Les pseudo-systèmes (`tmpfs`, `overlay`, `proc`, `squashfs`…) sont exclus.

## 6. Mesures (`metrics`)

Chaque bloc n'est présent que si son collecteur est actif.

```json
{
  "cpu": { "usage_pct": 12.5, "load1": 0.42, "load5": 0.35, "load15": 0.30 },
  "memory": { "used_bytes": 3221225472, "total_bytes": 8589934592, "swap_used_bytes": 0, "swap_total_bytes": 1073741824 },
  "disks": [ { "mount": "/", "used_bytes": 12884901888, "total_bytes": 34359738368 } ],
  "network": [ { "iface": "eth0", "rx_bps": 120400, "tx_bps": 80200 } ],
  "systemd": { "failed_units": ["nginx.service"] },
  "docker": {
    "containers": [
      { "id": "4f1c2a9e0b7d", "name": "api", "image": "ghcr.io/acme/api:1.2", "state": "running", "restart_count": 0 }
    ]
  }
}
```

- Collecteur `system` : `cpu`, `memory`, `disks`, `network`. `usage_pct` est la moyenne sur l'intervalle depuis la mesure précédente. Les débits sont aussi des moyennes sur cet intervalle.
- Collecteur `systemd` : la liste des unités en échec. Une liste vide signifie qu'aucune unité n'est en échec.
- Collecteur `docker` : `state` reprend l'état Docker (`running`, `exited`, `restarting`, `paused`, `created`, `dead`). Si le socket Docker est inaccessible, le bloc vaut `{ "error": "socket_unavailable" }`.
- `memory.used_bytes` exclut le cache et les tampons (`MemTotal - MemAvailable`).

## 7. Mises à jour

Deux déclencheurs :

- `horus-agent update`, lancé en root sur la machine ;
- la réponse au signal de vie (`update.version`), si l'option `allow_remote_update` est active.

Dans les deux cas, l'agent suit la même procédure :

1. Il refuse toute version inférieure ou égale à la sienne (comparaison semver).
2. Il télécharge, depuis `download_base_url` de sa configuration (par défaut `https://github.com/Shams2a/horus-agent/releases/download`) :
   - `v<version>/horus-agent_<version>_linux_<arch>.tar.gz`
   - `v<version>/SHA256SUMS`
   - `v<version>/SHA256SUMS.sig` (signature Ed25519 de `SHA256SUMS` : 64 octets en base64 standard)
3. Il vérifie la signature Ed25519 de `SHA256SUMS` avec la **clé publique intégrée au binaire**, puis l'empreinte de l'archive. Au moindre écart, il abandonne et le journalise.
4. L'installation dépend de la façon dont l'agent a été installé :
   - **avec le service d'aide (0.2.0 et suivantes)** : le binaire appartient à root et l'agent ne peut pas le modifier. Il dépose l'archive, `SHA256SUMS`, `SHA256SUMS.sig` et le numéro de version dans `/var/lib/horus-agent/requests/update/`, puis crée `ready`. Le service d'aide, lancé en root par systemd, **revérifie** la signature, l'empreinte et la version avec son propre binaire, installe le nouveau de façon atomique puis relance l'agent. Il ne fait confiance à rien de ce que l'agent a vérifié ;
   - **sans service d'aide (installations 0.1.x)** : l'agent remplace lui-même son binaire (`/opt/horus-agent/bin/horus-agent`, qui lui appartient) et quitte avec le code 0. systemd le relance avec la nouvelle version.

`horus-agent update`, lancé en root, installe directement : le binaire qui s'exécute appartient à root.

Pour passer une installation 0.1.x au nouveau modèle (binaire root, service d'aide, désinstallation à distance), relancer `install.sh` avec `--repair` : il réinstalle le binaire et les services sans toucher à l'identité de l'agent ni à son placement.

Network ne transmet jamais d'adresse, de script ni de paramètre : seulement un numéro de version. Changer `download_base_url` (passage à un sous-domaine, par exemple) ne permet donc pas d'installer un binaire non signé.

L'installation initiale (`install.sh`) vérifie l'empreinte SHA-256 de l'archive. La confiance initiale repose sur le HTTPS de GitHub, comme pour `cloudflared` ou `tailscale`. La signature protège les mises à jour, qui se font sans humain devant l'écran.

## 7 bis. Désinstallation demandée depuis Network

1. Un admin (`hnetwork.monitoring.manage`) demande la désinstallation dans la fiche de l'agent. Network note la demande, et l'agent passe à l'état `uninstall_requested`.
2. À son signal de vie suivant, l'agent reçoit `"uninstall": true`.
3. L'agent confirme par `POST /agent/v1/uninstall`, puis crée `/var/lib/horus-agent/requests/uninstall`.
4. Le service d'aide, lancé en root, arrête l'agent, puis supprime ses services, ses fichiers et son compte. Il ne lit rien dans le fichier de demande : sa présence suffit.
5. À la confirmation, Network supprime l'agent, ses mesures et ses liens, ainsi que les nœuds du canvas si l'admin l'a demandé.

Un agent sans service d'aide qui recevrait quand même la demande la confirme, puis s'arrête avec le code 78 : il est neutralisé, mais ses fichiers restent sur la machine.

Comme pour la mise à jour, Network ne transmet aucun paramètre. Au pire, quelqu'un qui contrôlerait Network pourrait désinstaller des agents, jamais exécuter de code sur la machine.

## 8. Fichiers et codes de sortie

| Chemin | Contenu | Propriétaire |
|---|---|---|
| `/opt/horus-agent/bin/horus-agent` | binaire | `root` (0.2.0) ; `horus-agent` pour les installations 0.1.x |
| `/usr/local/bin/horus-agent` | lien vers le binaire, pour les commandes d'admin | `root` |
| `/etc/horus-agent/config.json` | `api_url`, `download_base_url`, `agent_id`, `collectors`, options | `root`, lisible par `horus-agent` |
| `/var/lib/horus-agent/agent.key` | clé privée | `horus-agent`, `0600` |
| `/var/lib/horus-agent/requests/` | demandes de l'agent au service d'aide (`update/`, `uninstall`) | `horus-agent` |
| `/etc/systemd/system/horus-agent.service` | service | `root` |
| `/etc/systemd/system/horus-agent-helper.path` et `.service` | service d'aide : surveille `requests/` et lance `horus-agent helper` en root | `root` |

| Code de sortie | Sens |
|---|---|
| 0 | arrêt normal ou fin de mise à jour (systemd relance) |
| 1 | erreur inattendue (systemd relance) |
| 78 | agent révoqué ou configuration inutilisable. Le service déclare `RestartPreventExitStatus=78` |

## 9. État calculé par Network

Network calcule l'état ; ce n'est pas l'agent qui l'envoie. Les seuils sont fixes en v1.

| État | Condition |
|---|---|
| `pending` | inscrit, aucun signal reçu pour l'instant |
| `online` | signal reçu il y a moins de 3 × `report_interval_s` et aucune raison de dégradation |
| `degraded` | signal récent et au moins une raison ci-dessous |
| `unreachable` | aucun signal depuis 3 × `report_interval_s` (90 s par défaut) |
| `revoked` | révoqué depuis l'admin |
| `uninstall_requested` | désinstallation demandée depuis l'admin, en attente de la confirmation de l'agent |
| `uninstalled` | `POST /uninstall` reçu |

Raisons de dégradation (codes stables, affichés dans la fiche) :

| Code | Condition |
|---|---|
| `disk_high` | un disque au-dessus de 90 % (`used_bytes / total_bytes`) |
| `memory_high` | mémoire au-dessus de 95 % sur toutes les mesures des 5 dernières minutes |
| `systemd_failed` | au moins une unité dans `failed_units` |

L'état des conteneurs Docker est affiché, mais ne dégrade pas l'état en v1.

Network conserve les mesures 7 jours.

## Annexe A. Vecteur de test de signature

Généré par `internal/protocol/signature_test.go`. Les deux implémentations doivent retrouver exactement la même signature (Ed25519 est déterministe).

```
Graine de la clé privée (hex, 32 octets) :
  9d61b19deffd5a60ba844af492ec2cc44449c5697b326919703bac031cae7f60
Clé publique (base64) :
  11qYAYKxCrfVS/7TyWQHOg7hcvPapiMlrwIaaPcHURo=
Méthode : POST
Chemin : /agent/v1/report
X-Horus-Timestamp : 1762195350000
Corps (octets exacts) : {"agent_version":"1.0.0","samples":[]}
sha256 du corps (hex) :
  f505c4b1229d0c3c57795157e63ff0c469a2e7142ffdc5d961f8e9bfd47893b7
Signature (base64) :
  PIMYX18loGN0RFLtU0uV0UT2a7l9UavpJO6Sex4r4+QY1mDVxxZuoQ6lj0rgRhfKPqg6TeHUQxRkGVXGl6D7DA==
```
