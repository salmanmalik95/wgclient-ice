package peer

import (
	"context"
	ice "github.com/pion/ice/v2"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/netbirdio/netbird/iface"
	log "github.com/sirupsen/logrus"
	"golang.zx2c4.com/wireguard/wgctrl"
	"ztnav2client/internal/proxy"
	nbStatus "ztnav2client/status"
	"ztnav2client/system"
)

// ConnConfig is a peer Connection configuration
type ConnConfig struct {

	// Key is a public key of a remote peer
	Key string
	// LocalKey is a public key of a local peer
	LocalKey string

	// StunTurn is a list of STUN and TURN URLs
	StunTurn []*ice.URL

	// InterfaceBlackList is a list of machine interfaces that should be filtered out by ICE Candidate gathering
	// (e.g. if eth0 is in the list, host candidate of this interface won't be used)
	InterfaceBlackList   []string
	DisableIPv6Discovery bool

	Timeout time.Duration

	ProxyConfig proxy.Config

	UDPMux      ice.UDPMux
	UDPMuxSrflx ice.UniversalUDPMux

	LocalWgPort int

	NATExternalIPs []string
}

// OfferAnswer represents a session establishment offer or answer
type OfferAnswer struct {
	IceCredentials IceCredentials
	// WgListenPort is a remote WireGuard listen port.
	// This field is used when establishing a direct WireGuard connection without any proxy.
	// We can set the remote peer's endpoint with this port.
	WgListenPort int

	// Version of NetBird Agent
	Version string
}

// IceCredentials ICE protocol credentials struct
type IceCredentials struct {
	UFrag string
	Pwd   string
}

type Conn struct {
	config ConnConfig
	mu     sync.Mutex

	// signalCandidate is a handler function to signal remote peer about local connection candidate
	signalCandidate func(candidate ice.Candidate) error
	// signalOffer is a handler function to signal remote peer our connection offer (credentials)
	signalOffer  func(OfferAnswer) error
	signalAnswer func(OfferAnswer) error

	// remoteOffersCh is a channel used to wait for remote credentials to proceed with the connection
	remoteOffersCh chan OfferAnswer
	// remoteAnswerCh is a channel used to wait for remote credentials answer (confirmation of our offer) to proceed with the connection
	remoteAnswerCh     chan OfferAnswer
	closeCh            chan struct{}
	ctx                context.Context
	notifyDisconnected context.CancelFunc

	agent  *ice.Agent
	status ConnStatus

	statusRecorder *nbStatus.Status

	proxy proxy.Proxy
}

// GetConf returns the connection config
func (conn *Conn) GetConf() ConnConfig {
	return conn.config
}

// UpdateConf updates the connection config
func (conn *Conn) UpdateConf(conf ConnConfig) {
	conn.config = conf
}

// NewConn creates a new not opened Conn to the remote peer.
// To establish a connection run Conn.Open
func NewConn(config ConnConfig, statusRecorder *nbStatus.Status) (*Conn, error) {
	return &Conn{
		config:         config,
		mu:             sync.Mutex{},
		status:         StatusDisconnected,
		closeCh:        make(chan struct{}),
		remoteOffersCh: make(chan OfferAnswer),
		remoteAnswerCh: make(chan OfferAnswer),
		statusRecorder: statusRecorder,
	}, nil
}

// interfaceFilter is a function passed to ICE Agent to filter out not allowed interfaces
// to avoid building tunnel over them
func interfaceFilter(blackList []string) func(string) bool {

	return func(iFace string) bool {
		for _, s := range blackList {
			if strings.HasPrefix(iFace, s) {
				log.Debugf("ignoring interface %s - it is not allowed", iFace)
				return false
			}
		}
		// look for unlisted WireGuard interfaces
		wg, err := wgctrl.New()
		if err != nil {
			log.Debugf("trying to create a wgctrl client failed with: %v", err)
		}
		defer func() {
			err := wg.Close()
			if err != nil {
				return
			}
		}()

		_, err = wg.Device(iFace)
		return err != nil
	}
}

