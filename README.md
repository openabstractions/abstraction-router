# abstraction-router

See which models this computer can serve, where they are already loaded and
which allowed host best fits one request. The router reports the choice and its
cost or loading consequence. The inference service performs the model call and
keeps provider addresses and credentials away from the application.

    what models exist here, under every name they are known by
    which host currently holds which, and what that costs
    given a request, which host should serve it

Applications resolve `abstraction.router/router@1` through the facade. `Models`,
`Hosts` and `Pick` return bounded typed results. Inventory and route selection
have separate rights. A `would-load` verdict names a candidate host; the router
does not load, unload or evict a model.

Measured on the machine it was written for: consolidating three models into one
host saved 0.30 GB of 42.87 and turned one failed load from a 33% loss into a
100% one, while *not loading the same model twice* saved 22.52 GB with every
host left where it was. The saving is routing, so this is a router.

## Framed service clients (development)

The new Go and C++ clients expose `Models`, `Hosts` and `Pick` through generated
[Thrift bindings](router.thrift) and the shared IPC runtime. They contain no
model-host discovery implementation, local store or fallback provider.

Start the separate framed host from a locally built executable:

```sh
openabstractions serve router-v1
```

`ABSTRACTION_ROUTER_ENDPOINT` overrides its endpoint for clients and host; the
host also accepts `--endpoint`. The default is the shared `router-v1` endpoint
convention, distinct from the legacy `router` endpoint. This foreground command
does not register an OS service. The provider remains in the user's session.

```go
import router "github.com/openabstractions/abstraction-router/go/client"

client := router.Discover()
models, err := client.Models(false)
// Handle err before using models. Discovery does not start a provider.
result, err := client.Pick(router.PickRequest{Model: "qwen2.5:0.5b"})
```

For C++17, install the development CMake packages and use
`find_package(abstraction_router CONFIG REQUIRED)` with
`abstraction::router_client`. See [the C++ example](cpp/README.md).

`Pick` preserves the existing verdict strings. A routing refusal such as
`unauthorised` is a decision, not a transport failure. An absent `allowed`
structure permits every servable host; a present structure with `hosts: []`
permits none. Responses preserve that distinction. Caller attribution is
observed by the host through identity; requests contain no caller claims.

This subset includes model aliases, host residency, failed-host diagnostics,
cache timing and the routing audit. **GPU-cost fields and the read-only HTTP
window remain legacy surfaces.** The old `router` host/CLI continues to use its
original endpoint. This is not a claim of full router conformance, all-platform
validation or a published release. The new service reuses the existing provider
and therefore still never loads or unloads a model.

## Hosted hosts

A hosted host is a provider endpoint off this machine, such as OpenRouter or a
LiteLLM instance on the LAN. Its entry names a base URL, a wire kind
(`openai-compatible`, `anthropic-messages`, or `<owner>/<name>@<n>` from the
open `wire_kinds` catalogue) and a credential name. The service reads its model
listing with the credential applied through `abstraction.credentials/applier@1`,
as consumer `abstraction.router/router@1`, once per listing request.

- `Hosts` reports `hosted: true`, the wire and the credential name. No snapshot
  carries a header value.
- A listing the applier refuses reads `up: false` with
  `why: credential:<outcome>:<name>`, for example `credential:unknown:openrouter`.
- `Models` marks hosted aliases `hosted: true`, folded into families beside
  local names.
- `Pick` answers `hosted` after every `resident` and `would-load` host on this
  machine, unless the allowance names only hosted hosts. The decision's
  `endpoint` is empty: `abstraction-inference` performs the call, and no
  endpoint or key reaches the caller.

```go
r := router.New(append(router.Installed(),
	router.NewHosted("openrouter", "https://openrouter.ai/api/v1", router.WireOpenAICompatible, "openrouter"))...)
r.UseCredentials(apply) // the runtime's in-process applier
```

## Install

    go build -o bin/ ./router/go/cmd/routerd ./router/go/cmd/router

Nothing is installed, no service is registered, and the binaries are unsigned.

## Legacy CLI

    routerd                                  # listens on a pipe, no port
    router models
    router residency
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

