package sshxtest

import (
	"errors"
	"io"
	"net"
	"os/exec"
	"strconv"

	"golang.org/x/crypto/ssh"
)

// exitUnknown is the status of a command that did not report one.
const exitUnknown = 255

// Payloads of RFC 4254 section 7.
type (
	directPayload struct {
		Host     string
		Port     uint32
		OrigIP   string
		OrigPort uint32
	}
	forwardRequest struct {
		BindIP   string
		BindPort uint32
	}
	forwardedPayload struct {
		Addr       string
		Port       uint32
		OriginAddr string
		OriginPort uint32
	}
)

// serveSession runs the first exec request with sh -c.
func serveSession(nc ssh.NewChannel) {
	ch, requests, err := nc.Accept()
	if err != nil {
		return
	}
	defer ch.Close()
	for req := range requests {
		if req.Type != "exec" {
			_ = req.Reply(false, nil)
			continue
		}
		var payload struct{ Command string }
		if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
			_ = req.Reply(false, nil)
			return
		}
		_ = req.Reply(true, nil)
		cmd := exec.Command("sh", "-c", payload.Command)
		cmd.Stdin, cmd.Stdout, cmd.Stderr = ch, ch, ch.Stderr()
		status := 0
		if err := cmd.Run(); err != nil {
			status = exitUnknown
			var ee *exec.ExitError
			if errors.As(err, &ee) {
				status = ee.ExitCode()
			}
		}
		_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{uint32(status)}))
		return
	}
}

// serveDirect dials the target of a direct-tcpip channel (ssh -L).
func serveDirect(nc ssh.NewChannel) {
	var p directPayload
	if err := ssh.Unmarshal(nc.ExtraData(), &p); err != nil {
		_ = nc.Reject(ssh.ConnectionFailed, "bad direct-tcpip payload")
		return
	}
	out, err := net.Dial("tcp", net.JoinHostPort(p.Host, strconv.Itoa(int(p.Port))))
	if err != nil {
		_ = nc.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	ch, reqs, err := nc.Accept()
	if err != nil {
		out.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	join(ch, out)
}

// globalRequests serves tcpip-forward and cancel-tcpip-forward (ssh -R) on
// this machine's loopback.
func (s *Server) globalRequests(sc *ssh.ServerConn, reqs <-chan *ssh.Request) {
	for req := range reqs {
		var fr forwardRequest
		if ssh.Unmarshal(req.Payload, &fr) != nil {
			_ = req.Reply(false, nil)
			continue
		}
		switch req.Type {
		case "tcpip-forward":
			s.forward(sc, req, fr)
		case "cancel-tcpip-forward":
			s.mu.Lock()
			if ln, ok := s.listeners[fr.BindPort]; ok {
				ln.Close()
				delete(s.listeners, fr.BindPort)
			}
			s.mu.Unlock()
			_ = req.Reply(true, nil)
		default:
			_ = req.Reply(false, nil)
		}
	}
}

func (s *Server) forward(sc *ssh.ServerConn, req *ssh.Request, fr forwardRequest) {
	ln, err := net.Listen("tcp", net.JoinHostPort(fr.BindIP, strconv.Itoa(int(fr.BindPort))))
	if err != nil {
		_ = req.Reply(false, nil)
		return
	}
	port := uint32(ln.Addr().(*net.TCPAddr).Port)
	s.mu.Lock()
	s.listeners[port] = ln
	s.mu.Unlock()
	_ = req.Reply(true, ssh.Marshal(struct{ Port uint32 }{port}))
	go func() {
		for {
			in, err := ln.Accept()
			if err != nil {
				return
			}
			go openForwarded(sc, in, fr.BindIP, port)
		}
	}()
}

// openForwarded carries one connection to a remote-forward port back to the
// client as a forwarded-tcpip channel.
func openForwarded(sc *ssh.ServerConn, in net.Conn, bindIP string, port uint32) {
	orig := in.RemoteAddr().(*net.TCPAddr)
	payload := ssh.Marshal(forwardedPayload{Addr: bindIP, Port: port, OriginAddr: orig.IP.String(), OriginPort: uint32(orig.Port)})
	ch, reqs, err := sc.OpenChannel("forwarded-tcpip", payload)
	if err != nil {
		in.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	join(ch, in)
}

// join copies both ways until either side ends, then closes both.
func join(a io.ReadWriteCloser, b io.ReadWriteCloser) {
	defer a.Close()
	defer b.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(a, b); done <- struct{}{} }()
	go func() { _, _ = io.Copy(b, a); done <- struct{}{} }()
	<-done
}
