# MM Controller API documentation

This directory owns two handwritten OpenAPI 3.1 source contracts:

- `public.openapi.yaml` describes webhook-facing operations that carry their own
  credentials.
- `internal.openapi.yaml` describes health, administration, and
  service-to-service operations. It includes the typed CEX execution schemas
  that previously lived in a separate execution contract.

The generated strict routers under `internal/api/gen/` own normal JSON route
registration and request binding. The hedge-stream WebSocket remains a
handwritten raw transport and is marked accordingly in the internal contract.

From the parent `dexPendulum` checkout, regenerate all API artifacts with:

```bash
make api-generate
make api-check
```

The self-contained MM bundles are committed under
`api-docs/specs/dex-mm-controller/`, and typed Go clients are committed under
`commons/api/client/`. The monitor browser contract describes its
`/api/mm-controller` proxy surface and reuses the MM execution schemas; its
generated types live in
`dexMonitor/react-frontend/src/generated/monitor-api.ts`.

The `GET /api/transactions/dex-pending-liquidity` route reports the sum of
`btc_required_liquidity` and count of non-deleted transactions whose status is
`DexPositionTaken` or `DexFiatHasArrived`. The monitor publishes this controller
route as `GET /api/mm-controller/transactions/dex-pending-liquidity`.

Only transport schemas belong in these contracts. Domain models and UI-only
view models remain handwritten in their owning packages.