## Authorising a host

A routing decision may name a host the caller did not expect. `hosts` is the set
it will accept.

    router -hosts lemonade,lmstudio route qwen2.5:0.5b

    "decision": {
      "verdict": "unauthorised",
      "withheld": ["ollama"],
      "authorised": ["lemonade", "lmstudio"],
      "why": "[ollama] can serve it and the request authorised [lemonade lmstudio]; …"
    }

**The key absent authorises every servable host. `[]` authorises none and always
refuses.** A language that folds an empty list into a missing one turns a hard
requirement into a preference nothing observes, which is worse than a refusal
because nothing is on the record. `unauthorised` names no endpoint, and a caller
that only ever sends where a verdict points therefore sends nowhere; the audit
under `residency` carries the refusal beside the decisions that were served.

Order is the caller's preference and the router keeps it, except that a host
already holding the model beats one that would load it however the request is
ordered. A verdict always names the host it chose, so a caller served by its
second choice can tell.

    go test -run TestOneRequestRoutedThroughThreeOutcomes ./router/go -v

with `ROUTER_LIVE=1` and `ROUTER_LIVE_MODEL` set to a model one of your hosts
holds takes one request through all three: refused, fallback, resident. It skips
and names the hosts that were missing if none is running.

## Who may ask

The interface is a named pipe on Windows and an `AF_UNIX` socket elsewhere, and
every caller is bound to a process the kernel vouches for
([`abstraction-identity`](https://github.com/openabstractions/abstraction-identity/blob/main/CONTRACT.md)). The frame is read before
the caller is known — Windows will not identify the client of a pipe nothing has
been read from — and it stays bytes until the binding succeeds. A caller that
cannot be bound is refused before its frame is parsed as anything.

`routerd -window 127.0.0.1:11801` opens a second surface for things that cannot
speak the pipe. It is off by default, it is **read-only**, and every answer over
it says `"bound": false`. It answers `models` and `residency`; it cannot ask for
a routing decision, because a decision is recorded against the program that made
it and a loopback socket names no program.

## Names

Resolution is [`abstraction-model`](https://github.com/openabstractions/abstraction-model)'s, not ours. Four hosts
name one model four ways with no overlap — Lemonade's `id` and its `checkpoint`,
LM Studio's `id`, Ollama's `name:tag`, ComfyUI's bare filenames — and every one
of them is reported under the family it belongs to.

## What may break

- **ComfyUI is listed and never routed to.** What it runs is a graph, not a
  model, so no OpenAI-compatible endpoint can serve it; asking for one of its
  weights returns `unservable` with that reason. It is enumerated because a
  quarter of this machine's bytes are in its tree.
- **A cached answer is milliseconds; a fresh one is not.** `fresh: true` reads
  all four hosts and ComfyUI's per-folder enumeration dominates it. Every answer
  carries `cache_age_ms`, so a caller can decide for itself.
- **GPU cost is per process, not per model.** Lemonade spawns one `llama-server`
  child per model, so no host's own number is the model's. On Windows the
  performance counters are the only instrument that sees the whole device; where
  they cannot be read the answer says so rather than reporting zero.
- **Two builds of one family on one host** — llama.cpp, an MTP variant, an NPU
  recipe — are one family and several files. A build the host already holds
  wins; otherwise the host's own order decides.

## Refusals

Your application can tell why a request was refused without depending on the
wording of its error message. A newer server's unfamiliar refusal still counts
as a failure.

Service refusals carry an optional stable `code` alongside the existing diagnostic
`error` text. Either nonempty field means refusal. Old text-only replies remain
valid; unknown codes remain refusals and must not be treated as success. Servers
continue sending diagnostic text for older clients. Go clients return
`*RemoteError`, retaining the code and message; `Response.Err()` applies the same
rule to a decoded reply. The [code constants](go/errors.go) define the vocabulary. Diagnostic wording is not an API.

Caller `user_description` and `path_description` contain server-observed diagnostic
text, including proof decorations. They are not stable principal identifiers,
filesystem paths, or authorization evidence. The audit entries likewise contain
diagnostic caller descriptions.
