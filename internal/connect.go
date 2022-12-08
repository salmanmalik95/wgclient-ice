package internal

import (
	"context"
	mgmProto "github.com/netbirdio/netbird/management/proto"
	"time"

	nbStatus "ztnav2client/status"

	"github.com/netbirdio/netbird/iface"
	signal "github.com/netbirdio/netbird/signal/client"
	log "github.com/sirupsen/logrus"

	"github.com/cenkalti/backoff/v4"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"google.golang.org/grpc/codes"
	gstatus "google.golang.org/grpc/status"
)

type PeerConfig struct {
	Address string `json:"address,omitempty"`
	// Peer fully qualified domain name
	Fqdn string `json:"fqdn,omitempty"`
}

// RunClient with main logic.
func RunClient(ctx context.Context, config *Config, statusRecorder *nbStatus.Status, initConf *mgmProto.SyncResponse) error {
	backOff := &backoff.ExponentialBackOff{
		InitialInterval:     time.Second,
		RandomizationFactor: 1,
		Multiplier:          1.7,
		MaxInterval:         15 * time.Second,
		MaxElapsedTime:      3 * 30 * 24 * time.Hour, // 3 months
		Stop:                backoff.Stop,
		Clock:               backoff.SystemClock,
	}

	myPrivateKey, err := wgtypes.ParseKey(config.PrivateKey)
	if err != nil {
		log.Errorf("failed parsing Wireguard key %s: [%s]", config.PrivateKey, err.Error())
		return err
	}

	operation := func() error {
		// if context cancelled we not start new backoff cycle
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		engineCtx, cancel := context.WithCancel(ctx)
		defer func() {
			statusRecorder.CleanLocalPeerState()
			cancel()
		}()

		localPeerState := nbStatus.LocalPeerState{
			IP:              config.WgIp,
			PubKey:          myPrivateKey.PublicKey().String(),
			KernelInterface: iface.WireguardModuleIsLoaded(),
		}

		statusRecorder.UpdateLocalPeerState(localPeerState)

		// with the global Wiretrustee config in hand connect (just a connection, no stream yet) Signal
		signalClient, err := connectToSignal(engineCtx, config.SignalService.Protocol, config.SignalService.Uri, myPrivateKey)
		if err != nil {
			log.Error(err)
			return err
		}
		defer func() {
			err = signalClient.Close()
			if err != nil {
				log.Warnf("failed closing Signal service client %v", err)
			}
		}()

		engineConfig, err := createEngineConfig(myPrivateKey, config, config.PeerConfig)
		if err != nil {
			log.Error(err)
			return err
		}

		engine := NewEngine(engineCtx, cancel, signalClient, engineConfig, statusRecorder)
		err = engine.Start()
		if err != nil {
			log.Errorf("error while starting Netbird Connection Engine: %s", err)
			return err
		}

		log.Print("Netbird engine started, my IP is: ", config.PeerConfig.Address)

		err = engine.InitConf(initConf)
		if err != nil {
			log.Errorf("failed to initiate conf %v", err)
			return err
		}

		<-engineCtx.Done()

		backOff.Reset()

		err = engine.Stop()
		if err != nil {
			log.Errorf("failed stopping engine %v", err)
			return err
		}

		log.Info("stopped NetBird client")
		return nil
	}

	err = backoff.Retry(operation, backOff)
	if err != nil {
		log.Debugf("exiting client retry loop due to unrecoverable error: %s", err)
		return err
	}
	return nil
}

// createEngineConfig converts configuration received from Management Service to EngineConfig
func createEngineConfig(key wgtypes.Key, config *Config, peerConfig mgmProto.PeerConfig) (*EngineConfig, error) {

	engineConf := &EngineConfig{
		WgIfaceName:  config.WgIface,
		WgAddr:       peerConfig.Address,
		WgPrivateKey: key,
		WgPort:       config.WgPort,
		SSHKey:       []byte(config.SSHKey),
	}

	if config.PreSharedKey != "" {
		preSharedKey, err := wgtypes.ParseKey(config.PreSharedKey)
		if err != nil {
			return nil, err
		}
		engineConf.PreSharedKey = &preSharedKey
	}

	return engineConf, nil
}

// connectToSignal creates Signal Service client and established a connection
func connectToSignal(ctx context.Context, sigProtocol string, sigUri string, ourPrivateKey wgtypes.Key) (*signal.GrpcClient, error) {
	var sigTLSEnabled bool
	if sigProtocol == "https" {
		sigTLSEnabled = true
	} else {
		sigTLSEnabled = false
	}

	signalClient, err := signal.NewClient(ctx, sigUri, ourPrivateKey, sigTLSEnabled)
	if err != nil {
		log.Errorf("error while connecting to the Signal Exchange Service %s: %s", sigUri, err)
		return nil, gstatus.Errorf(codes.FailedPrecondition, "failed connecting to Signal Service : %s", err)
	}

	return signalClient, nil
}
