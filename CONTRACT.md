# abstraction.router contract

Binds: `router.thrift`

`abstraction.router/router@1` is the generated routing service: given a
model name, it reports what exists on this machine under every name it is
known by, which server currently holds it and what that costs, and, given a
request, chooses which server should serve it. Model identity is
[`abstraction-model`](https://github.com/openabstractions/abstraction-model/blob/main/CONTRACT.md)'s;
residency is read from
[`abstraction.resource/table@1`](https://github.com/openabstractions/abstraction-resource/blob/main/CONTRACT.md);
a caller is bound the way
[`abstraction-identity`](https://github.com/openabstractions/abstraction-identity/blob/main/CONTRACT.md)
binds one to every native service. [README.md](README.md) is the
walkthrough: obtaining the module, running it, and a worked `Pick` call.

## Reading this page

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD",
"SHOULD NOT", "RECOMMENDED", "MAY" and "OPTIONAL" in this page are to be
interpreted as described in RFC 2119 and RFC 8174, when, and only when, they
appear in all capitals, as shown here.

A rule id such as `ROUTE-D1` is declared once, in bold brackets before its
title, at the head of the rule it names; tests and refusals cite it the same
way. A retired id is never reused; [HISTORY.md](HISTORY.md) keeps it with the
release it left. `HISTORY.md` also carries this contract's prior art, the
retired JSON-over-socket protocol's own account, and what was measured or
tested, linked from here and linking back.

| letter | topic |
| --- | --- |
| A | admission — binding the caller, and the rights actions a call requires |
| D | deciding a server — how `Pick` ranks, chooses and reports one |
| N | naming — family and alias matching |
| H | held — where the weights are, in the storage inventory |
| S | servers off this machine — hosted servers and remote runtimes |
| E | error and outcome — the outcome words a call can return |
| U | the audit — the routing record no caller clears |
| X | extension — reserved, unused |

**Prose-to-wire.** The words below are the decided names
(`research/vocabulary/DECISION.md` D11, D51, D84, S11); the wire still
carries the name on the right until the release named ships
(`research/vocabulary/RENAME-PLAN.md` §4): a struct or field rename ships
with `router@2` beside `router@1`, and a rights action or resource rename's
stored-policy dual reader accepts both strings for that release.

| decided name | current wire name, until it ships |
| --- | --- |
| `Servers`, `ServerState`, `ServerAllowance`, `ServersSnapshot` | `Hosts`, `HostState`, `HostAllowance`, `HostsSnapshot` — second release |
| outcome `would_load`, `no_host`, `not_here` | `would-load`, `no-host`, `not-here` — second release |
| `Decision.allowed` | `Decision.authorised` — second release |
| outcome `not_allowed` | `unauthorised` — second release |
| `Decision.server` | `Decision.host`; `Decision.endpoint` is dropped — second release |
| `abstraction.router/route.pick` (action) | `abstraction.router/route` — second release |
| resource `account` (both router actions) | `abstraction.router/inventory`, `abstraction.router/routes` — second release |
| `server:<name>` (resource) | `host:<name>` — first release |

### Admission

**[ROUTE-A1] Every caller is bound before its frame is parsed as anything.**
The interface MUST be a named pipe on Windows and an `AF_UNIX` socket
elsewhere. The service MUST derive the caller from native Program proof the
kernel vouches for, and MUST refuse a caller it cannot bind before its frame
is read as a request.

**[ROUTE-A2] Reading and choosing carry separate rights.** `Models` and
`Hosts` MUST decide `abstraction.router/inventory.read`; `Pick` MUST decide
`abstraction.router/route.pick`; both actions apply to resource `account`.
Reading what exists is not the same permission as deciding where a request
goes.

### Deciding a server

**[ROUTE-D1] The router never loads, unloads or evicts a model.** A
`would_load` outcome names a candidate server; asking that server is the
caller's own next step, and MUST NOT be performed by the router itself.

**[ROUTE-D2] A resident server outranks one that would load; a hosted
server is asked last.** A server already holding the model MUST outrank one
that would load it, however the request ordered its allowance. A hosted
server MUST be asked only after every resident and would-load server on
this machine, unless the request's allowance names only hosted servers.
Order beyond that is the caller's own preference, and the service MUST keep
it.

**[ROUTE-D3] Two files of one family on one server are one family and
several files.** Differing only in `quant` or `format`, they MUST be
reported as one family and several files; a file the server already holds
wins, and otherwise the server's own order decides.

**[ROUTE-D4] `PickRequest.allowed` bounds which servers may answer.**
Absent, it MUST allow every servable server. Present with `servers: []`, it
MUST allow none, and the request MUST always refuse.

**[ROUTE-D5] A decision names the server that serves it, never an
address.** `Decision.server` MUST name what serves the request; the service
carries no `endpoint`, because `abstraction-inference` performs the call for
every outcome. A `not_allowed` decision MUST carry no server, and MUST carry
`withheld` (the servers that could have served the request) and `allowed`
(the set the request named); the routing audit (`ROUTE-U1`) records the
refusal beside the decisions that were served.

**[ROUTE-D6] Residency informs ranking; the router holds no card of its
own.** Who holds the machine's accelerator memory MUST be read as
`abstraction.resource/table@1`'s own answer. The router MUST carry no
holder of its own: it reads that table to rank servers, and ranks without
it when none is wired, saying so in `ServerState.why`.

### Naming and family matching

**[ROUTE-N1] A server model's family is the storage inventory's own.**
When a store publishes an `abstraction.model/descriptor@1` manifest naming
the content a server serves, the family MUST be the one that descriptor
declares, and `family_source` MUST read `descriptor`. A model no descriptor
names MUST keep the server's alias as its family, parsed by
`abstraction-model`'s identity rules, and `family_source` MUST read `alias`.
This fallback is the answer on a machine with no inventory source, for a
hosted server's listing, and for a name two stores describe as two
different models.

**[ROUTE-N2] A server's alias reaches a store's own name by exact match.**
An adapter that knows its server abbreviates names MUST complete them to
the store's own spelling (Ollama: `library/` prefixed when the alias
carries no slash) before matching. The service MUST NOT match by any tail
of a name starting after a slash. The descriptor match and each `held_in`
index use this same function. A server alias reaches both through it
(measured against the owner's inventory; see HISTORY.md).

**[ROUTE-N3] A key two differently-named held objects would both reach is
refused.** The service MUST refuse a name two stores describe as two
different models: it names no family and no store, the case `ROUTE-N1`'s
fallback already covers, and the router MUST NOT pick a winner between
them.

**[ROUTE-N4] A component is never a family of its own.** A projector, a
VAE, or another role-bearing object a store's descriptor names MUST fold
into the family its descriptor names as `base`, or, when no base can be
derived, into the family its own component's descriptor declares, and MUST
be listed there as a `Component{Store, Role, Name}`, distinguished by
`role` from a servable model (`abstraction-model` CONTRACT.md MODEL-C3).

### Where the weights are

**[ROUTE-H1] `held_in` names the stores holding an object, whether or not
a server serves it.** `held_in` MUST name the storage inventory stores
holding an object a family or one of its aliases matches, in store order,
by name or by a published digest the server reports. No file MUST be read
and nothing MUST be hashed to produce it. `held_in` is empty when no
configured store reports the model, the ordinary answer for a hosted
server's listing.

**[ROUTE-H2] A model held but served by nothing is still reported.** A
model a store holds that no server serves MUST still be reported, as a
family of its own with one alias: `servable` false, `resident` false,
`server` empty, `held_in` naming its stores. `Pick` MUST still route only
to servers: a name with no server is installed on nothing.

### Servers off this machine

**[ROUTE-S1] A hosted server is a provider endpoint off this machine.**
Its entry MUST name a base URL, a wire kind, and a credential name; the
wire kind MUST be one of `wire_kinds` (`openai-compatible`,
`anthropic-messages`, `openai-realtime`, `deepgram-prerecorded`,
`elevenlabs-stream`, `stability-v2beta`, `fal-queue`,
`replicate-predictions`, `oa-remote@1`) or `<owner>/<name>@<n>` from that
open catalogue.

**[ROUTE-S2] A hosted server's listing is read with its credential
applied.** The service MUST read it through
`abstraction.credentials/applier@1`, as consumer
`abstraction.router/router@1`, once per listing request. No snapshot,
audit entry or error MUST carry a header value.

**[ROUTE-S3] A listing the applier refuses reads `up: false` and says
why.** A refused listing MUST read `why: credential:<outcome>:<name>`. A
server that names a credential with no applier wired MUST read
`credential:unavailable:<name>`; a wire this router does not know MUST
read `unsupported_wire:<kind>`.

**[ROUTE-S4] A remote runtime is a hosted server whose own router answers
for others.** Its own `router@1` `Models` and `Hosts` MUST be read over the
mutual-TLS remote transport, and its models fold into this router's
families like any other server's. Its own servers MUST be listed under
`<remote-name>/<server>`, each carrying `domain: <remote-name>`; a caller
can tell which machine actually answers a call from it. The remote
runtime's names, states and credential names are its own; it applies its
own credential to the requests it performs on this runtime's behalf, and
this router MUST NOT carry or apply it.

### The audit

**[ROUTE-U1] Every routing decision is recorded in an audit no caller can
clear.** `ServersSnapshot.asked` MUST record the last 50 decisions, naming
which program asked for which model and what it was sent to, read under
`abstraction.router/inventory.read`. A server sees one request; only the
router sees who caused a load.

## Outcomes

**[ROUTE-E1] `Pick` answers with one outcome word.** The outcome MUST be
`resident` when an allowed server already holds the model; `would_load`
when no allowed server holds it but one has it installed and would load
one copy; `hosted` when no allowed server on this machine has it and a
hosted server answers instead; `not_allowed` when a server that could
serve it exists but the request's allowance did not name it; `no_host`
when no servable server serves the requested profile, or when every
server holding the model serves it for a different profile only;
`unservable` when only a server nothing can route a call to has it
installed; `not_here` when no server on this machine has it installed at
all; and `unparseable` when the asked name names no model.

**[ROUTE-E2] Protocol refusals carry a stable code.**
`router_error_codes` MUST be one of `internal`, `invalid_request`,
`caller_refused`, `unknown_operation`, `unavailable` or `forbidden`.
Either a nonempty `code` or a nonempty `error` MUST mean refusal; an
unknown code MUST still be treated as a refusal, never as success. An
outcome word such as `not_allowed` is a routing decision, carried in the
decision itself, never a transport failure carried in the response's
code.

| outcome | rule | meaning |
| --- | --- | --- |
| `resident` | ROUTE-E1 | an allowed server already holds the model |
| `would_load` | ROUTE-E1 | no allowed server holds it; one would load one copy |
| `hosted` | ROUTE-E1 | a hosted server answers instead |
| `not_allowed` | ROUTE-E1 | a server that could serve it exists outside the request's allowance |
| `no_host` | ROUTE-E1 | no servable server serves the requested profile |
| `unservable` | ROUTE-E1 | only a server nothing can route a call to has it installed |
| `not_here` | ROUTE-E1 | no server on this machine has it installed |
| `unparseable` | ROUTE-E1 | the asked name names no model |
| `internal` | ROUTE-E2 | an unclassified server failure |
| `invalid_request` | ROUTE-E2 | the request could not be parsed as a valid call |
| `caller_refused` | ROUTE-E2 | the caller could not be bound or rechecked |
| `unknown_operation` | ROUTE-E2 | the frame named an operation the service does not serve |
| `unavailable` | ROUTE-E2 | a policy decision could not be obtained (`rights:unavailable`) |
| `forbidden` | ROUTE-E2 | rights refused the action |

## Bounds

| what | bound | rule |
| --- | --- | --- |
| routing audit | the last 50 decisions | ROUTE-U1 |
| hosted listing credential application | once per listing request | ROUTE-S2 |

## Divergences

- ROUTE-N2 matches a server's completed alias to a store's name exactly,
  never by a tail after a slash. Docker and Ollama, the platforms this
  matching serves, complete a short name to its own canonical spelling and
  match only that (`tiny:1b` becomes `registry.ollama.ai/library/tiny:1b`,
  `ubuntu` becomes `docker.io/library/ubuntu`); neither completes a caller's
  guess against an arbitrary stored suffix (router review F5).
- ROUTE-U1's audit is readable by any caller holding
  `abstraction.router/inventory.read`, not limited to an operator: it
  explains a load's cause to any inventory reader, rather than serving as a
  restricted security log.
- ROUTE-E1 and ROUTE-E2 keep two separate vocabularies: outcome words for
  routing decisions, protocol codes for transport refusals, matching the
  same split `abstraction-rights` CONTRACT.md draws between its decision
  outcomes and its own protocol failures.

## Not built

- Full conformance is proven for Go and JavaScript clients only; Python,
  C++ and Rust router clients are unproven.
- All-platform validation is not established.
- This repository carries no published release tag.
- The framed Go and C++ clients generated over Thrift are a development
  surface; they carry no cross-language conformance run of their own yet.
