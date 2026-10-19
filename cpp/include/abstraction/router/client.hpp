#pragma once
#include <abstraction/router/rec.h>
#include <abstraction/ipc/frame.hpp>
#include <abstraction/facade/bootstrap.hpp>
#include <cstdlib>
#include <optional>

namespace abstraction::router {

inline std::string default_endpoint() {
    if (const char* value = std::getenv("ABSTRACTION_ROUTER_ENDPOINT")) {
        if (*value) return value;
    }
    return abstraction::facade::bootstrap::installed_endpoint("router-v1");
}

// Client only: all inventory and selection behavior remains in the Go provider.
class Client {
public:
    explicit Client(std::string endpoint = default_endpoint()) : endpoint_(std::move(endpoint)) {}
    // Explicit operation scope; copies retain the same absolute deadline.
    Client(std::string endpoint, ipc::Deadline deadline)
        : endpoint_(std::move(endpoint)), deadline_(deadline) {}

    ModelsSnapshot models(bool fresh = false) const {
        auto transport = deadline_ ? ipc::FrameTransport(endpoint_, *deadline_, ipc::kDefaultMaxFrame)
                                   : ipc::FrameTransport(endpoint_, 10000, ipc::kDefaultMaxFrame);
        transport = transport.with_cancellation(cancellation_).with_server_expectation(server_);
        RouterClient<ipc::FrameTransport> client(transport);
        return client.models(fresh);
    }
    HostsSnapshot hosts(bool fresh = false) const {
        auto transport = deadline_ ? ipc::FrameTransport(endpoint_, *deadline_, ipc::kDefaultMaxFrame)
                                   : ipc::FrameTransport(endpoint_, 10000, ipc::kDefaultMaxFrame);
        transport = transport.with_cancellation(cancellation_).with_server_expectation(server_);
        RouterClient<ipc::FrameTransport> client(transport);
        return client.hosts(fresh);
    }
    PickResult pick(const PickRequest& request) const {
        auto transport = deadline_ ? ipc::FrameTransport(endpoint_, *deadline_, ipc::kDefaultMaxFrame)
                                   : ipc::FrameTransport(endpoint_, 10000, ipc::kDefaultMaxFrame);
        transport = transport.with_cancellation(cancellation_).with_server_expectation(server_);
        RouterClient<ipc::FrameTransport> client(transport);
        return client.pick(request);
    }
    OfferSnapshot offer(const OfferRequest& request) const {
        auto transport = deadline_ ? ipc::FrameTransport(endpoint_, *deadline_, ipc::kDefaultMaxFrame)
                                   : ipc::FrameTransport(endpoint_, 10000, ipc::kDefaultMaxFrame);
        transport = transport.with_cancellation(cancellation_).with_server_expectation(server_);
        RouterClient<ipc::FrameTransport> client(transport);
        return client.offer(request);
    }
    // Waits up to wait_ms (0..30000) for the offers to differ from cursor; the
    // call's own time limit covers that wait.
    OfferObservation observe_offer(const OfferRequest& request, const std::string& cursor, std::int64_t wait_ms) const {
        const auto budget = static_cast<uint32_t>(10000 + (wait_ms > 0 && wait_ms <= 30000 ? wait_ms : 0));
        auto transport = deadline_ ? ipc::FrameTransport(endpoint_, *deadline_, ipc::kDefaultMaxFrame)
                                   : ipc::FrameTransport(endpoint_, budget, ipc::kDefaultMaxFrame);
        transport = transport.with_cancellation(cancellation_).with_server_expectation(server_);
        RouterClient<ipc::FrameTransport> client(transport);
        return client.observe_offer(request, cursor, wait_ms);
    }
    Client with_server_expectation(std::optional<ipc::ServerExpectation> server) const {auto copy=*this;copy.server_=std::move(server);return copy;}
 Client with_cancellation(ipc::CancellationToken token) const {
        auto scoped = *this;
        scoped.cancellation_ = std::move(token);
        return scoped;
    }
private:
    std::string endpoint_;
    std::optional<ipc::Deadline> deadline_;
    ipc::CancellationToken cancellation_;
 std::optional<ipc::ServerExpectation> server_;
};
}
