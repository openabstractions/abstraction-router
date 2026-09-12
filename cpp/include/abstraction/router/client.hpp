#pragma once
#include <abstraction/router/rec.h>
#include <abstraction/ipc/frame.hpp>
#include <cstdlib>
#include <optional>

namespace abstraction::router {

inline std::string default_endpoint() {
    if (const char* value = std::getenv("ABSTRACTION_ROUTER_ENDPOINT")) {
        if (*value) return value;
    }
#ifdef _WIN32
    return R"(\\.\pipe\openabstractions-router-v1)";
#else
    if (const char* value = std::getenv("XDG_RUNTIME_DIR")) {
        if (*value) return std::string(value) + "/openabstractions-router-v1.sock";
    }
    const char* temporary = std::getenv("TMPDIR");
    const char* user = std::getenv("USER");
    return std::string(temporary && *temporary ? temporary : "/tmp") +
        "/openabstractions-router-v1-" + (user ? user : "") + ".sock";
#endif
}

// Client only: all inventory and selection behavior remains in the Go provider.
class Client {
public:
    explicit Client(std::string endpoint = default_endpoint()) : endpoint_(std::move(endpoint)) {}
    // Explicit operation scope; copies retain the same absolute deadline.
    Client(std::string endpoint, ipc::Deadline deadline)
        : endpoint_(std::move(endpoint)), deadline_(deadline) {}

    ModelsSnapshot Models(bool fresh = false) const {
        auto transport = deadline_ ? ipc::FrameTransport(endpoint_, *deadline_, 1 << 20)
                                   : ipc::FrameTransport(endpoint_, 10000, 1 << 20);
        RouterClient<ipc::FrameTransport> client(transport);
        return client.Models(fresh);
    }
    HostsSnapshot Hosts(bool fresh = false) const {
        auto transport = deadline_ ? ipc::FrameTransport(endpoint_, *deadline_, 1 << 20)
                                   : ipc::FrameTransport(endpoint_, 10000, 1 << 20);
        RouterClient<ipc::FrameTransport> client(transport);
        return client.Hosts(fresh);
    }
    PickResult Pick(const PickRequest& request) const {
        auto transport = deadline_ ? ipc::FrameTransport(endpoint_, *deadline_, 1 << 20)
                                   : ipc::FrameTransport(endpoint_, 10000, 1 << 20);
        RouterClient<ipc::FrameTransport> client(transport);
        return client.Pick(request);
    }
private:
    std::string endpoint_;
    std::optional<ipc::Deadline> deadline_;
};
}
