package internal

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"net/netip"
	"strings"
	"sync"
	"time"

	nbdns "github.com/netbirdio/netbird/dns"
	"github.com/netbirdio/netbird/route"
	"ztnav2client/internal/routemanager"
	nbstatus "ztnav2client/status"

	"github.com/netbirdio/netbird/iface"
	mgm "github.com/netbirdio/netbird/management/client"
	mgmProto "github.com/netbirdio/netbird/management/proto"
	signal "github.com/netbirdio/netbird/signal/client"
	sProto "github.com/netbirdio/netbird/signal/proto"
	"github.com/netbirdio/netbird/util"
	"github.com/pion/ice/v2"
	log "github.com/sirupsen/logrus"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"ztnav2client/internal/peer"
	"ztnav2client/internal/proxy"
)

// PeerConnectionTimeoutMax is a timeout of an initial connection attempt to a remote peer.
// E.g. this peer will wait PeerConnectionTimeoutMax for the remote peer to respond,
// if not successful then it will retry the connection attempt.
// Todo pass timeout at EnginConfig
const (
	PeerConnectionTimeoutMax = 45000 // ms
	PeerConnectionTimeoutMin = 30000 // ms
)

var ErrResetConnection = fmt.Errorf("reset connection")

// EngineConfig is a config for the Engine
type EngineConfig struct {
	WgPort      int
	WgIfaceName string

	// WgAddr is a Wireguard local address (Netbird Network IP)
	WgAddr string

	// WgPrivateKey is a Wireguard private key of our peer (it MUST never leave the machine)
	WgPrivateKey wgtypes.Key

	// IFaceBlackList is a list of network interfaces to ignore when discovering connection candidates (ICE related)
	IFaceBlackList       []string
	DisableIPv6Discovery bool

	PreSharedKey *wgtypes.Key

	// UDPMuxPort default value 0 - the system will pick an available port
	UDPMuxPort int

	// UDPMuxSrflxPort default value 0 - the system will pick an available port
	UDPMuxSrflxPort int

	// SSHKey is a private SSH key in a PEM format
	SSHKey []byte

	NATExternalIPs []string
}

// Engine is a mechanism responsible for reacting on Signal and Management stream events and managing connections to the remote peers.
type Engine struct {
	// signal is a Signal Service client
	signal signal.Client
	// mgmClient is a Management Service client
	mgmClient mgm.Client
	// peerConns is a map that holds all the peers that are known to this peer
	peerConns map[string]*peer.Conn

	// syncMsgMux is used to guarantee sequential Management Service message processing
	syncMsgMux *sync.Mutex

	config *EngineConfig
	// STUNs is a list of STUN servers used by ICE
	STUNs []*ice.URL
	// TURNs is a list of STUN servers used by ICE
	TURNs []*ice.URL

	cancel context.CancelFunc

	ctx context.Context

	wgInterface *iface.WGIface

	udpMux          ice.UDPMux
	udpMuxSrflx     ice.UniversalUDPMux
	udpMuxConn      *net.UDPConn
	udpMuxConnSrflx *net.UDPConn

	// networkSerial is the latest CurrentSerial (state ID) of the network sent by the Management service
	networkSerial uint64

	statusRecorder *nbstatus.Status

	routeManager routemanager.Manager
}

// Peer is an instance of the Connection Peer
type Peer struct {
	WgPubKey     string
	WgAllowedIps string
}

// NewEngine creates a new Connection Engine
func NewEngine(
	ctx context.Context, cancel context.CancelFunc,
	signalClient signal.Client,
	config *EngineConfig, statusRecorder *nbstatus.Status,
) *Engine {
	return &Engine{
		ctx:            ctx,
		cancel:         cancel,
		signal:         signalClient,
		peerConns:      map[string]*peer.Conn{},
		syncMsgMux:     &sync.Mutex{},
		config:         config,
		STUNs:          []*ice.URL{},
		TURNs:          []*ice.URL{},
		networkSerial:  0,
		statusRecorder: statusRecorder,
	}
}

