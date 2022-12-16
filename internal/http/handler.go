package http

import (
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

	g.GET("/send-message/*message", handler.SendDebugMessages)

}

func (h *Handler) SendDebugMessages(c *gin.Context) {
	message := strings.TrimPrefix(c.Param("message"), "/")
	msg := []byte(fmt.Sprintf("[DEBUG] msg=%s intiated time=%s", message, time.Now().String()))
	_, err := h.remoteConn.Write(msg)
	if err != nil {
		log.Errorf("Failed to send ping message %v", err)
		c.JSON(400, map[string]string{"resp": "failed to send ping message"})
	}

	c.JSON(200, map[string]string{"resp": "message sent successfully"})

}
