package api

import (
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"spread-arbitrage/internal/engine"
	"spread-arbitrage/internal/exchange"
	"spread-arbitrage/internal/wsmanager"
)

func NewServer(eng *engine.Engine, tm *engine.TaskManager, clients map[string]exchange.Client, hub *Hub, wsMgr *wsmanager.Manager) *gin.Engine {
	r := gin.Default()

	r.Use(cors.New(cors.Config{
		AllowOrigins:     []string{"http://localhost:18528"},
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type"},
		AllowWebSockets:  true,
	}))

	handler := NewHandler(eng, tm, clients, hub, wsMgr)
	handler.RegisterRoutes(r)

	r.Static("/assets", "./web/dist/assets")
	r.StaticFile("/", "./web/dist/index.html")
	r.NoRoute(func(c *gin.Context) {
		c.File("./web/dist/index.html")
	})

	return r
}
