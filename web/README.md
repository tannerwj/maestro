# Maestro web dashboard

The React and TypeScript dashboard is built with Vite and embedded into the Go binary by `make build`. The dashboard talks to Maestro's local API; see the [operator guide](../docs/operator-guide.md) for runtime behavior.

From this directory:

```bash
npm ci
npm run dev
npm run lint
npm run build
npm run test:smoke
```

The browser smoke test uses Maestro's local demo server by default. To verify the exact assets embedded in the Go binary, follow the [web verification steps](../TESTING.md#web-verification). No tracker or model credentials are needed.