func (e *Engine) Stop() error {
	e.syncMsgMux.Lock()
	defer e.syncMsgMux.Unlock()

	err := e.removeAllPeers()
	if err != nil {
		return err
	}

	// very ugly but we want to remove peers from the WireGuard interface first before removing interface.
	// Removing peers happens in the conn.CLose() asynchronously
	time.Sleep(500 * time.Millisecond)

	log.Debugf("removing Netbird interface %s", e.config.WgIfaceName)
	if e.wgInterface.Interface != nil {
		err = e.wgInterface.Close()
		if err != nil {
			log.Errorf("failed closing Netbird interface %s %v", e.config.WgIfaceName, err)
			return err
		}
	}

	if e.udpMux != nil {
		if err := e.udpMux.Close(); err != nil {
			log.Debugf("close udp mux: %v", err)
		}
	}

	if e.udpMuxSrflx != nil {
		if err := e.udpMuxSrflx.Close(); err != nil {
			log.Debugf("close server reflexive udp mux: %v", err)
		}
	}

	if e.udpMuxConn != nil {
		if err := e.udpMuxConn.Close(); err != nil {
			log.Debugf("close udp mux connection: %v", err)
		}
	}

	if e.udpMuxConnSrflx != nil {
		if err := e.udpMuxConnSrflx.Close(); err != nil {
			log.Debugf("close server reflexive udp mux connection: %v", err)
		}
	}

	if e.routeManager != nil {
		e.routeManager.Stop()
	}

	log.Infof("stopped Netbird Engine")

	return nil
}

// Start creates a new Wireguard tunnel interface and listens to events from Signal and Management services
// Connections to remote peers are not established here.
// However, they will be established once an event with a list of peers to connect to will be received from Management Service
func (e *Engine) Start() error {
	e.syncMsgMux.Lock()
	defer e.syncMsgMux.Unlock()

	wgIfaceName := e.config.WgIfaceName
	wgAddr := e.config.WgAddr
	myPrivateKey := e.config.WgPrivateKey
	var err error

	e.wgInterface, err = iface.NewWGIFace(wgIfaceName, wgAddr, iface.DefaultMTU)
	if err != nil {
		log.Errorf("failed creating wireguard interface instance %s: [%s]", wgIfaceName, err.Error())
		return err
	}

	networkName := "udp"
	if e.config.DisableIPv6Discovery {
		networkName = "udp4"
	}

	e.udpMuxConn, err = net.ListenUDP(networkName, &net.UDPAddr{Port: e.config.UDPMuxPort})
	if err != nil {
		log.Errorf("failed listening on UDP port %d: [%s]", e.config.UDPMuxPort, err.Error())
		return err
	}

	e.udpMuxConnSrflx, err = net.ListenUDP(networkName, &net.UDPAddr{Port: e.config.UDPMuxSrflxPort})
	if err != nil {
		log.Errorf("failed listening on UDP port %d: [%s]", e.config.UDPMuxSrflxPort, err.Error())
		return err
	}

	e.udpMux = ice.NewUDPMuxDefault(ice.UDPMuxParams{UDPConn: e.udpMuxConn})
	e.udpMuxSrflx = ice.NewUniversalUDPMuxDefault(ice.UniversalUDPMuxParams{UDPConn: e.udpMuxConnSrflx})

	err = e.wgInterface.Create()
	if err != nil {
		log.Errorf("failed creating tunnel interface %s: [%s]", wgIfaceName, err.Error())
		return err
	}

	err = e.wgInterface.Configure(myPrivateKey.String(), e.config.WgPort)
	if err != nil {
		log.Errorf("failed configuring Wireguard interface [%s]: %s", wgIfaceName, err.Error())
		return err
	}

	e.routeManager = routemanager.NewManager(e.ctx, e.config.WgPrivateKey.PublicKey().String(), e.wgInterface, e.statusRecorder)

	e.receiveSignalEvents()

	return nil
}