func (conn *Conn) reCreateAgent() error {
	conn.mu.Lock()
	defer conn.mu.Unlock()

	failedTimeout := 6 * time.Second
	var err error
	agentConfig := &ice.AgentConfig{
		MulticastDNSMode: ice.MulticastDNSModeDisabled,
		NetworkTypes:     []ice.NetworkType{ice.NetworkTypeUDP4, ice.NetworkTypeUDP6},
		Urls:             conn.config.StunTurn,
		CandidateTypes:   []ice.CandidateType{ice.CandidateTypeHost, ice.CandidateTypeServerReflexive, ice.CandidateTypeRelay},
		FailedTimeout:    &failedTimeout,
		InterfaceFilter:  interfaceFilter(conn.config.InterfaceBlackList),
		UDPMux:           conn.config.UDPMux,
		UDPMuxSrflx:      conn.config.UDPMuxSrflx,
		NAT1To1IPs:       conn.config.NATExternalIPs,
	}

	if conn.config.DisableIPv6Discovery {
		agentConfig.NetworkTypes = []ice.NetworkType{ice.NetworkTypeUDP4}
	}

	conn.agent, err = ice.NewAgent(agentConfig)

	if err != nil {
		return err
	}

	err = conn.agent.OnCandidate(conn.onICECandidate)
	if err != nil {
		return err
	}

	err = conn.agent.OnConnectionStateChange(conn.onICEConnectionStateChange)
	if err != nil {
		return err
	}

	err = conn.agent.OnSelectedCandidatePairChange(conn.onICESelectedCandidatePair)
	if err != nil {
		return err
	}

	return nil
}

// Open opens connection to the remote peer starting ICE candidate gathering process.
// Blocks until connection has been closed or connection timeout.
// ConnStatus will be set accordingly
func (conn *Conn) Open() error {
	log.Debugf("trying to connect to peer %s", conn.config.Key)

	peerState := nbStatus.PeerState{PubKey: conn.config.Key}

	peerState.IP = strings.Split(conn.config.ProxyConfig.AllowedIps, "/")[0]
	peerState.ConnStatusUpdate = time.Now()
	peerState.ConnStatus = conn.status.String()

	err := conn.statusRecorder.UpdatePeerState(peerState)
	if err != nil {
		log.Warnf("erro while updating the state of peer %s,err: %v", conn.config.Key, err)
	}

	defer func() {
		err := conn.cleanup()
		if err != nil {
			log.Warnf("error while cleaning up peer connection %s: %v", conn.config.Key, err)
			return
		}
	}()

	err = conn.reCreateAgent()
	if err != nil {
		return err
	}

	err = conn.sendOffer()
	if err != nil {
		return err
	}

	log.Debugf("connection offer sent to peer %s, waiting for the confirmation", conn.config.Key)

	// Only continue once we got a connection confirmation from the remote peer.
	// The connection timeout could have happened before a confirmation received from the remote.
	// The connection could have also been closed externally (e.g. when we received an update from the management that peer shouldn't be connected)
	var remoteOfferAnswer OfferAnswer
	select {
	case remoteOfferAnswer = <-conn.remoteOffersCh:
		// received confirmation from the remote peer -> ready to proceed
		err = conn.sendAnswer()
		if err != nil {
			return err
		}
	case remoteOfferAnswer = <-conn.remoteAnswerCh:
	case <-time.After(conn.config.Timeout):
		return NewConnectionTimeoutError(conn.config.Key, conn.config.Timeout)
	case <-conn.closeCh:
		// closed externally
		return NewConnectionClosedError(conn.config.Key)
	}

	log.Debugf("received connection confirmation from peer %s running version %s and with remote WireGuard listen port %d",
		conn.config.Key, remoteOfferAnswer.Version, remoteOfferAnswer.WgListenPort)

	// at this point we received offer/answer and we are ready to gather candidates
	conn.mu.Lock()
	conn.status = StatusConnecting
	conn.ctx, conn.notifyDisconnected = context.WithCancel(context.Background())
	defer conn.notifyDisconnected()
	conn.mu.Unlock()

	peerState = nbStatus.PeerState{PubKey: conn.config.Key}

	peerState.ConnStatus = conn.status.String()
	peerState.ConnStatusUpdate = time.Now()
	err = conn.statusRecorder.UpdatePeerState(peerState)
	if err != nil {
		log.Warnf("erro while updating the state of peer %s,err: %v", conn.config.Key, err)
	}

	err = conn.agent.GatherCandidates()
	if err != nil {
		return err
	}

	// will block until connection succeeded
	// but it won't release if ICE Agent went into Disconnected or Failed state,
	// so we have to cancel it with the provided context once agent detected a broken connection
	isControlling := conn.config.LocalKey > conn.config.Key
	var remoteConn *ice.Conn
	if isControlling {
		remoteConn, err = conn.agent.Dial(conn.ctx, remoteOfferAnswer.IceCredentials.UFrag, remoteOfferAnswer.IceCredentials.Pwd)
	} else {
		remoteConn, err = conn.agent.Accept(conn.ctx, remoteOfferAnswer.IceCredentials.UFrag, remoteOfferAnswer.IceCredentials.Pwd)
	}
	if err != nil {
		return err
	}

	// dynamically set remote WireGuard port is other side specified a different one from the default one
	remoteWgPort := iface.DefaultWgPort
	if remoteOfferAnswer.WgListenPort != 0 {
		remoteWgPort = remoteOfferAnswer.WgListenPort
	}
	// the ice connection has been established successfully so we are ready to start the proxy
	err = conn.startProxy(remoteConn, remoteWgPort)
	if err != nil {
		return err
	}

	if conn.proxy.Type() == proxy.TypeNoProxy {
		host, _, _ := net.SplitHostPort(remoteConn.LocalAddr().String())
		rhost, _, _ := net.SplitHostPort(remoteConn.RemoteAddr().String())
		// direct Wireguard connection
		log.Infof("directly connected to peer %s [laddr <-> raddr] [%s:%d <-> %s:%d]", conn.config.Key, host, conn.config.LocalWgPort, rhost, remoteWgPort)
	} else {
		log.Infof("connected to peer %s [laddr <-> raddr] [%s <-> %s]", conn.config.Key, remoteConn.LocalAddr().String(), remoteConn.RemoteAddr().String())
	}

	// wait until connection disconnected or has been closed externally (upper layer, e.g. engine)
	select {
	case <-conn.closeCh:
		// closed externally
		return NewConnectionClosedError(conn.config.Key)
	case <-conn.ctx.Done():
		// disconnected from the remote peer
		return NewConnectionDisconnectedError(conn.config.Key)
	}
}

