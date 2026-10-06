package privhelper

// Unit names and paths the installer writes.
const (
	SocketUnitName  = "orama-privhelper.socket"
	ServiceUnitName = "orama-privhelper@.service"
)

// SocketUnit listens for requests. Accept=yes gives each connection its own
// service instance with the connection as stdin/stdout, so no daemon holds
// state between requests. Only root and the orama group can connect; the
// helper checks the peer's uid from the kernel as well, and authorises each
// request by the systemd unit the peer runs in (Authorize).
const SocketUnit = `[Unit]
Description=Orama privileged helper socket

[Socket]
ListenStream=` + SocketPath + `
SocketUser=root
SocketGroup=orama
SocketMode=0660
Accept=yes
MaxConnections=32

[Install]
WantedBy=sockets.target
`

// ServiceUnit runs one request as root.
const ServiceUnit = `[Unit]
Description=Orama privileged helper request
CollectMode=inactive-or-failed

[Service]
ExecStart=` + Path + ` serve
StandardInput=socket
StandardOutput=socket
StandardError=journal
RuntimeMaxSec=600
# Root's file access stays (writing /etc/wireguard, /etc/ufw and
# /var/lib/orama-deploy is the job); what it cannot need is taken away.
ProtectHome=yes
PrivateTmp=yes
NoNewPrivileges=yes
RestrictAddressFamilies=AF_UNIX AF_NETLINK AF_INET AF_INET6
SystemCallArchitectures=native
LockPersonality=yes
RestrictRealtime=yes
RestrictSUIDSGID=yes
`
