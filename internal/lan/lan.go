// Package lan finds the other computer on the local network with small UDP
// broadcasts, so nobody has to type an IP address.
package lan

import (
	"encoding/json"
	"net"

	"clipsync/internal/names"
)

const (
	appName = "clipsync"
	version = 1

	KindHere = "here" // "I am running and paired"
	KindPair = "pair" // "I am showing a pairing code"
)

// Beacon is an unauthenticated announcement. It only tells the receiver
// where to connect; the connection itself is authenticated.
type Beacon struct {
	App  string `json:"app"`
	V    int    `json:"v"`
	Kind string `json:"t"`
	ID   string `json:"id"`
	Name string `json:"name"`
	Port int    `json:"port"`
}

func NewBeacon(kind, id, name string, port int) Beacon {
	return Beacon{App: appName, V: version, Kind: kind, ID: id, Name: name, Port: port}
}

// Listen receives beacons on port.
func Listen(port int) (*net.UDPConn, error) {
	return net.ListenUDP("udp4", &net.UDPAddr{Port: port})
}

// Sender is a socket for sending beacons without taking the listening port.
func Sender() (*net.UDPConn, error) {
	return net.ListenUDP("udp4", &net.UDPAddr{})
}

// Broadcast sends b to every local IPv4 network.
func Broadcast(conn *net.UDPConn, port int, b Beacon) error {
	data, err := json.Marshal(b)
	if err != nil {
		return err
	}
	var lastErr error
	sent := false
	for _, a := range broadcastAddrs(port) {
		if _, err := conn.WriteToUDP(data, a); err != nil {
			lastErr = err
		} else {
			sent = true
		}
	}
	if !sent {
		return lastErr
	}
	return nil
}

// Read waits for the next datagram. Anything that is not a valid beacon
// comes back as a zero Beacon with a nil error, for the caller to skip.
func Read(conn *net.UDPConn) (Beacon, net.IP, error) {
	buf := make([]byte, 1500)
	n, from, err := conn.ReadFromUDP(buf)
	if err != nil {
		return Beacon{}, nil, err
	}
	var b Beacon
	if json.Unmarshal(buf[:n], &b) != nil || b.App != appName || b.V != version ||
		b.ID == "" || b.Port <= 0 || b.Port > 65535 {
		return Beacon{}, from.IP, nil
	}
	b.Name = names.Clean(b.Name)
	return b, from.IP, nil
}

// broadcastAddrs is 255.255.255.255 plus each interface's own broadcast
// address, since the former only leaves through one interface.
func broadcastAddrs(port int) []*net.UDPAddr {
	out := []*net.UDPAddr{{IP: net.IPv4bcast, Port: port}}
	for _, ipn := range localNets() {
		ip := ipn.IP.To4()
		mask := ipn.Mask
		if len(mask) == net.IPv6len {
			mask = mask[12:]
		}
		if len(mask) != net.IPv4len {
			continue
		}
		b := make(net.IP, net.IPv4len)
		for i := range b {
			b[i] = ip[i] | ^mask[i]
		}
		out = append(out, &net.UDPAddr{IP: b, Port: port})
	}
	return out
}

// LocalIPv4 lists this computer's LAN addresses, for showing to the user.
func LocalIPv4() []string {
	var out []string
	for _, ipn := range localNets() {
		out = append(out, ipn.IP.String())
	}
	return out
}

// localNets is every IPv4 network on an up, non-loopback interface that
// supports broadcast (which skips most VPN tunnels).
func localNets() []*net.IPNet {
	var out []*net.IPNet
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || ifc.Flags&net.FlagBroadcast == 0 {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() == nil || ipn.IP.IsLinkLocalUnicast() {
				continue
			}
			out = append(out, &net.IPNet{IP: ipn.IP.To4(), Mask: ipn.Mask})
		}
	}
	return out
}
