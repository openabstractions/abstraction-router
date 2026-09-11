namespace * abstraction.router

// Framed service subset: inventory, host residency, and routing decisions.
// GPU cost and the legacy HTTP window are not part of this interface.
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
 11: trailing_bytes(stage="document")
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
}(document="true",unknown_fields="refuse")
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
}(unknown_fields="grant")
struct Family {
 1: required string family
 2: required list<Alias> names
}(unknown_fields="grant")
struct ModelsSnapshot {
 1: required Observation observation
 2: required list<Family> models
}(unknown_fields="grant")
struct HostState {
 1: required string host
 2: required string base
 3: required bool up
 4: required string why
 5: required i64 installed
 6: required list<string> resident
 7: required bool servable
}(unknown_fields="grant")
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
const list<string> verdicts = ["resident","would-load","unservable","not-here","unparseable","unauthorised"]
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
service Router {
 ModelsSnapshot Models(1:bool fresh)(doc="Read model families and aliases. Fresh requests survey the existing hosts.")
 HostsSnapshot Hosts(1:bool fresh)(doc="Read host residency, duplicate families and routing audit. GPU cost is outside this subset.")
 PickResult Pick(1:PickRequest request)(doc="Choose a permitted host without loading any model. Missing allowance permits all hosts; an explicit empty allowance permits none. Attribution comes from the bound caller.")
}(wire_name="abstraction.router/router@1",doc="Read existing model hosts and choose where a caller may ask. Decisions do not enforce access to hosts outside the router.")