// modifyPeers updates peers that have been modified (e.g. IP address has been changed).
// It closes the existing connection, removes it from the peerConns map, and creates a new one.
func (e *Engine) modifyPeers(peersUpdate []*mgmProto.RemotePeerConfig) error {

	// first, check if peers have been modified
	var modified []*mgmProto.RemotePeerConfig
	for _, p := range peersUpdate {
		peerPubKey := p.GetWgPubKey()
		if peerConn, ok := e.peerConns[peerPubKey]; ok {
			if peerConn.GetConf().ProxyConfig.AllowedIps != strings.Join(p.AllowedIps, ",") {
				modified = append(modified, p)
				continue
			}
			err := e.statusRecorder.UpdatePeerFQDN(peerPubKey, p.GetFqdn())
			if err != nil {
				log.Warnf("error updating peer's %s fqdn in the status recorder, got error: %v", peerPubKey, err)
			}
		}
	}

	// second, close all modified connections and remove them from the state map
	for _, p := range modified {
		err := e.removePeer(p.GetWgPubKey())
		if err != nil {
			return err
		}
	}
	// third, add the peer connections again
	for _, p := range modified {
		err := e.addNewPeer(p)
		if err != nil {
			return err
		}
	}
	return nil
}

// removePeers finds and removes peers that do not exist anymore in the network map received from the Management Service.
// It also removes peers that have been modified (e.g. change of IP address). They will be added again in addPeers method.
func (e *Engine) removePeers(peersUpdate []*mgmProto.RemotePeerConfig) error {
	currentPeers := make([]string, 0, len(e.peerConns))
	for p := range e.peerConns {
		currentPeers = append(currentPeers, p)
	}

	newPeers := make([]string, 0, len(peersUpdate))
	for _, p := range peersUpdate {
		newPeers = append(newPeers, p.GetWgPubKey())
	}

	toRemove := util.SliceDiff(currentPeers, newPeers)

	for _, p := range toRemove {
		err := e.removePeer(p)
		if err != nil {
			return err
		}
		log.Infof("removed peer %s", p)
	}
	return nil
}

func (e *Engine) removeAllPeers() error {
	log.Debugf("removing all peer connections")
	for p := range e.peerConns {
		err := e.removePeer(p)
		if err != nil {
			return err
		}
	}
	return nil
}

// removePeer closes an existing peer connection, removes a peer, and clears authorized key of the SSH server
func (e *Engine) removePeer(peerKey string) error {
	log.Debugf("removing peer from engine %s", peerKey)

	defer func() {
		err := e.statusRecorder.RemovePeer(peerKey)
		if err != nil {
			log.Warnf("received error when removing peer %s from status recorder: %v", peerKey, err)
		}
	}()

	conn, exists := e.peerConns[peerKey]
	if exists {
		delete(e.peerConns, peerKey)
		err := conn.Close()
		if err != nil {
			switch err.(type) {
			case *peer.ConnectionAlreadyClosedError:
				return nil
			default:
				return err
			}
		}
	}
	return nil
}

// GetPeerConnectionStatus returns a connection Status or nil if peer connection wasn't found
func (e *Engine) GetPeerConnectionStatus(peerKey string) peer.ConnStatus {
	conn, exists := e.peerConns[peerKey]
	if exists && conn != nil {
		return conn.Status()
	}

	return -1
}

func (e *Engine) GetPeers() []string {
	e.syncMsgMux.Lock()
	defer e.syncMsgMux.Unlock()

	peers := []string{}
	for s := range e.peerConns {
		peers = append(peers, s)
	}
	return peers
}

