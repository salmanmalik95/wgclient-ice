package internal

import (
	"github.com/netbirdio/netbird/client/ssh"
	mgmProto "github.com/netbirdio/netbird/management/proto"
	"os"

	"github.com/netbirdio/netbird/iface"
	"github.com/netbirdio/netbird/util"
	log "github.com/sirupsen/logrus"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func init() {
}

type SignalService struct {
	Uri      string `json:"uri"`
	Protocol string `json:"protocol"`
}

// Config Configuration type
type Config struct {
	// Wireguard private key of local peer
	PrivateKey   string
	PreSharedKey string
	WgIface      string
	WgPort       int
	WgIp         string
	// SSHKey is a private SSH key in a PEM format
	SSHKey        string
	Peers         []*mgmProto.RemotePeerConfig
	PeerConfig    mgmProto.PeerConfig
	Stuns         []*mgmProto.HostConfig
	Turns         []*mgmProto.ProtectedHostConfig
	SignalService SignalService
}

// createNewConfig creates a new config generating a new Wireguard key and saving to file
func createNewConfig(configPath, preSharedKey string) (*Config, error) {
	wgKey := generateKey()
	pem, err := ssh.GeneratePrivateKey(ssh.ED25519)
	if err != nil {
		return nil, err
	}
	config := &Config{
		SSHKey:     string(pem),
		PrivateKey: wgKey,
		WgIface:    iface.WgInterfaceDefault,
		WgPort:     iface.DefaultWgPort,
	}

	err = util.WriteJson(configPath, config)
	if err != nil {
		return nil, err
	}

	return config, nil
}

// ReadConfig reads existing config. In case provided managementURL is not empty overrides the read property
func ReadConfig(configPath string, preSharedKey *string) (*Config, error) {
	config := &Config{}
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return nil, status.Errorf(codes.NotFound, "config file doesn't exist")
	}

	if _, err := util.ReadJson(configPath, config); err != nil {
		return nil, err
	}

	refresh := false

	if preSharedKey != nil && config.PreSharedKey != *preSharedKey {
		log.Infof("new pre-shared key provided, updated to %s (old value %s)",
			*preSharedKey, config.PreSharedKey)
		config.PreSharedKey = *preSharedKey
		refresh = true
	}
	if config.SSHKey == "" {
		pem, err := ssh.GeneratePrivateKey(ssh.ED25519)
		if err != nil {
			return nil, err
		}
		config.SSHKey = string(pem)
		refresh = true
	}

	if config.WgPort == 0 {
		config.WgPort = iface.DefaultWgPort
		refresh = true
	}

	if refresh {
		// since we have new management URL, we need to update config file
		if err := util.WriteJson(configPath, config); err != nil {
			return nil, err
		}
	}

	return config, nil
}

// GetConfig reads existing config or generates a new one
func GetConfig(configPath, preSharedKey string) (*Config, error) {
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		log.Infof("generating new config %s", configPath)
		return createNewConfig(configPath, preSharedKey)
	} else {
		// don't overwrite pre-shared key if we receive asterisks from UI
		pk := &preSharedKey
		if preSharedKey == "**********" {
			pk = nil
		}
		return ReadConfig(configPath, pk)
	}
}

// generateKey generates a new Wireguard private key
func generateKey() string {
	key, err := wgtypes.GeneratePrivateKey()
	if err != nil {
		panic(err)
	}
	return key.String()
}
