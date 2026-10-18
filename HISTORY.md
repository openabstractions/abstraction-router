# abstraction.router history

Prior art, provenance, retired ids and what was measured or tested for
[CONTRACT.md](CONTRACT.md), kept out of the contract itself per
`research/vocabulary/DECISION.md` S7.

## Why the shape changed, 2026-09-23

The page carried plain `ROUTE-1`..`ROUTE-24` ids with no letter grouping and
no `Binds:` line. `research/vocabulary/RENAME-PLAN.md` "router (12 steps)"
converted it to the `[ROUTE-A…]` / `[ROUTE-D…]` / `[ROUTE-N…]` / `[ROUTE-H…]`
/ `[ROUTE-S…]` / `[ROUTE-E…]` / `[ROUTE-U…]` id families, added the RFC 8174
boilerplate, the letter and prose-to-wire tables, gathered the Bounds and
Divergences sections that were previously scattered through the prose, and
applied `research/reviews/router-2026-09-23.md`'s ten findings (F1–F10) in
the same pass. The old numeric ids are retired; none is reused.

## Retired: the legacy JSON-over-socket protocol

Before this pass, CONTRACT.md and README.md both still described `routerd`
and the `router` CLI as a second, parallel protocol, even though the
installed runtime has never served it. `router.thrift` defines only
`PickRequest{model, fresh, allowed: HostAllowance{hosts}, profile}` and
three operations, `Models`, `Hosts`, `Pick`; its own header already says
"The legacy HTTP window is not part of it either." Four rules described
fields and operations that exist only in `go/wire.go` and the retired
binaries: the `hosts` request field, the `residency` operation, and
`holders_unreadable` (router review F1).

The retired protocol, kept buildable for manual, offline inspection only:

    go build -o bin/ ./router/go/cmd/routerd ./router/go/cmd/router
    routerd                                  # listens on a pipe, no port
    router route qwen/qwen3.6-35b-a3b

    {
      "caller": {"bound": true, "user": "…", "path": "…\\router.exe"},
      "took_ms": 2,
      "cache_age_ms": 812,
      "decision": {
        "asked": "qwen/qwen3.6-35b-a3b",
        "family": "qwen3.6-35b-a3b",
        "verdict": "resident",
        "host": "lemonade",
        "endpoint": "http://127.0.0.1:13305/api/v1/chat/completions",
        "loads": 0,
        "why": "lemonade already holds it; routing anywhere else loads a second copy"
      }
    }

Its authorising-host example, with the retired `-hosts` flag:

    router -hosts lemonade,lmstudio route qwen2.5:0.5b

    "decision": {
      "verdict": "unauthorised",
      "withheld": ["ollama"],
      "authorised": ["lemonade", "lmstudio"],
      "why": "[ollama] can serve it and the request authorised [lemonade lmstudio]; …"
    }

A second, read-only surface, `routerd -window 127.0.0.1:11801`, could be
opened over TCP for a caller that could not speak the pipe: off unless a
listen address was given, answering only `models` and `residency`, every
answer saying `"bound": false`, and never accepting a routing decision,
because a decision is recorded against the program that made it and a
loopback socket names no program.

The retired `residency` operation answered with `holders`, one row per
program the legacy protocol's own instrument saw, and `holders_unreadable`,
carrying a reason string in place of an invented empty card when no
residency source was wired — the same "say why instead of inventing an
empty answer" promise `ROUTE-D6` now states for `router@1`'s own
`ServerState.why`, without a `holders_unreadable` field of its own: the
framed protocol never had one.

Nothing here is installed by the runtime, no service is registered for it,
and the binaries are unsigned. This account replaces the "legacy protocol"
material CONTRACT.md's old ROUTE-12, ROUTE-13, ROUTE-20, ROUTE-22 and
ROUTE-23, and README.md's "The legacy protocol (retired)" section, used to
carry.

## Superseded ids

| old id | disposition | reason |
| --- | --- | --- |
| ROUTE-20 | deleted, not reused | described the retired TCP window; moved above (router review F1) |
| ROUTE-22, ROUTE-23 | merged into `ROUTE-D6` | both described residency; the field they named, `holders`, and the operation, `residency`, exist only in the retired protocol (router review F1) |
| ROUTE-17 | merged into `ROUTE-D2` (ranking) and `ROUTE-D5` (no `endpoint`, `Decision.server`) | duplicated ROUTE-4's ranking sentence; its `endpoint` sentence is now `ROUTE-D5`'s own rule (router review F6, F7) |
| ROUTE-2 | renumbered `ROUTE-A2`, actions and resource corrected | named neither action, and resources shaped like no other contract (router review F9) |
| ROUTE-7, ROUTE-8 | renumbered `ROUTE-N2`, `ROUTE-N3`, rewritten | the tail-after-a-slash match reached the wrong object for two publishers of one filename (router review F5) |
| ROUTE-21 | renumbered `ROUTE-E2`; `policy_unavailable` → `unavailable` on the wire | the contract's code list, `go/errors.go`'s and `router.thrift`'s disagreed; the fourth of six was spelled nowhere else in this project (router review F8) |
| ROUTE-1, ROUTE-3–6, ROUTE-9–16, ROUTE-18, ROUTE-19, ROUTE-24 | renumbered only, content otherwise carried forward with word renames | see the mapping below |