// GetConnectedPeers returns a connection Status or nil if peer connection wasn't found
func (e *Engine) GetConnectedPeers() []string {
	e.syncMsgMux.Lock()
	defer e.syncMsgMux.Unlock()

	peers := []string{}
	for s, conn := range e.peerConns {
		if conn.Status() == peer.StatusConnected {
			peers = append(peers, s)
		}
	}

	return peers
}

func signalCandidate(candidate ice.Candidate, myKey wgtypes.Key, remoteKey wgtypes.Key, s signal.Client) error {

	err := s.Send(&sProto.Message{
		Key:       myKey.PublicKey().String(),
		RemoteKey: remoteKey.String(),
		Body: &sProto.Body{
			Type:    sProto.Body_CANDIDATE,
			Payload: candidate.Marshal(),
		},
	})

	log.Debugf("Sent Signal Candidate %v , myKey=%v, remoteKey=%v, signal=%v", candidate, myKey.PublicKey().String(), remoteKey.String(), s)

	if err != nil {
		return err
	}

	return nil
}

// SignalOfferAnswer signals either an offer or an answer to remote peer
func SignalOfferAnswer(offerAnswer peer.OfferAnswer, myKey wgtypes.Key, remoteKey wgtypes.Key, s signal.Client, isAnswer bool) error {
	var t sProto.Body_Type
	if isAnswer {
		t = sProto.Body_ANSWER
	} else {
		t = sProto.Body_OFFER
	}

	msg, err := signal.MarshalCredential(myKey, offerAnswer.WgListenPort, remoteKey, &signal.Credential{
		UFrag: offerAnswer.IceCredentials.UFrag,
		Pwd:   offerAnswer.IceCredentials.Pwd,
	}, t)

	log.Debugf("Sending Signal Offer %v , myKey=%v, remoteKey=%v, signal=%v, t=%v, msg=%v", offerAnswer, myKey, remoteKey, s, t, msg)

	if err != nil {
		return err
	}
	err = s.Send(msg)
	if err != nil {
		return err
	}

	return nil
}

func (e *Engine) InitConf(update *mgmProto.SyncResponse) error {
	e.syncMsgMux.Lock()
	defer e.syncMsgMux.Unlock()

	if update.GetWiretrusteeConfig() != nil {
		err := e.updateTURNs(update.GetWiretrusteeConfig().GetTurns())
		if err != nil {
			return err
		}

		err = e.updateSTUNs(update.GetWiretrusteeConfig().GetStuns())
		if err != nil {
			return err
		}

		// todo update signal
	}

	if update.GetNetworkMap() != nil {
		// only apply new changes and ignore old ones
		err := e.updateNetworkMap(update.GetNetworkMap())
		if err != nil {
			return err
		}
	}

	return nil
}

func (e *Engine) handleSync(update *mgmProto.SyncResponse) error {
	e.syncMsgMux.Lock()
	defer e.syncMsgMux.Unlock()

	if update.GetWiretrusteeConfig() != nil {
		err := e.updateTURNs(update.GetWiretrusteeConfig().GetTurns())
		if err != nil {
			return err
		}

		err = e.updateSTUNs(update.GetWiretrusteeConfig().GetStuns())
		if err != nil {
			return err
		}

		// todo update signal
	}

	if update.GetNetworkMap() != nil {
		// only apply new changes and ignore old ones
		err := e.updateNetworkMap(update.GetNetworkMap())
		if err != nil {
			return err
		}
	}

	return nil
}

func (e *Engine) updateConfig(conf *mgmProto.PeerConfig) error {
	if e.wgInterface.Address.String() != conf.Address {
		oldAddr := e.wgInterface.Address.String()
		log.Debugf("updating peer address from %s to %s", oldAddr, conf.Address)
		err := e.wgInterface.UpdateAddr(conf.Address)
		if err != nil {
			return err
		}
		e.config.WgAddr = conf.Address
		log.Infof("updated peer address from %s to %s", oldAddr, conf.Address)
	}

	e.statusRecorder.UpdateLocalPeerState(nbstatus.LocalPeerState{
		IP:              e.config.WgAddr,
		PubKey:          e.config.WgPrivateKey.PublicKey().String(),
		KernelInterface: iface.WireguardModuleIsLoaded(),
		FQDN:            conf.GetFqdn(),
	})

	return nil
}

