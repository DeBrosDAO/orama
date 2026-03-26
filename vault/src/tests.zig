/// Test entry point — imports all test modules so `zig build test` runs everything.
const std = @import("std");

// SSS core
comptime {
    _ = @import("sss/field.zig");
    _ = @import("sss/polynomial.zig");
    _ = @import("sss/split.zig");
    _ = @import("sss/combine.zig");
    _ = @import("sss/commitment.zig");
    _ = @import("sss/reshare.zig");
    _ = @import("sss/test_cross_platform.zig");
}

// Crypto wrappers
comptime {
    _ = @import("crypto/aes.zig");
    _ = @import("crypto/hmac.zig");
    _ = @import("crypto/hkdf.zig");
    _ = @import("crypto/secure_mem.zig");
    _ = @import("crypto/pq_kem.zig");
    _ = @import("crypto/pq_sig.zig");
    _ = @import("crypto/hybrid.zig");
}

// Storage
comptime {
    _ = @import("storage/file_store.zig");
    _ = @import("storage/vault_store.zig");
    _ = @import("storage/migrate_v1_v2.zig");
}

// Auth
comptime {
    _ = @import("auth/challenge.zig");
    _ = @import("auth/session.zig");
}

// Membership
comptime {
    _ = @import("membership/node_list.zig");
    _ = @import("membership/quorum.zig");
    _ = @import("membership/discovery.zig");
}

// Peer protocol
comptime {
    _ = @import("peer/protocol.zig");
    _ = @import("peer/heartbeat.zig");
    _ = @import("peer/verify.zig");
    _ = @import("peer/listener.zig");
    _ = @import("peer/repair.zig");
}

// Guardian
comptime {
    _ = @import("guardian.zig");
}

// Config
comptime {
    _ = @import("config.zig");
}

// Server
comptime {
    _ = @import("server/router.zig");
    _ = @import("server/response.zig");
    _ = @import("server/handler_health.zig");
    _ = @import("server/handler_status.zig");
    _ = @import("server/handler_guardians.zig");
    _ = @import("server/handler_push.zig");
    _ = @import("server/handler_pull.zig");
    _ = @import("server/handler_auth.zig");
    _ = @import("server/handler_secrets.zig");
    _ = @import("server/listener.zig");
}

// Integration tests
comptime {
    _ = @import("test_integration.zig");
}

test "all tests imported" {
    // This test exists solely to verify the test harness runs.
    try std.testing.expect(true);
}
