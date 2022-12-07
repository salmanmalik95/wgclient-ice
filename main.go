package main

import (
	"context"
	"fmt"
	"ztnav2client/internal"
	nbStatus "ztnav2client/status"
)

func main() {
	configPath := "/etc/netbird/config.json"
	ctx := context.Background()
	config, err := internal.GetConfig(configPath, "")
	err = internal.RunClient(ctx, config, nbStatus.NewRecorder())
	if err != nil {
		panic(err)
	}
	fmt.Println("Connected")
}
