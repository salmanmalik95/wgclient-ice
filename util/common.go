package util

import (
	"encoding/json"
	"fmt"
	"time"
)

type PingMessage struct {
	Message  string `json:"message,omitempty"`
	PrevTime int64  `json:"prev_time,omitempty"`
	PrevHop  string `json:"prev_hop"`
}

func AddPingMessageHop(bytes []byte, currHop string) []byte {
	var pingMsg PingMessage
	_ = json.Unmarshal(bytes, &pingMsg)

	if pingMsg.PrevHop == "" {
		pingMsg.PrevHop = currHop
		pingMsg.PrevTime = time.Now().UTC().UnixMilli()
	} else {
		currTime := time.Now().UTC().UnixMilli()
		pingMsg.Message = fmt.Sprintf("%s \n [CLIENT] %s to %s = %d", pingMsg.Message, pingMsg.PrevHop, currHop, currTime-pingMsg.PrevTime)
		pingMsg.PrevHop = currHop
		pingMsg.PrevHop = currHop
	}

	resp, _ := json.Marshal(pingMsg)

	return resp
}
