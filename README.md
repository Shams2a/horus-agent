# horus-agent

Agent de monitoring de [Horus Network](https://github.com/Shams2a/H-network-v2). Il surveille la machine Linux sur laquelle il est installé et envoie son état à Network, qui l'affiche sur le nœud correspondant du canvas.

> État : en construction. Seule la commande `horus-agent version` existe pour l'instant. L'installation arrive avec la première version publiée.

## Principes

- **Il ne fait que surveiller.** L'agent n'exécute aucune commande reçue de Network. La seule instruction qu'il accepte est « passer à la version X », et seulement pour une version officielle, signée et plus récente que la sienne.
- **Aucune connexion entrante.** C'est toujours l'agent qui contacte Network, en HTTPS. Aucun port n'est ouvert sur la machine.
- **Pas de droits root.** Le service tourne sous un compte dédié, `horus-agent`, avec les protections de systemd.
- **Vous choisissez ce qui est surveillé**, à l'installation : système (processeur, mémoire, disques, réseau), services systemd, conteneurs Docker.

## Installation

La commande d'installation se génère dans l'admin de votre organisation Network (« Ajouter un agent »). Elle ressemblera à ceci :

```bash
curl -fsSL https://github.com/Shams2a/horus-agent/releases/latest/download/install.sh | sudo bash -s -- --token hn_enr_xxxx
```

Linux uniquement (x86_64 et ARM64), avec systemd.

## Documentation

- [Contrat agent ↔ Network](docs/protocole.md) : inscription, envoi des mesures, signature des requêtes, mises à jour.
- [Publication des versions](docs/publication.md) : créer une Release signée, gérer la clé de signature.

## Vérifier une version

Chaque Release contient `SHA256SUMS` et sa signature `SHA256SUMS.sig`. Pour les vérifier à la main :

```bash
scripts/verify-signature.sh SHA256SUMS SHA256SUMS.sig
sha256sum -c SHA256SUMS --ignore-missing
```
