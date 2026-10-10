# Brainbird

## Build and run the API from the repository root

The API entrypoint is `cmd/api/main.go`, so Go hosts that build from the repository root can use the standard `go build ./cmd/api` command. The implementation and backend dependencies remain in `backend/`. The frontend files are embedded in the Go binary, so startup does not depend on the deployment working directory containing `frontend/ui`.

```sh
go build -o app ./cmd/api
./app
```

Configure `DATABASE_URL` and `JWT_SECRET` through the deployment platform's environment variables. Set the Vercel project Root Directory to the repository root; build with `go build -o app ./cmd/api` and start with `./app`. The API uses the platform's `PORT` when `HTTP_ADDR` is not set. For local development, it also loads `backend/.env` when started from the repository root. Keep `.env` files and database exports out of Git.

The backend can also be built and run from its module directory:

```sh
cd backend
go build ./cmd/api
go run ./cmd/api
```