Every other surviving id kept its content, renamed to its new letter. Each
old id is retired, one per row, with the id that carries its content now:

| retired | now | rule |
| --- | --- | --- |
| `ROUTE-1` | ROUTE-D1 | the router never loads, unloads or evicts a model |
| `ROUTE-2` | ROUTE-A2 | reading and choosing carry separate rights |
| `ROUTE-3` | ROUTE-E1 | `Pick` answers with one outcome word |
| `ROUTE-4` | ROUTE-D2 | a resident server outranks one that would load |
| `ROUTE-5` | ROUTE-D3 | two files of one family on one server are one family |
| `ROUTE-6` | ROUTE-N1 | a server model's family is the storage inventory's own |
| `ROUTE-7` | ROUTE-N2 | a server's alias reaches a store's own name by exact match |
| `ROUTE-8` | ROUTE-N3 | a key two held objects would both reach |
| `ROUTE-9` | ROUTE-N4 | a component is never a family of its own |
| `ROUTE-10` | ROUTE-H1 | `held_in` names the stores holding an object |
| `ROUTE-11` | ROUTE-H2 | a model held but served by nothing is still reported |
| `ROUTE-12` | ROUTE-D4 | `PickRequest.allowed` bounds which servers may answer |
| `ROUTE-13` | ROUTE-D5 | a decision names the server that serves it |
| `ROUTE-14` | ROUTE-S1 | a hosted server is a provider endpoint off this machine |
| `ROUTE-15` | ROUTE-S2 | a hosted server's listing is read with its credential |
| `ROUTE-16` | ROUTE-S3 | a listing the applier refuses reads `up: false` |
| `ROUTE-17` | ROUTE-D2; ROUTE-D5 | merged: ranking; no `endpoint` |
| `ROUTE-18` | ROUTE-S4 | a remote runtime is a hosted server |
| `ROUTE-19` | ROUTE-A1 | every caller is bound before its frame is parsed |
| `ROUTE-20` | none | the retired TCP window |
| `ROUTE-21` | ROUTE-E2 | protocol refusals carry a stable code |
| `ROUTE-22` | ROUTE-D6 | merged: residency |
| `ROUTE-23` | ROUTE-D6 | merged: residency |
| `ROUTE-24` | ROUTE-U1 | every routing decision is recorded in an audit |

## Added ids

- 2026-10-01: `ROUTE-D7` added; a family had listed a name two servers serve once, with the first server's host, the smaller context and either server's residency (UX1 follow-up 1).
- 2026-10-09: `ROUTE-E3` gives the unpublished `Offer` method one typed outcome and keeps verified policy refusal `not_permitted` distinct from boundary refusal `forbidden` (vocabulary D86). Clients, remote folding, Panel and OpenCode check the outcome before consuming offers. Published `Models`, `Hosts` and `Pick` shapes retain their existing channel. `AvailabilityCause` adds `other` for an established cause outside the named vocabulary; `unknown` keeps its unestablished-cause meaning.
- 2026-10-01: `ROUTE-D8` added with the `Offer` method, and `ROUTE-S4` extended to remote offers and remote alias contexts. OpenCode's plugin (`adopters/opencode/inventory.js`) and `openabstractions opencode configure` (`serve/opencode.go`) each carried a copy of the family-context fold, held together by a shared case table; remote families reached both with zero lengths and a fabricated 32,768 default. The fold now has one owner, and both consumers keep only their OpenCode projection (`research/opencode-inventory/DECISION.md`).
- 2026-10-01: `ROUTE-D9` added; `ROUTE-D8` cites it for which profile an alias serves. A native declaration's models served every profile it listed, so once the Lemonade declaration named `Whisper-Large-v3-Turbo` and `kokoro-v1` beside its picture model, an `image@1` request naming `kokoro-v1` passed the router and the provider's admission and reached Lemonade's image route. The router also answered no transcription or speech address for a native host, and inference routed native audio around it (`feedback/speech-provider-2026-10-01.md` follow-ups 1 and 2). Declarations now scope an entry as `<profile>:<model>`, following the registry's `PROFILE:OWNER/NAME` spelling of a supported extension; an entry without a declared profile keeps its old reading. The same day's review (of 48d44c5b7) found a remote with one profiled and one unprofiled alias reading `no_host` for the unprofiled alias's chat, because a reported profile on one name hid the remote's declared profiles from another; ROUTE-D9 now names who reports an alias's profiles and who falls back, and judges every alias by name. Entries are read against the served profiles, `chat` of a chat-only declaration included, by one parser (`DeclaredEntry`).