func (e *Engine) updateSTUNs(stuns []*mgmProto.HostConfig) error {
	if len(stuns) == 0 {
		return nil
	}
	var newSTUNs []*ice.URL
	log.Debugf("got STUNs update from Management Service, updating")
	for _, stun := range stuns {
		url, err := ice.ParseURL(stun.Uri)
		if err != nil {
			return err
		}
		newSTUNs = append(newSTUNs, url)
	}
	e.STUNs = newSTUNs

	return nil
}

func (e *Engine) updateTURNs(turns []*mgmProto.ProtectedHostConfig) error {
	if len(turns) == 0 {
		return nil
	}
	var newTURNs []*ice.URL
	log.Debugf("got TURNs update from Management Service, updating")
	for _, turn := range turns {
		url, err := ice.ParseURL(turn.HostConfig.Uri)
		if err != nil {
			return err
		}
		url.Username = turn.User
		url.Password = turn.Password
		newTURNs = append(newTURNs, url)
	}
	e.TURNs = newTURNs

	return nil
}

func (e *Engine) updateNetworkMap(networkMap *mgmProto.NetworkMap) error {

	// intentionally leave it before checking serial because for now it can happen that peer IP changed but serial didn't
	if networkMap.GetPeerConfig() != nil {
		err := e.updateConfig(networkMap.GetPeerConfig())
		if err != nil {
			return err
		}
	}

	serial := networkMap.GetSerial()
	if e.networkSerial > serial {
		log.Debugf("received outdated NetworkMap with serial %d, ignoring", serial)
		return nil
	}

	log.Debugf("got peers update from Management Service, total peers to connect to = %d", len(networkMap.GetRemotePeers()))

	// cleanup request, most likely our peer has been deleted
	if networkMap.GetRemotePeersIsEmpty() {
		err := e.removeAllPeers()
		if err != nil {
			return err
		}
	} else {
		err := e.removePeers(networkMap.GetRemotePeers())
		if err != nil {
			return err
		}

		err = e.modifyPeers(networkMap.GetRemotePeers())
		if err != nil {
			return err
		}

		err = e.addNewPeers(networkMap.GetRemotePeers())
		if err != nil {
			return err
		}
	}
	protoRoutes := networkMap.GetRoutes()
	if protoRoutes == nil {
		protoRoutes = []*mgmProto.Route{}
	}
	err := e.routeManager.UpdateRoutes(serial, toRoutes(protoRoutes))
	if err != nil {
		log.Errorf("failed to update routes, err: %v", err)
	}

	protoDNSConfig := networkMap.GetDNSConfig()
	if protoDNSConfig == nil {
		protoDNSConfig = &mgmProto.DNSConfig{}
	}

	if err != nil {
		log.Errorf("failed to update dns server, err: %v", err)
	}

	e.networkSerial = serial
	return nil
}

func toRoutes(protoRoutes []*mgmProto.Route) []*route.Route {
	routes := make([]*route.Route, 0)
	for _, protoRoute := range protoRoutes {
		_, prefix, _ := route.ParseNetwork(protoRoute.Network)
		convertedRoute := &route.Route{
			ID:          protoRoute.ID,
			Network:     prefix,
			NetID:       protoRoute.NetID,
			NetworkType: route.NetworkType(protoRoute.NetworkType),
			Peer:        protoRoute.Peer,
			Metric:      int(protoRoute.Metric),
			Masquerade:  protoRoute.Masquerade,
		}
		routes = append(routes, convertedRoute)
	}
	return routes
}

