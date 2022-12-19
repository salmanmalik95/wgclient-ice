package http

import (
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"net"
	"strings"
	"time"
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
	InitiatedTime         string `json:"initiated_time,omitempty"`
	RelayReachedTime      string `json:"relay_reached_time,omitempty"`
	RelayExitTime         string `json:"relay_exit_time,omitempty"`
	DestReachedTime       string `json:"dest_reached_time,omitempty"`
	ReplyInitiatedTime    string `json:"reply_initiated_time,omitempty"`
	ReplyReachedRelayTime string `json:"reply_reached_relay_time,omitempty"`
	ReplyExitRelayTime    string `json:"reply_exit_relay_time,omitempty"`
	ReplyReachedTime      string `json:"reply_reached_time,omitempty"`
}

func (h *Handler) SendPing(c *gin.Context) {
	var pingMsg PingMessage

	message := strings.TrimPrefix(c.Param("message"), "/")
	pingMsg.Message = fmt.Sprintf("[DEBUG] msg=%s", message)
	pingMsg.InitiatedTime = time.Now().String()

	msg, _ := json.Marshal(pingMsg)
	_, err := h.remoteConn.Write(msg)
	if err != nil {
		log.Errorf("Failed to send ping message %v", err)
		c.JSON(400, map[string]string{"resp": "failed to send ping message"})
	}

	c.JSON(200, map[string]string{"resp": "message sent successfully"})
}
