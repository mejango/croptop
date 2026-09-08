# Phase 2 Engine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Embed an IPFS node (boxo) in croptop behind an `Engine` interface, with CID and IPNS parity to kubo, selectable with `--engine`.

**Architecture:** `internal/ipfs` keeps the kubo `*Node` and adds `*Embedded`; both satisfy `Engine`. `publish`, `server`, and `main` depend on `Engine` only. The keystore directory is shared.

**Tech Stack:** boxo v0.42.2, go-libp2p v0.49.0, go-libp2p-kad-dht v0.42.1, go-ds-flatfs, go-ds-leveldb, go-doh-resolver.

**Spec:** `docs/superpowers/specs/2026-09-08-phase2-engine-design.md`

## Global Constraints
- CIDs must equal kubo's: CIDv0 hash-only (chunk 262144, balanced, 174 links, protobuf leaves, V0 builder); CIDv1 directory adds (raw leaves, V1 dag-pb, HAMT at 256 KiB).
- IPNS: lifetime 7200h, TTL 1m, explicit sequence, PutValue under the name's routing key.
- Default engine stays `kubo` until the FOLLO acceptance run passes.
- Every task ends with `go vet ./... && go test ./...` green and a commit.

---

### Task 1: Engine interface
**Files:** create `internal/ipfs/engine.go`; modify `internal/ipfs/daemon.go` (add `Provide`, `LastError`), `internal/publish/publish.go` (`Node Engine`), `internal/server/server.go` (`Node Engine`), `cmd/croptop/main.go`.
- [ ] Define `Engine` with the methods in the spec. `*Node` satisfies it (`Provide` = `pin add`, `LastError` = `LastStderr`).
- [ ] Change `Publisher.Node`, `Server.Node`, and the app struct to `ipfs.Engine`. Build and tests pass unchanged.
- [ ] Commit `ipfs: Engine interface`.

### Task 2: Embedded node skeleton
**Files:** create `internal/ipfs/embedded.go`.
- [ ] `NewEmbedded(dataDir string) *Embedded`; `Start`: identity key at `node/identity.key` (create if missing), flatfs blocks + leveldb datastore, libp2p host on a free port, DHT with the ipns validator, bootstrap, bitswap, blockservice, dagservice; connect Planet's peering list and local kubo nodes. `Stop` closes in reverse. `Info` from host and DHT.
- [ ] Test: `TestEmbeddedStartsOffline` starts with no bootstrap peers on 127.0.0.1 and stops cleanly.
- [ ] Commit.

### Task 3: unixfs add and get with kubo parity
**Files:** create `internal/ipfs/embedded_unixfs.go`, `internal/ipfs/embedded_test.go`.
- [ ] `FileCIDv0` (hash-only DAG service that discards blocks), `AddDir` (recursive, hidden files included, HAMT threshold), `Get` (session + unixfile + files.WriteTo).
- [ ] Tests: `FileCIDv0` of `internal/render/testdata/public-post/nft.json` equals `nft.json.cid.txt`; `AddDir` of a generated tree (small and >256 KiB files, nested dirs, a hidden file) equals kubo's CID when `.data/kubo/ipfs` exists; add then get round-trips bytes.
- [ ] Commit.

### Task 4: IPNS and resolve
**Files:** create `internal/ipfs/embedded_ipns.go`; extend tests.
- [ ] `NamePublish` builds, signs, and puts the record; `Provide` the CID. `NetworkRecord` via `SearchValue` keeping the highest sequence. `Resolve` handles `.eth` via DoH DNSLink and IPNS names via `NetworkRecord`.
- [ ] Tests: record round trip offline; DNSLink TXT parsing.
- [ ] Commit.

### Task 5: Wiring
**Files:** modify `cmd/croptop/main.go`, `internal/config/config.go`, README.
- [ ] `--engine kubo|embedded` flag and `config.Engine`; `croptop engine` prints the active one; `ipfs-smoke` works for both.
- [ ] Commit.

### Task 6: Acceptance
- [ ] `./croptop ipfs-smoke --engine embedded --data .data-e k51…FOLLO` returns the live record.
- [ ] `./croptop adopt <FOLLO> --key follo.pem --engine embedded --data .data-e` succeeds.
- [ ] `./croptop publish FOLLO --engine embedded --data .data-e` publishes; the kubo console and `https://follo.eth.sucks/planet.json` show the new CID.
- [ ] Docker Linux run with `--engine embedded`.
- [ ] Flip the default to `embedded`, tag `v0.3.0`.
