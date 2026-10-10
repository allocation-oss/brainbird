# Brainbird Backend

Go Fiber backend with a local MariaDB database adapter and optional Redis/Kafka adapters.

## Local setup

1. Start the local MariaDB service.
2. Obtain the SQL dump through an approved secure channel. The dump contains application data and is intentionally not included in this repository. Create the local database and load the authorized dump (adjust the path as needed):

	```powershell
	mysql -u root -e "CREATE DATABASE brainbir1_brainbird_local CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci"
	mysql -u root brainbir1_brainbird_local < C:\secure\brainbir1_brainbird.sql
	```

3. Copy `.env.example` to `.env` and adjust `DATABASE_URL` if your MariaDB user or password differs.
4. Rehash short legacy passwords from the imported copy once:

	```powershell
	go run ./cmd/migrate-imported-passwords
	```

5. Start the API with `go run ./cmd/api`.

The worker requires Kafka and is optional for local API development.

The API health endpoint is `GET /api/v1/health`.

The migration command only updates non-empty `users.password` values up to 13 characters to bcrypt hashes. Existing bcrypt hashes and empty passwords are left unchanged; the original SQL dump is not modified.
