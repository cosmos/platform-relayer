# DB Scripts

Helpful DB scripts when we manually need to work with the database.

## Login

To log into a database, we can use

```bash
ENVIRONMENT=dev DB_LEVEL=reader ./scripts/db/login.sh
```

Valid values for `ENVIRONMENT` are `local`, `dev`, and `prod`. By default it uses local.

Valid values for `DB_LEVEL` are `reader` and `writer`.

## Migrate

To migrate our databases manually (this will also be done via deployment automatically)

```bash
ENVIRONMENT=dev ./scripts/db/migrate.sh up
```

`DB_LEVEL=writer` is implied by migration in this context. You can use the full scope of go-migrate toolset.

```bash
ENVIRONMENT=dev ./scripts/db/migrate.sh --help
```
