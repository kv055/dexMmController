# MM Controller API documentation

This directory contains two complementary OpenAPI documents:

- `openapi.yaml` is the generated catalog of every registered HTTP route. It is
  used by the project Swagger UI and intentionally does not claim precise body
  schemas for operations that have not been formalized yet.
- `execution.openapi.yaml` is the strongly typed source contract for the MM
  execution operations consumed by the monitor frontend. The controller serves
  these operations under `/api`; the monitor exposes them to the browser under
  `/api/mm-controller`.

From the parent `dexArbTrading` checkout, regenerate the TypeScript contract with:

```bash
cd dexMonitor/react-frontend
npm run generate:api
```

The generated file is committed at
`dexMonitor/react-frontend/src/generated/mm-controller-api.ts`. CI regenerates it
and fails when the committed output is stale. Changes to this specification also
trigger the frontend job, even when the monitor submodule itself did not change.

Regenerate the full route catalog from the repository root with:

```bash
python3 scripts/openapi-route-catalogs.py
```

The `GET /api/transactions/dex-pending-liquidity` route reports the sum of
`btc_required_liquidity` and count of non-deleted transactions whose status is
`DexPositionTaken` or `DexFiatHasArrived`. The monitor publishes this controller
route as `GET /api/mm-controller/transactions/dex-pending-liquidity`.

Only transport schemas belong in the typed contract. Domain models and UI-only
view models remain handwritten in their owning packages.
