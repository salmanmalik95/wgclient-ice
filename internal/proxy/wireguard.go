package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"time"
	"ztnav2client/internal/http"
)

// WireguardProxy proxies
type WireguardProxy struct {
	ctx    context.Context
	cancel context.CancelFunc

	config Config

	remoteConn net.Conn
	localConn  net.Conn
}

func NewWireguardProxy(config Config) *WireguardProxy {
	p := &WireguardProxy{config: config}
	p.ctx, p.cancel = context.WithCancel(context.Background())
	return p
}

func (p *WireguardProxy) updateEndpoint() error {
	udpAddr, err := net.ResolveUDPAddr(p.localConn.LocalAddr().Network(), p.localConn.LocalAddr().String())
	if err != nil {
		return err
	}
	// add local proxy connection as a Wireguard peer
	err = p.config.WgInterface.UpdatePeer(p.config.RemoteKey, p.config.AllowedIps, DefaultWgKeepAlive,
		udpAddr, p.config.PreSharedKey)
	if err != nil {
		return err
	}

	return nil
}

func (p *WireguardProxy) Start(remoteConn net.Conn) error {
	p.remoteConn = remoteConn

	var err error
	p.localConn, err = net.Dial("udp", p.config.WgListenAddr)
	if err != nil {
		log.Errorf("failed dialing to local Wireguard port %s", err)
		return err
	}

	err = p.updateEndpoint()
	if err != nil {
		log.Errorf("error while updating Wireguard peer endpoint [%s] %v", p.config.RemoteKey, err)
		return err
	}

	go p.proxyToRemote()
	go p.proxyToLocal()

	log.Debugf("[RemoteConn] Remote = %s, Local = %s", remoteConn.RemoteAddr().String(), remoteConn.LocalAddr().String())
	log.Debugf("[LocalConn] Remote = %s, Local = %s", p.localConn.RemoteAddr().String(), p.localConn.LocalAddr().String())

	router := gin.New()
	http.NewHandler(router, remoteConn)

	randPort := rand.Intn(8050-8010) + 8010
	if err := router.Run("0.0.0.0:" + strconv.Itoa(randPort)); err != nil {
		panic(err.Error())
	}

	log.Debugf("Running connection debug proxy at http://127.0.0.1:%d", randPort)

	return nil
}

func (p *WireguardProxy) Close() error {
	p.cancel()
	if c := p.localConn; c != nil {
		err := p.localConn.Close()
		if err != nil {
			return err
		}
	}
	err := p.config.WgInterface.RemovePeer(p.config.RemoteKey)
	if err != nil {
		return err
	}
	return nil
}

// proxyToRemote proxies everything from Wireguard to the RemoteKey peer
// blocks
func (p *WireguardProxy) proxyToRemote() {

	buf := make([]byte, 1500)
	for {
		select {
		case <-p.ctx.Done():
			log.Debugf("stopped proxying to remote peer %s due to closed connection", p.config.RemoteKey)
			return
		default:
			n, err := p.localConn.Read(buf)
			if err != nil {
				continue
			}

			//resp := []byte("peer1 to remote")
			_, err = p.remoteConn.Write(buf[:n])
			if err != nil {
				continue
			}
		}
	}
}

// proxyToLocal proxies everything from the RemoteKey peer to local Wireguard
// blocks
func (p *WireguardProxy) proxyToLocal() {

	buf := make([]byte, 1500)
	for {
		select {
		case <-p.ctx.Done():
			log.Debugf("stopped proxying from remote peer %s due to closed connection", p.config.RemoteKey)
			return
		default:
			n, err := p.remoteConn.Read(buf)

			tReached := time.Now().UTC().String()
			if err != nil {
				continue
			}

			msg := string(buf[:n])
			if strings.Contains(msg, "DEBUG") {
				log.Debugf("Resp from remote %s", msg)
				var pingMsg http.PingMessage

				_ = json.Unmarshal(buf[:n], &pingMsg)

				if !strings.Contains(msg, "REPLY") {
					pingMsg.Message = fmt.Sprintf("[REPLY] of message=[%s]", pingMsg.Message)
					pingMsg.DestReachedTime = tReached
					pingMsg.RelayExitTime = time.Now().UTC().String()

					reply, _ := json.Marshal(pingMsg)
					_, err = p.remoteConn.Write(reply)
					if err != nil {
						continue
					}
				} else {
					pingMsg.ReplyReachedTime = time.Now().UTC().String()
				}

				b, _ := json.MarshalIndent(pingMsg, "", "  ")
				log.Debugf("Ping Message %s", string(b))
				continue
			}

			_, err = p.localConn.Write(buf[:n])
			if err != nil {
				continue
			}
		}
	}
}

func (p *WireguardProxy) Type() Type {
	return TypeWireguard
}
