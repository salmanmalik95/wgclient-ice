package http

import (
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"net"
	"strings"
)

type Handler struct {
	remoteConn net.Conn
}

func NewHandler(router *gin.Engine, remoteConn net.Conn) {

	handler := &Handler{
		remoteConn: remoteConn,
	}
	g := router.Group("")
	g.GET("/ping/*message", handler.SendPing)
}

type PingMessage struct {
	Message               string `json:"message,omitempty"`
	InitiatedTime         int64  `json:"initiated_time,omitempty"`
	RelayReachedTime      int64  `json:"relay_reached_time,omitempty"`
	RelayExitTime         int64  `json:"relay_exit_time,omitempty"`
	DestReachedTime       int64  `json:"dest_reached_time,omitempty"`
	ReplyInitiatedTime    int64  `json:"reply_initiated_time,omitempty"`
	ReplyReachedRelayTime int64  `json:"reply_reached_relay_time,omitempty"`
	ReplyExitRelayTime    int64  `json:"reply_exit_relay_time,omitempty"`
	ReplyReachedTime      int64  `json:"reply_reached_time,omitempty"`
}

func (h *Handler) SendPing(c *gin.Context) {
	var pingMsg PingMessage

	message := strings.TrimPrefix(c.Param("message"), "/")
	pingMsg.Message = fmt.Sprintf("[DEBUG] msg=%s", message)

	msg, _ := json.Marshal(pingMsg)
	_, err := h.remoteConn.Write(msg)
	if err != nil {
		log.Errorf("Failed to send ping message %v", err)
		c.JSON(400, map[string]string{"resp": "failed to send ping message"})
	}

	c.JSON(200, map[string]string{"resp": "message sent successfully"})
}
