namespace * abstraction.router

// Framed service subset: inventory, host residency, and routing decisions.
// Who holds the machine's accelerator memory is abstraction.resource/table@1,
// which the router reads and this interface does not carry. The legacy HTTP
// window is not part of it either.
encoding json {
 escape="minimal"
 indent="2"
 map_keys="utf8-bytes"
 numbers="integer-decimal"
 opaque="verbatim"
 terminator="newline"
 duplicate_keys="refuse"
 depth_limit="64"
}
refusal {
 1: malformed(stage="grammar")
 2: bad_string(stage="grammar")
 3: number_spelling(stage="grammar")
 4: wrong_type(stage="grammar")
 5: bad_timestamp(stage="grammar")
 6: depth_exceeded(stage="grammar")
 7: duplicate_key(stage="grammar")
 8: duplicate_field(stage="structure")
 9: unknown_field(stage="structure")
 10: missing_field(stage="structure")
 11: bad_enum(stage="structure")
 12: trailing_bytes(stage="document")
}
typedef string timestamp(write="rfc3339-micros",read="rfc3339-wide")

// Absent HostAllowance permits all servable hosts. Present with [] permits none.
struct HostAllowance {
 1: required list<string> hosts
}(unknown_fields="refuse")
struct PickRequest {
 1: required string model
 2: required bool fresh
 3: optional HostAllowance allowed(omit="absent")
 4: optional string profile(omit="zero")
}(document="true",unknown_fields="refuse",doc="profile is what the chosen host must serve the model for, a profiles member or <owner>/<name>@<n>; empty is chat.")
// Profiles a host serves, the seed of an open catalogue shared with
// abstraction.inference host_profiles; a later profile is <owner>/<name>@<n>.
// chat and embed are the profiles with a context window; Offer answers
// context_source none for every other profile.
const list<string> profiles = ["chat","embed","transcription","speech","image"]
// Diagnostic descriptions observed by the server, including proof decorations.
// Neither is a stable principal/path identifier, filesystem path, or authorization
// proof. They are never accepted as request claims.
struct Caller {
 1: required string user_description
 2: required string path_description
}(unknown_fields="grant")
struct Observation {
 1: required Caller caller
 2: required i64 took_ms
 3: required i64 cache_age_ms
}(unknown_fields="grant")
struct Alias {
 1: required string host
 2: required string name
 3: required bool resident
 4: required bool servable
 5: optional bool hosted(omit="zero")
 6: optional list<string> profiles(omit="zero")
 7: optional list<string> held_in(omit="zero")
 8: optional i64 context_length(omit="zero")
 9: optional i64 effective_context_length(omit="zero")
}(unknown_fields="grant",doc="hosted is true for a name read from a hosted host's model listing; such a name is never resident. profiles are what the host's own model metadata says this name serves (LM Studio's type, Ollama's capabilities); empty when the host reports none, and then its HostState profiles apply. held_in names the storage inventory stores holding an object this alias's own name or digest matches, in store order; empty when no store's object matches this name. host is empty exactly for a name no host serves, which a store holds and which is never resident or servable. context_length is the model's maximum supported context window in tokens, read from host metadata without loading it (LM Studio's max_context_length, Ollama's model_info context_length, Lemonade's max_context_window); zero when the host reports none. effective_context_length is a positive per-request context limit the host currently reports: the loaded value when the model is resident, otherwise its configured value when the host reports one (Lemonade's context_length); zero when unknown. A nonpositive or contradictory effective value is omitted.")
struct Component {
 1: required string store
 2: required string role
 3: optional string name(omit="zero")
}(unknown_fields="grant",doc="One role-bearing object a store holds, folded into a family's detail rather than listed as a family of its own (abstraction.model/descriptor@1 MODEL-C3): store is the holder, role is projector, vae, or another value a storage inventory source's descriptor names, and name is the store's own name for the object when it named one.")
struct Family {
 1: required string family
 2: required list<Alias> names
 3: optional list<string> held_in(omit="zero")
 4: optional string family_source(omit="zero")
 5: optional list<Component> components(omit="zero")
}(unknown_fields="grant",doc="names lists one alias per server and name; a name two servers serve appears twice, once per server. held_in names the storage inventory stores holding an object of this family, in store order, whether or not a host serves it; empty when no store reports one. A family whose names are all unservable is on this machine and answered by nothing. family_source is descriptor when the storage inventory published an abstraction.model/descriptor@1 naming this family, and alias when no descriptor names it and a program's own name for the model is the family; empty when the service reports neither. components lists a projector, VAE or other role-bearing object a store holds and this family's descriptor named as its base, or, when no descriptor could derive a base for it, an object that is this family's only content — held_in and family_source cover the family it was folded into or the family it stands in for either way; components exists to say the family answers for a part, not a whole model, when that is what it is.")
struct ModelsSnapshot {
 1: required Observation observation
 2: required list<Family> models
}(unknown_fields="grant")
// A typed cause for a host whose model listing could not be observed. Only
// transport_unavailable establishes only that this listing route was
// unavailable for the observation; callers must not infer a cause from
// HostState.why or claim that every host is offline.
enum AvailabilityCause {
 1: transport_unavailable
 2: credential
 3: rights
 4: configuration
 5: malformed
 6: unknown
 7: other
}(unknown="grant",reader="validate")
struct HostState {
 1: required string host
 2: required string base
 3: required bool up
 4: required string why
 5: required i64 installed
 6: required list<string> resident
 7: required bool servable
 8: optional bool hosted(omit="zero")
 9: optional string wire(omit="zero")
 10: optional string credential(omit="zero")
 11: optional string declared_by(omit="zero")
 12: optional list<string> profiles(omit="zero")
 13: optional string domain(omit="zero")
 14: optional AvailabilityCause availability_cause(omit="absent")
}(unknown_fields="grant",doc="domain names the remote runtime a host belongs to: an oa-remote@1 host lists itself, and each host that runtime reports, named <remote>/<host>, with domain <remote>. The remote runtime's names, states and credential names are its own; its credentials stay on it. profiles are what the host serves: those its registration declares, or its wire's default (every seeded profile for a host on this machine and openai-compatible, chat for another wire). declared_by names what registered the host: operator for a person's configuration, the product's name (ollama, lmstudio, docker-model-runner, foundry-local) for a host that product's own record declared, or default for a built-in address; it is omitted when unknown. A hosted host is a provider endpoint reached over the network by its wire kind, one of wire_kinds or <owner>/<name>@<n>. credential names the abstraction.credentials entry the service applies to its listing and requests; the snapshot carries the name and never a header value. A listing the applier refuses reads up false with why credential:<outcome>:<name>. A host on this machine omits all three.")
// caller and user retain diagnostic descriptions including proof decorations;
// they are not stable identifiers or authorization proof.
struct Ask {
 1: required timestamp at
 2: required string caller
 3: required string user
 4: required string model
 5: required string family
 6: required string verdict
 7: required string host
}(unknown_fields="grant")
struct HostsSnapshot {
 1: required Observation observation
 2: required list<HostState> hosts
 3: required list<string> doubled
 4: required list<Ask> asked
}(unknown_fields="grant")
// Verdict strings remain open to future values; current provider vocabulary follows.
// hosted names a permitted hosted host, ranked after resident and would-load
// hosts on this machine; its Decision.endpoint is empty, because the service
// performs hosted calls and no endpoint or key reaches the caller. no-host
// means no servable host serves the requested profile, or the hosts holding
// the model serve it only for other profiles.
const list<string> verdicts = ["resident","would-load","hosted","unservable","not-here","unparseable","unauthorised","no-host"]
// Wire kinds a hosted host speaks. The seed is open: a provider serving another
// wire names it <owner>/<name>@<n>, and a hosted host entry naming a wire no
// provider in the receiving runtime serves is listed up false with why
// unsupported_wire:<kind>. Registration is the runtime's host management call.
// oa-remote@1 is another runtime reached over the mutual-TLS remote transport:
// its router@1 Models and Hosts are read there and chat@1 is delegated there.
const list<string> wire_kinds = ["openai-compatible","anthropic-messages","deepgram-prerecorded","elevenlabs-stream","stability-v2beta","fal-queue","replicate-predictions","openai-realtime","oa-remote@1"]
// The consumer contract name the router service gives abstraction.credentials
// applier@1 when it applies a hosted host's credential to a listing request.
const list<string> credential_consumers = ["abstraction.router/router@1"]
struct Decision {
 1: required string asked
 2: required string family
 3: required string verdict
 4: required string host
 5: required string model
 6: required string endpoint
 7: required list<string> installed_on
 8: optional HostAllowance authorised(omit="absent")
 9: required list<string> withheld
 10: required i64 loads
 11: required string why
}(unknown_fields="grant")
struct PickResult {
 1: required Observation observation
 2: required Decision decision
}(unknown_fields="grant")
enum Placement {
 1: local
 2: hosted
 3: any
}(unknown="refuse",reader="act")
struct OfferRequest {
 1: required string profile
 2: required Placement placement
 3: required i64 reserve
 4: required bool fresh
}(unknown_fields="refuse",doc="profile is a profiles member or <owner>/<name>@<n>; empty is chat. placement local counts servers on this machine, hosted counts hosted servers and remote runtimes, any counts both. reserve is the output budget in tokens the caller plans to keep for the answer; 0 keeps none. fresh surveys the servers first.")
enum ContextSource {
 1: effective
 2: maximum
 3: default
 4: unknown
 5: none
}(unknown="grant",reader="validate")
struct Offer {
 1: required string family
 2: required list<string> names
 3: required list<string> servers
 4: required i64 context
 5: required ContextSource context_source
 6: required i64 input
 7: optional list<string> unknown(omit="zero")
}(unknown_fields="grant",doc="names are the counted aliases in UTF-8 byte order; the first is the name to ask for. servers are the servers counted, in preference order. context is the smallest bound across the counted aliases, 0 when context_source is unknown or none. context_source says where the smallest bound came from: effective for a server-reported request limit, maximum for a model maximum, default for the profile's catalogue default when no server reported a length, unknown when a counted server fixes its window at load and reported none, none for a profile without a context window. input is context minus the request's reserve, 0 when nothing remains or context is 0. unknown names the aliases whose server reported no usable request limit, as <server>/<name>.")
// Offered carries current model offers. Invalid is unsupported request
// semantics; unavailable is an uncertain offer or policy decision. Forbidden
// is a receiving boundary refusal; not_permitted is the subject's policy.
enum OfferOutcome {
 1: offered
 2: invalid
 3: unavailable
 4: forbidden
 5: not_permitted
}(unknown="refuse",reader="act")
struct OfferSnapshot {
 1: required Observation observation
 2: required list<Offer> offers
 3: required OfferOutcome outcome
}(unknown_fields="grant",doc="Exactly offered carries observation and offers. Every refusal carries an empty observation and no offers; it makes no model availability claim.")
// Codes Router handlers send on the reply error channel, beside the
// dispatcher's own. A provider may pass through a code of its own.
const list<string> router_error_codes = ["internal", "invalid_request", "caller_refused", "unknown_operation", "unavailable", "forbidden"]

service Router {
 ModelsSnapshot Models(1:bool fresh)(doc="Read model families and aliases. Fresh requests survey the existing hosts.")
 HostsSnapshot Hosts(1:bool fresh)(doc="Read host residency, duplicate families and routing audit. Who holds the card is abstraction.resource/table@1, outside this subset.")
 PickResult Pick(1:PickRequest request)(doc="Choose a permitted host without loading any model. Missing allowance permits all hosts; an explicit empty allowance permits none. Attribution comes from the bound caller.")
 OfferSnapshot Offer(1:OfferRequest request)(doc="List the families a caller may ask for a profile, one name each and the conservative context, after the request's placement and answer reserve. Gated by abstraction.router/inventory.read.")
}(wire_name="abstraction.router/router@1",error_codes="router_error_codes",doc="Read existing model hosts and choose where a caller may ask. Decisions do not enforce access to hosts outside the router.")
