# Mostro trading connector

## Purpose

The connector takes a Mostro **buy order** as the BTC seller. It supports the
hedgefull bid route over mainnet Lightning. Ask-side execution, hedgeless pool
refills, Cashu, proof-of-work nodes, and automated dispute resolution remain
unsupported and must not be enabled by changing an exchange switch alone.

Mostro execution is opt-in. Merely supplying a mnemonic does not enable trades.
The implementation has local protocol, coordinator, wallet, and PostgreSQL
regression tests; a funded trade against a deployed Mostro daemon has not been
performed as part of this change.

## Configuration

| Variable | Behavior |
| --- | --- |
| `MOSTRO_ENABLED` | Defaults to `false`; set to `true` to permit new takes. |
| `MOSTRO_ALLOWED_NODES` | Comma-separated hex Nostr public keys of trusted coordinators. Required for new takes. |
| `MOSTRO_RELAYS` | Relay URLs used for signed order discovery, protocol responses, and encrypted chat. |
| `MOSTRO_IDENTITY_LABEL` | Database identity label; defaults to `default`. |
| `MOSTRO_MNEMONIC` | BIP-39 mnemonic for initial identity creation. Use a dedicated, previously unused identity, exclusively managed by this application. |
| `MOSTRO_PRIVACY_MODE` | Omits reputation proofs and the trade index from outgoing messages. Defaults to `false`. |
| `APP_ENCRYPTION_KEY` | Shared application credential-encryption key; required to encrypt and recover the mnemonic. |

After initialization the encrypted mnemonic and derivation counter live in
PostgreSQL. Keep the database and encryption key together in the recovery plan.
A restart can load the stored mnemonic without `MOSTRO_MNEMONIC`, provided
Mostro is configured at startup. A changed mnemonic under an existing label is
rejected. Reusing an identity under another label is rejected once its public
key has been recorded. Importing a mnemonic used by another Mostro client does
not synchronize that client's historical trade indices.

Apply Unified API's forward migrations before starting the controller. LND must
support Router `SendPaymentV2` and `TrackPaymentV2`, and its macaroon must permit
invoice decoding and router payment operations. The application uses the
existing Lightning liquidity preparation before funding.

## Main flow

1. Snapshot ingestion verifies Nostr signatures and records the node public key,
   signed source-event ID, and event time. Both API offer shapes and the frontend
   preserve these values in `selected_offer`. An order event does not establish
   the transport version.
2. Before the CEX leg starts, `Taker.CheckMostro` checks configuration, the node
   allowlist, fresh signed node capabilities, the selected pending order,
   network, payment method, and selected fiat amount. The coordinator repeats
   these checks immediately before creating a new take session. A changed quote
   requires a new selection.
3. `Manager.Take` locks the transaction, allocates one trade key at
   `m/44'/1237'/38383'/0/index`, and commits the session. Index zero is the identity
   key. An unresolved take blocks allocating a newer key for the same identity.
4. The exact signed and encrypted request is committed to `mostro_outbox` before
   publication. A response subscription is written before publishing. Retries
   reuse the session, key, request ID, selected amount, and event. An expired
   ambiguous take is observed for responses, never replaced with a new take.
5. A bond and the trade escrow are separate payment records. The coordinator
   validates their decoded Lightning destination against signed node metadata,
   their amounts against separate approved ceilings, and their expiry. Their
   combined routing-fee limits cannot exceed the approved allowance. Invoice
   and payment hash are committed before LND receives a payment request.
6. LND is queried by payment hash before submission. `NOT_FOUND` permits an
   initial submission; an RPC failure does not. `IN_FLIGHT` means a payment is
   pending, not that Mostro accepted the escrow. Only an authenticated daemon
   acceptance followed by wallet reconciliation lets the normal trade flow
   advance. Generic synchronous `PayInvoice` is not used for Mostro.
7. Delayed peer-key disclosures are read by the chat adapter. Chats are scoped
   to node, order, and transaction. Separate ECDH/HKDF keys sign and encrypt the
   outer event; the inner message is signed by the sender's trade key. Only
   encrypted chat events are persisted. Identical outbound text is idempotent
   within a trade, including after a lost relay acknowledgement.
8. The fiat-controller confirmation is required before release. The release
   worker and the repository both enforce the fiat-received status. Release
   reuses the stored trade key and immutable outbox request. Completion requires
   an authenticated daemon settlement response, an LND `SUCCEEDED` escrow, and,
   when present, a canceled bond payment (`FAILED` in LND).

## Recovery and limits

All operations have bounded contexts and close their relay subscriptions.
Transport retries preserve the original request. A changed invoice, unexpected
payment settlement, canceled order, slashed bond, or dispute requires operator
reconciliation. The encrypted inbox records processed daemon events alongside
the session checkpoint. LND preimages are neither returned nor persisted.

This connector reconciles when the existing funding/release workers retry, and
when chat polling runs; it does not introduce an independent global outbox
scheduler. Funding timeouts can leave a real offer or HTLC active. Keep the
transaction and its session, inspect the daemon and wallet state, and resume
that transaction rather than creating a replacement trade. Sessions created by
the earlier incomplete implementation without recovery data are rejected for
manual recovery.

Turning off new takes does not revoke release authorization for a previously
funded session when its manager and wallet are still configured. Recovery also
requires the original relay configuration and encryption key. Do not delete
identity, session, payment, inbox, or outbox records to clear an error.

The accepted wire formats follow the current
[Mostro message types](https://github.com/MostroP2P/mostro-core/blob/main/src/message.rs),
[transport](https://github.com/MostroP2P/mostro-core/blob/main/src/transport.rs),
[key management](https://mostro.network/protocol/key_management.html), and
[peer chat protocol](https://mostro.network/protocol/chat.html).
