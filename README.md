# router — which host should serve this model

Three questions about the model runtimes already on a machine, and nothing else.

    what models exist here, under every name they are known by
    which host currently holds which, and what that costs
    given a request, which host should serve it

It hosts no model, ships no chat client, and renders no interface. It never
loads, unloads or evicts: a verdict of `would-load` names the host to ask and
leaves the asking to the caller.

Measured on the machine it was written for: consolidating three models into one
host saved 0.30 GB of 42.87 and turned one failed load from a 33% loss into a
100% one, while *not loading the same model twice* saved 22.52 GB with every
host left where it was. The saving is routing, so this is a router.

## Install

    go build -o bin/ ./router/go/cmd/routerd ./router/go/cmd/router

Nothing is installed, no service is registered, and the binaries are unsigned.

## Run it

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
and names the hosts that were missing if none is running
([transcript](../research/route58/MEASURED.txt)).

## Who may ask

The interface is a named pipe on Windows and an `AF_UNIX` socket elsewhere, and
every caller is bound to a process the kernel vouches for
([`abstraction-identity`](../identity/CONTRACT.md)). The frame is read before
the caller is known — Windows will not identify the client of a pipe nothing has
been read from — and it stays bytes until the binding succeeds. A caller that
cannot be bound is refused before its frame is parsed as anything.

`routerd -window 127.0.0.1:11801` opens a second surface for things that cannot
speak the pipe. It is off by default, it is **read-only**, and every answer over
it says `"bound": false`. It answers `models` and `residency`; it cannot ask for
a routing decision, because a decision is recorded against the program that made
it and a loopback socket names no program.

## Names

Resolution is [`abstraction-model`](../model/README.md)'s, not ours. Four hosts
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
