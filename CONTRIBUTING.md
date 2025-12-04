# Getting Started

Backend:
- go
- linter: golangci-lint

Frontend:
- react [svelte tbd]

### Backend

Ensure that you have Go installed. You can modify the backend in [internal](./internal/). Go automatically installs dependencies.

To run the Go server, you need to build the frontend first. You can do this by `cd`ing to the frontend in [web](./web/) and running `npm run build`.

Then, at the root of the project (`cd ..` if you're in web) run `go run .` to run the backend server. You should see the following logs:

```console
2025/12/04 16:06:23 Loading `.env.production`
2025/12/04 16:06:23 Running HTTP Server at `:8080`
```

You can view the page at: `http://localhost:8080`.

### Frontend

Set up `npm` if necessary. `cd` to `web` then `npm i`.

Once the dependencies are installed, `npm start` to serve the frontend. You should see the following:

```console
Compiled successfully!

You can now view EggsFM in the browser.

  Local:            http://localhost:3000
  On Your Network:  http://192.168.1.57:3000

Note that the development build is not optimized.
To create a production build, use npm run build.

webpack compiled successfully
```
