package chatproto

import "encoding/json"

// RemoteControlState is where the Remote Control bridge of a session stands.
type RemoteControlState string

// Remote Control states. Connecting covers the time between the host asking for
// the bridge and claude reporting it connected.
const (
	RemoteOff          RemoteControlState = "off"
	RemoteConnecting   RemoteControlState = "connecting"
	RemoteConnected    RemoteControlState = "connected"
	RemoteReconnecting RemoteControlState = "reconnecting"
	RemoteFailed       RemoteControlState = "failed"
)

// RemoteControl is the Remote Control bridge of a session, which lets
// claude.ai/code or the Claude app drive it. URL is the session's claude.ai/code
// address, set once claude has accepted the request. Detail is the reason when
// State is RemoteFailed.
type RemoteControl struct {
	State  RemoteControlState `json:"state"`
	URL    string             `json:"url"`
	Detail string             `json:"detail"`
}

// bridgeState maps a system/bridge_state value to a RemoteControlState. Claude's
// ready means the bridge is registered but not connected, so it reads as
// connecting. A state this package does not know reports false.
func bridgeState(s string) (RemoteControlState, bool) {
	switch s {
	case "ready":
		return RemoteConnecting, true
	case "connected":
		return RemoteConnected, true
	case "reconnecting":
		return RemoteReconnecting, true
	case "failed":
		return RemoteFailed, true
	}
	return "", false
}

// remoteEnabled reads the enabled flag of a remote_control request.
func remoteEnabled(request json.RawMessage) bool {
	var w struct {
		Enabled bool `json:"enabled"`
	}
	return json.Unmarshal(request, &w) == nil && w.Enabled
}

// answerRemote folds claude's answer to the remote_control request that asked
// for enabled.
func (f *Folder) answerRemote(r ControlResponse, enabled bool) {
	switch {
	case !enabled && r.OK:
		f.remote = RemoteControl{State: RemoteOff}
	case enabled && r.OK:
		var body struct {
			SessionURL string `json:"session_url"`
		}
		if json.Unmarshal(r.Response, &body) == nil {
			f.remote.URL = body.SessionURL
		}
	case enabled:
		f.remote.State = RemoteFailed
		if r.Error != "" {
			f.remote.Detail = r.Error
		}
	}
}