## Superseded: the error code, router review F8

`router.thrift`'s `router_error_codes` listed `policy_unavailable`, spelled
nowhere else in the project's outcome vocabulary; `go/errors.go`'s public
constant list carried neither `policy_unavailable` nor `forbidden`. Three
different lists of the same six codes disagreed. `router.thrift` now
declares `unavailable` in `policy_unavailable`'s place; `scripts/generate.sh
--schema router.thrift` regenerated the Go, Python, C++, Rust and JavaScript
bindings and the schema page from it, and `go/service/service.go` now
answers a policy lookup it could not complete with `CodeUnavailable` and the
message `rights:unavailable`, the same reason family `INF-S2` and `REG-5`
already use. `go/client/contract.go`'s hand-maintained alias was renamed to
match. `ErrPolicyUnavailable`, the Go sentinel a `Policy` implementation
wraps to report a failed lookup, keeps its name: it names what went wrong on
the policy side, not the wire code it now produces.

## Prior art

`Pick`/`PickRequest`/`PickResult` follows the gRPC balancer's
`Picker.Pick(info PickInfo) (PickResult, error)`, which likewise returns a
chosen target and never an address. `resident`, `up` and `servable` follow
Kubernetes EndpointSlice's `serving`/`ready` distinction. `domain` follows
Consul's `Datacenter`. `hosted` follows Kubernetes `ExternalName` and Istio
`MESH_EXTERNAL`: an entry that names an endpoint the cluster (here, the
machine) does not run. `server` as the routable member follows every model
runtime this router adapts to — Ollama, LM Studio, llama.cpp and vLLM each
call themselves a server and reserve "host" for an address
(`research/vocabulary/DECISION.md` D11); Envoy's "upstream host" is the one
dissent, recorded there. Full citations and dates:
`research/reviews/router-2026-09-23.md` "Names against the platforms" and
"Checks run".

`Offer` (ROUTE-D8) folds per-server context the way OpenRouter's
`/api/v1/models` folds endpoint windows into one model-level
`context_length` while `/models/{id}/endpoints` keeps each endpoint's
figure: a family answers one conservative number and its aliases keep
their own. OpenAI's `GET /v1/models` carries no window, which is why the
gateway's OpenAI-shaped listing stays name-only. LM Studio's
`/api/v0/models` lists `max_context_length`, a maximum; Ollama separates
`/api/show`'s `model_info` maximum from `/api/ps`'s loaded `context_length`;
Lemonade reports `max_context_window`, the loaded recipe's `ctx_size` and
`resolved_ctx_size` for the next load, and fixes the window at load.
LiteLLM's `model_prices_and_context_window.json` is a table shipped with the
client, which drifts from the server in front of it; an offer is an
observation with an age instead. OpenCode's provider `limit.output` is the
application's answer reserve, which is why `reserve` travels in the
request. Sources and dates: `research/opencode-inventory/DECISION.md` §3.

## Measured

Consolidating three models onto one server saved 0.30 GB of 42.87 GB on the
machine this contract was written for, and turned one failed load from a
33% loss into a 100% one; not loading the same model twice saved 22.52 GB
with every server left where it was. The saving is routing. This is a
router.

`ROUTE-N2`'s exact-match rewrite (router review F5) was checked by counting,
on the owner's machine, the keys the old tail match refused in the composed
inventory and the families whose `family_source` fell to `alias` because of
one; a nonzero count on a store with two publishers of one model showed the
defect the rewrite closes.

## Tested

`go test -run TestOneRequestRoutedThroughThreeOutcomes ./router/go -v`, with
`ROUTER_LIVE=1` and `ROUTER_LIVE_MODEL` set to a model one of the owner's
servers holds, takes one request through all three routing outcomes:
refused, fallback, resident. It skips and names the servers that were
missing if none is running. `go -C go test ./... -count=1` covers the
generated protocol, the service and the client; see
[CONTRACT.md](CONTRACT.md) "Not built" for what conformance does not yet
cover.

---

Back to [CONTRACT.md](CONTRACT.md).
