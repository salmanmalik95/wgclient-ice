package main

import (
	"context"
	"fmt"
	mgmProto "github.com/netbirdio/netbird/management/proto"
	"ztnav2client/internal"
	nbStatus "ztnav2client/status"
	"ztnav2client/util"
)

func main() {
	configPath := "/etc/netbird/config.json"

	ctx := context.Background()
	config, err := internal.GetConfig(configPath, "")

	initConf := mgmProto.SyncResponse{
		NetworkMap: &mgmProto.NetworkMap{
			RemotePeers: config.Peers,
			PeerConfig:  &config.PeerConfig,
		},
		WiretrusteeConfig: &mgmProto.WiretrusteeConfig{
			Stuns: config.Stuns,
			Turns: config.Turns,
		},
	}

	err = util.InitLog("debug", "console")
	err = internal.RunClient(ctx, config, nbStatus.NewRecorder(), &initConf)
	if err != nil {
		panic(err)
	}
	fmt.Println("Connected")
}