// useProxy determines whether a direct connection (without a go proxy) is possible
// There are 3 cases: one of the peers has a public IP or both peers are in the same private network
// Please note, that this check happens when peers were already able to ping each other using ICE layer.
func shouldUseProxy(pair *ice.CandidatePair) bool {
	remoteIP := net.ParseIP(pair.Remote.Address())
	myIp := net.ParseIP(pair.Local.Address())
	remoteIsPublic := IsPublicIP(remoteIP)
	myIsPublic := IsPublicIP(myIp)

	if pair.Local.Type() == ice.CandidateTypeRelay || pair.Remote.Type() == ice.CandidateTypeRelay {
		return true
	}

	//one of the hosts has a public IP
	if remoteIsPublic && pair.Remote.Type() == ice.CandidateTypeHost {
		return false
	}
	if myIsPublic && pair.Local.Type() == ice.CandidateTypeHost {
		return false
	}

	if pair.Local.Type() == ice.CandidateTypeHost && pair.Remote.Type() == ice.CandidateTypeHost {
		if !remoteIsPublic && !myIsPublic {
			//both hosts are in the same private network
			return false
		}
	}

	return true
}

// IsPublicIP indicates whether IP is public or not.
func IsPublicIP(ip net.IP) bool {
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsPrivate() {
		return false
	}
	return true
}

