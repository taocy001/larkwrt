package collector

import (
	"net"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
	"golang.org/x/net/dns/dnsmessage"
)

// mdnsCache holds IP → hostname from passively observed mDNS traffic.
var mdnsCache = struct {
	sync.RWMutex
	m map[string]string
}{m: make(map[string]string)}

// MDNSHostname returns the last-seen mDNS hostname for an IP, or "".
func MDNSHostname(ip string) string {
	mdnsCache.RLock()
	defer mdnsCache.RUnlock()
	return mdnsCache.m[ip]
}

// StartMDNSListener joins 224.0.0.251:5353 and passively records A/AAAA
// answers to enrich device hostnames. Runs until stop is closed.
func StartMDNSListener(stop <-chan struct{}) {
	group := net.IPv4(224, 0, 0, 251)
	addr := &net.UDPAddr{IP: group, Port: 5353}

	conn, err := net.ListenMulticastUDP("udp4", nil, addr)
	if err != nil {
		log.Warn().Err(err).Msg("mdns: failed to join multicast group — device name enrichment via mDNS disabled")
		return
	}
	defer conn.Close()

	log.Info().Msg("mdns: listening on 224.0.0.251:5353")

	buf := make([]byte, 4096)
	go func() {
		<-stop
		conn.SetDeadline(time.Now())
	}()

	for {
		n, src, err := conn.ReadFromUDP(buf)
		if err != nil {
			select {
			case <-stop:
				return
			default:
				// transient read error — keep going
				continue
			}
		}
		parseMDNSPacket(buf[:n], src.IP.String())
	}
}

func parseMDNSPacket(data []byte, srcIP string) {
	var msg dnsmessage.Message
	if err := msg.Unpack(data); err != nil {
		return
	}

	// Collect all resource sections: answers + additional records
	var allRRs []dnsmessage.Resource
	allRRs = append(allRRs, msg.Answers...)
	allRRs = append(allRRs, msg.Additionals...)

	for _, rr := range allRRs {
		switch rr.Header.Type {
		case dnsmessage.TypeA:
			body, ok := rr.Body.(*dnsmessage.AResource)
			if !ok {
				continue
			}
			ip := net.IP(body.A[:]).String()
			name := stripLocal(rr.Header.Name.String())
			if name != "" {
				storeMDNS(ip, name)
			}

		case dnsmessage.TypeAAAA:
			body, ok := rr.Body.(*dnsmessage.AAAAResource)
			if !ok {
				continue
			}
			ip := net.IP(body.AAAA[:]).String()
			name := stripLocal(rr.Header.Name.String())
			if name != "" {
				storeMDNS(ip, name)
			}

		case dnsmessage.TypePTR:
			// PTR records in mDNS can carry service instance names that reveal
			// the device hostname embedded as the first label.
			// e.g. "My iPhone._tcp.local." → first label "My iPhone"
			ptr := rr.Header.Name.String()
			if strings.Contains(ptr, "._") {
				// This is a service PTR; the answer points to instance name.
				// We'll capture the source IP → first label of the PTR target.
				if body, ok := rr.Body.(*dnsmessage.PTRResource); ok {
					instanceName := firstLabel(body.PTR.String())
					if instanceName != "" && srcIP != "" {
						storeMDNS(srcIP, instanceName)
					}
				}
			}
		}
	}
}

func storeMDNS(ip, name string) {
	if ip == "" || name == "" {
		return
	}
	mdnsCache.Lock()
	mdnsCache.m[ip] = name
	mdnsCache.Unlock()
}

// stripLocal removes the trailing ".local." (or ".local") suffix and returns
// just the hostname label, or "" if the name is not useful.
func stripLocal(name string) string {
	name = strings.TrimSuffix(name, ".")
	// Only process names ending in .local
	if !strings.HasSuffix(strings.ToLower(name), ".local") {
		return ""
	}
	name = name[:len(name)-len(".local")]
	// Reject multi-label service names like "_http._tcp"
	if strings.Contains(name, ".") || strings.HasPrefix(name, "_") {
		return ""
	}
	return name
}

// firstLabel returns the first DNS label of a name (before the first dot).
func firstLabel(name string) string {
	name = strings.TrimSuffix(name, ".")
	if idx := strings.Index(name, "."); idx > 0 {
		return name[:idx]
	}
	return name
}