func toDNSConfig(protoDNSConfig *mgmProto.DNSConfig) nbdns.Config {
	dnsUpdate := nbdns.Config{
		ServiceEnable:    protoDNSConfig.GetServiceEnable(),
		CustomZones:      make([]nbdns.CustomZone, 0),
		NameServerGroups: make([]*nbdns.NameServerGroup, 0),
	}

	for _, zone := range protoDNSConfig.GetCustomZones() {
		dnsZone := nbdns.CustomZone{
			Domain: zone.GetDomain(),
		}
		for _, record := range zone.Records {
			dnsRecord := nbdns.SimpleRecord{
				Name:  record.GetName(),
				Type:  int(record.GetType()),
				Class: record.GetClass(),
				TTL:   int(record.GetTTL()),
				RData: record.GetRData(),
			}
			dnsZone.Records = append(dnsZone.Records, dnsRecord)
		}
		dnsUpdate.CustomZones = append(dnsUpdate.CustomZones, dnsZone)
	}

	for _, nsGroup := range protoDNSConfig.GetNameServerGroups() {
		dnsNSGroup := &nbdns.NameServerGroup{
			Primary: nsGroup.GetPrimary(),
			Domains: nsGroup.GetDomains(),
		}
		for _, ns := range nsGroup.GetNameServers() {
			dnsNS := nbdns.NameServer{
				IP:     netip.MustParseAddr(ns.GetIP()),
				NSType: nbdns.NameServerType(ns.GetNSType()),
				Port:   int(ns.GetPort()),
			}
			dnsNSGroup.NameServers = append(dnsNSGroup.NameServers, dnsNS)
		}
		dnsUpdate.NameServerGroups = append(dnsUpdate.NameServerGroups, dnsNSGroup)
	}
	return dnsUpdate
}

// addNewPeers adds peers that were not know before but arrived from the Management service with the update
func (e *Engine) addNewPeers(peersUpdate []*mgmProto.RemotePeerConfig) error {
	for _, p := range peersUpdate {
		err := e.addNewPeer(p)
		if err != nil {
			return err
		}
	}
	return nil
}

// addNewPeer add peer if connection doesn't exist
func (e *Engine) addNewPeer(peerConfig *mgmProto.RemotePeerConfig) error {
	peerKey := peerConfig.GetWgPubKey()
	peerIPs := peerConfig.GetAllowedIps()
	if _, ok := e.peerConns[peerKey]; !ok {
		conn, err := e.createPeerConn(peerKey, strings.Join(peerIPs, ","))
		if err != nil {
			return err
		}
		e.peerConns[peerKey] = conn

		err = e.statusRecorder.AddPeer(peerKey)
		if err != nil {
			log.Warnf("error adding peer %s to status recorder, got error: %v", peerKey, err)
		}

		go e.connWorker(conn, peerKey)
	}
	err := e.statusRecorder.UpdatePeerFQDN(peerKey, peerConfig.Fqdn)
	if err != nil {
		log.Warnf("error updating peer's %s fqdn in the status recorder, got error: %v", peerKey, err)
	}
	return nil
}

func (e *Engine) connWorker(conn *peer.Conn, peerKey string) {
	for {

		// randomize starting time a bit
		min := 500
		max := 2000
		time.Sleep(time.Duration(rand.Intn(max-min)+min) * time.Millisecond)

		// if peer has been removed -> give up
		if !e.peerExists(peerKey) {
			log.Debugf("peer %s doesn't exist anymore, won't retry connection", peerKey)
			return
		}

		if !e.signal.Ready() {
			log.Infof("signal client isn't ready, skipping connection attempt %s", peerKey)
			continue
		}

		// we might have received new STUN and TURN servers meanwhile, so update them
		e.syncMsgMux.Lock()
		conf := conn.GetConf()
		conf.StunTurn = append(e.STUNs, e.TURNs...)
		conn.UpdateConf(conf)
		e.syncMsgMux.Unlock()

		err := conn.Open()
		if err != nil {
			log.Debugf("connection to peer %s failed: %v", peerKey, err)
			switch err.(type) {
			case *peer.ConnectionClosedError:
				// conn has been forced to close, so we exit the loop
				return
			default:
			}
		}
	}
}

