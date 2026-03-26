/// GET /v1/vault/status — Guardian status info.
const std = @import("std");
const response = @import("response.zig");
const router = @import("router.zig");

pub fn handle(writer: anytype, ctx: *const router.RouteContext) !void {
    var buf: [512]u8 = undefined;
    const body = std.fmt.bufPrint(&buf,
        \\{{"status":"ok","version":"0.1.0","data_dir":"{s}","client_port":{d},"peer_port":{d}}}
    , .{ ctx.data_dir, ctx.client_port, ctx.peer_port }) catch {
        return response.internalError(writer);
    };
    try response.jsonOk(writer, body);
}
