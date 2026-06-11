-- =============================================================================
-- 030_webrtc_stealth.sql
--
-- Stealth TURNS-over-443 per namespace — feat-124 (censorship-resistant
-- calling). When stealth_enabled is true the namespace's TURN servers carry a
-- second TLS certificate for the neutral stealth hostname
-- (cdn-<hash>.<base-domain>, derived via turn.StealthHostForNamespace), the
-- SNI router forwards :443 ClientHellos for that hostname to the TURN TLS
-- listener, and turn.credentials advertises `turns:<stealth-host>:443` as the
-- final rung of the ICE URI ladder.
--
-- Default false → backward compatible: existing WebRTC namespaces keep the
-- baseline udp:3478 / tcp:3478 / turns:5349 URIs unchanged.
-- =============================================================================

ALTER TABLE namespace_webrtc_config ADD COLUMN stealth_enabled BOOLEAN DEFAULT FALSE;