func (e Engine) peerExists(peerKey string) bool {
	e.syncMsgMux.Lock()
	defer e.syncMsgMux.Unlock()
	_, ok := e.peerConns[peerKey]
	return ok
}

func (e Engine) createPeerConn(pubKey string, allowedIPs string) (*peer.Conn, error) {
	log.Debugf("creating peer connection %s", pubKey)
	var stunTurn []*ice.URL
	stunTurn = append(stunTurn, e.STUNs...)
	stunTurn = append(stunTurn, e.TURNs...)

	proxyConfig := proxy.Config{
		RemoteKey:    pubKey,
		WgListenAddr: fmt.Sprintf("127.0.0.1:%d", e.config.WgPort),
		WgInterface:  e.wgInterface,
		AllowedIps:   allowedIPs,
		PreSharedKey: e.config.PreSharedKey,
	}

	// randomize connection timeout
	timeout := time.Duration(rand.Intn(PeerConnectionTimeoutMax-PeerConnectionTimeoutMin)+PeerConnectionTimeoutMin) * time.Millisecond
	config := peer.ConnConfig{
		Key:                  pubKey,
		LocalKey:             e.config.WgPrivateKey.PublicKey().String(),
		StunTurn:             stunTurn,
		InterfaceBlackList:   e.config.IFaceBlackList,
		DisableIPv6Discovery: e.config.DisableIPv6Discovery,
		Timeout:              timeout,
		UDPMux:               e.udpMux,
		UDPMuxSrflx:          e.udpMuxSrflx,
		ProxyConfig:          proxyConfig,
		LocalWgPort:          e.config.WgPort,
		NATExternalIPs:       e.parseNATExternalIPMappings(),
	}

	peerConn, err := peer.NewConn(config, e.statusRecorder)
	if err != nil {
		return nil, err
	}

	wgPubKey, err := wgtypes.ParseKey(pubKey)
	if err != nil {
		return nil, err
	}

	signalOffer := func(offerAnswer peer.OfferAnswer) error {
		return SignalOfferAnswer(offerAnswer, e.config.WgPrivateKey, wgPubKey, e.signal, false)
	}

	signalCandidate := func(candidate ice.Candidate) error {
		return signalCandidate(candidate, e.config.WgPrivateKey, wgPubKey, e.signal)
	}

	signalAnswer := func(offerAnswer peer.OfferAnswer) error {
		return SignalOfferAnswer(offerAnswer, e.config.WgPrivateKey, wgPubKey, e.signal, true)
	}

	peerConn.SetSignalCandidate(signalCandidate)
	peerConn.SetSignalOffer(signalOffer)
	peerConn.SetSignalAnswer(signalAnswer)

	return peerConn, nil
}