// startProxy starts proxying traffic from/to local Wireguard and sets connection status to StatusConnected
func (conn *Conn) startProxy(remoteConn net.Conn, remoteWgPort int) error {
	conn.mu.Lock()
	defer conn.mu.Unlock()

	var pair *ice.CandidatePair
	pair, err := conn.agent.GetSelectedCandidatePair()
	if err != nil {
		return err
	}

	peerState := nbStatus.PeerState{PubKey: conn.config.Key}
	useProxy := shouldUseProxy(pair)
	var p proxy.Proxy
	if useProxy {
		p = proxy.NewWireguardProxy(conn.config.ProxyConfig)
		peerState.Direct = false
	} else {
		p = proxy.NewNoProxy(conn.config.ProxyConfig, remoteWgPort)
		peerState.Direct = true
	}
	conn.proxy = p
	err = p.Start(remoteConn)
	if err != nil {
		return err
	}

	conn.status = StatusConnected

	peerState.ConnStatus = conn.status.String()
	peerState.ConnStatusUpdate = time.Now()
	peerState.LocalIceCandidateType = pair.Local.Type().String()
	peerState.RemoteIceCandidateType = pair.Remote.Type().String()
	if pair.Local.Type() == ice.CandidateTypeRelay || pair.Remote.Type() == ice.CandidateTypeRelay {
		peerState.Relayed = true
	}

	err = conn.statusRecorder.UpdatePeerState(peerState)
	if err != nil {
		log.Warnf("unable to save peer's state, got error: %v", err)
	}

	return nil
}

// cleanup closes all open resources and sets status to StatusDisconnected
func (conn *Conn) cleanup() error {
	log.Debugf("trying to cleanup %s", conn.config.Key)
	conn.mu.Lock()
	defer conn.mu.Unlock()

	if conn.agent != nil {
		err := conn.agent.Close()
		if err != nil {
			return err
		}
		conn.agent = nil
	}

	if conn.proxy != nil {
		err := conn.proxy.Close()
		if err != nil {
			return err
		}
		conn.proxy = nil
	}

	if conn.notifyDisconnected != nil {
		conn.notifyDisconnected()
		conn.notifyDisconnected = nil
	}

	conn.status = StatusDisconnected

	peerState := nbStatus.PeerState{PubKey: conn.config.Key}
	peerState.ConnStatus = conn.status.String()
	peerState.ConnStatusUpdate = time.Now()

	err := conn.statusRecorder.UpdatePeerState(peerState)
	if err != nil {
		// pretty common error because by that time Engine can already remove the peer and status won't be available.
		//todo rethink status updates
		log.Debugf("error while updating peer's %s state, err: %v", conn.config.Key, err)
	}

	log.Debugf("cleaned up connection to peer %s", conn.config.Key)

	return nil
}

// SetSignalOffer sets a handler function to be triggered by Conn when a new connection offer has to be signalled to the remote peer
func (conn *Conn) SetSignalOffer(handler func(offer OfferAnswer) error) {
	conn.signalOffer = handler
}

// SetSignalAnswer sets a handler function to be triggered by Conn when a new connection answer has to be signalled to the remote peer
func (conn *Conn) SetSignalAnswer(handler func(answer OfferAnswer) error) {
	conn.signalAnswer = handler
}

// SetSignalCandidate sets a handler function to be triggered by Conn when a new ICE local connection candidate has to be signalled to the remote peer
func (conn *Conn) SetSignalCandidate(handler func(candidate ice.Candidate) error) {
	conn.signalCandidate = handler
}

// onICECandidate is a callback attached to an ICE Agent to receive new local connection candidates
// and then signals them to the remote peer
func (conn *Conn) onICECandidate(candidate ice.Candidate) {
	if candidate != nil {
		// TODO: reported port is incorrect for CandidateTypeHost, makes understanding ICE use via logs confusing as port is ignored
		log.Debugf("discovered local candidate %s", candidate.String())
		go func() {
			err := conn.signalCandidate(candidate)
			if err != nil {
				log.Errorf("failed signaling candidate to the remote peer %s %s", conn.config.Key, err)
			}
		}()
	}
}

func (conn *Conn) onICESelectedCandidatePair(c1 ice.Candidate, c2 ice.Candidate) {
	log.Debugf("selected candidate pair [local <-> remote] -> [%s <-> %s], peer %s", c1.String(), c2.String(),
		conn.config.Key)
}

// onICEConnectionStateChange registers callback of an ICE Agent to track connection state
func (conn *Conn) onICEConnectionStateChange(state ice.ConnectionState) {
	log.Debugf("peer %s ICE ConnectionState has changed to %s", conn.config.Key, state.String())
	if state == ice.ConnectionStateFailed || state == ice.ConnectionStateDisconnected {
		conn.notifyDisconnected()
	}
}

