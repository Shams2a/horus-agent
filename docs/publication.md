# Publication des versions

## Publier une version

1. Vérifier que `main` est vert (workflow CI).
2. Créer et pousser un tag semver :

   ```bash
   git tag v1.0.0
   git push origin v1.0.0
   ```

3. Le workflow « Release » :
   - contrôle que le secret de signature correspond à la clé publique intégrée (`internal/release/signing_key.go`), et refuse de publier sinon ;
   - compile `horus-agent` pour Linux x86_64 et ARM64 ;
   - publie sur GitHub les archives `horus-agent_<version>_linux_<arch>.tar.gz`, le fichier `SHA256SUMS` et sa signature `SHA256SUMS.sig` ;
   - revérifie la signature publiée.

Un tag avec suffixe (`v1.1.0-rc.1`) publie une préversion. Les agents ne s'y mettent à jour que si on le leur demande explicitement.

Tester la chaîne sans rien publier :

```bash
goreleaser release --snapshot --clean
```

## La clé de signature

- Algorithme : Ed25519. La signature porte sur `SHA256SUMS` : 64 octets encodés en base64 standard.
- La **clé publique** est dans `internal/release/signing_key.go` et donc intégrée à chaque binaire. C'est elle que l'agent utilise pour accepter ou refuser une mise à jour.
- La **clé privée** n'existe qu'à deux endroits :
  - le secret `HORUS_AGENT_SIGNING_KEY` du dépôt GitHub (contenu PEM complet) ;
  - la sauvegarde hors ligne de Shams (gestionnaire de mots de passe).

  Elle ne doit jamais être commitée (`*.pem` est dans `.gitignore`).

### Changer de clé

Les agents déjà installés ne connaissent que l'ancienne clé publique. Pour changer de clé sans les perdre :

1. Publier une version de transition, **signée avec l'ancienne clé**, qui accepte les deux clés publiques.
2. Laisser les agents passer à cette version.
3. Remplacer le secret, puis publier les versions suivantes avec la nouvelle clé uniquement.

En cas de fuite de la clé privée, la même procédure s'applique en urgence, et la version de transition retire l'ancienne clé dès que possible.