// receiveSignalEvents connects to the Signal Service event stream to negotiate connection with remote peers
func (e *Engine) receiveSignalEvents() {
	go func() {
		// connect to a stream of messages coming from the signal server
		err := e.signal.Receive(func(msg *sProto.Message) error {
			e.syncMsgMux.Lock()
			defer e.syncMsgMux.Unlock()

			conn := e.peerConns[msg.Key]
			if conn == nil {
				return fmt.Errorf("wrongly addressed message %s", msg.Key)
			}

			switch msg.GetBody().Type {
			case sProto.Body_OFFER:
				remoteCred, err := signal.UnMarshalCredential(msg)
				if err != nil {
					return err
				}
				conn.OnRemoteOffer(peer.OfferAnswer{
					IceCredentials: peer.IceCredentials{
						UFrag: remoteCred.UFrag,
						Pwd:   remoteCred.Pwd,
					},
					WgListenPort: int(msg.GetBody().GetWgListenPort()),
					Version:      msg.GetBody().GetNetBirdVersion(),
				})
			case sProto.Body_ANSWER:
				remoteCred, err := signal.UnMarshalCredential(msg)
				if err != nil {
					return err
				}
				conn.OnRemoteAnswer(peer.OfferAnswer{
					IceCredentials: peer.IceCredentials{
						UFrag: remoteCred.UFrag,
						Pwd:   remoteCred.Pwd,
					},
					WgListenPort: int(msg.GetBody().GetWgListenPort()),
					Version:      msg.GetBody().GetNetBirdVersion(),
				})
			case sProto.Body_CANDIDATE:
				candidate, err := ice.UnmarshalCandidate(msg.GetBody().Payload)
				if err != nil {
					log.Errorf("failed on parsing remote candidate %s -> %s", candidate, err)
					return err
				}
				conn.OnRemoteCandidate(candidate)
			}

			return nil
		})
		if err != nil {
			// happens if signal is unavailable for a long time.
			// We want to cancel the operation of the whole client
			log.Error(err)
			e.cancel()
			return
		}
	}()

	e.signal.WaitStreamConnected()
}

func (e *Engine) parseNATExternalIPMappings() []string {
	var mappedIPs []string
	var ignoredIFaces = make(map[string]interface{})
	for _, iFace := range e.config.IFaceBlackList {
		ignoredIFaces[iFace] = nil
	}
	for _, mapping := range e.config.NATExternalIPs {
		var external, internal string
		var externalIP, internalIP net.IP
		var err error
		split := strings.Split(mapping, "/")
		if len(split) > 2 {
			log.Warnf("ignoring invalid external mapping '%s', too many delimiters", mapping)
			break
		}
		if len(split) > 1 {
			internal = split[1]
			internalIP = net.ParseIP(internal)
			if internalIP == nil {
				// not a properly formatted IP address, maybe it's interface name?
				if _, present := ignoredIFaces[internal]; present {
					log.Warnf("internal interface '%s' in blacklist, ignoring external mapping '%s'", internal, mapping)
					break
				}
				internalIP, err = findIPFromInterfaceName(internal)
				if err != nil {
					log.Warnf("error finding interface IP for interface '%s', ignoring external mapping '%s': %v", internal, mapping, err)
					break
				}
			}
		}
		external = split[0]
		externalIP = net.ParseIP(external)
		if externalIP == nil {
			log.Warnf("invalid external IP, ignoring external IP mapping '%s'", mapping)
			break
		}
		if externalIP != nil {
			mappedIP := externalIP.String()
			if internalIP != nil {
				mappedIP = mappedIP + "/" + internalIP.String()
			}
			mappedIPs = append(mappedIPs, mappedIP)
			log.Infof("parsed external IP mapping of '%s' as '%s'", mapping, mappedIP)
		}
	}
	if len(mappedIPs) != len(e.config.NATExternalIPs) {
		log.Warnf("one or more external IP mappings failed to parse, ignoring all mappings")
		return nil
	}
	return mappedIPs
}

func findIPFromInterfaceName(ifaceName string) (net.IP, error) {
	iface, err := net.InterfaceByName(ifaceName)
	if err != nil {
		return nil, err
	}
	return findIPFromInterface(iface)
}

func findIPFromInterface(iface *net.Interface) (net.IP, error) {
	ifaceAddrs, err := iface.Addrs()
	if err != nil {
		return nil, err
	}
	for _, addr := range ifaceAddrs {
		if ipv4Addr := addr.(*net.IPNet).IP.To4(); ipv4Addr != nil {
			return ipv4Addr, nil
		}
	}
	return nil, fmt.Errorf("interface %s don't have an ipv4 address", iface.Name)
}
