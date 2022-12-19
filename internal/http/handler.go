package http

import (
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	log "github.com/sirupsen/logrus"
	"net"
	"strings"
	"ztnav2client/util"
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

func (h *Handler) SendPing(c *gin.Context) {
	message := strings.TrimPrefix(c.Param("message"), "/")
	message = fmt.Sprintf("[DEBUG] msg=%s", message)
	var pingMsg util.PingMessage
	pingMsg.Message = message

	b, _ := json.Marshal(pingMsg)

	msg := util.AddPingMessageHop(b, "Ping Initiated")

	_, err := h.remoteConn.Write(msg)
	if err != nil {
		log.Errorf("Failed to send ping message %v", err)
		c.JSON(400, map[string]string{"resp": "failed to send ping message"})
	}

	c.JSON(200, map[string]string{"resp": "message sent successfully"})
}
