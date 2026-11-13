package guard

import (
	"encoding/json"
	"math"
	"time"
)

// BlockHeader is the response header Caddy adds to its own refusals, so the
// access log tells them apart from answers of control.
const BlockHeader = "X-Dropsite-Block"

// Values of BlockHeader.
const (
	BlockRate = "rate" // the global rate limit answered 429
	BlockBan  = "ban"  // a ban marker answered 429
)

// Access is the part of an access-log line the ban detector needs.
type Access struct {
	Time   time.Time
	IP     string // canonical client address
	Status int
	Block  string // value of BlockHeader, "" for anything else
}

type accessLine struct {
	TS      float64 `json:"ts"`
	Request struct {
		ClientIP string `json:"client_ip"`
		RemoteIP string `json:"remote_ip"`
	} `json:"request"`
	Status      int                 `json:"status"`
	RespHeaders map[string][]string `json:"resp_headers"`
}

// ParseAccess decodes one line of Caddy's JSON access log; ok is false for
// anything that is not a request entry with a client address.
func ParseAccess(line []byte) (Access, bool) {
	var l accessLine
	if err := json.Unmarshal(line, &l); err != nil || l.TS <= 0 || l.Status == 0 {
		return Access{}, false
	}
	ip := l.Request.ClientIP
	if ip == "" {
		ip = l.Request.RemoteIP
	}
	ip = CanonicalIP(ip)
	if ip == "" {
		return Access{}, false
	}
	sec, frac := math.Modf(l.TS)
	a := Access{Time: time.Unix(int64(sec), int64(frac*1e9)).UTC(), IP: ip, Status: l.Status}
	if v := l.RespHeaders[BlockHeader]; len(v) > 0 {
		a.Block = v[0]
	}
	return a, true
}
