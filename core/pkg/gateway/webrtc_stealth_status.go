package gateway

import (
	"errors"
	"net/http"
)

// Codes the namespace package puts on the errors it returns for a stealth
// toggle (namespace.Code*). They are matched by code, not by importing that
// package: importing it from here would close an import cycle through
// pkg/install.
const (
	codeClusterNotFound             = "cluster_not_found"
	codeWebRTCNotEnabled            = "webrtc_not_enabled"
	codeWebRTCStealthAlreadyEnabled = "webrtc_stealth_already_enabled"
	codeWebRTCStealthNotEnabled     = "webrtc_stealth_not_enabled"
)

// errorCode is the code an error in err's chain names, "" if none does.
func errorCode(err error) string {
	var coded interface{ ErrorCode() string }
	if errors.As(err, &coded) {
		return coded.ErrorCode()
	}
	return ""
}

// stealthToggleOutcome maps the result of a stealth enable/disable to the HTTP
// status and message the route answers with.
//
// Disabling stealth that is already off is the state the caller asked for, so
// it answers 200 and says so; enabling it twice, or toggling it on a namespace
// without WebRTC, is a conflict with the namespace's current state (409), not
// a server failure. Anything else is a real failure (500) whose detail stays in the
// log, never in the response.
func stealthToggleOutcome(enable bool, err error) (status int, message string) {
	switch {
	case err == nil && enable:
		return http.StatusOK, "WebRTC stealth enabled successfully"
	case err == nil:
		return http.StatusOK, "WebRTC stealth disabled successfully"
	case !enable && errorCode(err) == codeWebRTCStealthNotEnabled:
		return http.StatusOK, "WebRTC stealth was already disabled"
	case errorCode(err) == codeWebRTCStealthAlreadyEnabled,
		errorCode(err) == codeWebRTCStealthNotEnabled,
		errorCode(err) == codeWebRTCNotEnabled:
		return http.StatusConflict, err.Error()
	case errorCode(err) == codeClusterNotFound:
		return http.StatusNotFound, err.Error()
	default:
		return http.StatusInternalServerError, "failed to change WebRTC stealth; see the gateway log"
	}
}

// clusterStatusFailure maps an error from reading a namespace cluster's status
// to the status and message /v1/namespace/status answers with. Only a cluster
// that does not exist is a 404; a registry that could not be read says nothing
// about the cluster, so it is a 503 the client can retry, and its detail stays
// in the log.
func clusterStatusFailure(err error) (status int, message string) {
	if errorCode(err) == codeClusterNotFound {
		return http.StatusNotFound, "cluster not found"
	}
	return http.StatusServiceUnavailable, "cluster status is temporarily unavailable; retry in a few seconds, and if it persists ask the cluster operator to check the namespace registry"
}
