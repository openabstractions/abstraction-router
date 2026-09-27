# abstraction-router

See which models this computer can serve, where they are already loaded and
which allowed server best fits one request. The router reports the choice
and its cost or loading consequence. The inference service performs the
model call and keeps provider addresses and credentials away from the
application.

    what models exist here, under every name they are known by
    which server currently holds which, and what that costs
    given a request, which server should serve it

Applications resolve `abstraction.router/router@1` through the facade.
`Models`, `Hosts` and `Pick` return bounded typed results. Inventory and
route selection have separate rights: `Models` and `Hosts` decide
`abstraction.router/inventory.read`, and `Pick` decides
`abstraction.router/route.pick`; both apply to resource `account`
(`abstraction.router/inventory` and `abstraction.router/routes` today —
[CONTRACT.md](CONTRACT.md)'s prose-to-wire table names the release).
`rights grant --for inference` grants both, alongside the per-server
`complete` rule. A `would_load` outcome names a candidate server; the
router does not load, unload or evict a model.

The installed runtime serves this contract as `router-v1`, the framed
Thrift protocol in the next section, and `facade.ResolveRouter` reaches it
there. The legacy JSON-over-socket protocol — `routerd` and the `router`
CLI — is retired: the installed runtime does not run it, and its binaries
remain buildable only for manual, offline inspection
([HISTORY.md](HISTORY.md) carries its full account).

Measured on the machine it was written for: consolidating three models
into one server saved 0.30 GB of 42.87 and turned one failed load from a
33% loss into a 100% one, while *not loading the same model twice* saved
22.52 GB with every server left where it was. The saving is routing. This
is a router.

## The router-v1 protocol

The Go and C++ clients expose `Models`, `Hosts` and `Pick` through
generated [Thrift bindings](router.thrift) and the shared IPC runtime;
this is the protocol the installed runtime serves for
`abstraction.router/router@1`. They contain no model-server discovery
implementation, local store or fallback provider. Conformance is proven
for the Go and JavaScript clients only; the C++, Python and Rust clients
are unproven ([CONTRACT.md](CONTRACT.md) "Not built").

For development against a manually started runtime instead of an
installed one, start the standalone framed host from a locally built
executable:

```sh
openabstractions serve router-v1
```

`ABSTRACTION_ROUTER_ENDPOINT` overrides its listen address for clients and
host; the host also accepts `--endpoint`. The default is the shared
`router-v1` listen address convention, distinct from the retired `router`
one. This foreground command does not register an OS service. The
provider remains in the user's session.

`go/wire.go`'s `Alias`, `Family`, `HostState`, `Ask` and `Decision` keep
their own types and `json` tags for the legacy JSON-over-socket protocol;
each has one named `toWire<Type>` function in `go/service/service.go` that
is the one place carrying its fields onto its generated
`go/abstraction/router` counterpart, and `go/service/convert_test.go`
checks every pair's field sets by reflection, through one shared filler,
so a field added to either side without its conversion updated fails a
named test instead of dropping from a response unnoticed.

Applications reach `router-v1` the same way as any other capability,
through the facade:

```go
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	facade "github.com/openabstractions/abstraction-facade/go"
	router "github.com/openabstractions/abstraction-router/go/client"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	route, err := facade.Discover().ResolveRouter(ctx, facade.Requirements{})
	if err != nil {
		log.Fatal(err)
	}
	models, err := route.ModelsContext(ctx, false)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(len(models.Models), "families")

	result, err := route.PickContext(ctx, router.PickRequest{Model: "qwen2.5:0.5b"})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(result.Decision.Verdict, result.Decision.Host)
}
```

For C++17, install the development CMake packages and use
`find_package(abstraction_router CONFIG REQUIRED)` with
`abstraction::router_client`. See [the C++ example](cpp/README.md).

An outcome such as `not_allowed` is a routing decision, not a transport
failure. `PickRequest.allowed` absent allows every servable server;
present with `servers: []`, it allows none and the request always
refuses. Responses preserve that distinction. A decision names the server
that serves it and carries no address of its own: `abstraction-inference`
performs the call for every outcome. Caller attribution is observed by
the server through identity; requests contain no caller claims.

This subset includes model aliases, server residency, failed-server
diagnostics, cache timing and the routing audit. **Who holds the
machine's accelerator memory is `abstraction.resource/table@1`, which
this router reads and this interface does not carry.** This is not a
claim of all-platform validation or a published release. The service
reuses the existing provider and therefore still never loads or unloads
a model.

## Hosted servers

A model server is what OpenCode calls a provider. A hosted server is a
provider endpoint off this machine, such as OpenRouter or a LiteLLM
instance on the LAN. Its entry names a base URL,
a wire kind (`openai-compatible`, `anthropic-messages`, or
`<owner>/<name>@<n>` from the open `wire_kinds` catalogue) and a
credential name. The service reads its model listing with the credential
applied through `abstraction.credentials/applier@1`, as consumer
`abstraction.router/router@1`, once per listing request.

- `Hosts` reports `hosted: true`, the wire and the credential name. No
  snapshot carries a header value.
- A listing the applier refuses reads `up: false` with
  `why: credential:<outcome>:<name>`, for example
  `credential:unknown:openrouter`.
- `Models` marks hosted aliases `hosted: true`, folded into families
  beside local names.
- `Pick` answers `hosted` after every `resident` and `would_load` server
  on this machine, unless the allowance names only hosted servers. The
  decision names the server, not an address: `abstraction-inference`
  performs the call, and no endpoint or key reaches the caller.

```go
r := router.New(append(router.Installed(),
	router.NewHosted("openrouter", "https://openrouter.ai/api/v1", router.WireOpenAICompatible, "openrouter"))...)
r.UseCredentials(apply) // the runtime's in-process applier
```

A remote runtime is a hosted server whose own `router@1` `Models` and
`Hosts` are read over the mutual-TLS remote transport; its own servers
are listed under `<remote-name>/<server>`, each carrying
`domain: <remote-name>`. The remote applies its own credential to the
requests it performs on this runtime's behalf; this router never carries
or applies it.

The legacy JSON-over-socket protocol's own account of `routerd`, the
retired `router` CLI, and the read-only TCP window is in
[HISTORY.md](HISTORY.md), not here: the installed runtime never served
it.

## Authorising a server

A routing decision may name a server the caller did not expect.
`PickRequest.allowed` is the set the caller will accept.

**The key absent allows every servable server. `servers: []` allows none
and always refuses.** A language that folds an empty list into a missing
one turns a hard requirement into a preference nothing observes, which is
worse than a refusal because nothing is on the record. A `not_allowed`
outcome names no server, and a caller that only ever sends where an
outcome points therefore sends nowhere; the routing audit carries the
refusal beside the decisions that were served.

Order is the caller's preference and the router keeps it, except that a
server already holding the model beats one that would load it however
the request is ordered. An outcome always names the server it chose, so
a caller served by its second choice can tell.

    go test -run TestOneRequestRoutedThroughThreeOutcomes ./router/go -v

with `ROUTER_LIVE=1` and `ROUTER_LIVE_MODEL` set to a model one of your
servers holds takes one request through all three: refused, fallback,
resident. It skips and names the servers that were missing if none is
running.

## Who may ask

The interface is a named pipe on Windows and an `AF_UNIX` socket
elsewhere, and every caller is bound to a process the kernel vouches for
([`abstraction-identity`](https://github.com/openabstractions/abstraction-identity/blob/main/CONTRACT.md)). The frame is read before
the caller is known — Windows will not identify the client of a pipe
nothing has been read from — and it stays bytes until the binding
succeeds. A caller that cannot be bound is refused before its frame is
parsed as anything.

## Names

Resolution is [`abstraction-model`](https://github.com/openabstractions/abstraction-model)'s, not ours. Four servers
name one model four ways with no overlap — Lemonade's `id` and its
`checkpoint`, LM Studio's `id`, Ollama's `name:tag`, ComfyUI's bare
filenames — and every one of them is reported under the family it
belongs to. A `family` is what two names for the same weights must agree
on before one of them can answer for the other.

The family is the storage inventory's. A store that can name what a file
is publishes an `abstraction.model/descriptor@1` manifest, and the router
reports the `family` that descriptor declares for every server model the
descriptor's store names. `family_source` is then `descriptor`.

A model no descriptor names keeps the server's alias as its family,
parsed by `abstraction-model`'s identity rules, and `family_source` is
`alias`. That is the answer on a machine with no inventory source, for a
hosted server's listing, and for a name two stores describe as two
different models — a contested name names nothing, and the router does
not pick a winner. The router derives a family from a server's name only
in this fallback; it derives none of its own anywhere else.

A server model is matched to a descriptor, and to its `held_in` stores,
by one matching function: an adapter that knows its server abbreviates
names completes them to the store's own spelling first (Ollama:
`library/` prefixed when the alias carries no slash), and the completed
name then matches the store's own name for the content exactly. No tail
of a name after a slash is matched. The descriptor match and `held_in`
index a store's name under the same keys; a server alias reaches both
through the one function. A key two differently-named held objects would
both reach is refused: it names no family and no store, the same refusal
a name two stores describe as two models already gets. `Pick` is
unchanged: a request naming a model reaches the family that model was
catalogued under.

Evidence from the four adapters this machine reads:

| Server | Server's alias | Store's own name | Match |
| --- | --- | --- | --- |
| LM Studio | `publisher/repo/file.gguf` (`id`) | `publisher/repo/file.gguf` (its tree, path relative to the models folder) | exact, no completion needed |
| ComfyUI | `checkpoints/file.safetensors` (folder + file name) | `checkpoints/file.safetensors` (its tree, same relative path) | exact, one folder deep |
| Ollama | `tiny:1b` (`name:tag`, registry-implicit) | `library/tiny:1b` (the manifest path under `registry.ollama.ai`) | the adapter completes `tiny:1b` to `library/tiny:1b` before matching exactly |
| Lemonade | `unsloth/Qwen3.6-35B-A3B-GGUF:Qwen3.6-35B-A3B-UD-Q4_K_XL.gguf` (`checkpoint`) | the same string (Hugging Face's own `owner/repo:file`) | exact, a Hugging Face `owner/repo` name |
| Lemonade | `Qwen3.6-35B-A3B-GGUF` (`id`) | no store name shares this key | none — `id` carries no path a store spells, and stays an alias of its own |

The adapter completion an exact match now needs, and why the earlier
tail-after-a-slash match reached the wrong object for two publishers of
one filename, is in [CONTRACT.md](CONTRACT.md) ROUTE-N2 and its
Divergences entry.

## Where the weights are

A `Family` and each of its `Alias` names carry `held_in`: the storage
inventory stores holding an object that name matches, in store order. A
server's model matches a store's object by the one matching function
above, or by the digest a store published for its own unnamed object; no
file is read and nothing is hashed. `held_in` is empty when no store
reports the model, which is the ordinary answer for a hosted server's
listing.

A model a store holds that no server serves is a family of its own with
one alias: `servable` false, `resident` false, `server` empty, and
`held_in` naming its stores. An application reading `Models` therefore
sees every model on this machine, and `Pick` still routes only to
servers, because a name with no server is installed on nothing.

The runtime supplies this through `Router.SetHeld`, from
`abstraction.storage/inventory@1` composed over the accepted inventory
sources. A router told nothing keeps the catalogue its servers alone
produce.

## What may break

- **ComfyUI is listed and never routed to.** It runs a graph, not a
  model. Asking for one of its weights returns `unservable`. It is
  enumerated because a quarter of this machine's bytes are in its tree.
- **A cached answer is milliseconds; a fresh one is not.** `fresh: true`
  reads all four servers, and ComfyUI's per-folder enumeration dominates
  it. Every answer carries `cache_age_ms`.
- **The router does not measure the card itself.** It reads who holds
  the machine's accelerator memory from a residency source the runtime
  supplies, and reports why in `ServerState.why` rather than inventing
  an empty card when none is wired ([CONTRACT.md](CONTRACT.md)
  ROUTE-D6).

The full set of promises, including outcomes, family matching and hosted
servers, is in [CONTRACT.md](CONTRACT.md).

## Refusals

Your application can tell why a request was refused without depending on
the wording of its error message. A newer server's unfamiliar refusal
still counts as a failure.

Service refusals carry an optional stable `code` alongside the existing
diagnostic `error` text. Either nonempty field means refusal. Old
text-only replies remain valid; unknown codes remain refusals and must
not be treated as success. Servers continue sending diagnostic text for
older clients. Go clients return `*RemoteError`, retaining the code and
message; `Response.Err()` applies the same rule to a decoded reply. The
[code constants](go/errors.go) define the vocabulary. Diagnostic wording
is not an API.

Caller `user_description` and `path_description` contain server-observed
diagnostic text, including proof decorations. They are not stable
principal identifiers, filesystem paths, or authorization evidence. The
audit entries likewise contain diagnostic caller descriptions.
