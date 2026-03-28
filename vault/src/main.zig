const std = @import("std");
const config = @import("config.zig");
const log = @import("log.zig");
const listener = @import("server/listener.zig");
const router = @import("server/router.zig");
const guardian_mod = @import("guardian.zig");
const heartbeat = @import("peer/heartbeat.zig");
const posix = std.posix;

/// Global running flag — true while the server should keep running.
/// Signal handlers set this to false to trigger graceful shutdown.
var running_flag = std.atomic.Value(bool).init(true);

fn signalHandler(sig: i32) callconv(.c) void {
    _ = sig;
    running_flag.store(false, .release);
}

pub fn main() !void {
    var gpa = std.heap.GeneralPurposeAllocator(.{}){};
    defer _ = gpa.deinit();
    const allocator = gpa.allocator();

    // Parse CLI args
    const args = try std.process.argsAlloc(allocator);
    defer std.process.argsFree(allocator, args);

    var config_path: []const u8 = "/opt/orama/.orama/data/vault/vault.yaml";
    var data_dir_override: ?[]const u8 = null;
    var port_override: ?u16 = null;
    var bind_override: ?[]const u8 = null;
    var i: usize = 1;
    while (i < args.len) : (i += 1) {
        if (std.mem.eql(u8, args[i], "--config") and i + 1 < args.len) {
            config_path = args[i + 1];
            i += 1;
        } else if (std.mem.eql(u8, args[i], "--data-dir") and i + 1 < args.len) {
            data_dir_override = args[i + 1];
            i += 1;
        } else if (std.mem.eql(u8, args[i], "--port") and i + 1 < args.len) {
            port_override = std.fmt.parseInt(u16, args[i + 1], 10) catch {
                log.err("invalid port: {s}", .{args[i + 1]});
                std.process.exit(1);
            };
            i += 1;
        } else if (std.mem.eql(u8, args[i], "--bind") and i + 1 < args.len) {
            bind_override = args[i + 1];
            i += 1;
        } else if (std.mem.eql(u8, args[i], "--help") or std.mem.eql(u8, args[i], "-h")) {
            printUsage();
            return;
        } else if (std.mem.eql(u8, args[i], "--version") or std.mem.eql(u8, args[i], "-v")) {
            std.debug.print("vault-guardian v0.1.0\n", .{});
            return;
        }
    }

    log.info("vault-guardian v0.1.0 starting", .{});
    log.info("config: {s}", .{config_path});

    // Load config
    var cfg = config.loadOrDefault(allocator, config_path) catch |err| {
        log.err("failed to load config from {s}: {}", .{ config_path, err });
        std.process.exit(1);
    };
    defer cfg.deinit();

    // Apply CLI overrides
    if (data_dir_override) |d| cfg.data_dir = d;
    if (port_override) |p| cfg.client_port = p;
    if (bind_override) |b| cfg.listen_address = b;

    log.info("listening on {s}:{d} (client)", .{ cfg.listen_address, cfg.client_port });
    log.info("listening on {s}:{d} (peer)", .{ cfg.listen_address, cfg.peer_port });
    log.info("data directory: {s}", .{cfg.data_dir});

    // Ensure data directory exists
    std.fs.cwd().makePath(cfg.data_dir) catch |err| {
        if (err != error.PathAlreadyExists) {
            log.err("failed to create data directory {s}: {}", .{ cfg.data_dir, err });
            std.process.exit(1);
        }
    };

    // Initialize Guardian
    var guardian = guardian_mod.Guardian.init(allocator, cfg) catch |err| {
        log.err("failed to initialize guardian: {}", .{err});
        std.process.exit(1);
    };
    defer guardian.deinit();

    log.info("guardian initialized: {d} nodes, {d} shares", .{ guardian.nodes.nodes.len, guardian.share_count });

    // Install signal handlers for graceful shutdown
    installSignalHandlers();

    log.info("guardian ready — starting HTTP server", .{});

    // Start heartbeat thread
    var hb_thread: ?std.Thread = blk: {
        break :blk std.Thread.spawn(.{}, heartbeatLoop, .{ &guardian, &running_flag }) catch |err| {
            log.warn("failed to start heartbeat thread: {}, running without heartbeat", .{err});
            break :blk null;
        };
    };
    _ = &hb_thread;

    // Start HTTP server (blocks until shutdown)
    const ctx = router.RouteContext{
        .data_dir = cfg.data_dir,
        .listen_address = cfg.listen_address,
        .client_port = cfg.client_port,
        .peer_port = cfg.peer_port,
        .allocator = allocator,
        .guardian = &guardian,
    };
    listener.serve(ctx, &running_flag) catch |err| {
        log.err("server failed: {}", .{err});
        std.process.exit(1);
    };

    // Wait for heartbeat thread to finish
    if (hb_thread) |t| {
        t.join();
    }

    log.info("vault-guardian shutdown complete", .{});
}

fn installSignalHandlers() void {
    const act = posix.Sigaction{
        .handler = .{ .handler = signalHandler },
        .mask = .{0},
        .flags = 0,
    };

    posix.sigaction(posix.SIG.TERM, &act, null);
    posix.sigaction(posix.SIG.INT, &act, null);
}

fn heartbeatLoop(guardian: *guardian_mod.Guardian, running: *std.atomic.Value(bool)) void {
    const interval_ns: u64 = @intCast(@as(i128, heartbeat.HEARTBEAT_INTERVAL_NS));

    while (running.load(.acquire)) {
        // Evaluate node states
        heartbeat.evaluateNodeStates(&guardian.nodes);

        // Refresh share count
        guardian.refreshShareCount();

        // Send heartbeats to all peers
        for (guardian.nodes.nodes, 0..) |node, idx| {
            if (guardian.nodes.self_index != null and idx == guardian.nodes.self_index.?) continue;
            if (node.state == .dead) continue;

            // Parse self IP for heartbeat
            var self_ip: [4]u8 = .{ 127, 0, 0, 1 };
            if (guardian.nodes.self_index) |si| {
                const self_addr = guardian.nodes.nodes[si].address;
                if (std.net.Ip4Address.parse(self_addr, 0)) |addr| {
                    self_ip = @bitCast(addr.sa.addr);
                } else |_| {}
            }

            _ = heartbeat.sendHeartbeat(
                node.address,
                guardian.cfg.peer_port,
                self_ip,
                guardian.cfg.peer_port,
                guardian.share_count,
            );
        }

        // Sleep for heartbeat interval
        std.Thread.sleep(interval_ns);
    }
}

fn printUsage() void {
    std.debug.print(
        \\Usage: vault-guardian [OPTIONS]
        \\
        \\Orama Vault Guardian — distributed secret share storage
        \\
        \\Options:
        \\  --config <path>   Path to config file (default: /opt/orama/.orama/data/vault/vault.yaml)
        \\  --data-dir <path> Override data directory
        \\  --port <port>     Override client port (default: 7500)
        \\  --bind <addr>     Override bind address (default: 0.0.0.0)
        \\  --help, -h        Show this help
        \\  --version, -v     Show version
        \\
    , .{});
}