func (conn *Conn) sendAnswer() error {
	conn.mu.Lock()
	defer conn.mu.Unlock()

	localUFrag, localPwd, err := conn.agent.GetLocalUserCredentials()
	if err != nil {
		return err
	}

	log.Debugf("sending answer to %s", conn.config.Key)
	err = conn.signalAnswer(OfferAnswer{
		IceCredentials: IceCredentials{localUFrag, localPwd},
		WgListenPort:   conn.config.LocalWgPort,
		Version:        system.NetbirdVersion(),
	})
	if err != nil {
		return err
	}

	return nil
}

// sendOffer prepares local user credentials and signals them to the remote peer
func (conn *Conn) sendOffer() error {
	conn.mu.Lock()
	defer conn.mu.Unlock()

	localUFrag, localPwd, err := conn.agent.GetLocalUserCredentials()
	if err != nil {
		return err
	}
	err = conn.signalOffer(OfferAnswer{
		IceCredentials: IceCredentials{localUFrag, localPwd},
		WgListenPort:   conn.config.LocalWgPort,
		Version:        system.NetbirdVersion(),
	})
	if err != nil {
		return err
	}
	return nil
}

// Close closes this peer Conn issuing a close event to the Conn closeCh
func (conn *Conn) Close() error {
	conn.mu.Lock()
	defer conn.mu.Unlock()
	select {
	case conn.closeCh <- struct{}{}:
		return nil
	default:
		// probably could happen when peer has been added and removed right after not even starting to connect
		// todo further investigate
		// this really happens due to unordered messages coming from management
		// more importantly it causes inconsistency -> 2 Conn objects for the same peer
		// e.g. this flow:
		// update from management has peers: [1,2,3,4]
		// engine creates a Conn for peers:  [1,2,3,4] and schedules Open in ~1sec
		// before conn.Open() another update from management arrives with peers: [1,2,3]
		// engine removes peer 4 and calls conn.Close() which does nothing (this default clause)
		// before conn.Open() another update from management arrives with peers: [1,2,3,4,5]
		// engine adds a new Conn for 4 and 5
		// therefore peer 4 has 2 Conn objects
		log.Warnf("connection has been already closed or attempted closing not started coonection %s", conn.config.Key)
		return NewConnectionAlreadyClosed(conn.config.Key)
	}
}

// Status returns current status of the Conn
func (conn *Conn) Status() ConnStatus {
	conn.mu.Lock()
	defer conn.mu.Unlock()
	return conn.status
}

// OnRemoteOffer handles an offer from the remote peer and returns true if the message was accepted, false otherwise
// doesn't block, discards the message if connection wasn't ready
func (conn *Conn) OnRemoteOffer(offer OfferAnswer) bool {
	log.Debugf("OnRemoteOffer from peer %s on status %s", conn.config.Key, conn.status.String())

	select {
	case conn.remoteOffersCh <- offer:
		return true
	default:
		log.Debugf("OnRemoteOffer skipping message from peer %s on status %s because is not ready", conn.config.Key, conn.status.String())
		// connection might not be ready yet to receive so we ignore the message
		return false
	}
}

// OnRemoteAnswer handles an offer from the remote peer and returns true if the message was accepted, false otherwise
// doesn't block, discards the message if connection wasn't ready
func (conn *Conn) OnRemoteAnswer(answer OfferAnswer) bool {
	log.Debugf("OnRemoteAnswer from peer %s on status %s", conn.config.Key, conn.status.String())

	select {
	case conn.remoteAnswerCh <- answer:
		return true
	default:
		// connection might not be ready yet to receive so we ignore the message
		log.Debugf("OnRemoteAnswer skipping message from peer %s on status %s because is not ready", conn.config.Key, conn.status.String())
		return false
	}
}

// OnRemoteCandidate Handles ICE connection Candidate provided by the remote peer.
func (conn *Conn) OnRemoteCandidate(candidate ice.Candidate) {
	log.Debugf("OnRemoteCandidate from peer %s -> %s", conn.config.Key, candidate.String())
	go func() {
		conn.mu.Lock()
		defer conn.mu.Unlock()

		if conn.agent == nil {
			return
		}

		err := conn.agent.AddRemoteCandidate(candidate)
		if err != nil {
			log.Errorf("error while handling remote candidate from peer %s", conn.config.Key)
			return
		}
	}()
}

func (conn *Conn) GetKey() string {
	return conn.config.Key
}
