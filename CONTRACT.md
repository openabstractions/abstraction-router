# abstraction.router contract

Binds: `router.thrift`

`abstraction.router/router@1` is the generated routing service: given a
model name, it reports what exists on this machine under every name it is
known by, which server currently holds it and what that costs, and, given a
request, chooses which server should serve it. Model identity is
[`abstraction-model`](https://github.com/openabstractions/abstraction-model/blob/main/CONTRACT.md)'s;
who holds the accelerator memory is read from
[`abstraction.resource/table@1`](https://github.com/openabstractions/abstraction-resource/blob/main/CONTRACT.md),
and the models a native server holds loaded from its own description
(ROUTE-D14);
a caller is bound the way
[`abstraction-identity`](https://github.com/openabstractions/abstraction-identity/blob/main/CONTRACT.md)
binds one to every native service. [README.md](README.md) is the
walkthrough: obtaining the module, running it, and a worked `Pick` call.

## Reading this page

The conventions every OA contract carries are stated once in [CONVENTIONS.md](https://github.com/openabstractions/abstractions/blob/main/idl/CONVENTIONS.md); a rule here that applies one cites its `CONV-` id.

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
| D | deciding a server — how `Pick` ranks, chooses and reports one, and what [`Offer`](https://openabstractions.github.io/reference.html#concept-router.offer) lists |
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
elsewhere. The service MUST derive the caller from native [Program](https://openabstractions.github.io/reference.html#concept-identity.listener-program-need) proof the
kernel vouches for, and MUST refuse a caller it cannot bind before its frame
is read as a request.

**[ROUTE-A2] Reading and choosing carry separate rights.** `Models`,
`Hosts`, `Offer` and `ObserveOffer` MUST decide
`abstraction.router/inventory.read` on resource `abstraction.router/inventory`
(VISION ROUTE-D8, "read under `inventory.read`"). `Pick` MUST decide
`abstraction.router/route` on resource `abstraction.router/routes`
(research/rights-defaults/DECISION.md §1).
Reading what exists is not the same permission as deciding where a request
goes.

The standalone `--window` view is deprecated. This service admits callers only at
Identity `listen.Program`, and no platform binds a loopback TCP caller at that
proof (Windows and Linux bind Process at `bound`; macOS has no binding).
`Window` opens no listener and returns an error; `serve router --window` fails
at startup before opening its endpoint. `Answer` and `AnswerContext` are
post-admission seams for receiving owners that have bound and authorized their
caller.

### Deciding a server

**[ROUTE-D13] Engine facts preserve object identity and their provider's
provenance.** `Models` MAY carry `Family.objects` and `Hosts` MAY carry
`HostState.engines`. The runtime MUST obtain selected-engine associations from
the accepted provider registration owner's interpretation of its own
settings and recipe tables. An association MUST match evidenced content
identity or the source's opaque object ID; family equality and extension
spelling MUST NOT establish one. A multi-entry manifest MUST retain every
entry, including its role and unknown evidence. Its collection ID MUST NOT be
presented as one content object's identity. MODEL-C3's logical components keep
their own role/base-family meaning.

`bound` says an explicit configured association, with readiness and residency
reported separately. Missing content identity, unknown provider facts or an
unobserved required companion MUST carry `unknown` and a plain explanation.
The open cause enum admits future display causes; clients MUST NOT parse `why`.
`source_only` requires a complete registration reading proving no OA binding;
`incompatible` requires an explicit provider refusal for this object. An
unclassified future cause uses `other`; neither word substitutes for unknown.
Selected versions MUST be omitted when unreported. The first slice defines no
general engine acceptance constraints. The same inventory.read policy evaluation gates
these fields, and this read MUST NOT load a model or start an engine. No
executable paths, endpoints, arguments or credentials enter these facts.
Paired-runtime facts MUST retain their reporting domain and provenance; a local
runtime MUST NOT derive remote engine facts from local tables. An earlier server's
absent fields mean unknown. Presentation clients MUST consume this public
answer and MUST NOT display content digests as page text.
Digest-only association and recipe-companion joins MUST require storage's
`Hashed` evidence. `Named` and `Foreign` digests MUST NOT establish such a
join; their inventory entries remain present with unknown/unverified engine
association. An exact provider-owned store and opaque object ID MAY establish
a configured association without claiming byte verification or readiness.
Names, filename-derived digests and family equality MUST NOT establish it.

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

**[ROUTE-D3] Two files of one [family](https://openabstractions.github.io/reference.html#concept-model.family-identity) on one server are one family and
several files.** Differing only in `quant` or `format`, they MUST be
reported as one family and several files. When the caller names an exact
alias a server lists (a quant or a file), the router MUST serve that alias,
loading it if needed, or refuse with the reason. A resident sibling of the
same family MUST NOT answer it silently; the refusal names the sibling it
kept out. When the caller names only the family, a file the server already
holds wins, and otherwise the server's own order decides; the decision's
`model` and `why` name the served alias (A-F20). The router reads a name
stating a quant as that quant, which every alias stating the same quant
serves, and a name ending in a model-file extension as that file alone. A
name equal to one server's full listed alias is also exact; for llama.cpp
that is the file-derived name. A bare family name (`qwen3.6-35b-a3b`,
`qwen/qwen3.6-35b-a3b`) stays a family request.

**[ROUTE-D4] `PickRequest.allowed` bounds which servers may answer.**
Absent, it MUST allow every servable server. Present with `servers: []`, it
MUST allow none, and the request MUST always refuse.

**[ROUTE-D5] A decision names the server that serves it, never an
address.** `Decision.server` MUST name what serves the request; the service
carries no `endpoint`, because `abstraction-inference` performs the call for
every outcome. Under router@1, whose `Decision` still has an `endpoint`
field, that field MUST be empty for every verdict. A `not_allowed` decision MUST carry no server, and MUST carry
`withheld` (the servers that could have served the request) and `allowed`
(the set the request named); the routing audit (`ROUTE-U1`) records the
refusal beside the decisions that were served.

**[ROUTE-D6] Residency informs ranking; the router holds no card of its
own.** Who holds the machine's accelerator memory MUST be read as
`abstraction.resource/table@1`'s own answer. The router MUST carry no
holder of its own: it reads that table to rank servers, and ranks without
it when none is wired, saying so in `ServerState.why`.

**[ROUTE-D7] One name on two servers is two aliases of one family.** A
family's `names` MUST list one alias per server and name: two servers
serving the same name are two aliases, each carrying its own server's
residency and context lengths, in the servers' preference order. A request
naming that name MUST reach the family once.

**[ROUTE-D8] An [offer](https://openabstractions.github.io/reference.html#concept-router.offer) is one name and one conservative context per family.**
`Offer` MUST list every family with at least one alias that is servable,
serves the requested profile (`ROUTE-D9`), and sits on a server the
placement admits. The counted
aliases MUST be ordered by UTF-8 bytes, and `names[0]` is the alias to ask
for. `context` MUST be the smallest bound across the counted aliases, each
alias bounded by its own server's valid effective length, else its maximum,
else the profile's catalogue default (32,768 tokens for `chat`, 8,192 for
`embed`). An alias's maximum is its server's report, else, when the server
reports no window for it, the smallest `context_length` the storage
inventory's descriptors name for its family (abstraction-model MODEL-C2), the
context the weights were trained for. `context_source` MUST name the source of that smallest bound. When
a counted alias sits on a server that fixes its request window at load and
reports no valid effective length, `context` MUST be 0, `context_source`
`unknown`, and `unknown` MUST name that alias as `<server>/<name>`. `input`
MUST be `context - reserve`, floored at 0. A profile without a context
window answers `context_source: none`, `context: 0` and `input: 0`.
Families are listed in UTF-8 byte order of `family`. The fold reads the
latest survey (`fresh` surveys first) and sends nothing to a server on this
machine; the servers that fix their window at load are a router host-table
fact: Lemonade and Ollama. Ollama's effective length is the running model's
`/api/ps` `context_length`, else the Modelfile `num_ctx` in `/api/show`
`parameters`. LM Studio reports a loaded model's `loaded_context_length` in
`/api/v0/models` as its effective length. `LoadedWindow` reads one alias's
window from its server at request time for these three servers and, on
Ollama, loads a model whose window is unknown with a prompt-less
`/api/generate`, as the request would load it (inference INF-Q7). `research/opencode-inventory/DECISION.md` records the
decision; `go/testdata/offer.json` holds the cases.

**[ROUTE-D9] A model serves only the profiles named for it.** Each alias
MUST be judged by the first source in this table that names profiles for
it, and `Pick`'s profile check and `Offer` MUST judge every alias of a
server by name, so one alias's profiles never decide another's.

| server | names an alias's profiles | when it names none, the alias serves |
| --- | --- | --- |
| native provider | its declaration's `models` entries | (every entry names some) |
| LM Studio, Ollama | the server's own model metadata | the server's profiles |
| remote runtime | its own `Models` alias `profiles`, kept to the profiles declared for the remote | the profiles declared for the remote |
| any other server | nothing | the server's profiles |

A native provider's declaration names its models in `models`, each entry
read against the profiles the declaration serves: its listed profiles
whose contracts it declares, or `chat` for a chat-only declaration that
lists none (`abstraction-facade` CONTRACT.md FAC-R3). An entry
`<profile>:<model>` with a nonempty `<model>`, where `<profile>` is a served
profile, MUST serve `<model>` for that profile alone, and every other entry,
a colon inside a model name included, MUST serve the whole entry for every
served profile. A model literally named `chat:x` is written `chat:chat:x`
for a declaration that serves chat. `Pick` for a model whose every server
serves it for other profiles only MUST answer `no_host` (`ROUTE-E1`), and
`Offer` for a profile MUST leave out the aliases that do not serve it
(`ROUTE-D8`). `Models` MUST report each alias's profiles. A native provider
is reached for each profile it serves, audio included, at
`abstraction.inference/<profile>@1` over its native transport, and decides
its own speech formats at its own admission. The profiles are the seeded
`chat`, `embed`, `transcription`, `speech`, `image` and `live`, which a
server that declares none serves by its wire, and `video`, which only a
native provider declaring it serves: the runtime's job executor reaches it
at `abstraction.inference/video@1` (inference CONTRACT.md INF-G5).

**[ROUTE-D10] Modalities lists the current declared uses independently of models.**
`Modalities` MUST decide `abstraction.router/inventory.read` before disclosure.
It MUST read the seed catalogue and valid modalities from current registered host
declarations without surveying a host or requesting a vendor model list. A use
remains listed when no model is available. Removed declarations contribute no
uses to a new listing. IDs MUST be deduplicated and ordered by UTF-8 bytes.
Owned IDs follow the registered-host grammar: an owner of 1..63 lowercase ASCII
letters, digits or hyphens, a slash, a name of 1..63 lowercase ASCII letters,
digits, underscores, dots or hyphens, and `@` followed by a positive decimal
version of 1..6 digits. Owner and name begin with a letter or digit.

Each request MUST use one catalogue snapshot. `limit` is 1..64 and a cursor
is at most 256 UTF-8 bytes. Empty cursor starts a listing. Continuations MUST
bind the admitted caller, runtime instance and complete catalogue. A changed
catalogue or foreign cursor MUST answer `gap`, requiring a new listing.
`page` carries at most `limit` IDs; `complete` is true exactly at the end and
`next` is empty exactly then. Invalid limits or oversized/invalid UTF-8 cursors
answer `invalid`. Policy denial answers `not_permitted`; a missing provider or
policy outage answers `unavailable`. Every refusal MUST carry no IDs, an empty
`next` and `complete: false`. Binding failures retain the protocol error channel.
Remote access follows the inventory receiving policy under `remote="see"`.

**[ROUTE-D11] ObserveOffer answers Offer once it changes.** `ObserveOffer`
MUST answer the offers `Offer` answers for the same request (`ROUTE-D8`) with
a `cursor`, a digest of those offers. An empty cursor MUST answer at once; a
`fresh` request surveys before that first reading only. Given a cursor, it
MUST answer once the offers' digest differs from it, or when `wait_ms` ends
with the unchanged offers and cursor. `wait_ms` is 0..30 000; another value
answers `invalid`. A caller's own deadline shortens the wait and leaves one
second for the reply. What a wait reads MUST be renewed by every survey: the
runtime's own clock, a declaration change and a fresh call. One reading per
request and survey MUST serve every waiting caller, so a paired runtime's
`Offer` is asked once per survey however many callers wait (`ROUTE-S4`).
Refusals follow `ROUTE-E3` and carry an empty cursor. Remote access follows
`Offer`'s receiving policy under `remote="see"`. The answer's `cache_age_ms`
MUST be the age of the survey it was read from when the answer is sent, so
now minus that age names one survey across answers; a wait that ends with no
new survey answers an age at least as long as the wait.

**[ROUTE-D12] Surveys commit in the order they start, on a cadence per kind
of host.** Every survey MUST take a start number, and its snapshot MUST
replace the committed one only when its number is higher: a survey of the
hosts before a declaration or pairing change that finishes later never
replaces the survey after it. The runtime's clock, a declaration or pairing
change, `Survey` and a fresh call MUST share the survey in flight when it
started under the current hosts and reads at least the hosts the caller
needs, and each survey MUST finish within 30 s. The clock reads a server on
this machine at every tick (the composed runtime's tick is 60 s). It reads a
hosted listing that answered, which costs the provider a request and the
account a credential audit entry, at most every 15 minutes. It reads a paired
runtime at every tick while it answers. After a hosted listing or a paired
runtime fails, the clock backs off, up to 15 minutes, until it answers or the
hosts change, and `Offer` does not ask a paired runtime it is backing off
from. Storing a host's credential is a host change for that host: it MUST be
read at once, and a survey that read it before records nothing and is joined
no more. A host the clock does not read keeps its last reading, which can
predate the survey's `cache_age_ms`. A fresh call and a
declaration or pairing change read every host at once.

**[ROUTE-D14] A native server states its own residency.** A native
server's residency MUST be read from the display capabilities `resident`
(the count) and `resident.1` to `resident.<count>` on the
`abstraction.inference/chat@1` service of its endpoint description. The
router MUST keep only the names the server's declaration lists and MUST
ignore every other stated name. A native server that states no `resident`
fact reads as unreported, and none of its names is resident. A stated
`resident.<n>.context` is the context that resident was loaded with, and
MUST be that name's effective context, outranking the context the
runtime's plan reports for the name (ROUTE-D8).

The supervisor of an on-demand provider supplies its current readiness through
the Go constructor option `WithSurveyReady`. While that provider is idle, a
passive survey lists the declaration and skips endpoint metadata. The declared
models remain routable and the existing request admission owns activation.

**[ROUTE-D15] Each alias states its own capabilities (architect ruling
2026-11-12).** `Alias.capabilities` names what one alias on its server
takes and gives: input and output modalities (`text`, `image`, `audio`,
`video`), `tools`, `structured_output`, its `reasoning` controls (the
efforts it answers to, approximated ones included, whether it takes a
budget, and its default effort) and the `chat@3` extension names it takes.
Each fact comes from the first source that states it: the server's own
declaration or the hosted catalogue's entry for the model, then the facts
the server reports (a native server states them on its
`abstraction.inference/chat@3` description as `model.<n>` and
`model.<n>.capabilities`), then the hosted server's model listing. A fact
no source states is absent and never guessed. Two servers of one family
carry two aliases with their own capabilities, never a merged value.
Context stays in `context_length` and `effective_context_length`.

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

**[ROUTE-N4] A [component](https://openabstractions.github.io/reference.html#concept-model-identity.component) is never a family of its own.** A projector, a
VAE, or another role-bearing object a store's descriptor names MUST fold
into the family its descriptor names as `base`, or, when no base can be
derived, into the family its own component's descriptor declares, and MUST
be listed there as a `Component{Store, Role, Name}`, distinguished by
`role` from a servable model (`abstraction-model` CONTRACT.md MODEL-C3).

**[ROUTE-N5] A server's declared object association outranks a name
match.** When a server's registration associates a name it serves with a
stored weights object (a recipe object whose role is `model` or empty, by
store and object id, or by a digest the store verified by hashing), that
name MUST take the family the object's descriptor declares, and the
family's and the alias's `held_in` MUST name the object's store. The
server's name and the store's own name need not match. Associated objects
that declare two families name none, and the name falls back to
`ROUTE-N1`.

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

The receiving runtime MUST decide `abstraction.router/inventory.read` for
Models, Hosts, Offer and ObserveOffer before reading. It MUST decide
`abstraction.router/route` for Pick before routing. Its native Rights enforcer relays the canonical pinned-key
remote subject under the receiving account. An absent policy serves no remote
router views; a policy outage remains unavailable. Remote attribution names
the key identity and display domain and carries no local program proof.
A failed remote listing retains `transport_unavailable` for network or deadline
failure, distinct from rights refusal and unknown-method failure. Held by:
`go/service/views_test.go` (`TestRemoteViewsPolicyRunsBeforeProvider`,
`TestRemoteViewsActionsAndHonestAttribution`) and `go/availability_test.go`
(`TestRemoteListingPreservesAvailabilityCause`).

A remote runtime's aliases MUST carry the `context_length` and
`effective_context_length` its own `Models` reports, the smallest bound
across its aliases of one name. Its families MUST be offered by asking the
remote's own `router@1` `Offer` with the same profile, reserve and fresh
flag and `placement: any`; each remote offer folds into the local family of
the same name by the minimum of `context`, the union of `names`, its
`servers` prefixed `<remote-name>/` at the remote's place in the server
order, and its `unknown` entries prefixed `<remote-name>/`. A remote that
answers `unknown_method` is older than this rule; its `Models` aliases are
then folded locally like any hosted server's, with their `context_length`
and `effective_context_length` carried (ROUTE-D8). A remote whose `Offer`
fails otherwise MUST count nothing, as a server whose listing fails is left
out. The fallback retires when the earliest supported remote runtime carries
`Offer`.

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
For a native declaration's recipe whose objects name files by digest,
installed requires storage-held `hashed` evidence matching every role digest,
whatever registration declared it. Named, foreign or mismatched evidence
does not establish installation. A later storage observation may establish it
without changing the declaration or restarting the provider. A model that is
not installed is neither listed nor offered. A declared native host with
digest recipes and no installed model is listed with `up` and `servable`
false, `why` `engine:not_installed:<host>` and availability cause
`configuration`, and offers nothing. A recipe naming store objects keeps its
producer's account of installation. A host its registration reads
unavailable, such as a provider stopped after failed starts (facade FAC-R2),
is listed with `up` and `servable` false, `why` its reason word
(`start_failed:<host>`) and availability cause `configuration`. Its models
stay listed with `servable` false, and no route goes to it: another server
holding the model takes the request. If no other server holds it, the route
reply keeps the host in `installed_on`.

**[ROUTE-E2] Protocol refusals carry a stable code.**
`router_error_codes` MUST be one of `internal`, `invalid_request`,
`caller_refused`, `unknown_operation`, `unavailable` or `forbidden`.
Either a nonempty `code` or a nonempty `error` MUST mean refusal; an
unknown code MUST still be treated as a refusal, never as success. An
outcome word such as `not_allowed` is a routing decision, carried in the
decision itself, never a transport failure carried in the response's
code.

**[ROUTE-E3] Offer reports its decision in one typed outcome.** `Offer`
MUST return `offered` with the observed inventory, including an empty
inventory. A negative reserve or unsupported request semantics MUST return
`invalid`; a missing offer or policy decision MUST return `unavailable`.
A verified subject whose inventory rule refuses MUST receive
`not_permitted`. A receiving boundary refusal MUST return `forbidden`.
Every refusal MUST carry no offers and an empty observation. Callers MUST
check `outcome` before consuming offers. Binding and envelope failures
remain protocol refusals under `ROUTE-A1` and `ROUTE-E2`.

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
| `unavailable` | ROUTE-E2 | a policy decision could not be obtained (`rights:unavailable`), or a fresh survey did not finish within the call |
| `forbidden` | ROUTE-E2 | rights refused the action |
| `offered` | ROUTE-E3 | the current model offers, including an empty inventory |
| `invalid` | ROUTE-E3 | the offer request has unsupported semantics |
| `unavailable` | ROUTE-E3 | the offer or its policy decision could not be obtained, or a fresh survey did not finish within the call |
| `not_permitted` | ROUTE-E3 | the verified subject's inventory rule refuses |
| `forbidden` | ROUTE-E3 | the receiving boundary refuses |

## Bounds

| what | bound | rule |
| --- | --- | --- |
| routing audit | the last 50 decisions | ROUTE-U1 |
| hosted listing credential application | once per listing request | ROUTE-S2 |
| a fresh call's wait for its survey | the call's deadline less 1 s kept for the reply | ROUTE-E2, ROUTE-E3 |
| one fresh survey shared by concurrent fresh calls | 30 s, cancelled when its last waiting call ends | ROUTE-E2, ROUTE-E3 |
| one `ObserveOffer` wait | 0..30 000 ms, less 1 s of a shorter caller deadline | ROUTE-D11 |
| a paired runtime's `Offer` for waiting `ObserveOffer` callers | once per request and survey | ROUTE-D11, ROUTE-S4 |
| one survey, the clock's included | 30 s | ROUTE-D12 |
| the clock's read of a hosted listing | at most every 15 minutes | ROUTE-D12 |
| the clock's wait after a hosted listing or a paired runtime fails | 2, 4, 8, then 15 minutes | ROUTE-D12 |

## Divergences

The published Go compatibility default `hosts.go`'s package `client`
remains an unverified HTTP client. Installed runtime composition uses
`NewWithHTTPClient` with the kit's native program and PID/start binding.
The router drives no engine: the published `Host.Unload` and
`Host.UnloadWithHTTPClient` are removed (resource RES-L3). Missing trusted program
evidence fails closed there. `hosted.go`'s `hostedClient` remains the separate
remote HTTPS client. The fixed construction sites and installed call paths are
checked by `scripts.provider_kit_test`.
Publication authority: annotated
`published/abstraction-router/3e08b5f32a8f38d620cbde66c58caac7209f9011`
maps to private source `4b7f684db7ab7adc36ffb36d3257814c83c63f34`, where
`go/hosts.go:208` and `go/unload.go:28,40` supply these defaults and wrapper.

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
  `Offer`, added after the published method set, carries semantic refusals
  through `ROUTE-E3`'s typed result. The published `Models`, `Hosts` and
  `Pick` shapes retain their original refusal channel until `router@2`.
- LM Studio also loads a model with a window, and the router reads it only
  once the model is loaded: a cold LM Studio alias is bounded by its maximum.
- On ROUTE-S4's `unknown_method` fallback a remote's aliases reach this
  router under the remote's own name, and a remote Lemonade alias loses its
  window-at-load marking: without an effective length it is bounded by its
  maximum, where the remote's own `Offer` would answer `unknown`.
- ROUTE-D2 and ROUTE-S4 rank a remote runtime that already holds a model
  below a local server that would load it. `DREAM.md`'s multi-machine
  "Won" prefers the machine that already holds the model. The rule encodes
  the single-machine reading until the remote ranking is decided.
- A placement outside `local`, `hosted` and `any` is refused by the
  generated dispatcher with the codec refusal `bad_enum` before the service
  is asked; the legacy line request answers `invalid_request` for it.

## Not built

- Full conformance is proven for Go and JavaScript clients. C++
  (`conformance/clients/router`) and Rust
  (`conformance/clients/rust_capabilities`) clients have Windows proofs
  only. The Python router client is generated and unproven.
- All-platform validation is not established.
- This repository carries no published release tag.
- The framed Go and C++ clients generated over Thrift are a development
  surface; they carry no cross-language conformance run of their own yet.
