# Local PostgreSQL

The local database setup creates two PostgreSQL databases in one container:

- `mentat` is used by the API during local development.
- `mentat_test` is isolated for integration tests, which may truncate data.

The Docker CLI must be able to access the Docker daemon. On Linux, if Docker
reports a permission error for `/var/run/docker.sock`, add your account to the
`docker` group and start a new login session before continuing:

```sh
sudo usermod -aG docker "$USER"
```

Start PostgreSQL and apply all application migrations to both databases:

```sh
make db-up
```

Configure the current shell:

```sh
export MENTAT_DATABASE_URL='postgres://mentat:mentat@localhost:5432/mentat?sslmode=disable'
export MENTAT_TEST_DATABASE_URL='postgres://mentat:mentat@localhost:5432/mentat_test?sslmode=disable'
```

Run the Go tests with integration tests enabled (this also starts and migrates
PostgreSQL when needed):

```sh
make test-integration
```

Stop PostgreSQL without deleting its data:

```sh
make db-down
```

The `mentat` role and password are intentionally simple and suitable only for
local development. PostgreSQL data is kept in the Docker volume
`mentat_mentat-postgres-data`.
