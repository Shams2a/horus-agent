# horus-agent

Agent de monitoring de [Horus Network](https://github.com/Shams2a/H-network-v2). Il surveille la machine Linux sur laquelle il est installé et envoie son état à Network, qui l'affiche sur le nœud correspondant du canvas.

> État : première version en préparation. L'agent fonctionne, mais aucune version n'est encore publiée.

## Principes

- **Il ne fait que surveiller.** L'agent n'exécute aucune commande reçue de Network. La seule instruction qu'il accepte est « passer à la version X », et seulement pour une version officielle, signée et plus récente que la sienne.
- **Aucune connexion entrante.** C'est toujours l'agent qui contacte Network, en HTTPS. Aucun port n'est ouvert sur la machine.
- **Pas de droits root.** Le service tourne sous un compte dédié, `horus-agent`, avec les protections de systemd.
- **Vous choisissez ce qui est surveillé**, à l'installation : système (processeur, mémoire, disques, réseau), services systemd, conteneurs Docker.

## Installation

La commande d'installation se génère dans l'admin de votre organisation Network (« Ajouter un agent »). Elle ressemble à ceci :

```bash
curl -fsSL https://github.com/Shams2a/horus-agent/releases/latest/download/install.sh | sudo bash -s -- --token hn_enr_xxxx --api-url https://networkapi.example.com
```

Linux uniquement (x86_64 et ARM64), avec systemd.

Le script vérifie la machine, télécharge le binaire, contrôle son empreinte, crée le compte `horus-agent`, puis lance l'assistant. L'assistant demande :

1. ce qu'il faut surveiller (cases à cocher) ;
2. le projet où placer la machine, si vous voulez la placer tout de suite ;
3. si la machine pourra être placée dans plusieurs projets ;
4. si Network pourra lancer les mises à jour.

Pour installer sans questions (Ansible, scripts) :

```bash
… | sudo bash -s -- --token hn_enr_xxxx --api-url https://networkapi.example.com   --yes --metrics system,systemd --project "Infra" --multi-project --no-remote-update
```

## Commandes

| Commande | Rôle |
|---|---|
| `horus-agent status` | configuration et état du service |
| `sudo horus-agent configure` | change ce qui est surveillé, autorise ou refuse les mises à jour à distance |
| `sudo horus-agent update [version]` | installe la dernière version signée, ou celle indiquée |
| `sudo horus-agent uninstall` | prévient Network puis supprime l'agent, sa configuration et son compte |
| `horus-agent run` | boucle de l'agent, lancée par le service systemd |

## Fichiers

| Chemin | Contenu |
|---|---|
| `/opt/horus-agent/bin/horus-agent` | binaire, propriété du compte `horus-agent` (mises à jour sans root) |
| `/etc/horus-agent/config.json` | configuration |
| `/var/lib/horus-agent/agent.key` | clé privée de l'agent, lisible par lui seul |
| `/etc/systemd/system/horus-agent.service` | service, avec les protections systemd |

Journal : `journalctl -u horus-agent`.

## Développement

```bash
go test ./...
```

L'agent ne dépend que de la bibliothèque standard de Go. Le code ne compile que pour Linux.

## Documentation

- [Contrat agent ↔ Network](docs/protocole.md) : inscription, envoi des mesures, signature des requêtes, mises à jour.
- [Publication des versions](docs/publication.md) : créer une Release signée, gérer la clé de signature.

## Vérifier une version

Chaque Release contient `SHA256SUMS` et sa signature `SHA256SUMS.sig`. Pour les vérifier à la main :

```bash
scripts/verify-signature.sh SHA256SUMS SHA256SUMS.sig
sha256sum -c SHA256SUMS --ignore-missing
```
