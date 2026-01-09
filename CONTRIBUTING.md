# Getting Started

Backend:
- go
- linter: golangci-lint

Frontend:
- svelte
- linter: prettier (via `npm run format`)

### Backend

Ensure that you have Go installed. You can modify the backend in [internal](./internal/). Go automatically installs dependencies.

At the root of the project (`cd ..` if you're in web) run `go run .` to run the backend server. You should see the following logs:

```console
2025/12/04 16:06:23 Loading `.env.production`
2025/12/04 16:06:23 Running HTTP Server at `:8080`
```

You can view the page at: `http://localhost:8080`.

### Frontend

Set up `npm` if necessary. `cd` to `web` then `npm i`.

Once the dependencies are installed, `npm run dev` to serve the frontend. You should see the following:

```console
> web@0.0.1 dev
> vite dev


  VITE v7.3.1  ready in 727 ms

  ➜  Local:   http://localhost:5173/
  ➜  Network: use --host to expose
  ➜  press h + enter to show help
```
