# Router from C++

Read model families, inspect host residency, and choose a permitted model host.
The client uses generated typed RPC over the shared IPC runtime; the Go provider
owns discovery and routing. There is no C++ server, embedded provider or silent
fallback when the service is absent.

With development packages installed under your `CMAKE_PREFIX_PATH`:

```cmake
find_package(abstraction_router CONFIG REQUIRED)
target_link_libraries(my_app PRIVATE abstraction::router_client)
```

```cpp
#include <abstraction/router/client.hpp>

abstraction::router::Client router;
auto inventory = router.Models();
auto hosts = router.Hosts();
abstraction::router::PickRequest request;
request.model = "qwen2.5:0.5b";
auto result = router.Pick(request);
```

Handle exceptions at your application's error boundary. Generated `ServiceError`
retains `.code` and `.message`; unknown codes still mean refusal. Connection and
codec failures also throw. `Models(true)` and `Hosts(true)` refresh the provider's
survey; cached answers include `observation.cache_age_ms` and caller attribution.

`Pick` returns the provider's verdict, including `resident`, `would-load` and
`unauthorised`. It does not load anything. A denied choice has no endpoint.
By default `request.allowed` is absent and all servable hosts are eligible.
To permit none, use `request.allowed.emplace()` and leave its `hosts` empty.
To restrict selection, populate `request.allowed->hosts`. An empty allowance
never turns into an unrestricted request. This governs the router's decisions;
it cannot stop an application accessing a model host directly.

Run `openabstractions serve router-v1` from a locally built executable. Clients
and host share the `ABSTRACTION_ROUTER_ENDPOINT` override; the host also accepts
`--endpoint`. The framed router-v1 endpoint is separate from legacy router/HTTP
surfaces. The command runs a foreground Go process, not an OS service installer.

C++17 and CMake 3.16 are required. `abstraction_router` depends on
`abstraction_ipc`; CMake uses installed packages or a sibling abstraction-identity
source checkout and never downloads missing dependencies.

The current subset is Models/Hosts/Pick with residency and audit. GPU cost and
the HTTP window remain outside it. Development examples do not claim a tagged
release or all-platform conformance. [Schema](../router.thrift) ·
[Capability and legacy behavior](../README.md).

Caller `user_description` and `path_description` contain server-observed diagnostic
text, including proof decorations. They are not stable principal identifiers,
filesystem paths, or authorization evidence. The audit entries likewise contain
diagnostic caller descriptions.
